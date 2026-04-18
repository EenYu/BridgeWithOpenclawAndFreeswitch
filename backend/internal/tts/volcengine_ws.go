package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"
	"bridgewithclawandfreeswitch/backend/internal/providerhttp"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	volcTTSClientFullRequest  = 0x1
	volcTTSServerFullResponse = 0x9
	volcTTSServerAudioOnly    = 0xB
	volcTTSServerError        = 0xF

	volcTTSMessageNoEvent   = 0x0
	volcTTSMessageWithEvent = 0x4
	volcTTSJSON             = 0x1

	volcTTSStartConnection  = 1
	volcTTSFinishConnection = 2
	volcTTSStartSession     = 100
	volcTTSFinishSession    = 102
	volcTTSTaskRequest      = 200

	volcTTSConnectionStarted  = 50
	volcTTSConnectionFailed   = 51
	volcTTSConnectionFinished = 52
	volcTTSSessionStarted     = 150
	volcTTSSessionCanceled    = 151
	volcTTSSessionFinished    = 152
	volcTTSSessionFailed      = 153
	volcTTSSentenceStart      = 350
	volcTTSSentenceEnd        = 351
	volcTTSAudioResponse      = 352
)

type VolcengineWSClient struct {
	httpClient *http.Client
	dialer     websocket.Dialer
	config     config.ProviderConfig
	tokenMu    sync.Mutex
	token      string
	tokenValid time.Time
}

type volcTTSResponse struct {
	MessageType int
	MessageFlag int
	Event       int
	StatusCode  int
	Payload     []byte
}

type volcSTSTokenResponse struct {
	JWTToken string `json:"jwt_token"`
}

func NewVolcengineWSClient(cfg config.ProviderConfig) *VolcengineWSClient {
	httpClient := providerhttp.NewProviderClient(cfg)
	if httpClient.Timeout < 30*time.Second {
		httpClient.Timeout = 30 * time.Second
	}
	return &VolcengineWSClient{
		httpClient: httpClient,
		dialer: websocket.Dialer{
			HandshakeTimeout: cfg.Timeout,
		},
		config: cfg,
	}
}

func (c *VolcengineWSClient) Synthesize(ctx context.Context, sessionID string, text string) (AudioPayload, error) {
	if c.config.AppKey == "" || c.config.APIKey == "" || c.config.ResourceID == "" || c.config.VoiceType == "" {
		return AudioPayload{}, fmt.Errorf("volcengine tts requires app key, api key, resource id, and voice type")
	}
	if strings.TrimSpace(text) == "" {
		return AudioPayload{}, fmt.Errorf("volcengine tts requires non-empty text")
	}

	jwtToken, err := c.fetchJWTToken(ctx)
	if err != nil {
		return AudioPayload{}, err
	}

	endpoint, err := c.buildEndpoint(jwtToken)
	if err != nil {
		return AudioPayload{}, err
	}

	conn, _, err := c.dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return AudioPayload{}, fmt.Errorf("dial volcengine tts websocket: %w", err)
	}
	defer conn.Close()

	sessionWSID := uuid.NewString()
	sessionConfig := c.buildSessionConfig(sessionID)
	var audio bytes.Buffer
	taskSent := false
	sessionFinished := false

	if err := c.writeBinaryFrame(ctx, conn, buildVolcTTSRequest(volcTTSStartConnection, nil, "")); err != nil {
		return AudioPayload{}, err
	}

	for {
		resp, err := c.readBinaryFrame(ctx, conn)
		if err != nil {
			if sessionFinished && audio.Len() > 0 {
				return c.buildAudioPayload(audio.Bytes()), nil
			}
			return AudioPayload{}, err
		}
		if resp.MessageType == volcTTSServerError {
			return AudioPayload{}, validateVolcTTSPayload(resp.Payload, resp.StatusCode)
		}

		switch resp.Event {
		case volcTTSConnectionStarted:
			if err := c.writeBinaryFrame(ctx, conn, buildVolcTTSRequest(volcTTSStartSession, sessionConfig, sessionWSID)); err != nil {
				return AudioPayload{}, err
			}
		case volcTTSSessionStarted:
			if taskSent {
				continue
			}
			taskSent = true
			taskConfig := cloneSessionConfig(sessionConfig)
			reqParams, _ := taskConfig["req_params"].(map[string]any)
			reqParams["text"] = text
			if err := c.writeBinaryFrame(ctx, conn, buildVolcTTSRequest(volcTTSTaskRequest, taskConfig, sessionWSID)); err != nil {
				return AudioPayload{}, err
			}
			if err := c.writeBinaryFrame(ctx, conn, buildVolcTTSRequest(volcTTSFinishSession, nil, sessionWSID)); err != nil {
				return AudioPayload{}, err
			}
		case volcTTSAudioResponse:
			audio.Write(resp.Payload)
		case volcTTSSentenceStart, volcTTSSentenceEnd:
			continue
		case volcTTSSessionFinished, volcTTSSessionCanceled:
			if err := validateVolcTTSPayload(resp.Payload, resp.StatusCode); err != nil {
				return AudioPayload{}, err
			}
			sessionFinished = true
			_ = c.writeBinaryFrame(ctx, conn, buildVolcTTSRequest(volcTTSFinishConnection, nil, ""))
			if audio.Len() == 0 {
				return AudioPayload{}, fmt.Errorf("volcengine tts returned no audio")
			}
			return c.buildAudioPayload(audio.Bytes()), nil
		case volcTTSSessionFailed, volcTTSConnectionFailed:
			return AudioPayload{}, validateVolcTTSPayload(resp.Payload, resp.StatusCode)
		case volcTTSConnectionFinished:
			if audio.Len() == 0 {
				return AudioPayload{}, fmt.Errorf("volcengine tts returned no audio")
			}
			return c.buildAudioPayload(audio.Bytes()), nil
		}
	}
}

func (c *VolcengineWSClient) Interrupt(_ context.Context, _ string) error {
	// 当前实现按次请求建立连接；开始播放前已完整收齐音频，服务端没有独立会话需要额外打断。
	return nil
}

func (c *VolcengineWSClient) fetchJWTToken(ctx context.Context) (string, error) {
	if token, ok := c.cachedToken(); ok {
		return token, nil
	}

	stsEndpoint := c.config.STSEndpoint
	if stsEndpoint == "" {
		stsEndpoint = "https://openspeech.bytedance.com/api/v1/sts/token"
	}

	body, err := json.Marshal(map[string]any{
		"appid":    c.config.AppKey,
		"duration": 300,
	})
	if err != nil {
		return "", fmt.Errorf("marshal volcengine sts request: %w", err)
	}

	stsCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		stsCtx, cancel = context.WithTimeout(ctx, c.httpClient.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(stsCtx, http.MethodPost, stsEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build volcengine sts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer; "+c.config.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request volcengine sts token: %w", err)
	}

	payloadBytes, err := providerhttp.ReadBody(resp)
	if err != nil {
		return "", err
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", fmt.Errorf("decode volcengine sts token: %w", err)
	}
	token := providerhttp.LookupString(payload, "jwt_token", "data.jwt_token", "data.token", "token")
	if token == "" {
		return "", fmt.Errorf("volcengine sts response missing jwt_token")
	}
	c.storeToken(token)
	return token, nil
}

func (c *VolcengineWSClient) cachedToken() (string, bool) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token == "" || time.Now().After(c.tokenValid) {
		return "", false
	}
	return c.token, true
}

func (c *VolcengineWSClient) storeToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	c.token = token
	c.tokenValid = time.Now().Add(4 * time.Minute)
}

func (c *VolcengineWSClient) buildEndpoint(jwtToken string) (string, error) {
	parsed, err := url.Parse(c.config.Endpoint)
	if err != nil {
		return "", fmt.Errorf("parse volcengine tts endpoint: %w", err)
	}

	query := parsed.Query()
	query.Set("api_resource_id", c.config.ResourceID)
	query.Set("api_app_key", c.config.AppKey)
	query.Set("api_access_key", "Jwt; "+jwtToken)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *VolcengineWSClient) buildSessionConfig(sessionID string) map[string]any {
	return map[string]any{
		"user": map[string]any{
			"uid": firstNonEmpty(c.config.UID, sessionID),
		},
		"namespace": firstNonEmpty(c.config.Namespace, "BidirectionalTTS"),
		"req_params": map[string]any{
			"speaker": c.config.VoiceType,
			"audio_params": map[string]any{
				"format":      firstNonEmpty(c.config.AudioFormat, "pcm"),
				"sample_rate": firstPositive(c.config.SampleRateHz, 16000),
			},
		},
	}
}

func (c *VolcengineWSClient) readBinaryFrame(ctx context.Context, conn *websocket.Conn) (volcTTSResponse, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	} else if c.config.Timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(c.config.Timeout))
	}

	_, frame, err := conn.ReadMessage()
	if err != nil {
		return volcTTSResponse{}, fmt.Errorf("read volcengine tts websocket frame: %w", err)
	}
	return decodeVolcTTSResponse(frame)
}

func (c *VolcengineWSClient) writeBinaryFrame(ctx context.Context, conn *websocket.Conn, frame []byte) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else if c.config.Timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(c.config.Timeout))
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("write volcengine tts websocket frame: %w", err)
	}
	return nil
}

func buildVolcTTSRequest(event int, payload map[string]any, sessionID string) []byte {
	rawPayload := []byte("{}")
	if payload != nil {
		rawPayload, _ = json.Marshal(payload)
	}

	sessionBytes := []byte(sessionID)
	bufferLen := 4 + 4 + 4 + len(rawPayload)
	if len(sessionBytes) > 0 {
		bufferLen += 4 + len(sessionBytes)
	}

	out := make([]byte, bufferLen)
	offset := 0
	copy(out[offset:], []byte{0x11, byte(volcTTSClientFullRequest<<4) | volcTTSMessageWithEvent, byte(volcTTSJSON << 4), 0x00})
	offset += 4
	binary.BigEndian.PutUint32(out[offset:], uint32(event))
	offset += 4
	if len(sessionBytes) > 0 {
		binary.BigEndian.PutUint32(out[offset:], uint32(len(sessionBytes)))
		offset += 4
		copy(out[offset:], sessionBytes)
		offset += len(sessionBytes)
	}
	binary.BigEndian.PutUint32(out[offset:], uint32(len(rawPayload)))
	offset += 4
	copy(out[offset:], rawPayload)
	return out
}

func decodeVolcTTSResponse(frame []byte) (volcTTSResponse, error) {
	if len(frame) < 4 {
		return volcTTSResponse{}, fmt.Errorf("volcengine tts frame too short: %d", len(frame))
	}

	messageType := int(frame[1] >> 4)
	messageFlag := int(frame[1] & 0x0F)
	if messageType != volcTTSServerFullResponse && messageType != volcTTSServerAudioOnly && messageType != volcTTSServerError {
		return volcTTSResponse{}, fmt.Errorf("unsupported volcengine tts message type %d", messageType)
	}
	if messageType == volcTTSServerError {
		return decodeVolcTTSErrorResponse(frame, messageFlag)
	}
	if messageFlag != volcTTSMessageWithEvent {
		return volcTTSResponse{}, fmt.Errorf("unsupported volcengine tts message flag %d", messageFlag)
	}
	if len(frame) < 12 {
		return volcTTSResponse{}, fmt.Errorf("volcengine tts frame too short: %d", len(frame))
	}

	offset := 4
	event := int(binary.BigEndian.Uint32(frame[offset:]))
	offset += 4

	payloadSection := frame[offset:]
	return volcTTSResponse{
		MessageType: messageType,
		MessageFlag: messageFlag,
		Event:       event,
		Payload:     parseVolcTTSPayload(payloadSection, event, messageType),
	}, nil
}

func decodeVolcTTSErrorResponse(frame []byte, messageFlag int) (volcTTSResponse, error) {
	if messageFlag != volcTTSMessageNoEvent {
		return volcTTSResponse{}, fmt.Errorf("unsupported volcengine tts error flag %d", messageFlag)
	}
	if len(frame) < 12 {
		return volcTTSResponse{}, fmt.Errorf("volcengine tts error frame too short: %d", len(frame))
	}

	statusCode := int(binary.BigEndian.Uint32(frame[4:8]))
	payloadSize := int(binary.BigEndian.Uint32(frame[8:12]))
	if payloadSize < 0 || len(frame) < 12+payloadSize {
		return volcTTSResponse{}, fmt.Errorf("invalid volcengine tts error payload size %d", payloadSize)
	}

	return volcTTSResponse{
		MessageType: volcTTSServerError,
		MessageFlag: messageFlag,
		StatusCode:  statusCode,
		Payload:     append([]byte(nil), frame[12:12+payloadSize]...),
	}, nil
}

func parseVolcTTSPayload(section []byte, event int, messageType int) []byte {
	section = trimVolcTTSSessionPrefix(section)
	if len(section) < 4 {
		return nil
	}
	size := int(binary.BigEndian.Uint32(section[:4]))
	if size <= 0 || len(section) < 4+size {
		return nil
	}
	return append([]byte(nil), section[4:4+size]...)
}

func trimVolcTTSSessionPrefix(section []byte) []byte {
	if len(section) < 4 {
		return section
	}
	sessionLen := int(binary.BigEndian.Uint32(section[:4]))
	if sessionLen <= 0 || len(section) < 4+sessionLen+4 {
		return section
	}
	sessionID := section[4 : 4+sessionLen]
	if !looksLikeUUIDString(sessionID) {
		return section
	}
	return section[4+sessionLen:]
}

func looksLikeUUIDString(value []byte) bool {
	if len(value) != 36 {
		return false
	}
	for index, ch := range value {
		switch index {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !isHexChar(ch) {
				return false
			}
		}
	}
	return true
}

func isHexChar(ch byte) bool {
	return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'
}

func validateVolcTTSPayload(payload []byte, fallbackStatus int) error {
	if len(payload) == 0 {
		if fallbackStatus != 0 && fallbackStatus != 20000000 {
			return fmt.Errorf("volcengine tts returned error (%d)", fallbackStatus)
		}
		return nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Errorf("decode volcengine tts response payload: %w", err)
	}

	statusCode, _ := decoded["status_code"].(float64)
	if statusCode == 0 && fallbackStatus != 0 {
		statusCode = float64(fallbackStatus)
	}
	message := firstNonEmpty(
		providerhttp.LookupString(decoded, "message", "error", "status_msg"),
	)
	if statusCode != 0 && int(statusCode) != 20000000 {
		if message == "" {
			message = "volcengine tts returned error"
		}
		return fmt.Errorf("%s (%d)", message, int(statusCode))
	}
	if message == "" {
		return nil
	}
	return nil
}

func cloneSessionConfig(source map[string]any) map[string]any {
	raw, _ := json.Marshal(source)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (c *VolcengineWSClient) buildAudioPayload(audio []byte) AudioPayload {
	return AudioPayload{
		Bytes:        normalizeAudioBytes(c.config.AudioFormat, audio),
		Format:       NormalizeAudioFormat(c.config.AudioFormat),
		SampleRateHz: firstPositive(c.config.SampleRateHz, 16000),
	}
}
