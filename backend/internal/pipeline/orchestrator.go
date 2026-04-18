package pipeline

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

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

	if err := o.stt.StartStream(ctx, updated.ID, updated.Stream); err != nil {
		_, _ = o.sessions.Close(updated.ID)
		return nil, err
	}

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

	return o.stt.PushAudio(ctx, sessionID, pcm)
}

func (o *Orchestrator) HandleTranscriptPartial(_ context.Context, sessionID string, transcript string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	updated, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.State = session.StateRecognizing
		current.LastTranscript = transcript
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

	thinking, err := o.sessions.Update(sessionID, func(current *session.Session) error {
		current.State = session.StateThinking
		current.LastTranscript = transcript
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
		bridgews.SessionPayload(contract.SessionSummaryFromSession(thinking)),
	)

	reply, err := o.openClaw.Reply(ctx, sessionID, transcript)
	if err != nil {
		log.Printf("orchestrator openclaw reply failed session=%s: %v", sessionID, err)
		return err
	}
	log.Printf("orchestrator openclaw reply ok session=%s reply=%q", sessionID, reply)

	audio, err := o.tts.Synthesize(ctx, sessionID, reply)
	if err != nil {
		log.Printf("orchestrator tts synth failed session=%s: %v", sessionID, err)
		return err
	}
	log.Printf("orchestrator tts synth ok session=%s audio_bytes=%d format=%s sample_rate_hz=%d", sessionID, len(audio.Bytes), audio.Format, audio.SampleRateHz)

	if err := o.output.Play(ctx, sessionID, audio); err != nil {
		log.Printf("orchestrator output play failed session=%s: %v", sessionID, err)
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

func (o *Orchestrator) Interrupt(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrMissingSessionID
	}

	if err := o.tts.Interrupt(ctx, sessionID); err != nil {
		return err
	}
	if err := o.output.Interrupt(ctx, sessionID); err != nil {
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
