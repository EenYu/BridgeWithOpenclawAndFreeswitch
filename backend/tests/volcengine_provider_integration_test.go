package tests

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/runtime"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/stt"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"

	"github.com/gorilla/websocket"
)

func TestVolcengineProvidersDriveTranscriptReplyAndTTS(t *testing.T) {
	var sttStartPayload map[string]any
	var receivedSTTAudio []byte
	var openClawSessionKey string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	sttServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-App-Key") != "volc-app-id" {
			t.Fatalf("unexpected stt app key header %q", r.Header.Get("X-Api-App-Key"))
		}
		if r.Header.Get("X-Api-Access-Key") != "stt-token" {
			t.Fatalf("unexpected stt access key header %q", r.Header.Get("X-Api-Access-Key"))
		}
		if r.Header.Get("X-Api-Resource-Id") != "volc.seedasr.sauc.duration" {
			t.Fatalf("unexpected stt resource id header %q", r.Header.Get("X-Api-Resource-Id"))
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade stt ws: %v", err)
		}
		defer conn.Close()

		_, firstFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read stt start frame: %v", err)
		}
		firstMessage := decodeTestVolcengineSTTMessage(t, firstFrame)
		if firstMessage.MessageType != 0x1 {
			t.Fatalf("expected full client request, got %d", firstMessage.MessageType)
		}
		sttStartPayload = firstMessage.JSON

		_, audioFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read stt audio frame: %v", err)
		}
		audioMessage := decodeTestVolcengineSTTMessage(t, audioFrame)
		if audioMessage.MessageType != 0x2 {
			t.Fatalf("expected audio-only request, got %d", audioMessage.MessageType)
		}
		receivedSTTAudio = audioMessage.Payload

		response := map[string]any{
			"audio_info": map[string]any{"duration": 120},
			"result": map[string]any{
				"text": "need help",
				"utterances": []map[string]any{
					{"text": "need help", "definite": true},
				},
			},
		}
		payload, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal stt response: %v", err)
		}
		serverFrame := encodeTestVolcengineSTTServerResponse(t, payload, 1)
		if err := conn.WriteMessage(websocket.BinaryMessage, serverFrame); err != nil {
			t.Fatalf("write stt response: %v", err)
		}
	}))
	defer sttServer.Close()

	openClawServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://127.0.0.1" {
			t.Fatalf("unexpected openclaw origin %q", r.Header.Get("Origin"))
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade openclaw ws: %v", err)
		}
		defer conn.Close()

		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "connect.challenge",
			"payload": map[string]any{
				"nonce": "nonce-1",
				"ts":    123,
			},
		}); err != nil {
			t.Fatalf("write connect challenge: %v", err)
		}

		var connectReq testOpenClawRequest
		if err := conn.ReadJSON(&connectReq); err != nil {
			t.Fatalf("read openclaw connect request: %v", err)
		}
		if connectReq.Method != "connect" {
			t.Fatalf("expected connect method, got %q", connectReq.Method)
		}
		connectParams := connectReq.Params
		clientMeta := connectParams["client"].(map[string]any)
		if clientMeta["id"] != "openclaw-control-ui" || clientMeta["mode"] != "ui" {
			t.Fatalf("unexpected openclaw connect client %#v", clientMeta)
		}
		if connectParams["role"] != "operator" {
			t.Fatalf("unexpected openclaw role %#v", connectParams["role"])
		}
		auth := connectParams["auth"].(map[string]any)
		if auth["token"] != "oc-token" {
			t.Fatalf("unexpected openclaw token %#v", auth["token"])
		}

		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   connectReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"type":     "hello-ok",
				"protocol": 3,
				"server":   map[string]any{"version": "2026.4.15", "connId": "oc-1"},
				"features": map[string]any{
					"methods": []string{"sessions.create", "chat.send"},
					"events":  []string{"chat"},
				},
				"snapshot": map[string]any{
					"sessionDefaults": map[string]any{
						"defaultAgentId": "main",
						"mainKey":        "main",
						"mainSessionKey": "agent:main:main",
					},
				},
			},
		}); err != nil {
			t.Fatalf("write openclaw hello: %v", err)
		}

		var createReq testOpenClawRequest
		if err := conn.ReadJSON(&createReq); err != nil {
			t.Fatalf("read openclaw session create request: %v", err)
		}
		if createReq.Method != "sessions.create" {
			t.Fatalf("expected sessions.create, got %q", createReq.Method)
		}
		createKey := createReq.Params["key"].(string)
		openClawSessionKey = "agent:main:" + createKey
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   createReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"ok":  true,
				"key": openClawSessionKey,
			},
		}); err != nil {
			t.Fatalf("write openclaw sessions.create response: %v", err)
		}

		var chatReq testOpenClawRequest
		if err := conn.ReadJSON(&chatReq); err != nil {
			t.Fatalf("read openclaw chat request: %v", err)
		}
		if chatReq.Method != "chat.send" {
			t.Fatalf("expected chat.send, got %q", chatReq.Method)
		}
		if chatReq.Params["sessionKey"] != openClawSessionKey {
			t.Fatalf("unexpected chat session key %#v", chatReq.Params["sessionKey"])
		}
		if chatReq.Params["message"] != "need help" {
			t.Fatalf("unexpected chat message %#v", chatReq.Params["message"])
		}

		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   chatReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"runId":  "oc-run-1",
				"status": "started",
			},
		}); err != nil {
			t.Fatalf("write openclaw chat response: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "chat",
			"payload": map[string]any{
				"runId":      "oc-run-1",
				"sessionKey": openClawSessionKey,
				"seq":        1,
				"state":      "final",
				"message": map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{"type": "text", "text": "agent reply"},
					},
				},
			},
		}); err != nil {
			t.Fatalf("write openclaw final event: %v", err)
		}
	}))
	defer openClawServer.Close()

	ttsMux := http.NewServeMux()
	var capturedTTSSessionID string
	ttsMux.HandleFunc("/sts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer; tts-access-key" {
			t.Fatalf("unexpected sts authorization header %q", r.Header.Get("Authorization"))
		}
		defer r.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode sts payload: %v", err)
		}
		if payload["appid"] != "tts-app-id" {
			t.Fatalf("unexpected sts appid %#v", payload["appid"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jwt_token":"tts-jwt-token"}`))
	})
	ttsMux.HandleFunc("/tts", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("api_resource_id") != "volc.service_type.10029" {
			t.Fatalf("unexpected tts resource id %q", query.Get("api_resource_id"))
		}
		if query.Get("api_app_key") != "tts-app-id" {
			t.Fatalf("unexpected tts app key %q", query.Get("api_app_key"))
		}
		if query.Get("api_access_key") != "Jwt; tts-jwt-token" {
			t.Fatalf("unexpected tts access key %q", query.Get("api_access_key"))
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade tts ws: %v", err)
		}
		defer conn.Close()

		_, startConnFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read tts start connection frame: %v", err)
		}
		startConnReq := decodeTestVolcengineTTSRequest(t, startConnFrame)
		if startConnReq.Event != 1 {
			t.Fatalf("expected tts start connection event, got %d", startConnReq.Event)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, encodeTestVolcengineTTSResponse(50, "", map[string]any{"status_code": 20000000})); err != nil {
			t.Fatalf("write tts connection started: %v", err)
		}

		_, startSessionFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read tts start session frame: %v", err)
		}
		startSessionReq := decodeTestVolcengineTTSRequest(t, startSessionFrame)
		if startSessionReq.Event != 100 {
			t.Fatalf("expected tts start session event, got %d", startSessionReq.Event)
		}
		capturedTTSSessionID = startSessionReq.SessionID
		if startSessionReq.SessionID == "" {
			t.Fatal("expected tts session id to be present")
		}
		if startSessionReq.Payload["namespace"] != "BidirectionalTTS" {
			t.Fatalf("unexpected tts namespace %#v", startSessionReq.Payload["namespace"])
		}
		reqParams := startSessionReq.Payload["req_params"].(map[string]any)
		audioParams := reqParams["audio_params"].(map[string]any)
		if reqParams["speaker"] != "BV001_streaming" {
			t.Fatalf("unexpected tts speaker %#v", reqParams["speaker"])
		}
		if audioParams["format"] != "wav" || audioParams["sample_rate"] != float64(8000) {
			t.Fatalf("unexpected tts audio params %#v", audioParams)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, encodeTestVolcengineTTSResponse(150, capturedTTSSessionID, map[string]any{"status_code": 20000000, "message": "ok"})); err != nil {
			t.Fatalf("write tts session started: %v", err)
		}

		_, taskFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read tts task request frame: %v", err)
		}
		taskReq := decodeTestVolcengineTTSRequest(t, taskFrame)
		if taskReq.Event != 200 {
			t.Fatalf("expected tts task request event, got %d", taskReq.Event)
		}
		taskParams := taskReq.Payload["req_params"].(map[string]any)
		if taskParams["text"] != "agent reply" {
			t.Fatalf("unexpected tts task text %#v", taskParams["text"])
		}

		_, finishSessionFrame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read tts finish session frame: %v", err)
		}
		finishSessionReq := decodeTestVolcengineTTSRequest(t, finishSessionFrame)
		if finishSessionReq.Event != 102 {
			t.Fatalf("expected tts finish session event, got %d", finishSessionReq.Event)
		}

		if err := conn.WriteMessage(websocket.BinaryMessage, encodeTestVolcengineTTSAudioResponse(352, capturedTTSSessionID, []byte("audio:agent reply"))); err != nil {
			t.Fatalf("write tts audio response: %v", err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, encodeTestVolcengineTTSResponse(152, capturedTTSSessionID, map[string]any{"status_code": 20000000, "message": "ok"})); err != nil {
			t.Fatalf("write tts session finished: %v", err)
		}

		_, finishConnFrame, err := conn.ReadMessage()
		if err == nil {
			finishConnReq := decodeTestVolcengineTTSRequest(t, finishConnFrame)
			if finishConnReq.Event != 2 {
				t.Fatalf("expected tts finish connection event, got %d", finishConnReq.Event)
			}
		}
	})
	ttsServer := httptest.NewServer(ttsMux)
	defer ttsServer.Close()

	sttURL := "ws" + strings.TrimPrefix(sttServer.URL, "http")
	openClawURL := "ws" + strings.TrimPrefix(openClawServer.URL, "http")
	ttsWSURL := "ws" + strings.TrimPrefix(ttsServer.URL, "http") + "/tts"
	providers := config.Providers{
		STT: config.ProviderConfig{
			Vendor:         "volcengine-stt-ws",
			Endpoint:       sttURL,
			Enabled:        true,
			Transport:      "volcengine-binary-ws",
			AppKey:         "volc-app-id",
			APIKey:         "stt-token",
			ResourceID:     "volc.seedasr.sauc.duration",
			AudioFormat:    "pcm",
			AudioCodec:     "raw",
			SampleRateHz:   16000,
			BitsPerSample:  16,
			ShowUtterances: true,
			EnableITN:      true,
			EnablePunc:     true,
		},
		OpenClaw: config.ProviderConfig{
			Vendor:   "openclaw-gateway-ws",
			Endpoint: openClawURL,
			Enabled:  true,
			APIKey:   "oc-token",
			Origin:   "http://127.0.0.1",
			Timeout:  5 * time.Second,
		},
		TTS: config.ProviderConfig{
			Vendor:       "volcengine-tts-ws-v3",
			Endpoint:     ttsWSURL,
			Enabled:      true,
			AppKey:       "tts-app-id",
			APIKey:       "tts-access-key",
			ResourceID:   "volc.service_type.10029",
			VoiceType:    "BV001_streaming",
			AudioFormat:  "wav",
			SampleRateHz: 8000,
			STSEndpoint:  ttsServer.URL + "/sts",
			UID:          "bridge-fs1",
			Namespace:    "BidirectionalTTS",
			Timeout:      5 * time.Second,
		},
	}

	sessions := session.NewManager()
	providerStore := config.NewProviderStore(providers)
	sttClient, openClawClient, ttsClient := runtime.BuildProviderClients(providers)
	outputSink := &syncOutputSink{}
	events := &syncEventPublisher{}

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
		CallID: "call-volc-1",
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

	waitForCondition(t, 5*time.Second, func() bool {
		return len(outputSink.snapshot()) == 1
	})

	if sttStartPayload == nil {
		t.Fatal("expected stt start payload to be captured")
	}
	audioConfig := sttStartPayload["audio"].(map[string]any)
	requestConfig := sttStartPayload["request"].(map[string]any)
	if audioConfig["format"] != "pcm" || audioConfig["codec"] != "raw" {
		t.Fatalf("unexpected stt audio config %#v", audioConfig)
	}
	if audioConfig["rate"] != float64(16000) || audioConfig["bits"] != float64(16) || audioConfig["channel"] != float64(1) {
		t.Fatalf("unexpected stt pcm config %#v", audioConfig)
	}
	if requestConfig["model_name"] != "bigmodel" || requestConfig["show_utterances"] != true {
		t.Fatalf("unexpected stt request config %#v", requestConfig)
	}
	if !bytes.Equal(receivedSTTAudio, []byte{1, 2, 3}) {
		t.Fatalf("unexpected forwarded stt audio %v", receivedSTTAudio)
	}
	if openClawSessionKey == "" {
		t.Fatal("expected openclaw session key to be created")
	}
	if capturedTTSSessionID == "" {
		t.Fatal("expected tts session id to be captured")
	}

	playCalls := outputSink.snapshot()
	if got := string(playCalls[0].pcm); got != "audio:agent reply" {
		t.Fatalf("unexpected synthesized audio payload %q", got)
	}
	if playCalls[0].format != "wav" || playCalls[0].sampleRateHz != 8000 {
		t.Fatalf("unexpected synthesized audio metadata format=%s sampleRateHz=%d", playCalls[0].format, playCalls[0].sampleRateHz)
	}

	current, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatal("expected session to exist")
	}
	if current.State != session.StateSpeaking {
		t.Fatalf("expected speaking state, got %s", current.State)
	}
	if current.Providers.STT != "volcengine-stt-ws" || current.Providers.OpenClaw != "openclaw-gateway-ws" || current.Providers.TTS != "volcengine-tts-ws-v3" {
		t.Fatalf("unexpected provider bindings: %+v", current.Providers)
	}

	assertEventTypes(t, events.snapshot(),
		bridgews.EventSessionCreated,
		bridgews.EventSessionTranscriptFinal,
		bridgews.EventSessionUpdated,
		bridgews.EventSessionTTSStarted,
		bridgews.EventSessionUpdated,
	)
}

type syncOutputSink struct {
	mu        sync.Mutex
	playCalls []audioPush
}

func (s *syncOutputSink) Play(_ context.Context, sessionID string, audio tts.AudioPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playCalls = append(s.playCalls, audioPush{
		sessionID:    sessionID,
		pcm:          append([]byte(nil), audio.Bytes...),
		format:       audio.Format,
		sampleRateHz: audio.SampleRateHz,
	})
	return nil
}

func (s *syncOutputSink) Interrupt(_ context.Context, _ string) error {
	return nil
}

func (s *syncOutputSink) snapshot() []audioPush {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audioPush, len(s.playCalls))
	copy(out, s.playCalls)
	return out
}

type syncEventPublisher struct {
	mu     sync.Mutex
	events []publishedEvent
}

func (p *syncEventPublisher) Broadcast(eventType string, sessionID string, payload map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, publishedEvent{
		eventType: eventType,
		sessionID: sessionID,
		data:      payload,
	})
}

func (p *syncEventPublisher) snapshot() []publishedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]publishedEvent, len(p.events))
	copy(out, p.events)
	return out
}

type testVolcengineSTTMessage struct {
	MessageType int
	Sequence    int32
	Payload     []byte
	JSON        map[string]any
}

type testVolcengineTTSRequest struct {
	Event     int
	SessionID string
	Payload   map[string]any
}

type testOpenClawRequest struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func decodeTestVolcengineSTTMessage(t *testing.T, frame []byte) testVolcengineSTTMessage {
	t.Helper()

	headerSize := int(frame[0]&0x0F) * 4
	messageType := int(frame[1] >> 4)
	flags := frame[1] & 0x0F
	serialization := frame[2] >> 4
	compression := frame[2] & 0x0F

	offset := headerSize
	var sequence int32
	if flags == 0x1 || flags == 0x3 {
		sequence = int32(binary.BigEndian.Uint32(frame[offset : offset+4]))
		offset += 4
	}
	payloadSize := int(binary.BigEndian.Uint32(frame[offset : offset+4]))
	offset += 4
	payload := append([]byte(nil), frame[offset:offset+payloadSize]...)
	if compression == 0x1 {
		payload = mustGunzip(t, payload)
	}

	message := testVolcengineSTTMessage{
		MessageType: messageType,
		Sequence:    sequence,
		Payload:     payload,
	}
	if serialization == 0x1 && len(payload) > 0 {
		if err := json.Unmarshal(payload, &message.JSON); err != nil {
			t.Fatalf("decode stt json payload: %v", err)
		}
	}
	return message
}

func encodeTestVolcengineSTTServerResponse(t *testing.T, payload []byte, sequence int32) []byte {
	t.Helper()

	compressed := mustGzip(t, payload)
	var buf bytes.Buffer
	buf.Write([]byte{0x11, 0x91, 0x11, 0x00})
	if err := binary.Write(&buf, binary.BigEndian, sequence); err != nil {
		t.Fatalf("write stt sequence: %v", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(compressed))); err != nil {
		t.Fatalf("write stt payload size: %v", err)
	}
	buf.Write(compressed)
	return buf.Bytes()
}

func decodeTestVolcengineTTSRequest(t *testing.T, frame []byte) testVolcengineTTSRequest {
	t.Helper()

	if len(frame) < 12 {
		t.Fatalf("tts request frame too short: %d", len(frame))
	}
	event := int(binary.BigEndian.Uint32(frame[4:8]))
	offset := 8
	var sessionID string
	if event == 100 || event == 102 || event == 200 {
		sessionLen := int(binary.BigEndian.Uint32(frame[offset : offset+4]))
		offset += 4
		sessionID = string(frame[offset : offset+sessionLen])
		offset += sessionLen
	}
	payloadLen := int(binary.BigEndian.Uint32(frame[offset : offset+4]))
	offset += 4

	var payload map[string]any
	if payloadLen > 0 {
		if err := json.Unmarshal(frame[offset:offset+payloadLen], &payload); err != nil {
			t.Fatalf("decode tts request payload: %v", err)
		}
	}
	return testVolcengineTTSRequest{
		Event:     event,
		SessionID: sessionID,
		Payload:   payload,
	}
}

func encodeTestVolcengineTTSResponse(event int, sessionID string, payload map[string]any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return encodeTestVolcengineTTSFrame(0x9, event, sessionID, raw)
}

func encodeTestVolcengineTTSAudioResponse(event int, sessionID string, audio []byte) []byte {
	return encodeTestVolcengineTTSFrame(0xB, event, sessionID, audio)
}

func encodeTestVolcengineTTSFrame(messageType int, event int, sessionID string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x11, byte(messageType<<4) | 0x04, 0x10, 0x00})
	_ = binary.Write(&buf, binary.BigEndian, uint32(event))
	if sessionID != "" {
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(sessionID)))
		buf.WriteString(sessionID)
	}
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}

func mustGzip(t *testing.T, payload []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("gzip payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func mustGunzip(t *testing.T, payload []byte) []byte {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("open gzip payload: %v", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip payload: %v", err)
	}
	return body
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
