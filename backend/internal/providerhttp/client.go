package providerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
)

func NewClient(timeoutMs int64) *http.Client {
	return &http.Client{Timeout: durationFromMilliseconds(timeoutMs)}
}

func NewProviderClient(cfg config.ProviderConfig) *http.Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = durationFromMilliseconds(10000)
	}
	return &http.Client{Timeout: timeout}
}

func NewJSONRequest(ctx context.Context, method string, cfg config.ProviderConfig, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal provider payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, cfg)
	return req, nil
}

func applyAuth(req *http.Request, cfg config.ProviderConfig) {
	if cfg.APIKey == "" {
		return
	}

	switch strings.ToLower(cfg.AuthType) {
	case "", "bearer":
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	case "header":
		headerName := cfg.APIKeyHeader
		if headerName == "" {
			headerName = "X-API-Key"
		}
		req.Header.Set(headerName, cfg.APIKey)
	}
}

func ReadBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func ReadJSONMap(resp *http.Response) (map[string]any, error) {
	body, err := ReadBody(resp)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return map[string]any{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode provider json: %w", err)
	}
	return payload, nil
}

func LookupString(payload map[string]any, paths ...string) string {
	for _, path := range paths {
		if value, ok := lookupValue(payload, path); ok {
			if text, ok := value.(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}

func LookupBool(payload map[string]any, paths ...string) (bool, bool) {
	for _, path := range paths {
		if value, ok := lookupValue(payload, path); ok {
			if flag, ok := value.(bool); ok {
				return flag, true
			}
		}
	}
	return false, false
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

func durationFromMilliseconds(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
