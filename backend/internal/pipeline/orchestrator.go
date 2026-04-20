package pipeline

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"bridgewithclawandfreeswitch/backend/internal/contract"
	"bridgewithclawandfreeswitch/backend/internal/openclaw"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/stt"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"
)

var ErrMissingSessionID = errors.New("missing session id")

type EventPublisher interface {
	Broadcast(eventType string, sessionID string, payload map[string]any)
}

type AudioOutputSink interface {
	Play(ctx context.Context, sessionID string, audio tts.AudioPayload) error
	Interrupt(ctx context.Context, sessionID string) error
}

type ProviderBindingsSource interface {
	SessionBindings() session.ProviderBindings
}

type StreamStartRequest struct {
	CallID string
	Caller string
	Stream session.StreamMeta
}

type Orchestrator struct {
	sessions  *session.Manager
	stt       stt.Client
	openClaw  openclaw.Client
	tts       tts.Client
	output    AudioOutputSink
	events    EventPublisher
	providers ProviderBindingsSource
}

func NewOrchestrator(
	sessions *session.Manager,
	sttClient stt.Client,
	openClawClient openclaw.Client,
	ttsClient tts.Client,
	output AudioOutputSink,
	events EventPublisher,
	providers ProviderBindingsSource,
) *Orchestrator {
	return &Orchestrator{
		sessions:  sessions,
		stt:       sttClient,
		openClaw:  openClawClient,
		tts:       ttsClient,
		output:    output,
		events:    events,
		providers: providers,
	}
}

func (o *Orchestrator) HandleStreamStart(ctx context.Context, req StreamStartRequest) (*session.Session, error) {
	bindings := session.ProviderBindings{
		STT:      "noop-stt",
		OpenClaw: "echo-openclaw",
		TTS:      "noop-tts",
	}
	if o.providers != nil {
		bindings = o.providers.SessionBindings()
	}

	current := o.sessions.CreateWithParams(session.CreateParams{
		CallID:    req.CallID,
		Caller:    req.Caller,
		Providers: bindings,
		Stream:    req.Stream,
	})

	updated, err := o.sessions.Update(current.ID, func(working *session.Session) error {
		working.State = session.StateListening
		return nil
	})
	if err != nil {
		return nil, err
	}

	sttStartedAt := time.Now().UTC()
	if err := o.stt.StartStream(ctx, updated.ID, updated.Stream); err != nil {
		_, _ = o.sessions.Close(updated.ID)
		return nil, err
	}
	o.recordProviderObservation(updated.ID, session.ProviderNameSTT, session.LogLevelInfo, session.LogSourceSTT, "stt stream started", time.Now().UTC(), time.Since(sttStartedAt))

	o.events.Broadcast(
		bridgews.EventSessionCreated,
		updated.ID,
		bridgews.SessionPayload(contract.SessionSummaryFromSession(updated)),
	)

	return updated, nil
}

func (o *Orchestrator) HandleAudioFrame(ctx context.Context, sessionID string, pcm []byte) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	current, ok := o.sessions.Get(sessionID)
	if !ok {
		return session.ErrSessionNotFound
	}

	if current.State == session.StateSpeaking {
		if err := o.Interrupt(ctx, sessionID); err != nil {
			return err
		}
	}

	if current.PendingSTTSince == nil {
		audioStartedAt := time.Now().UTC()
		if _, err := o.sessions.Update(sessionID, func(working *session.Session) error {
			working.MarkSTTPending(audioStartedAt)
			return nil
		}); err != nil {
			return err
		}
	}

	pushStartedAt := time.Now().UTC()
	if err := o.stt.PushAudio(ctx, sessionID, pcm); err != nil {
		o.recordProviderObservation(sessionID, session.ProviderNameSTT, session.LogLevelError, session.LogSourceSTT, "stt audio push failed: "+err.Error(), time.Now().UTC(), time.Since(pushStartedAt))
		return err
	}

	return nil
}

func (o *Orchestrator) HandleTranscriptPartial(_ context.Context, sessionID string, transcript string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	observedAt := time.Now().UTC()
	updated, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.State = session.StateRecognizing
		current.LastTranscript = transcript
		if latency, ok := current.ObserveSTTResult(observedAt, false); ok {
			current.UpdateProviderLatency(session.ProviderNameSTT, latency, observedAt)
		}
		return nil
	})
	if err != nil {
		return err
	}

	transcriptEntry := contract.TranscriptEntryForSession(sessionID, "partial", transcript, time.Now().UTC())
	o.events.Broadcast(bridgews.EventSessionTranscriptPartial, sessionID, bridgews.TranscriptPayload(transcriptEntry, false))
	o.events.Broadcast(
		bridgews.EventSessionUpdated,
		sessionID,
		bridgews.SessionPayload(contract.SessionSummaryFromSession(updated)),
	)

	return nil
}

func (o *Orchestrator) HandleTranscriptFinal(ctx context.Context, sessionID string, transcript string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}
	log.Printf("orchestrator transcript final start session=%s transcript=%q", sessionID, transcript)

	var (
		delta        string
		previousSent string
	)
	observedAt := time.Now().UTC()
	updated, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		previousSent = strings.TrimSpace(current.LastSentToOpenClaw)
		delta = incrementalTranscript(current.LastSentToOpenClaw, transcript)
		current.LastTranscript = transcript
		current.AppendTranscript(session.TranscriptKindFinal, transcript, observedAt)
		if latency, ok := current.ObserveSTTResult(observedAt, true); ok {
			current.UpdateProviderLatency(session.ProviderNameSTT, latency, observedAt)
			current.AppendLog(session.LogLevelInfo, session.LogSourceSTT, formatLatencyMessage("stt final transcript received", latency), observedAt)
		}
		if delta != "" {
			current.State = session.StateThinking
			current.LastSentToOpenClaw = strings.TrimSpace(transcript)
		}
		return nil
	})
	if err != nil {
		return err
	}

	transcriptEntry := contract.TranscriptEntryForSession(sessionID, "final", transcript, time.Now().UTC())
	o.events.Broadcast(bridgews.EventSessionTranscriptFinal, sessionID, bridgews.TranscriptPayload(transcriptEntry, true))
	o.events.Broadcast(
		bridgews.EventSessionUpdated,
		sessionID,
		bridgews.SessionPayload(contract.SessionSummaryFromSession(updated)),
	)
	if delta == "" {
		log.Printf("orchestrator transcript final skip openclaw session=%s previous=%q current=%q", sessionID, previousSent, strings.TrimSpace(transcript))
		return nil
	}

	log.Printf("orchestrator openclaw request session=%s previous=%q current=%q delta=%q", sessionID, previousSent, strings.TrimSpace(transcript), delta)
	openClawStartedAt := time.Now().UTC()
	reply, err := o.openClaw.Reply(ctx, sessionID, delta)
	if err != nil {
		log.Printf("orchestrator openclaw reply failed session=%s: %v", sessionID, err)
		o.recordProviderObservation(sessionID, session.ProviderNameOpenClaw, session.LogLevelError, session.LogSourceOpenClaw, "openclaw reply failed: "+err.Error(), time.Now().UTC(), time.Since(openClawStartedAt))
		return err
	}
	openClawCompletedAt := time.Now().UTC()
	openClawLatency := time.Since(openClawStartedAt)
	if _, updateErr := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.AppendTranscript(session.TranscriptKindAssistant, reply, openClawCompletedAt)
		current.UpdateProviderLatency(session.ProviderNameOpenClaw, openClawLatency, openClawCompletedAt)
		current.AppendLog(session.LogLevelInfo, session.LogSourceOpenClaw, formatLatencyMessage("openclaw reply received", openClawLatency), openClawCompletedAt)
		return nil
	}); updateErr != nil {
		log.Printf("orchestrator openclaw telemetry update failed session=%s: %v", sessionID, updateErr)
	}
	log.Printf("orchestrator openclaw reply ok session=%s delta=%q reply=%q", sessionID, delta, reply)

	ttsStartedAt := time.Now().UTC()
	audio, err := o.tts.Synthesize(ctx, sessionID, reply)
	if err != nil {
		log.Printf("orchestrator tts synth failed session=%s: %v", sessionID, err)
		o.recordProviderObservation(sessionID, session.ProviderNameTTS, session.LogLevelError, session.LogSourceTTS, "tts synthesis failed: "+err.Error(), time.Now().UTC(), time.Since(ttsStartedAt))
		return err
	}
	ttsCompletedAt := time.Now().UTC()
	ttsLatency := time.Since(ttsStartedAt)
	o.recordProviderObservation(sessionID, session.ProviderNameTTS, session.LogLevelInfo, session.LogSourceTTS, formatLatencyMessage("tts synthesis completed", ttsLatency), ttsCompletedAt, ttsLatency)
	log.Printf("orchestrator tts synth ok session=%s audio_bytes=%d format=%s sample_rate_hz=%d", sessionID, len(audio.Bytes), audio.Format, audio.SampleRateHz)

	if err := o.output.Play(ctx, sessionID, audio); err != nil {
		log.Printf("orchestrator output play failed session=%s: %v", sessionID, err)
		o.recordSessionLog(sessionID, session.LogLevelError, session.LogSourceBridge, "bridge playback failed: "+err.Error(), time.Now().UTC())
		return err
	}
	log.Printf("orchestrator output play ok session=%s audio_bytes=%d format=%s sample_rate_hz=%d", sessionID, len(audio.Bytes), audio.Format, audio.SampleRateHz)

	speaking, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.State = session.StateSpeaking
		return nil
	})
	if err != nil {
		log.Printf("orchestrator speaking state update failed session=%s: %v", sessionID, err)
		return err
	}

	o.events.Broadcast(
		bridgews.EventSessionTTSStarted,
		sessionID,
		bridgews.TTSStartedPayload(reply, len(audio.Bytes), speaking.State, speaking.UpdatedAt),
	)
	o.events.Broadcast(
		bridgews.EventSessionUpdated,
		sessionID,
		bridgews.SessionPayload(contract.SessionSummaryFromSession(speaking)),
	)
	log.Printf("orchestrator transcript final done session=%s", sessionID)

	return nil
}

func incrementalTranscript(lastSent string, transcript string) string {
	current := strings.TrimSpace(transcript)
	previous := strings.TrimSpace(lastSent)
	if current == "" {
		return ""
	}
	if previous == "" {
		return current
	}
	if current == previous {
		return ""
	}
	if strings.HasPrefix(current, previous) {
		return trimDeltaPrefix(current[len(previous):])
	}
	if end, ok := normalizedPrefixEnd(previous, current); ok {
		return trimDeltaPrefix(current[end:])
	}
	return current
}

func normalizedPrefixEnd(previous string, current string) (int, bool) {
	prevIndex := 0
	currIndex := 0

	for {
		prevIndex = skipIgnorableRunes(previous, prevIndex)
		if prevIndex >= len(previous) {
			return currIndex, true
		}

		currIndex = skipIgnorableRunes(current, currIndex)
		if currIndex >= len(current) {
			return 0, false
		}

		prevRune, prevSize := utf8.DecodeRuneInString(previous[prevIndex:])
		currRune, currSize := utf8.DecodeRuneInString(current[currIndex:])
		if prevRune != currRune {
			return 0, false
		}

		prevIndex += prevSize
		currIndex += currSize
	}
}

func skipIgnorableRunes(value string, index int) int {
	for index < len(value) {
		r, size := utf8.DecodeRuneInString(value[index:])
		if !isIgnorableTranscriptRune(r) {
			return index
		}
		index += size
	}
	return index
}

func isIgnorableTranscriptRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r)
}

func trimDeltaPrefix(delta string) string {
	trimmed := strings.TrimSpace(delta)
	trimmed = strings.TrimLeftFunc(trimmed, isIgnorableTranscriptRune)
	return strings.TrimSpace(trimmed)
}

func (o *Orchestrator) Interrupt(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	if err := o.tts.Interrupt(ctx, sessionID); err != nil {
		o.recordProviderObservation(sessionID, session.ProviderNameTTS, session.LogLevelError, session.LogSourceTTS, "tts interrupt failed: "+err.Error(), time.Now().UTC(), 0)
		return err
	}
	if err := o.output.Interrupt(ctx, sessionID); err != nil {
		o.recordSessionLog(sessionID, session.LogLevelError, session.LogSourceBridge, "bridge playback interrupt failed: "+err.Error(), time.Now().UTC())
		return err
	}

	updated, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.State = session.StateListening
		return nil
	})
	if err != nil {
		return err
	}

	o.events.Broadcast(
		bridgews.EventSessionTTSStopped,
		sessionID,
		bridgews.TTSStoppedPayload(bridgews.ReasonInterrupted, updated.State, updated.UpdatedAt),
	)
	o.events.Broadcast(
		bridgews.EventSessionUpdated,
		sessionID,
		bridgews.SessionPayload(contract.SessionSummaryFromSession(updated)),
	)

	return nil
}

func (o *Orchestrator) HandleHangup(ctx context.Context, sessionID string, reason string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	_ = o.stt.CloseStream(ctx, sessionID)
	_ = o.tts.Interrupt(ctx, sessionID)
	_ = o.output.Interrupt(ctx, sessionID)

	closed, ok := o.sessions.Close(sessionID)
	if !ok {
		return session.ErrSessionNotFound
	}

	summary := contract.SessionSummaryFromSession(closed)
	o.events.Broadcast(bridgews.EventSessionUpdated, closed.ID, bridgews.SessionPayload(summary))
	o.events.Broadcast(bridgews.EventSessionClosed, closed.ID, bridgews.ClosedPayload(summary, reason))

	return nil
}

func (o *Orchestrator) recordProviderObservation(sessionID string, provider string, level string, source string, message string, observedAt time.Time, latency time.Duration) {
	_, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		if provider != "" {
			current.UpdateProviderLatency(provider, latency, observedAt)
		}
		current.AppendLog(level, source, message, observedAt)
		return nil
	})
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		log.Printf("orchestrator provider observation update failed session=%s provider=%s: %v", sessionID, provider, err)
	}
}

func (o *Orchestrator) recordSessionLog(sessionID string, level string, source string, message string, observedAt time.Time) {
	_, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.AppendLog(level, source, message, observedAt)
		return nil
	})
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		log.Printf("orchestrator session log update failed session=%s source=%s: %v", sessionID, source, err)
	}
}

func formatLatencyMessage(prefix string, latency time.Duration) string {
	return prefix + " in " + fmtDurationMillis(latency)
}

func fmtDurationMillis(latency time.Duration) string {
	if latency <= 0 {
		return "0 ms"
	}
	return strconv.FormatInt(latency.Milliseconds(), 10) + " ms"
}

type MemoryOutputSink struct {
	mu      sync.RWMutex
	payload map[string]tts.AudioPayload
}

func NewMemoryOutputSink() *MemoryOutputSink {
	return &MemoryOutputSink{
		payload: make(map[string]tts.AudioPayload),
	}
}

func (s *MemoryOutputSink) Play(_ context.Context, sessionID string, audio tts.AudioPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.payload[sessionID] = tts.AudioPayload{
		Bytes:        append([]byte(nil), audio.Bytes...),
		Format:       audio.Format,
		SampleRateHz: audio.SampleRateHz,
	}
	return nil
}

func (s *MemoryOutputSink) Interrupt(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.payload, sessionID)
	return nil
}
