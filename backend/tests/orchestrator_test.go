package tests

import (
	"context"
	"testing"

	"bridgewithclawandfreeswitch/backend/internal/contract"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"
)

type fakeSTTClient struct {
	startCalls []streamStartCall
	pushCalls  []audioPush
	closeCalls []string
	startErr   error
	pushErr    error
	closeErr   error
}

type audioPush struct {
	sessionID    string
	pcm          []byte
	format       string
	sampleRateHz int
}

type streamStartCall struct {
	sessionID string
	meta      session.StreamMeta
}

func (f *fakeSTTClient) StartStream(_ context.Context, sessionID string, meta session.StreamMeta) error {
	f.startCalls = append(f.startCalls, streamStartCall{
		sessionID: sessionID,
		meta:      meta,
	})
	return f.startErr
}

func (f *fakeSTTClient) PushAudio(_ context.Context, sessionID string, pcm []byte) error {
	f.pushCalls = append(f.pushCalls, audioPush{
		sessionID: sessionID,
		pcm:       append([]byte(nil), pcm...),
	})
	return f.pushErr
}

func (f *fakeSTTClient) CloseStream(_ context.Context, sessionID string) error {
	f.closeCalls = append(f.closeCalls, sessionID)
	return f.closeErr
}

type fakeOpenClawClient struct {
	replies  []openClawRequest
	replyErr error
}

type openClawRequest struct {
	sessionID  string
	transcript string
}

func (f *fakeOpenClawClient) Reply(_ context.Context, sessionID string, transcript string) (string, error) {
	f.replies = append(f.replies, openClawRequest{
		sessionID:  sessionID,
		transcript: transcript,
	})
	if f.replyErr != nil {
		return "", f.replyErr
	}
	return "reply:" + transcript, nil
}

type fakeTTSClient struct {
	synthCalls     []ttsRequest
	interruptCalls []string
	synthErr       error
	interruptErr   error
}

type ttsRequest struct {
	sessionID string
	text      string
}

func (f *fakeTTSClient) Synthesize(_ context.Context, sessionID string, text string) (tts.AudioPayload, error) {
	f.synthCalls = append(f.synthCalls, ttsRequest{
		sessionID: sessionID,
		text:      text,
	})
	if f.synthErr != nil {
		return tts.AudioPayload{}, f.synthErr
	}
	return tts.AudioPayload{
		Bytes:        []byte("audio:" + text),
		Format:       "wav",
		SampleRateHz: 8000,
	}, nil
}

func (f *fakeTTSClient) Interrupt(_ context.Context, sessionID string) error {
	f.interruptCalls = append(f.interruptCalls, sessionID)
	return f.interruptErr
}

type fakeOutputSink struct {
	playCalls      []audioPush
	interruptCalls []string
}

func (f *fakeOutputSink) Play(_ context.Context, sessionID string, audio tts.AudioPayload) error {
	f.playCalls = append(f.playCalls, audioPush{
		sessionID: sessionID,
		pcm:       append([]byte(nil), audio.Bytes...),
	})
	return nil
}

func (f *fakeOutputSink) Interrupt(_ context.Context, sessionID string) error {
	f.interruptCalls = append(f.interruptCalls, sessionID)
	return nil
}

type fakeEventPublisher struct {
	events []publishedEvent
}

type publishedEvent struct {
	eventType string
	sessionID string
	data      map[string]any
}

func (f *fakeEventPublisher) Broadcast(eventType string, sessionID string, payload map[string]any) {
	f.events = append(f.events, publishedEvent{
		eventType: eventType,
		sessionID: sessionID,
		data:      payload,
	})
}

type fakeProviderBindings struct{}

func (fakeProviderBindings) SessionBindings() session.ProviderBindings {
	return session.ProviderBindings{
		STT:      "test-stt",
		OpenClaw: "test-openclaw",
		TTS:      "test-tts",
	}
}

func TestOrchestratorStreamStartAndPartialTranscript(t *testing.T) {
	orchestrator, sttClient, _, _, _, events, sessions := newTestOrchestrator()

	created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
		CallID: "call-100",
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

	if created.State != session.StateListening {
		t.Fatalf("expected listening state, got %s", created.State)
	}
	if len(sttClient.startCalls) != 1 {
		t.Fatalf("expected 1 stt start call, got %d", len(sttClient.startCalls))
	}
	if sttClient.startCalls[0].meta.Encoding != "pcm_s16le" {
		t.Fatalf("expected stream meta to reach stt start call, got %+v", sttClient.startCalls[0].meta)
	}
	if created.Providers.STT != "test-stt" {
		t.Fatalf("expected provider bindings to come from store, got %+v", created.Providers)
	}

	if err := orchestrator.HandleTranscriptPartial(context.Background(), created.ID, "hello"); err != nil {
		t.Fatalf("HandleTranscriptPartial returned error: %v", err)
	}

	current, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatal("expected session to still exist")
	}
	if current.State != session.StateRecognizing {
		t.Fatalf("expected recognizing state, got %s", current.State)
	}
	if current.LastTranscript != "hello" {
		t.Fatalf("expected last transcript to be updated, got %q", current.LastTranscript)
	}

	assertEventTypes(t, events.events,
		bridgews.EventSessionCreated,
		bridgews.EventSessionTranscriptPartial,
		bridgews.EventSessionUpdated,
	)
	assertSessionSummaryPayload(t, events.events[0], created.ID, "call-100", session.StateListening)
	assertTranscriptPayload(t, events.events[1], "hello", "partial", false)
	assertSessionSummaryPayload(t, events.events[2], created.ID, "call-100", session.StateRecognizing)
}

func TestOrchestratorFinalTranscriptTriggersOpenClawAndTTS(t *testing.T) {
	orchestrator, _, openClawClient, ttsClient, outputSink, events, sessions := newTestOrchestrator()

	created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
		CallID: "call-200",
		Caller: "bob",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	})
	if err != nil {
		t.Fatalf("HandleStreamStart returned error: %v", err)
	}

	if err := orchestrator.HandleTranscriptFinal(context.Background(), created.ID, "need help"); err != nil {
		t.Fatalf("HandleTranscriptFinal returned error: %v", err)
	}

	if len(openClawClient.replies) != 1 {
		t.Fatalf("expected 1 openclaw call, got %d", len(openClawClient.replies))
	}
	if openClawClient.replies[0].transcript != "need help" {
		t.Fatalf("expected transcript to reach openclaw, got %q", openClawClient.replies[0].transcript)
	}

	if len(ttsClient.synthCalls) != 1 {
		t.Fatalf("expected 1 tts synthesis call, got %d", len(ttsClient.synthCalls))
	}
	if ttsClient.synthCalls[0].text != "reply:need help" {
		t.Fatalf("expected synthesized text to use openclaw reply, got %q", ttsClient.synthCalls[0].text)
	}
	if len(outputSink.playCalls) != 1 {
		t.Fatalf("expected 1 output playback call, got %d", len(outputSink.playCalls))
	}

	current, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatal("expected session to exist after synthesis")
	}
	if current.State != session.StateSpeaking {
		t.Fatalf("expected speaking state, got %s", current.State)
	}
	if current.LastSentToOpenClaw != "need help" {
		t.Fatalf("expected last sent transcript to be tracked, got %q", current.LastSentToOpenClaw)
	}
	if len(current.Transcripts) != 2 {
		t.Fatalf("expected 2 transcript timeline entries, got %d", len(current.Transcripts))
	}
	if current.Transcripts[0].Kind != session.TranscriptKindAssistant || current.Transcripts[0].Text != "reply:need help" {
		t.Fatalf("unexpected assistant transcript entry %+v", current.Transcripts[0])
	}
	if current.Transcripts[1].Kind != session.TranscriptKindFinal || current.Transcripts[1].Text != "need help" {
		t.Fatalf("unexpected final transcript entry %+v", current.Transcripts[1])
	}
	if len(current.ProviderLatencies) != 3 {
		t.Fatalf("expected 3 provider latency entries, got %d", len(current.ProviderLatencies))
	}
	assertProviderLatencyPresent(t, current, session.ProviderNameSTT)
	assertProviderLatencyPresent(t, current, session.ProviderNameOpenClaw)
	assertProviderLatencyPresent(t, current, session.ProviderNameTTS)
	if len(current.RecentLogs) < 3 {
		t.Fatalf("expected at least 3 recent logs, got %d", len(current.RecentLogs))
	}
	assertSessionLogSourcePresent(t, current, session.LogSourceSTT, session.LogLevelInfo)
	assertSessionLogSourcePresent(t, current, session.LogSourceOpenClaw, session.LogLevelInfo)
	assertSessionLogSourcePresent(t, current, session.LogSourceTTS, session.LogLevelInfo)

	assertEventTypes(t, events.events,
		bridgews.EventSessionCreated,
		bridgews.EventSessionTranscriptFinal,
		bridgews.EventSessionUpdated,
		bridgews.EventSessionTTSStarted,
		bridgews.EventSessionUpdated,
	)
	assertTranscriptPayload(t, events.events[1], "need help", "final", true)
	assertSessionSummaryPayload(t, events.events[2], created.ID, "call-200", session.StateThinking)
	assertTTSStartedPayload(t, events.events[3], session.StateSpeaking)
	assertSessionSummaryPayload(t, events.events[4], created.ID, "call-200", session.StateSpeaking)
}

func TestOrchestratorFinalTranscriptSendsOnlyIncrementalText(t *testing.T) {
	testCases := []struct {
		name          string
		firstFinal    string
		secondFinal   string
		expectedDelta string
	}{
		{
			name:          "accumulated final",
			firstFinal:    "need help",
			secondFinal:   "need help with billing",
			expectedDelta: "with billing",
		},
		{
			name:          "definite utterance accumulation",
			firstFinal:    "Welcome to free switch",
			secondFinal:   "Welcome to free switch the future of voice",
			expectedDelta: "the future of voice",
		},
		{
			name:          "scheduled silence final accumulation",
			firstFinal:    "Hello",
			secondFinal:   "Hello I need support",
			expectedDelta: "I need support",
		},
		{
			name:          "trim leading punctuation from delta",
			firstFinal:    "\u4f60\u597d\uff0c\u5e2e\u6211\u67e5\u4e00\u4e0b\u5317\u4eac\u4eca\u5929\u7684\u5929\u6c14",
			secondFinal:   "\u4f60\u597d\uff0c\u5e2e\u6211\u67e5\u4e00\u4e0b\u5317\u4eac\u4eca\u5929\u7684\u5929\u6c14\u3002\u4e0b\u5348\u4f1a\u4e0d\u4f1a\u4e0b\u96e8",
			expectedDelta: "\u4e0b\u5348\u4f1a\u4e0d\u4f1a\u4e0b\u96e8",
		},
		{
			name:          "ignore punctuation rewrite inside accumulated prefix",
			firstFinal:    "\u60a8\u597d\uff0ccan you\u76f4\u63a5\u4e00\u70b9\u3002\u770b\u770b\u4eca\u5929\u4e0b\u5348\u5317\u4eac\u7684\u5929\u6c14",
			secondFinal:   "\u60a8\u597d\uff0ccan you\u76f4\u63a5\u4e00\u70b9\u770b\u770b\u4eca\u5929\u4e0b\u5348\u5317\u4eac\u7684\u5929\u6c14\u3002\u4f60\u597d",
			expectedDelta: "\u4f60\u597d",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			orchestrator, _, openClawClient, ttsClient, outputSink, _, sessions := newTestOrchestrator()

			created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
				CallID: "call-incremental-" + testCase.name,
				Caller: "erin",
				Stream: session.StreamMeta{
					Encoding:     "pcm_s16le",
					SampleRateHz: 16000,
					Channels:     1,
				},
			})
			if err != nil {
				t.Fatalf("HandleStreamStart returned error: %v", err)
			}

			if err := orchestrator.HandleTranscriptFinal(context.Background(), created.ID, testCase.firstFinal); err != nil {
				t.Fatalf("first HandleTranscriptFinal returned error: %v", err)
			}
			if err := orchestrator.HandleTranscriptFinal(context.Background(), created.ID, testCase.secondFinal); err != nil {
				t.Fatalf("second HandleTranscriptFinal returned error: %v", err)
			}

			if len(openClawClient.replies) != 2 {
				t.Fatalf("expected 2 openclaw calls, got %d", len(openClawClient.replies))
			}
			if openClawClient.replies[0].transcript != testCase.firstFinal {
				t.Fatalf("unexpected first openclaw transcript %q", openClawClient.replies[0].transcript)
			}
			if openClawClient.replies[1].transcript != testCase.expectedDelta {
				t.Fatalf("unexpected incremental transcript %q", openClawClient.replies[1].transcript)
			}
			if len(ttsClient.synthCalls) != 2 {
				t.Fatalf("expected 2 tts synthesis calls, got %d", len(ttsClient.synthCalls))
			}
			if ttsClient.synthCalls[1].text != "reply:"+testCase.expectedDelta {
				t.Fatalf("unexpected second synthesized text %q", ttsClient.synthCalls[1].text)
			}
			if len(outputSink.playCalls) != 2 {
				t.Fatalf("expected 2 output playback calls, got %d", len(outputSink.playCalls))
			}

			current, ok := sessions.Get(created.ID)
			if !ok {
				t.Fatal("expected session to still exist")
			}
			if current.LastSentToOpenClaw != testCase.secondFinal {
				t.Fatalf("expected tracked transcript %q, got %q", testCase.secondFinal, current.LastSentToOpenClaw)
			}
		})
	}
}

func TestOrchestratorProviderFailuresRecordSessionBreakpoints(t *testing.T) {
	t.Run("stt push failure", func(t *testing.T) {
		orchestrator, sttClient, _, _, _, _, sessions := newTestOrchestrator()
		sttClient.pushErr = context.DeadlineExceeded

		created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
			CallID: "call-stt-failure",
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

		if err := orchestrator.HandleAudioFrame(context.Background(), created.ID, []byte{1, 2, 3}); err == nil {
			t.Fatal("expected HandleAudioFrame to fail")
		}

		current, ok := sessions.Get(created.ID)
		if !ok {
			t.Fatal("expected session to exist")
		}
		assertSessionLogSourcePresent(t, current, session.LogSourceSTT, session.LogLevelError)
		assertProviderLatencyPresent(t, current, session.ProviderNameSTT)
	})

	t.Run("openclaw failure", func(t *testing.T) {
		orchestrator, _, openClawClient, _, _, _, sessions := newTestOrchestrator()
		openClawClient.replyErr = context.DeadlineExceeded

		created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
			CallID: "call-openclaw-failure",
			Caller: "bob",
			Stream: session.StreamMeta{
				Encoding:     "pcm_s16le",
				SampleRateHz: 16000,
				Channels:     1,
			},
		})
		if err != nil {
			t.Fatalf("HandleStreamStart returned error: %v", err)
		}

		if err := orchestrator.HandleTranscriptFinal(context.Background(), created.ID, "need help"); err == nil {
			t.Fatal("expected HandleTranscriptFinal to fail")
		}

		current, ok := sessions.Get(created.ID)
		if !ok {
			t.Fatal("expected session to exist")
		}
		assertSessionLogSourcePresent(t, current, session.LogSourceOpenClaw, session.LogLevelError)
		assertProviderLatencyPresent(t, current, session.ProviderNameOpenClaw)
	})

	t.Run("tts failure", func(t *testing.T) {
		orchestrator, _, _, ttsClient, _, _, sessions := newTestOrchestrator()
		ttsClient.synthErr = context.DeadlineExceeded

		created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
			CallID: "call-tts-failure",
			Caller: "carol",
			Stream: session.StreamMeta{
				Encoding:     "pcm_s16le",
				SampleRateHz: 16000,
				Channels:     1,
			},
		})
		if err != nil {
			t.Fatalf("HandleStreamStart returned error: %v", err)
		}

		if err := orchestrator.HandleTranscriptFinal(context.Background(), created.ID, "need help"); err == nil {
			t.Fatal("expected HandleTranscriptFinal to fail")
		}

		current, ok := sessions.Get(created.ID)
		if !ok {
			t.Fatal("expected session to exist")
		}
		assertSessionLogSourcePresent(t, current, session.LogSourceTTS, session.LogLevelError)
		assertProviderLatencyPresent(t, current, session.ProviderNameTTS)
	})
}

func TestOrchestratorAudioFrameInterruptsSpeakingState(t *testing.T) {
	orchestrator, sttClient, _, ttsClient, outputSink, events, sessions := newTestOrchestrator()

	created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
		CallID: "call-300",
		Caller: "carol",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	})
	if err != nil {
		t.Fatalf("HandleStreamStart returned error: %v", err)
	}

	if _, err := sessions.Update(created.ID, func(current *session.Session) error {
		current.State = session.StateSpeaking
		return nil
	}); err != nil {
		t.Fatalf("failed to prepare speaking session: %v", err)
	}

	if err := orchestrator.HandleAudioFrame(context.Background(), created.ID, []byte{1, 2, 3}); err != nil {
		t.Fatalf("HandleAudioFrame returned error: %v", err)
	}

	current, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatal("expected session to exist after interrupt")
	}
	if current.State != session.StateListening {
		t.Fatalf("expected listening state after interrupt, got %s", current.State)
	}
	if len(ttsClient.interruptCalls) != 1 {
		t.Fatalf("expected 1 tts interrupt, got %d", len(ttsClient.interruptCalls))
	}
	if len(outputSink.interruptCalls) != 1 {
		t.Fatalf("expected 1 output interrupt, got %d", len(outputSink.interruptCalls))
	}
	if len(sttClient.pushCalls) != 1 {
		t.Fatalf("expected 1 stt push after interrupt, got %d", len(sttClient.pushCalls))
	}

	assertEventTypes(t, events.events,
		bridgews.EventSessionCreated,
		bridgews.EventSessionTTSStopped,
		bridgews.EventSessionUpdated,
	)
	assertTTSStoppedPayload(t, events.events[1], bridgews.ReasonInterrupted, session.StateListening)
	assertSessionSummaryPayload(t, events.events[2], created.ID, "call-300", session.StateListening)
}

func TestOrchestratorHangupCleansUpAndBroadcastsClosure(t *testing.T) {
	orchestrator, sttClient, _, ttsClient, outputSink, events, sessions := newTestOrchestrator()

	created, err := orchestrator.HandleStreamStart(context.Background(), pipeline.StreamStartRequest{
		CallID: "call-400",
		Caller: "dave",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	})
	if err != nil {
		t.Fatalf("HandleStreamStart returned error: %v", err)
	}

	if err := orchestrator.HandleHangup(context.Background(), created.ID, bridgews.ReasonHangup); err != nil {
		t.Fatalf("HandleHangup returned error: %v", err)
	}

	if len(sttClient.closeCalls) != 1 {
		t.Fatalf("expected 1 stt close call, got %d", len(sttClient.closeCalls))
	}
	if len(ttsClient.interruptCalls) != 1 {
		t.Fatalf("expected 1 tts interrupt during hangup, got %d", len(ttsClient.interruptCalls))
	}
	if len(outputSink.interruptCalls) != 1 {
		t.Fatalf("expected 1 output interrupt during hangup, got %d", len(outputSink.interruptCalls))
	}
	if _, ok := sessions.Get(created.ID); ok {
		t.Fatal("expected session to be removed after hangup")
	}

	assertEventTypes(t, events.events,
		bridgews.EventSessionCreated,
		bridgews.EventSessionUpdated,
		bridgews.EventSessionClosed,
	)
	assertSessionSummaryPayload(t, events.events[1], created.ID, "call-400", session.StateClosed)
	assertClosedPayload(t, events.events[2], created.ID, "call-400", bridgews.ReasonHangup)
}

func newTestOrchestrator() (*pipeline.Orchestrator, *fakeSTTClient, *fakeOpenClawClient, *fakeTTSClient, *fakeOutputSink, *fakeEventPublisher, *session.Manager) {
	sessions := session.NewManager()
	sttClient := &fakeSTTClient{}
	openClawClient := &fakeOpenClawClient{}
	ttsClient := &fakeTTSClient{}
	outputSink := &fakeOutputSink{}
	events := &fakeEventPublisher{}

	return pipeline.NewOrchestrator(
		sessions,
		sttClient,
		openClawClient,
		ttsClient,
		outputSink,
		events,
		fakeProviderBindings{},
	), sttClient, openClawClient, ttsClient, outputSink, events, sessions
}

func assertEventTypes(t *testing.T, events []publishedEvent, expected ...string) {
	t.Helper()

	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(events))
	}

	for index, eventType := range expected {
		if events[index].eventType != eventType {
			t.Fatalf("expected event %d to be %s, got %s", index, eventType, events[index].eventType)
		}
	}
}

func assertSessionSummaryPayload(t *testing.T, event publishedEvent, sessionID string, callID string, state session.SessionState) {
	t.Helper()

	rawSession, ok := event.data[bridgews.DataKeySession]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeySession)
	}

	summary, ok := rawSession.(contract.SessionSummary)
	if !ok {
		t.Fatalf("expected session payload type %T to be contract.SessionSummary", rawSession)
	}
	if summary.ID != sessionID || summary.CallID != callID || summary.State != state {
		t.Fatalf("unexpected session payload: %+v", summary)
	}
}

func assertTranscriptPayload(t *testing.T, event publishedEvent, text string, kind string, final bool) {
	t.Helper()

	rawTranscript, ok := event.data[bridgews.DataKeyTranscript]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyTranscript)
	}

	transcript, ok := rawTranscript.(contract.TranscriptEntry)
	if !ok {
		t.Fatalf("expected transcript payload type %T to be contract.TranscriptEntry", rawTranscript)
	}
	if transcript.Text != text || transcript.Kind != kind {
		t.Fatalf("unexpected transcript payload: %+v", transcript)
	}

	rawFinal, ok := event.data[bridgews.DataKeyFinal]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyFinal)
	}
	if typedFinal, ok := rawFinal.(bool); !ok || typedFinal != final {
		t.Fatalf("unexpected final flag payload: %#v", rawFinal)
	}
}

func assertTTSStartedPayload(t *testing.T, event publishedEvent, state session.SessionState) {
	t.Helper()

	rawState, ok := event.data[bridgews.DataKeyState]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyState)
	}
	if typedState, ok := rawState.(session.SessionState); !ok || typedState != state {
		t.Fatalf("unexpected tts started state payload: %#v", rawState)
	}
	if _, ok := event.data[bridgews.DataKeyUpdatedAt]; !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyUpdatedAt)
	}
	if _, ok := event.data[bridgews.DataKeyText]; !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyText)
	}
}

func assertTTSStoppedPayload(t *testing.T, event publishedEvent, reason string, state session.SessionState) {
	t.Helper()

	rawReason, ok := event.data[bridgews.DataKeyReason]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyReason)
	}
	if typedReason, ok := rawReason.(string); !ok || typedReason != reason {
		t.Fatalf("unexpected tts stopped reason payload: %#v", rawReason)
	}

	rawState, ok := event.data[bridgews.DataKeyState]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyState)
	}
	if typedState, ok := rawState.(session.SessionState); !ok || typedState != state {
		t.Fatalf("unexpected tts stopped state payload: %#v", rawState)
	}
}

func assertClosedPayload(t *testing.T, event publishedEvent, sessionID string, callID string, reason string) {
	t.Helper()

	assertSessionSummaryPayload(t, event, sessionID, callID, session.StateClosed)

	rawReason, ok := event.data[bridgews.DataKeyReason]
	if !ok {
		t.Fatalf("expected event %s to contain %s", event.eventType, bridgews.DataKeyReason)
	}
	if typedReason, ok := rawReason.(string); !ok || typedReason != reason {
		t.Fatalf("unexpected closed reason payload: %#v", rawReason)
	}
}

func assertProviderLatencyPresent(t *testing.T, current *session.Session, provider string) {
	t.Helper()

	for _, entry := range current.ProviderLatencies {
		if entry.Provider == provider {
			return
		}
	}
	t.Fatalf("expected provider latency for %s, got %+v", provider, current.ProviderLatencies)
}

func assertSessionLogSourcePresent(t *testing.T, current *session.Session, source string, level string) {
	t.Helper()

	for _, entry := range current.RecentLogs {
		if entry.Source == source && entry.Level == level {
			return
		}
	}
	t.Fatalf("expected session log for source=%s level=%s, got %+v", source, level, current.RecentLogs)
}
