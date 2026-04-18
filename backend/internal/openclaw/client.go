package openclaw

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/providerhttp"
)

type Client interface {
	Reply(ctx context.Context, sessionID string, transcript string) (string, error)
}

type EchoClient struct{}

func NewClient(cfg config.ProviderConfig) Client {
	if !cfg.Enabled || cfg.Endpoint == "" || cfg.Endpoint == "memory://openclaw" || cfg.Vendor == "echo-openclaw" {
		return EchoClient{}
	}
	if cfg.Vendor == "openclaw-gateway-ws" || strings.HasPrefix(strings.ToLower(cfg.Endpoint), "ws://") || strings.HasPrefix(strings.ToLower(cfg.Endpoint), "wss://") {
		return NewGatewayWSClient(cfg)
	}
	return &HTTPClient{
		httpClient: providerhttp.NewProviderClient(cfg),
		config:     cfg,
	}
}

func (EchoClient) Reply(_ context.Context, _ string, transcript string) (string, error) {
	if transcript == "" {
		return "我还没有听清，请再说一次。", nil
	}

	return "OpenClaw stub: " + transcript, nil
}

type HTTPClient struct {
	httpClient *http.Client
	config     config.ProviderConfig
}

func (c *HTTPClient) Reply(ctx context.Context, sessionID string, transcript string) (string, error) {
	req, err := providerhttp.NewJSONRequest(ctx, http.MethodPost, c.config, map[string]any{
		"sessionId":  sessionID,
		"transcript": transcript,
		"text":       transcript,
		"input":      transcript,
		"query":      transcript,
		"model":      c.config.Model,
	})
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}

	payload, err := providerhttp.ReadJSONMap(resp)
	if err != nil {
		return "", err
	}

	reply := providerhttp.LookupString(
		payload,
		"reply",
		"text",
		"output",
		"answer",
		"message",
		"data.reply",
		"data.text",
		"result.reply",
		"result.text",
	)
	if reply == "" {
		return "", fmt.Errorf("openclaw response missing reply text")
	}

	return reply, nil
}
