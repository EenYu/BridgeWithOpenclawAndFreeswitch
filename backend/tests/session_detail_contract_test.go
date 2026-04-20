package tests

import (
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/contract"
	"bridgewithclawandfreeswitch/backend/internal/session"
)

func TestSessionDetailFromSessionIncludesTelemetryFields(t *testing.T) {
	now := time.Now().UTC()
	current := &session.Session{
		ID:             "sess-contract",
		CallID:         "call-contract",
		Caller:         "alice",
		State:          session.StateSpeaking,
		StartedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
		LastTranscript: "need help",
		Providers: session.ProviderBindings{
			STT:      "volcengine-stt-ws",
			OpenClaw: "openclaw-gateway-ws",
			TTS:      "volcengine-tts-ws-v3",
		},
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
		Transcripts: []session.TranscriptEntry{
			{ID: "assistant-1", Text: "reply text", Kind: session.TranscriptKindAssistant, CreatedAt: now},
			{ID: "final-1", Text: "need help", Kind: session.TranscriptKindFinal, CreatedAt: now.Add(-time.Second)},
		},
		RecentLogs: []session.SessionLogEntry{
			{ID: "log-1", Level: session.LogLevelInfo, Message: "openclaw reply received in 42 ms", Source: session.LogSourceOpenClaw, CreatedAt: now},
		},
		ProviderLatencies: []session.ProviderLatency{
			{Provider: session.ProviderNameSTT, LatencyMs: 1200, UpdatedAt: now.Add(-2 * time.Second)},
			{Provider: session.ProviderNameOpenClaw, LatencyMs: 42, UpdatedAt: now},
		},
	}

	detail := contract.SessionDetailFromSession(current, "fs1-local-bridge")

	if detail.BridgeNode != "fs1-local-bridge" {
		t.Fatalf("unexpected bridge node %q", detail.BridgeNode)
	}
	if len(detail.Transcripts) != 2 {
		t.Fatalf("expected 2 transcripts, got %d", len(detail.Transcripts))
	}
	if detail.Transcripts[0].Kind != session.TranscriptKindAssistant {
		t.Fatalf("unexpected first transcript %+v", detail.Transcripts[0])
	}
	if len(detail.RecentLogs) != 1 || detail.RecentLogs[0].Source != session.LogSourceOpenClaw {
		t.Fatalf("unexpected recent logs %+v", detail.RecentLogs)
	}
	if len(detail.ProviderLatencies) != 2 {
		t.Fatalf("expected 2 provider latencies, got %d", len(detail.ProviderLatencies))
	}
	if detail.ProviderLatencies[0].Provider != session.ProviderNameSTT && detail.ProviderLatencies[1].Provider != session.ProviderNameSTT {
		t.Fatalf("expected stt provider latency in %+v", detail.ProviderLatencies)
	}
	if detail.Providers.STT != "volcengine-stt-ws" || detail.Stream.SampleRateHz != 16000 {
		t.Fatalf("unexpected summary telemetry %+v", detail)
	}
}
