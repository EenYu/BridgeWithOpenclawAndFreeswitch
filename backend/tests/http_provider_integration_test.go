package tests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/openclaw"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/runtime"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/stt"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"
)

func TestHTTPProvidersDriveTranscriptReplyAndTTS(t *testing.T) {
	var sttEvents []map[string]any
	var openClawRequests []map[string]any
	var ttsRequests []map[string]any

	sttServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode stt request: %v", err)
		}
		sttEvents = append(sttEvents, payload)

		w.Header().Set("Content-Type", "application/json")
		if payload["event"] == "audio" {
			_, _ = w.Write([]byte(`{"transcript":"need help","final":true}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer sttServer.Close()

	openClawServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode openclaw request: %v", err)
		}
		openClawRequests = append(openClawRequests, payload)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reply":"agent reply"}`))
	}))
	defer openClawServer.Close()

	ttsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode tts request: %v", err)
		}
		ttsRequests = append(ttsRequests, payload)

		w.Header().Set("Content-Type", "application/json")
		audio := base64.StdEncoding.EncodeToString([]byte("audio:agent reply"))
		_, _ = w.Write([]byte(`{"audioBase64":"` + audio + `"}`))
	}))
	defer ttsServer.Close()

	providers := config.Providers{
		STT: config.ProviderConfig{
			Vendor:    "http-stt",
			Endpoint:  sttServer.URL,
			Enabled:   true,
			Transport: "http-json-events",
			AuthType:  "none",
		},
		OpenClaw: config.ProviderConfig{
			Vendor:   "http-openclaw",
			Endpoint: openClawServer.URL,
			Enabled:  true,
			AuthType: "none",
		},
		TTS: config.ProviderConfig{
			Vendor:   "http-tts",
			Endpoint: ttsServer.URL,
			Enabled:  true,
			AuthType: "none",
		},
	}

	sessions := session.NewManager()
	providerStore := config.NewProviderStore(providers)
	sttClient, openClawClient, ttsClient := runtime.BuildProviderClients(providers)
	outputSink := &fakeOutputSink{}
	events := &fakeEventPublisher{}

	orchestrator := pipeline.NewOrchestrator(
		sessions,
		sttClient,
		openClawClient,
		ttsClient,
		outputSink,
		events,
		providerStore,
	)
	stt.AttachTranscriptHandler(sttClient, func(ctx context.Context, sessionID string, transcript string, final bool) error {
		if final {
			return orchestrator.HandleTranscriptFinal(ctx, sessionID, transcript)
		}
		return orchestrator.HandleTranscriptPartial(ctx, sessionID, transcript)
	})

	created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
		CallID: "call-http-1",
		Caller: "alice",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	})
	if err != nil {
		t.Fatalf("HandleStreamStart returned error: %v", err)
	}

	if err := orchestrator.HandleAudioFrame(context.Background(), created.ID, []byte{1, 2, 3}); err != nil {
		t.Fatalf("HandleAudioFrame returned error: %v", err)
	}

	if len(sttEvents) != 2 {
		t.Fatalf("expected 2 stt requests, got %d", len(sttEvents))
	}
	if len(openClawRequests) != 1 {
		t.Fatalf("expected 1 openclaw request, got %d", len(openClawRequests))
	}
	if len(ttsRequests) != 1 {
		t.Fatalf("expected 1 tts request, got %d", len(ttsRequests))
	}
	if len(outputSink.playCalls) != 1 {
		t.Fatalf("expected 1 playback call, got %d", len(outputSink.playCalls))
	}
	if got := sttEvents[0]["event"]; got != "start" {
		t.Fatalf("expected first stt event to be start, got %#v", got)
	}
	if got := sttEvents[1]["event"]; got != "audio" {
		t.Fatalf("expected second stt event to be audio, got %#v", got)
	}
	if got := sttEvents[0]["transport"]; got != "http-json-events" {
		t.Fatalf("expected transport to reach stt start request, got %#v", got)
	}
	if got := sttEvents[0]["encoding"]; got != "pcm_s16le" {
		t.Fatalf("expected encoding to reach stt start request, got %#v", got)
	}
	if got := sttEvents[0]["sampleRateHz"]; got != float64(16000) {
		t.Fatalf("expected sampleRateHz to reach stt start request, got %#v", got)
	}
	if got := sttEvents[0]["channels"]; got != float64(1) {
		t.Fatalf("expected channels to reach stt start request, got %#v", got)
	}
	if got := string(outputSink.playCalls[0].pcm); got != "audio:agent reply" {
		t.Fatalf("unexpected synthesized audio payload %q", got)
	}

	current, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatal("expected session to exist")
	}
	if current.State != session.StateSpeaking {
		t.Fatalf("expected speaking state, got %s", current.State)
	}
	if current.Providers.STT != "http-stt" || current.Providers.OpenClaw != "http-openclaw" || current.Providers.TTS != "http-tts" {
		t.Fatalf("unexpected provider bindings: %+v", current.Providers)
	}

	assertEventTypes(t, events.events,
		bridgews.EventSessionCreated,
		bridgews.EventSessionTranscriptFinal,
		bridgews.EventSessionUpdated,
		bridgews.EventSessionTTSStarted,
		bridgews.EventSessionUpdated,
	)
	assertTranscriptPayload(t, events.events[1], "need help", "final", true)
	assertTTSStartedPayload(t, events.events[3], session.StateSpeaking)
}

func TestNoopClientsRemainFallbackWhenProviderDisabled(t *testing.T) {
	sttClient, openClawClient, ttsClient := runtime.BuildProviderClients(config.Providers{
		STT:      config.ProviderConfig{Vendor: "http-stt", Endpoint: "", Enabled: false},
		OpenClaw: config.ProviderConfig{Vendor: "http-openclaw", Endpoint: "", Enabled: false},
		TTS:      config.ProviderConfig{Vendor: "http-tts", Endpoint: "", Enabled: false},
	})

	if _, ok := sttClient.(stt.NoopClient); !ok {
		t.Fatalf("expected noop stt fallback, got %T", sttClient)
	}
	if _, ok := openClawClient.(openclaw.EchoClient); !ok {
		t.Fatalf("expected echo openclaw fallback, got %T", openClawClient)
	}
	if _, ok := ttsClient.(tts.NoopClient); !ok {
		t.Fatalf("expected noop tts fallback, got %T", ttsClient)
	}
}
