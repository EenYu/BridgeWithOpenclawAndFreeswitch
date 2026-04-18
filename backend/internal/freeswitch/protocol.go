package freeswitch

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/tts"
)

const (
	ControlTypeStreamStart = "stream.start"
	ControlTypeStreamStop  = "stream.stop"
	ServerTypeStreamAck    = "stream.ack"
	ServerTypeStreamError  = "stream.error"
	ServerTypeStreamAudio  = "streamAudio"
)

const (
	ProtocolCodeInvalidControlMessage = "invalid_control_message"
	ProtocolCodeStreamAlreadyStarted  = "stream_already_started"
	ProtocolCodeStreamStartRequired   = "stream_start_required"
	ProtocolCodeStreamStopBeforeStart = "stream_stop_before_start"
	ProtocolCodeUnsupportedFrame      = "unsupported_frame"
)

var (
	errInvalidControlMessage = errors.New("invalid control message")
	errStreamAlreadyStarted  = errors.New("stream already started")
	errStreamStartRequired   = errors.New("stream.start required before audio frames")
	errStreamStopBeforeStart = errors.New("stream.stop received before stream.start")
	errUnsupportedFrame      = errors.New("unsupported websocket frame")
	errMissingCallID         = errors.New("stream.start requires callId")
	errInvalidStreamMeta     = errors.New("stream.start requires valid stream metadata")
)

type ControlMessage struct {
	Type   string             `json:"type"`
	CallID string             `json:"callId,omitempty"`
	Caller string             `json:"caller,omitempty"`
	Reason string             `json:"reason,omitempty"`
	Stream session.StreamMeta `json:"stream,omitempty"`
}

type ServerMessage struct {
	Type      string `json:"type"`
	Accepted  bool   `json:"accepted,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Code      string `json:"code,omitempty"`
	Error     string `json:"error,omitempty"`
}

type StreamAudioMessage struct {
	Type string          `json:"type"`
	Data StreamAudioData `json:"data"`
}

type StreamAudioData struct {
	AudioDataType string `json:"audioDataType"`
	SampleRate    int    `json:"sampleRate"`
	AudioData     string `json:"audioData"`
}

func parseControlMessage(payload []byte) (ControlMessage, error) {
	var message ControlMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return ControlMessage{}, errInvalidControlMessage
	}
	if message.Type == "" {
		return ControlMessage{}, errInvalidControlMessage
	}

	switch message.Type {
	case ControlTypeStreamStart:
		if message.CallID == "" {
			return ControlMessage{}, errMissingCallID
		}
		if message.Stream.Encoding == "" || message.Stream.SampleRateHz <= 0 || message.Stream.Channels <= 0 {
			return ControlMessage{}, errInvalidStreamMeta
		}
	case ControlTypeStreamStop:
	default:
		return ControlMessage{}, errInvalidControlMessage
	}

	return message, nil
}

func protocolCode(err error) string {
	switch err {
	case errStreamAlreadyStarted:
		return ProtocolCodeStreamAlreadyStarted
	case errStreamStartRequired:
		return ProtocolCodeStreamStartRequired
	case errStreamStopBeforeStart:
		return ProtocolCodeStreamStopBeforeStart
	case errUnsupportedFrame:
		return ProtocolCodeUnsupportedFrame
	default:
		return ProtocolCodeInvalidControlMessage
	}
}

func normalizeStopReason(reason string) string {
	if reason == "" {
		return "hangup"
	}
	return reason
}

func newStreamAudioMessage(stream session.StreamMeta, audio tts.AudioPayload) StreamAudioMessage {
	sampleRate := audio.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = stream.SampleRateHz
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	audioBytes, audioType := tts.StreamAudioPayload(audio.Format, audio.Bytes)
	if audioType == "" {
		audioType = "raw"
	}

	return StreamAudioMessage{
		Type: ServerTypeStreamAudio,
		Data: StreamAudioData{
			AudioDataType: audioType,
			SampleRate:    sampleRate,
			AudioData:     base64.StdEncoding.EncodeToString(audioBytes),
		},
	}
}
