package session

import (
	"fmt"
	"time"
)

type SessionState string

const (
	StateIdle        SessionState = "idle"
	StateListening   SessionState = "listening"
	StateRecognizing SessionState = "recognizing"
	StateThinking    SessionState = "thinking"
	StateSpeaking    SessionState = "speaking"
	StateClosed      SessionState = "closed"
)

const (
	TranscriptKindPartial   = "partial"
	TranscriptKindFinal     = "final"
	TranscriptKindAssistant = "assistant"

	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"

	LogSourceBridge   = "bridge"
	LogSourceFreeSW   = "freeswitch"
	LogSourceSTT      = "stt"
	LogSourceOpenClaw = "openclaw"
	LogSourceTTS      = "tts"
	LogSourceSystem   = "system"

	ProviderNameSTT      = "stt"
	ProviderNameOpenClaw = "openclaw"
	ProviderNameTTS      = "tts"
)

const (
	maxStoredTranscripts = 50
	maxStoredLogs        = 50
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

type TranscriptEntry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

type ProviderLatency struct {
	Provider  string    `json:"provider"`
	LatencyMs int       `json:"latencyMs"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SessionLogEntry struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
}

type Session struct {
	ID             string       `json:"id"`
	CallID         string       `json:"callId"`
	Caller         string       `json:"caller"`
	State          SessionState `json:"state"`
	StartedAt      time.Time    `json:"startedAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	ClosedAt       *time.Time   `json:"closedAt,omitempty"`
	LastTranscript string       `json:"lastTranscript,omitempty"`

	Providers ProviderBindings `json:"providers"`
	Stream    StreamMeta       `json:"stream"`

	Transcripts       []TranscriptEntry `json:"transcripts,omitempty"`
	ProviderLatencies []ProviderLatency `json:"providerLatencies,omitempty"`
	RecentLogs        []SessionLogEntry `json:"recentLogs,omitempty"`

	// LastSentToOpenClaw 只用于后端增量裁剪，避免累计 final 重复送入对话模型。
	LastSentToOpenClaw string `json:"-"`
	// PendingSTTSince 用于估算本轮语音片段从首次入流到识别结果产出的耗时。
	PendingSTTSince *time.Time `json:"-"`
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
	if s.PendingSTTSince != nil {
		pending := *s.PendingSTTSince
		clone.PendingSTTSince = &pending
	}
	clone.Transcripts = append([]TranscriptEntry(nil), s.Transcripts...)
	clone.ProviderLatencies = append([]ProviderLatency(nil), s.ProviderLatencies...)
	clone.RecentLogs = append([]SessionLogEntry(nil), s.RecentLogs...)

	return &clone
}

func (s *Session) AppendTranscript(kind string, text string, createdAt time.Time) {
	if text == "" {
		return
	}

	s.Transcripts = prependBounded(s.Transcripts, TranscriptEntry{
		ID:        fmt.Sprintf("%s-%s-%d", s.ID, kind, createdAt.UnixNano()),
		Text:      text,
		Kind:      kind,
		CreatedAt: createdAt,
	}, maxStoredTranscripts)
}

func (s *Session) AppendLog(level string, source string, message string, createdAt time.Time) {
	if message == "" {
		return
	}

	s.RecentLogs = prependBounded(s.RecentLogs, SessionLogEntry{
		ID:        fmt.Sprintf("%s-%s-%d", s.ID, source, createdAt.UnixNano()),
		Level:     level,
		Message:   message,
		Source:    source,
		CreatedAt: createdAt,
	}, maxStoredLogs)
}

func (s *Session) UpdateProviderLatency(provider string, latency time.Duration, updatedAt time.Time) {
	if provider == "" {
		return
	}

	entry := ProviderLatency{
		Provider:  provider,
		LatencyMs: durationToMilliseconds(latency),
		UpdatedAt: updatedAt,
	}

	for index := range s.ProviderLatencies {
		if s.ProviderLatencies[index].Provider == provider {
			s.ProviderLatencies[index] = entry
			return
		}
	}

	s.ProviderLatencies = append(s.ProviderLatencies, entry)
}

func (s *Session) MarkSTTPending(startedAt time.Time) {
	if s.PendingSTTSince != nil {
		return
	}
	timestamp := startedAt.UTC()
	s.PendingSTTSince = &timestamp
}

func (s *Session) ObserveSTTResult(observedAt time.Time, final bool) (time.Duration, bool) {
	if s.PendingSTTSince == nil {
		return 0, false
	}

	latency := observedAt.Sub(*s.PendingSTTSince)
	if final {
		s.PendingSTTSince = nil
	}
	return latency, true
}

func prependBounded[T any](items []T, next T, limit int) []T {
	items = append([]T{next}, items...)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func durationToMilliseconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int(value.Milliseconds())
}
