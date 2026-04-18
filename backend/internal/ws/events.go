package ws

import (
	"time"

	"bridgewithclawandfreeswitch/backend/internal/contract"
	"bridgewithclawandfreeswitch/backend/internal/session"
)

const (
	EventSessionCreated           = "session.created"
	EventSessionUpdated           = "session.updated"
	EventSessionTranscriptPartial = "session.transcript.partial"
	EventSessionTranscriptFinal   = "session.transcript.final"
	EventSessionTTSStarted        = "session.tts.started"
	EventSessionTTSStopped        = "session.tts.stopped"
	EventSessionClosed            = "session.closed"
)

const (
	DataKeySession    = "session"
	DataKeyTranscript = "transcript"
	DataKeyFinal      = "final"
	DataKeyText       = "text"
	DataKeyAudioBytes = "audioBytes"
	DataKeyReason     = "reason"
	DataKeyState      = "state"
	DataKeyUpdatedAt  = "updatedAt"
)

const (
	ReasonInterrupted           = "interrupted"
	ReasonHangup                = "hangup"
	ReasonWebSocketDisconnected = "websocket_disconnected"
)

func SessionPayload(current contract.SessionSummary) map[string]any {
	return map[string]any{
		DataKeySession: current,
	}
}

func TranscriptPayload(transcript contract.TranscriptEntry, final bool) map[string]any {
	return map[string]any{
		DataKeyTranscript: transcript,
		DataKeyFinal:      final,
	}
}

func TTSStartedPayload(text string, audioBytes int, state session.SessionState, updatedAt time.Time) map[string]any {
	return map[string]any{
		DataKeyText:       text,
		DataKeyAudioBytes: audioBytes,
		DataKeyState:      state,
		DataKeyUpdatedAt:  updatedAt,
	}
}

func TTSStoppedPayload(reason string, state session.SessionState, updatedAt time.Time) map[string]any {
	return map[string]any{
		DataKeyReason:    reason,
		DataKeyState:     state,
		DataKeyUpdatedAt: updatedAt,
	}
}

func ClosedPayload(summary contract.SessionSummary, reason string) map[string]any {
	return map[string]any{
		DataKeySession: summary,
		DataKeyReason:  reason,
	}
}
