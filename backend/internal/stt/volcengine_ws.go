package stt

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/session"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const volcenginePartialFinalizeDelay = 1200 * time.Millisecond

const (
	volcMessageTypeFullClientRequest  = 0x1
	volcMessageTypeAudioOnlyRequest   = 0x2
	volcMessageTypeFullServerResponse = 0x9
	volcMessageTypeErrorResponse      = 0xF

	volcFlagNoSequence       = 0x0
	volcFlagPositiveSequence = 0x1
	volcFlagNegativeSequence = 0x3

	volcSerializationNone = 0x0
	volcSerializationJSON = 0x1

	volcCompressionNone = 0x0
	volcCompressionGzip = 0x1
)

type VolcengineWSClient struct {
	mu      sync.RWMutex
	config  config.ProviderConfig
	dialer  websocket.Dialer
	handler TranscriptHandler
	streams map[string]*volcengineStream
}

type volcengineStream struct {
	conn        *websocket.Conn
	meta        session.StreamMeta
	connectID   string
	lastPartial string
	lastFinal   string
	closeOnce   sync.Once
	mu          sync.Mutex
	finalTimer  *time.Timer
}

type volcengineMessage struct {
	MessageType int
	Sequence    int32
	Payload     []byte
	JSON        map[string]any
}

func NewVolcengineWSClient(cfg config.ProviderConfig) *VolcengineWSClient {
	return &VolcengineWSClient{
		config: cfg,
		dialer: websocket.Dialer{
			HandshakeTimeout: cfg.Timeout,
		},
		streams: make(map[string]*volcengineStream),
	}
}

func (c *VolcengineWSClient) SetTranscriptHandler(handler TranscriptHandler) {
	c.handler = handler
}

func (c *VolcengineWSClient) StartStream(ctx context.Context, sessionID string, meta session.StreamMeta) error {
	if c.config.AppKey == "" || c.config.APIKey == "" || c.config.ResourceID == "" {
		return fmt.Errorf("volcengine stt requires app key, access key, and resource id")
	}

	if meta.SampleRateHz <= 0 {
		meta.SampleRateHz = firstPositive(c.config.SampleRateHz, 16000)
	}
	if meta.Channels <= 0 {
		meta.Channels = 1
	}
	if meta.Encoding == "" {
		meta.Encoding = "pcm_s16le"
	}

	connectID := uuid.NewString()
	headers := http.Header{
		"X-Api-App-Key":     []string{c.config.AppKey},
		"X-Api-Access-Key":  []string{c.config.APIKey},
		"X-Api-Resource-Id": []string{c.config.ResourceID},
		"X-Api-Connect-Id":  []string{connectID},
	}

	conn, resp, err := c.dialer.DialContext(ctx, c.config.Endpoint, headers)
	if err != nil {
		return err
	}
	if resp != nil {
		log.Printf("volcengine stt connected session=%s logid=%s connectId=%s", sessionID, resp.Header.Get("X-Tt-Logid"), connectID)
	}

	stream := &volcengineStream{
		conn:      conn,
		meta:      meta,
		connectID: connectID,
	}

	frame, err := encodeVolcengineFullClientRequest(c.buildStartPayload(sessionID, meta))
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		_ = conn.Close()
		return err
	}

	c.mu.Lock()
	c.streams[sessionID] = stream
	c.mu.Unlock()

	go c.readLoop(sessionID, stream)
	return nil
}

func (c *VolcengineWSClient) PushAudio(_ context.Context, sessionID string, pcm []byte) error {
	stream, ok := c.lookupStream(sessionID)
	if !ok {
		return fmt.Errorf("volcengine stt stream not found for %s", sessionID)
	}

	frame, err := encodeVolcengineAudioFrame(pcm)
	if err != nil {
		return err
	}
	return stream.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *VolcengineWSClient) CloseStream(_ context.Context, sessionID string) error {
	stream, ok := c.lookupStream(sessionID)
	if !ok {
		return nil
	}

	stream.closeOnce.Do(func() {
		stream.stopFinalTimer()
		_ = stream.conn.Close()
		c.mu.Lock()
		delete(c.streams, sessionID)
		c.mu.Unlock()
	})
	return nil
}

func (c *VolcengineWSClient) buildStartPayload(sessionID string, meta session.StreamMeta) map[string]any {
	payload := map[string]any{
		"user": map[string]any{
			"uid": firstNonEmpty(c.config.UID, sessionID),
		},
		"audio": map[string]any{
			"format":   firstNonEmpty(c.config.AudioFormat, "pcm"),
			"codec":    firstNonEmpty(c.config.AudioCodec, "raw"),
			"rate":     firstPositive(meta.SampleRateHz, c.config.SampleRateHz, 16000),
			"bits":     firstPositive(c.config.BitsPerSample, 16),
			"channel":  firstPositive(meta.Channels, 1),
			"language": c.config.Language,
		},
		"request": map[string]any{
			"model_name":      firstNonEmpty(c.config.Model, "bigmodel"),
			"enable_itn":      c.config.EnableITN,
			"enable_punc":     c.config.EnablePunc,
			"show_utterances": c.config.ShowUtterances,
		},
	}

	if payload["audio"].(map[string]any)["language"] == "" {
		delete(payload["audio"].(map[string]any), "language")
	}
	return payload
}

func (c *VolcengineWSClient) readLoop(sessionID string, stream *volcengineStream) {
	for {
		_, frame, err := stream.conn.ReadMessage()
		if err != nil {
			return
		}

		message, err := decodeVolcengineMessage(frame)
		if err != nil {
			log.Printf("decode volcengine stt message session=%s: %v", sessionID, err)
			continue
		}
		if message.MessageType == volcMessageTypeErrorResponse {
			log.Printf("volcengine stt server error session=%s payload=%s", sessionID, string(message.Payload))
			continue
		}
		if message.MessageType != volcMessageTypeFullServerResponse {
			continue
		}
		if err := c.handleServerResponse(sessionID, stream, message.JSON, message.Sequence); err != nil {
			log.Printf("handle volcengine stt response session=%s: %v", sessionID, err)
		}
	}
}

func (c *VolcengineWSClient) handleServerResponse(sessionID string, stream *volcengineStream, payload map[string]any, sequence int32) error {
	if c.handler == nil {
		return nil
	}

	result, ok := payload["result"].(map[string]any)
	if !ok {
		return nil
	}

	if utterances, ok := result["utterances"].([]any); ok && len(utterances) > 0 {
		last := utterances[len(utterances)-1]
		if utterance, ok := last.(map[string]any); ok {
			text, _ := utterance["text"].(string)
			definite, _ := utterance["definite"].(bool)
			text = strings.TrimSpace(text)
			if text == "" {
				return nil
			}
			if definite {
				if !stream.markFinal(text) {
					return nil
				}
				return c.handler(context.Background(), sessionID, text, true)
			}
			if !stream.markPartial(text) {
				return nil
			}
			stream.scheduleFinal(c.handler, sessionID)
			return c.handler(context.Background(), sessionID, text, false)
		}
	}

	text, _ := result["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	final := sequence < 0
	if final {
		if !stream.markFinal(text) {
			return nil
		}
	} else {
		if !stream.markPartial(text) {
			return nil
		}
		stream.scheduleFinal(c.handler, sessionID)
	}
	return c.handler(context.Background(), sessionID, text, final)
}

func (c *VolcengineWSClient) lookupStream(sessionID string) (*volcengineStream, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stream, ok := c.streams[sessionID]
	return stream, ok
}

func encodeVolcengineFullClientRequest(payload map[string]any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal volcengine stt payload: %w", err)
	}
	compressed, err := gzipBytes(raw)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(volcengineHeader(volcMessageTypeFullClientRequest, volcFlagNoSequence, volcSerializationJSON, volcCompressionGzip))
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(compressed))); err != nil {
		return nil, err
	}
	buf.Write(compressed)
	return buf.Bytes(), nil
}

func encodeVolcengineAudioFrame(pcm []byte) ([]byte, error) {
	compressed, err := gzipBytes(pcm)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(volcengineHeader(volcMessageTypeAudioOnlyRequest, volcFlagNoSequence, volcSerializationNone, volcCompressionGzip))
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(compressed))); err != nil {
		return nil, err
	}
	buf.Write(compressed)
	return buf.Bytes(), nil
}

func decodeVolcengineMessage(frame []byte) (volcengineMessage, error) {
	if len(frame) < 8 {
		return volcengineMessage{}, fmt.Errorf("frame too short: %d", len(frame))
	}

	headerSize := int(frame[0]&0x0F) * 4
	if len(frame) < headerSize {
		return volcengineMessage{}, fmt.Errorf("invalid header size: %d", headerSize)
	}

	messageType := int(frame[1] >> 4)
	flags := frame[1] & 0x0F
	serialization := frame[2] >> 4
	compression := frame[2] & 0x0F

	offset := headerSize
	var sequence int32
	if flags == volcFlagPositiveSequence || flags == volcFlagNegativeSequence {
		if len(frame) < offset+4 {
			return volcengineMessage{}, fmt.Errorf("missing sequence field")
		}
		sequence = int32(binary.BigEndian.Uint32(frame[offset : offset+4]))
		offset += 4
	}

	if len(frame) < offset+4 {
		return volcengineMessage{}, fmt.Errorf("missing payload length field")
	}
	payloadSize := int(binary.BigEndian.Uint32(frame[offset : offset+4]))
	offset += 4
	if len(frame) < offset+payloadSize {
		return volcengineMessage{}, fmt.Errorf("invalid payload size %d for frame %d", payloadSize, len(frame))
	}

	payload := append([]byte(nil), frame[offset:offset+payloadSize]...)
	var err error
	if compression == volcCompressionGzip {
		payload, err = gunzipBytes(payload)
		if err != nil {
			return volcengineMessage{}, err
		}
	}

	message := volcengineMessage{
		MessageType: messageType,
		Sequence:    sequence,
		Payload:     payload,
	}

	if serialization == volcSerializationJSON && len(payload) > 0 {
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			return volcengineMessage{}, fmt.Errorf("decode volcengine json payload: %w", err)
		}
		message.JSON = body
	}
	return message, nil
}

func volcengineHeader(messageType int, flags byte, serialization byte, compression byte) []byte {
	return []byte{
		0x11,
		byte(messageType<<4) | flags,
		byte(serialization<<4) | compression,
		0x00,
	}
}

func gzipBytes(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("gzip payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

func gunzipBytes(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open gzip payload: %w", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read gzip payload: %w", err)
	}
	return body, nil
}

func (s *volcengineStream) markPartial(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if text == s.lastPartial || text == s.lastFinal {
		return false
	}
	s.lastPartial = text
	return true
}

func (s *volcengineStream) markFinal(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if text == s.lastFinal {
		return false
	}
	s.lastFinal = text
	s.lastPartial = ""
	if s.finalTimer != nil {
		s.finalTimer.Stop()
		s.finalTimer = nil
	}
	return true
}

func (s *volcengineStream) scheduleFinal(handler TranscriptHandler, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalTimer != nil {
		s.finalTimer.Stop()
	}
	log.Printf("volcengine stt schedule final session=%s text=%q delay=%s", sessionID, s.lastPartial, volcenginePartialFinalizeDelay)
	s.finalTimer = time.AfterFunc(volcenginePartialFinalizeDelay, func() {
		s.mu.Lock()
		text := s.lastPartial
		if text == "" || text == s.lastFinal {
			log.Printf("volcengine stt skip scheduled final session=%s text=%q lastFinal=%q", sessionID, text, s.lastFinal)
			s.mu.Unlock()
			return
		}
		s.lastFinal = text
		s.lastPartial = ""
		s.finalTimer = nil
		s.mu.Unlock()

		if handler != nil {
			log.Printf("volcengine stt emit scheduled final session=%s text=%q", sessionID, text)
			if err := handler(context.Background(), sessionID, text, true); err != nil {
				log.Printf("volcengine stt scheduled final handler session=%s: %v", sessionID, err)
			}
		}
	})
}

func (s *volcengineStream) stopFinalTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalTimer != nil {
		s.finalTimer.Stop()
		s.finalTimer = nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
