# Frontend Minimal Live-Call Checklist

## Scope

Use this checklist immediately after a real FreeSWITCH call can reach the bridge.
It is intentionally minimal and focused on what the frontend operator should
watch in the browser while the live call is in progress.

## Preconditions

Before starting the call:

1. Open the frontend in a browser and land on `/`
2. Open DevTools
3. Keep these tabs visible:
   - Network
   - Console
   - the `/ws` WebSocket frame inspector
4. Confirm the frontend is pointing at the target backend:
   - `VITE_API_BASE_URL`
   - `VITE_WS_URL`
5. Confirm Dashboard already loads:
   - health cards render
   - session list does not crash
   - realtime log panel is visible

## Live-Call Steps

### Step 1. Start the real call

Action:

- Place an inbound call through FreeSWITCH so the media adapter opens the bridge stream.

Watch for:

- Dashboard session list gets a new row
- Realtime log panel receives a new event
- WebSocket `/ws` shows `session.created`

Expected event order:

1. `session.created`
2. at least one `session.updated`

If this fails:

- Check whether `/ws` is connected at all
- Check whether the backend created the session but the frontend failed to parse the event

### Step 2. Open SessionDetail for the new call

Action:

- Click the new row in Dashboard to enter `/sessions/:id`

Watch for:

- Browser sends `GET /api/sessions/{id}`
- SessionDetail renders caller, state, stream info, and provider bindings
- No frontend runtime error appears in Console

Expected:

- SessionDetail is not a blank shell
- Summary state matches the latest Dashboard row

### Step 3. Speak into the live call

Action:

- Say a short phrase clearly into the call

Watch for:

- Dashboard log panel shows transcript activity
- SessionDetail transcript timeline starts to fill
- WebSocket frames show:
  1. `session.transcript.partial`
  2. `session.updated`
  3. `session.transcript.final`
  4. `session.updated`

Expected UI outcome:

- Dashboard row updates `lastTranscript`
- SessionDetail transcript timeline shows the recognized text

### Step 4. Observe the TTS response

Action:

- Wait for the bridge to produce the AI reply

Watch for:

- WebSocket shows `session.tts.started`
- Dashboard row state changes to `speaking`
- SessionDetail playback state changes to active
- SessionDetail log panel records the TTS start event

Expected event order after final transcript:

1. `session.transcript.final`
2. `session.updated`
3. `session.tts.started`
4. `session.updated`

### Step 5. Test Interrupt

Action:

- While the session is speaking, open SessionDetail and click `Interrupt TTS`

Watch for:

- Browser sends `POST /api/sessions/{id}/interrupt`
- Button enters pending state briefly
- WebSocket shows:
  1. `session.tts.stopped`
  2. `session.updated`

Expected UI outcome:

- SessionDetail playback switches away from active
- Session state returns to listening behavior
- Session log records the stop reason

If this fails:

- Check the interrupt request status code
- Check whether WebSocket events were emitted but not reflected in UI

### Step 6. Hang up the call

Action:

- End the live call from the caller side or through FreeSWITCH

Watch for:

- Dashboard row moves to `closed` state or disappears according to backend behavior
- WebSocket shows:
  1. `session.updated`
  2. `session.closed`

Expected:

- SessionDetail no longer appears active
- Dashboard active session count eventually drops

## Minimum Event Sequence To Confirm

For one successful live call, confirm this sequence is visible from the browser:

1. `session.created`
2. `session.updated`
3. `session.transcript.partial`
4. `session.updated`
5. `session.transcript.final`
6. `session.updated`
7. `session.tts.started`
8. `session.updated`
9. `session.tts.stopped` after manual interrupt
10. `session.updated`
11. `session.closed` after hangup

## Quick Failure Notes

- No Dashboard row: check `session.created` is emitted and parsed
- Row appears but detail is empty: check `GET /api/sessions/{id}`
- Transcript events visible but UI unchanged: check adapter logic for transcript payload
- Interrupt request succeeds but UI stays speaking: check `session.tts.stopped` and follow-up `session.updated`
- Events out of order: inspect raw `/ws` frames first, then compare with page state
