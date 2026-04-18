package stt

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/providerhttp"
	"bridgewithclawandfreeswitch/backend/internal/session"
)

type Client interface {
	StartStream(ctx context.Context, sessionID string, meta session.StreamMeta) error
	PushAudio(ctx context.Context, sessionID string, pcm []byte) error
	CloseStream(ctx context.Context, sessionID string) error
}

type TranscriptHandler func(ctx context.Context, sessionID string, transcript string, final bool) error

type transcriptAware interface {
	SetTranscriptHandler(handler TranscriptHandler)
}

type NoopClient struct{}

func (NoopClient) StartStream(_ context.Context, _ string, _ session.StreamMeta) error {
	return nil
}

func (NoopClient) PushAudio(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (NoopClient) CloseStream(_ context.Context, _ string) error {
	return nil
}

type HTTPClient struct {
	mu         sync.RWMutex
	httpClient *http.Client
	config     config.ProviderConfig
	handler    TranscriptHandler
	streams    map[string]session.StreamMeta
}

func NewClient(cfg config.ProviderConfig) Client {
	if !cfg.Enabled || cfg.Endpoint == "" || cfg.Endpoint == "memory://stt" || cfg.Vendor == "noop-stt" {
		return NoopClient{}
	}
	if cfg.Vendor == "volcengine-stt-ws" || strings.Contains(cfg.Endpoint, "/api/v3/sauc/") {
		return NewVolcengineWSClient(cfg)
	}
	return &HTTPClient{
		httpClient: providerhttp.NewProviderClient(cfg),
		config:     cfg,
		streams:    make(map[string]session.StreamMeta),
	}
}

func AttachTranscriptHandler(client Client, handler TranscriptHandler) {
	if aware, ok := client.(transcriptAware); ok {
		aware.SetTranscriptHandler(handler)
	}
}

func (c *HTTPClient) SetTranscriptHandler(handler TranscriptHandler) {
	c.handler = handler
}

func (c *HTTPClient) StartStream(ctx context.Context, sessionID string, meta session.StreamMeta) error {
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, c.buildStreamPayload(sessionID, "start", meta, nil))
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}

	payload, err := providerhttp.ReadJSONMap(resp)
	if err != nil {
		return err
	}

	c.rememberStream(sessionID, meta)
	return c.publishTranscript(ctx, sessionID, payload)
}

func (c *HTTPClient) PushAudio(ctx context.Context, sessionID string, pcm []byte) error {
	meta := c.streamMeta(sessionID)
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, c.buildStreamPayload(sessionID, "audio", meta, map[string]any{
		"audioBase64": base64.StdEncoding.EncodeToString(pcm),
	}))
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}

	payload, err := providerhttp.ReadJSONMap(resp)
	if err != nil {
		return err
	}

	return c.publishTranscript(ctx, sessionID, payload)
}

func (c *HTTPClient) CloseStream(ctx context.Context, sessionID string) error {
	meta := c.streamMeta(sessionID)
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, c.buildStreamPayload(sessionID, "close", meta, nil))
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}

	payload, err := providerhttp.ReadJSONMap(resp)
	if err != nil {
		return err
	}

	c.forgetStream(sessionID)
	return c.publishTranscript(ctx, sessionID, payload)
}

func (c *HTTPClient) publishTranscript(ctx context.Context, sessionID string, payload map[string]any) error {
	if c.handler == nil {
		return nil
	}

	transcript := providerhttp.LookupString(
		payload,
		"transcript",
		"text",
		"data.transcript",
		"data.text",
		"result.transcript",
		"result.text",
	)
	if transcript == "" {
		return nil
	}

	final, ok := providerhttp.LookupBool(payload, "final", "data.final", "result.final")
	if !ok {
		final = true
	}
	return c.handler(ctx, sessionID, transcript, final)
}

func (c *HTTPClient) buildStreamPayload(sessionID string, event string, meta session.StreamMeta, extra map[string]any) map[string]any {
	payload := map[string]any{
		"sessionId": sessionID,
		"event":     event,
		"model":     c.config.Model,
	}
	if c.config.Transport != "" {
		payload["transport"] = c.config.Transport
	}

	// provider 契约仍在联调阶段，同时保留扁平字段和 stream 对象，
	// 这样后续可以按真实上游协议裁剪，而不必回滚当前测试链路。
	stream := map[string]any{}
	if meta.Encoding != "" {
		payload["encoding"] = meta.Encoding
		stream["encoding"] = meta.Encoding
	}
	if meta.SampleRateHz > 0 {
		payload["sampleRateHz"] = meta.SampleRateHz
		stream["sampleRateHz"] = meta.SampleRateHz
	}
	if meta.Channels > 0 {
		payload["channels"] = meta.Channels
		stream["channels"] = meta.Channels
	}
	if len(stream) > 0 {
		payload["stream"] = stream
	}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func (c *HTTPClient) streamMeta(sessionID string) session.StreamMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()

	meta, ok := c.streams[sessionID]
	if !ok {
		return session.StreamMeta{Encoding: "pcm_s16le", SampleRateHz: 16000, Channels: 1}
	}
	return meta
}

func (c *HTTPClient) rememberStream(sessionID string, meta session.StreamMeta) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.streams[sessionID] = meta
}

func (c *HTTPClient) forgetStream(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.streams, sessionID)
}
