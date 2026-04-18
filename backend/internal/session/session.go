package session

import "time"

type SessionState string

const (
	StateIdle        SessionState = "idle"
	StateListening   SessionState = "listening"
	StateRecognizing SessionState = "recognizing"
	StateThinking    SessionState = "thinking"
	StateSpeaking    SessionState = "speaking"
	StateClosed      SessionState = "closed"
)

type ProviderBindings struct {
	STT      string `json:"stt"`
	OpenClaw string `json:"openclaw"`
	TTS      string `json:"tts"`
}

type StreamMeta struct {
	Encoding     string `json:"encoding"`
	SampleRateHz int    `json:"sampleRateHz"`
	Channels     int    `json:"channels"`
}

type Session struct {
	ID             string           `json:"id"`
	CallID         string           `json:"callId"`
	Caller         string           `json:"caller"`
	State          SessionState     `json:"state"`
	StartedAt      time.Time        `json:"startedAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
	ClosedAt       *time.Time       `json:"closedAt,omitempty"`
	LastTranscript string           `json:"lastTranscript,omitempty"`
	Providers      ProviderBindings `json:"providers"`
	Stream         StreamMeta       `json:"stream"`
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}

	clone := *s
	if s.ClosedAt != nil {
		closedAt := *s.ClosedAt
		clone.ClosedAt = &closedAt
	}

	return &clone
}
