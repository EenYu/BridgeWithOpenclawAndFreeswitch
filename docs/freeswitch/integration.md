# FreeSWITCH Bridge WebSocket Integration

## Goal

V1 keeps FreeSWITCH thin. FreeSWITCH streams PCM audio to the backend bridge over WebSocket, and the backend owns session orchestration, provider calls, dashboard events, and TTS audio push-back to the media stream.

## Endpoints

- FreeSWITCH media ingress: `GET /ws/freeswitch/stream`
- Dashboard events: `GET /ws`

## Real FreeSWITCH Module

On staging host `<staging-fs-host>` (`FS1`), the working media module is:

- `mod_audio_stream`
- API syntax:
  `uuid_audio_stream <uuid> [start | stop | send_text | pause | resume | graceful-shutdown ] [wss-url | path] [mono | mixed | stereo] [8000 | 16000] [metadata]`

The bridge endpoint is compatible with this module by using the `metadata` argument to carry the frozen `stream.start` JSON envelope.

## Audio Contract

- Encoding: `pcm_s16le`
- Sample rate: `16000`
- Channels: `1`
- Recommended frame size: 20ms or 40ms

## FreeSWITCH Control Protocol

### Client -> Server: `stream.start`

The first text frame must be:

```json
{
  "type": "stream.start",
  "callId": "call-123",
  "caller": "+8613800138000",
  "stream": {
    "encoding": "pcm_s16le",
    "sampleRateHz": 16000,
    "channels": 1
  }
}
```

Required fields:

- `type`
- `callId`
- `stream.encoding`
- `stream.sampleRateHz`
- `stream.channels`

Success response:

```json
{
  "type": "stream.ack",
  "accepted": true,
  "sessionId": "sess_123"
}
```

When using `mod_audio_stream`, the `metadata` parameter should be this exact JSON string so the first WebSocket text frame already matches the bridge contract.

### Client -> Server: audio frames

After `stream.start`, each binary WebSocket frame is raw PCM bytes for one audio chunk.

### Client -> Server: `stream.stop`

The client may end the stream explicitly:

```json
{
  "type": "stream.stop",
  "reason": "hangup"
}
```

If `reason` is empty, the backend normalizes it to `hangup`.

### Connection close

If the WebSocket closes without `stream.stop`, the backend treats it as:

- close reason: `websocket_disconnected`

This is currently the safest hangup path for `mod_audio_stream` on `FS1`: invoke `uuid_audio_stream <uuid> stop` and let the backend clean up from the connection close if no explicit stop metadata is delivered.

## Server -> FreeSWITCH Audio Playback

The bridge still supports a `streamAudio` WebSocket response compatible with `mod_audio_stream`:

```json
{
  "type": "streamAudio",
  "data": {
    "audioDataType": "raw",
    "sampleRate": 16000,
    "audioData": "<base64-encoded-raw-pcm>"
  }
}
```

Notes:

- fallback `audioDataType` is currently `raw`
- `sampleRate` uses the synthesized audio sample rate when available
- `audioData` is base64-encoded raw PCM payload

### Current `FS1` deployment note

On `<staging-fs-host>` (`FS1`), the final working playback path is more specific:

- bridge ingress still uses `mod_audio_stream`
- for synthesized `wav/8000` audio, the bridge now prefers native FreeSWITCH playback:
  `uuid_broadcast <callId> playback::<tmp.wav> aleg`
- the `streamAudio` WebSocket path remains as a compatibility fallback, but it is not the primary audible playback path on `FS1`

This host-specific behavior was adopted because `mod_audio_stream` accepted returned audio but did not reliably produce audible playback on the live call, while native `uuid_broadcast ... playback` did.

## Protocol Error Paths

On protocol errors the server replies with a text frame and closes the socket.

Error envelope:

```json
{
  "type": "stream.error",
  "code": "stream_start_required",
  "error": "stream.start required before audio frames"
}
```

Defined error codes:

- `invalid_control_message`
- `stream_already_started`
- `stream_start_required`
- `stream_stop_before_start`
- `unsupported_frame`

Covered invalid paths:

- malformed JSON control message
- unknown control message type
- `stream.start` missing `callId`
- `stream.start` missing stream metadata
- binary frame before `stream.start`
- duplicate `stream.start`
- `stream.stop` before `stream.start`

## Dashboard Event Envelope

The dashboard WebSocket uses a fixed envelope:

```json
{
  "type": "session.updated",
  "sessionId": "sess_123",
  "timestamp": "2026-04-18T08:00:00Z",
  "data": {}
}
```

Fixed event names:

- `session.created`
- `session.updated`
- `session.transcript.partial`
- `session.transcript.final`
- `session.tts.started`
- `session.tts.stopped`
- `session.closed`

Fixed `data` fields:

- `session.created`
  `data.session`
- `session.updated`
  `data.session`
- `session.transcript.partial`
  `data.transcript`, `data.final`
- `session.transcript.final`
  `data.transcript`, `data.final`
- `session.tts.started`
  `data.text`, `data.audioBytes`, `data.state`, `data.updatedAt`
- `session.tts.stopped`
  `data.reason`, `data.state`, `data.updatedAt`
- `session.closed`
  `data.session`, `data.reason`

### Fixed payload shapes

- `data.session` is a session summary object:
  `id`, `callId`, `state`, `caller`, `startedAt`, `lastTranscript`
- `data.transcript` is a transcript entry object:
  `id`, `text`, `kind`, `createdAt`
- `session.tts.started` always carries `state = "speaking"`
- `session.tts.stopped` currently carries `reason = "interrupted"` and `state = "listening"`
- `session.closed` carries the final closed session summary plus `reason`

## Session Update Guarantees

- `interrupt` path emits `session.tts.stopped` then `session.updated`
- `hangup` path emits `session.updated` then `session.closed`
- `session.updated.data.session.state` is the source of truth for frontend session state
