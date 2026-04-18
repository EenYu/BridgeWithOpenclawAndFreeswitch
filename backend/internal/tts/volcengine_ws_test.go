package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
)

func TestVolcengineWSClientFetchJWTTokenCachesToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer; tts-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode sts body: %v", err)
		}
		if body["appid"] != "tts-app" {
			t.Fatalf("unexpected appid %#v", body["appid"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jwt_token":"jwt-1"}`))
	}))
	defer server.Close()

	client := NewVolcengineWSClient(config.ProviderConfig{
		AppKey:      "tts-app",
		APIKey:      "tts-token",
		STSEndpoint: server.URL,
		Timeout:     2 * time.Second,
	})

	token1, err := client.fetchJWTToken(context.Background())
	if err != nil {
		t.Fatalf("first fetchJWTToken error: %v", err)
	}
	token2, err := client.fetchJWTToken(context.Background())
	if err != nil {
		t.Fatalf("second fetchJWTToken error: %v", err)
	}

	if token1 != "jwt-1" || token2 != "jwt-1" {
		t.Fatalf("unexpected tokens %q %q", token1, token2)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one sts request, got %d", got)
	}
}
