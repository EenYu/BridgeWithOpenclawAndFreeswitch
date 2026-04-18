package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"bridgewithclawandfreeswitch/backend/internal/config"

	"github.com/google/uuid"
)

type VolcengineHTTPClient struct {
	httpClient *http.Client
	config     config.ProviderConfig
}

func (c *VolcengineHTTPClient) Synthesize(ctx context.Context, sessionID string, text string) (AudioPayload, error) {
	if c.config.AppKey == "" || c.config.APIKey == "" || c.config.VoiceType == "" {
		return AudioPayload{}, fmt.Errorf("volcengine tts requires app key, api key, and voice type")
	}

	body := map[string]any{
		"app": map[string]any{
			"appid":   c.config.AppKey,
			"token":   c.config.APIKey,
			"cluster": httpFirstNonEmpty(c.config.Cluster, "volcano_tts"),
		},
		"user": map[string]any{
			"uid": httpFirstNonEmpty(c.config.UID, sessionID),
		},
		"audio": map[string]any{
			"voice_type": c.config.VoiceType,
			"encoding":   httpFirstNonEmpty(c.config.AudioFormat, "pcm"),
			"rate":       httpFirstPositive(c.config.SampleRateHz, 16000),
		},
		"request": map[string]any{
			"reqid":     uuid.NewString(),
			"text":      text,
			"operation": "query",
		},
	}
	if c.config.Model != "" {
		body["request"].(map[string]any)["model"] = c.config.Model
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return AudioPayload{}, fmt.Errorf("marshal volcengine tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return AudioPayload{}, fmt.Errorf("build volcengine tts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer;"+c.config.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AudioPayload{}, err
	}

	return decodeHTTPAudioResponse(resp, c.config)
}

func (c *VolcengineHTTPClient) Interrupt(_ context.Context, _ string) error {
	// 一次性合成接口没有持续会话，当前播放中断只需要停止下行播放，
	// 不需要再向 TTS 服务端补发额外的 interrupt 请求。
	return nil
}

func httpFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func httpFirstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
