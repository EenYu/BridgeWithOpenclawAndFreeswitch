package tts

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/providerhttp"
)

type Client interface {
	Synthesize(ctx context.Context, sessionID string, text string) (AudioPayload, error)
	Interrupt(ctx context.Context, sessionID string) error
}

type AudioPayload struct {
	Bytes        []byte
	Format       string
	SampleRateHz int
}

type NoopClient struct {
	config config.ProviderConfig
}

func NewClient(cfg config.ProviderConfig) Client {
	if !cfg.Enabled || cfg.Endpoint == "" || cfg.Endpoint == "memory://tts" || cfg.Vendor == "noop-tts" {
		return NoopClient{config: cfg}
	}
	if cfg.Vendor == "volcengine-tts-ws-v3" || strings.Contains(cfg.Endpoint, "/api/v3/tts/bidirection") {
		return NewVolcengineWSClient(cfg)
	}
	if cfg.Vendor == "volcengine-tts-http-v1" {
		return &VolcengineHTTPClient{
			httpClient: providerhttp.NewProviderClient(cfg),
			config:     cfg,
		}
	}
	return &HTTPClient{
		httpClient: providerhttp.NewProviderClient(cfg),
		config:     cfg,
	}
}

func (c NoopClient) Synthesize(_ context.Context, _ string, text string) (AudioPayload, error) {
	return AudioPayload{
		Bytes:        []byte(text),
		Format:       NormalizeAudioFormat(c.config.AudioFormat),
		SampleRateHz: firstPositive(c.config.SampleRateHz, 16000),
	}, nil
}

func (NoopClient) Interrupt(_ context.Context, _ string) error {
	return nil
}

type HTTPClient struct {
	httpClient *http.Client
	config     config.ProviderConfig
}

func (c *HTTPClient) Synthesize(ctx context.Context, sessionID string, text string) (AudioPayload, error) {
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, map[string]any{
		"sessionId": sessionID,
		"text":      text,
		"model":     c.config.Model,
	})
	if err != nil {
		return AudioPayload{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AudioPayload{}, err
	}

	return decodeHTTPAudioResponse(resp, c.config)
}

func (c *HTTPClient) Interrupt(ctx context.Context, sessionID string) error {
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, map[string]any{
		"sessionId": sessionID,
		"event":     "interrupt",
		"model":     c.config.Model,
	})
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}

	_, err = providerhttp.ReadBody(resp)
	return err
}

func decodeHTTPAudioResponse(resp *http.Response, cfg config.ProviderConfig) (AudioPayload, error) {
	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(strings.ToLower(contentType), "audio/") {
		audio, err := providerhttp.ReadBody(resp)
		if err != nil {
			return AudioPayload{}, err
		}
		return AudioPayload{
			Bytes:        audio,
			Format:       audioFormatFromContentType(contentType, cfg.AudioFormat),
			SampleRateHz: firstPositive(cfg.SampleRateHz, 16000),
		}, nil
	}

	payload, err := providerhttp.ReadJSONMap(resp)
	if err != nil {
		return AudioPayload{}, err
	}

	audioBase64 := providerhttp.LookupString(
		payload,
		"audioBase64",
		"audio",
		"data",
		"base64Audio",
		"data.audioBase64",
		"data.audio",
		"result.audioBase64",
		"result.audio",
	)
	if audioBase64 == "" {
		return AudioPayload{}, fmt.Errorf("tts response missing audio payload")
	}

	audio, err := base64.StdEncoding.DecodeString(audioBase64)
	if err != nil {
		return AudioPayload{}, fmt.Errorf("decode tts audio payload: %w", err)
	}
	return AudioPayload{
		Bytes:        audio,
		Format:       NormalizeAudioFormat(providerhttp.LookupString(payload, "audioFormat", "format", "data.audioFormat", "result.audioFormat", "encoding", "data.encoding", "result.encoding", "contentType")),
		SampleRateHz: firstPositive(lookupInt(payload, "sampleRateHz", "sampleRate", "data.sampleRateHz", "data.sampleRate", "result.sampleRateHz", "result.sampleRate"), cfg.SampleRateHz, 16000),
	}, nil
}

func NormalizeAudioFormat(format string) string {
	format = strings.TrimSpace(strings.ToLower(format))
	switch format {
	case "", "pcm", "pcm_s16le", "s16le", "raw":
		return "raw"
	case "wave":
		return "wav"
	case "wav", "mp3", "ogg", "opus":
		return format
	default:
		return format
	}
}

func audioFormatFromContentType(contentType string, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(normalized, "wav"):
		return "wav"
	case strings.Contains(normalized, "mpeg"), strings.Contains(normalized, "mp3"):
		return "mp3"
	case strings.Contains(normalized, "ogg"):
		return "ogg"
	case strings.Contains(normalized, "opus"):
		return "opus"
	case strings.Contains(normalized, "pcm"), strings.Contains(normalized, "raw"):
		return "raw"
	default:
		return NormalizeAudioFormat(fallback)
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func lookupInt(payload map[string]any, paths ...string) int {
	for _, path := range paths {
		value, ok := lookupValue(payload, path)
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				return typed
			}
		case int32:
			if typed > 0 {
				return int(typed)
			}
		case int64:
			if typed > 0 {
				return int(typed)
			}
		case float64:
			if typed > 0 {
				return int(typed)
			}
		}
	}
	return 0
}

func lookupValue(payload map[string]any, path string) (any, bool) {
	current := any(payload)
	for _, key := range strings.Split(path, ".") {
		next, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = next[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
