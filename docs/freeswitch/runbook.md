# FreeSWITCH Bridge Staging Runbook

## Scope

This runbook is now aligned to the real staging host `<staging-fs-host>`:

- hostname: `FS1`
- OS: Debian 12 (bookworm)
- FreeSWITCH: `1.10.12-release`
- active binary path: `/usr/local/freeswitch/bin/freeswitch`
- CLI path: `/usr/local/freeswitch/bin/fs_cli`
- module directory: `/usr/local/freeswitch/mod`
- config directory: `/usr/local/freeswitch/conf`
- source tree already present: `/usr/src/freeswitch`
- no `systemctl` managed `freeswitch.service`

The goal is to make this host stream caller audio to the bridge endpoint:

- `wss://<bridge-public-host>/ws/freeswitch/stream`

For the current on-host validation on `FS1`, the bridge is already running locally at:

- `ws://127.0.0.1:8080/ws/freeswitch/stream`

## Current Host Findings

Confirmed on `FS1`:

- built-in `socket` only supports `<ip>[:<port>]`, not WebSocket
- built-in `uuid_audio` only controls a media bug on the local channel
- `mod_audio_fork` is not installed
- `mod_audio_stream` is now installed and loadable
- `fs_cli -x "module_exists mod_audio_stream"` returns `true`
- `fs_cli -x "show api" | grep uuid_audio_stream` reports:
  `uuid_audio_stream <uuid> [start | stop | send_text | pause | resume | graceful-shutdown ] [wss-url | path] [mono | mixed | stereo] [8000 | 16000] [metadata]`

Conclusion:

- the shortest workable path on this host is confirmed to be `mod_audio_stream`
- the bridge can now use `uuid_audio_stream` for real WebSocket audio ingress
- dialplan wiring and live call verification have now been proven on-host
- real STT / OpenClaw / TTS providers have now been activated on the on-host bridge
- the final audio playback path on `FS1` is not `mod_audio_stream` auto-playback; it is native FreeSWITCH `uuid_broadcast ... playback::<tmp.wav> aleg`

## Installed Module State

The module was installed from the Debian 12 package release:

- package: `mod-audio-stream_1.0.0_amd64.deb`
- installed file: `/usr/lib/freeswitch/mod/mod_audio_stream.so`

This host loads runtime modules from `/usr/local/freeswitch/mod`, so the working fix applied on `FS1` is:

```bash
ln -sf /usr/lib/freeswitch/mod/mod_audio_stream.so /usr/local/freeswitch/mod/mod_audio_stream.so
/usr/local/freeswitch/bin/fs_cli -x "load mod_audio_stream"
```

Success criteria already observed:

- `module_exists mod_audio_stream` returns `true`
- `uuid_audio_stream` appears in `show api`

## Persisting Module Availability

To keep the module available after restart, ensure `modules.conf.xml` contains:

```xml
<load module="mod_audio_stream"/>
```

Then reload or restart FreeSWITCH using the local runtime controls instead of `systemctl`.

## Why This Module Fits The Frozen Bridge Protocol

The bridge requires:

1. first WebSocket text frame: `stream.start`
2. then binary PCM frames
3. then `stream.stop` or close

`mod_audio_stream` is the viable fit on this host because it can:

- open `ws://` or `wss://` sessions from FreeSWITCH
- stream mono `16k` PCM audio
- attach start metadata at stream open
- attach stop metadata on stream close

That lets us send the exact JSON envelope already frozen in `docs/freeswitch/integration.md`.

## Validated TTS Playback Strategy On `FS1`

The final on-host production finding is more specific than the original design assumption:

- `mod_audio_stream` is reliable for WebSocket audio ingress from FreeSWITCH into the bridge
- `mod_audio_stream` on this host was not reliable for audible TTS playback, even after validating both:
  - `wav/8000` return payloads
  - `raw/8000` return payloads
- in both cases the module accepted the response and created temporary files under `/tmp`, but the caller still heard no audio

What was proven to work:

- the same TTS WAV file was playable through native FreeSWITCH commands
- `uuid_broadcast <uuid> playback::<tmp.wav> aleg` produced audible output on the live call

Because of that, the deployed bridge on `FS1` now uses this split strategy:

- ingress: `uuid_audio_stream` over WebSocket
- egress: bridge writes a temporary WAV file locally on `FS1` and invokes native FreeSWITCH playback with `uuid_broadcast`

This is the current known-good path for `<staging-fs-host>`.

## Dialplan Pattern For This Host

After `mod_audio_stream` is installed, the dialplan should use:

```text
uuid_audio_stream <uuid> start <ws-or-wss-url> mono 16000 <stream.start-json>
```

And on hangup:

```text
uuid_audio_stream <uuid> stop
```

The working example for this repository is:

- `docs/freeswitch/examples/inbound_bridge.xml`

The files currently deployed on `FS1` are:

- `/usr/local/freeswitch/conf/dialplan/default/20_bridge_ai.xml`
- `/usr/local/freeswitch/conf/dialplan/public/20_bridge_ai.xml`

The safe test entrypoints currently in use are:

- `default/9199`
- `public/5551213` which transfers to `9199 XML default`

## Runtime Parameters To Freeze Before Real Calls

These values still must be fixed per environment:

- bridge public HTTP host and port
- bridge public WebSocket URL
- TLS termination and CA trust for `wss://`
- PCM encoding
- sample rate
- channel count
- frame duration
- upstream STT / OpenClaw / TTS endpoints and credentials
- TTS audio return path back into FreeSWITCH

Reference values for the current bridge contract:

- encoding: `pcm_s16le`
- sample rate: `16000`
- channels: `1`
- recommended frame size: `20ms`

Reference values for the current `FS1` playback path:

- bridge TTS provider returns `wav/8000`
- bridge stores the synthesized WAV on local disk
- bridge executes `uuid_broadcast <call-uuid> playback::<tmp.wav> aleg`

## End-To-End Validation Checklist

### Step 1. Module readiness on `FS1`

Expected:

- `/usr/local/freeswitch/mod/mod_audio_stream.so` exists
- `/usr/local/freeswitch/bin/fs_cli -x "module_exists mod_audio_stream"` returns `true`
- `/usr/local/freeswitch/bin/fs_cli -x "help uuid_audio_stream"` returns syntax

Failure checks:

- symlink from `/usr/local/freeswitch/mod` to `/usr/lib/freeswitch/mod` missing
- module package installed but not loaded
- ABI mismatch between runtime FreeSWITCH and installed module

### Step 2. Bridge reachability

Expected:

- `GET /api/health` returns `200`
- `status` is `ok` or expected degraded state
- `checkedAt`, `activeSessions`, `services[]` are populated

Failure checks:

- reverse proxy route missing
- public hostname mismatch
- backend not listening on expected port

### Step 3. WebSocket ingress handshake

Expected:

- `uuid_audio_stream` connects to `/ws/freeswitch/stream`
- backend returns `stream.ack`
- dashboard receives `session.created`

Failure checks:

- wrong `ws://` vs `wss://`
- proxy not forwarding WebSocket upgrade headers
- `stream.start` metadata missing `callId` or stream fields

Observed on `2026-04-18`:

- `originate {origination_caller_id_number=13900009999,ignore_early_media=true}loopback/9199/default &park()` created one active bridge session and later cleaned it up
- `originate {origination_caller_id_number=13900007777,origination_caller_id_name=SelfTest,ignore_early_media=true,originate_timeout=20}sofia/external/sip:5551213@<staging-fs-host>:5080 &park()` created one active bridge session through the `public` context and later cleaned it up
- `GET http://127.0.0.1:8080/api/sessions` showed `state = "listening"` during the live call and returned `[]` after hangup

### Step 4. Audio upload

Expected:

- binary PCM frames are accepted after `stream.start`
- no `stream.error` with `stream_start_required`
- session stays active and leaves `idle`

Failure checks:

- audio starts before start metadata
- wrong sampling rate or channel count
- TLS handshake succeeds but media frames never arrive

### Step 5. STT -> OpenClaw -> TTS path

Expected:

- dashboard receives `session.transcript.partial`
- dashboard receives `session.transcript.final`
- dashboard receives `session.tts.started`
- session moves through `thinking` and `speaking`
- bridge log shows `orchestrator output play ok`
- FreeSWITCH log shows native playback activity for the same call UUID when TTS is delivered

Failure checks:

- provider credentials missing
- provider endpoint unreachable
- transcript never finalizes
- frontend still reads an old event envelope
- `orchestrator tts synth ok` is present but there is no corresponding FreeSWITCH playback command
- bridge is still trying `mod_audio_stream` auto-playback instead of native `uuid_broadcast`

### Step 6. Interrupt and hangup cleanup

Expected:

- caller speech during TTS produces `session.tts.stopped`
- hangup sends `stream.stop`
- dashboard receives `session.updated` then `session.closed`

Failure checks:

- no hangup hook configured for `uuid_audio_stream ... stop`
- FreeSWITCH channel variables not expanded correctly in JSON metadata
- proxy or network closes the socket before stop metadata is delivered

## Failure Triage Order

When a real staging call fails, check in this order:

1. `mod_audio_stream` loaded on `FS1`
2. bridge public URL and TLS correctness
3. `stream.start` metadata validity
4. PCM format and frame ordering
5. provider network reachability
6. frontend event envelope compatibility

## Mock Verification Without FreeSWITCH

Use the included script:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\mock-freeswitch-stream.ps1 `
  -Uri ws://127.0.0.1:8080/ws/freeswitch/stream `
  -CallId call-staging-001 `
  -Caller +8613800138000 `
  -DurationMs 1000
```

This simulates:

- `stream.start`
- PCM silence frames
- `stream.stop`

It validates the bridge ingress protocol, but not the real FreeSWITCH media path.

## Current External Blockers

- there is no blocker for on-host `FS1` end-to-end validation anymore; live calls now have audible TTS
- if the bridge is later moved behind public `wss://`, certificate trust from FreeSWITCH to the public bridge must still be confirmed
- if the host topology changes and the bridge no longer runs on `FS1`, the native playback shortcut must be revisited because it currently assumes local filesystem and local `fs_cli`
