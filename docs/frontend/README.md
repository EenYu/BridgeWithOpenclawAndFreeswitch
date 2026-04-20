# Frontend Console Runbook

## Scope

This note covers how to run and build the React frontend locally, which environment
variables are used for real integration, and what to verify on the main pages when
the console is connected to a live backend.

## Workspace

- Frontend root: `frontend/`
- Stack: React 18 + TypeScript + Vite
- Default dev server: `http://127.0.0.1:5173`

## Install

From the repository root:

```powershell
cd frontend
npm install
```

## Run Locally

```powershell
cd frontend
npm run dev
```

Notes:

- If no env vars are provided, REST requests use the current origin.
- If no WebSocket env var is provided, the client derives `ws://<current-host>/ws`
  or `wss://<current-host>/ws` from the browser location.
- For local split frontend/backend development, set `VITE_API_BASE_URL` and
  `VITE_WS_URL` explicitly.

## Build

```powershell
cd frontend
npm run build
```

The build command performs:

1. TypeScript type checking with `tsc --noEmit`
2. Production bundle generation with Vite

## Environment Variables

Create `frontend/.env.local` for local overrides, or copy from
`frontend/.env.example`.

### `VITE_API_BASE_URL`

- Purpose: base URL for REST requests
- Used by: `frontend/src/lib/api.ts`
- Example local value:

```env
VITE_API_BASE_URL=http://127.0.0.1:8080
```

- Example reverse-proxy value:

```env
VITE_API_BASE_URL=https://bridge.example.com
```

### `VITE_WS_URL`

- Purpose: dashboard WebSocket endpoint for live events
- Used by: `frontend/src/lib/ws.ts`
- Example local value:

```env
VITE_WS_URL=ws://127.0.0.1:8080/ws
```

- Example TLS value:

```env
VITE_WS_URL=wss://bridge.example.com/ws
```

## Current Backend Contract Assumptions

The frontend currently adapts to these backend shapes:

- `GET /api/health`
- `GET /api/sessions`
- `GET /api/sessions/{id}`
- `POST /api/settings/providers`
- `GET /ws` using the envelope `{ type, sessionId, timestamp, data }`

The compatibility logic lives in:

- `frontend/src/lib/api.ts`
- `frontend/src/lib/ws.ts`
- `frontend/src/lib/bridge-contract.ts`

## Additional Validation Asset

For the shortest possible real-call verification flow after FreeSWITCH is wired to
the bridge, use:

- `docs/frontend/live-call-checklist.md`

## Real Integration Validation Checklist

Use this list when the frontend is pointed at a live backend or staging proxy.

### 1. Dashboard boot

Expected:

- Opening `/` loads without blank screen or runtime error
- `GET /api/health` resolves successfully
- `GET /api/sessions` resolves successfully
- Health cards render bridge status, checked time, active sessions, and dependency summary

Failure checks:

- `VITE_API_BASE_URL` points to wrong host or port
- backend CORS / proxy route missing
- frontend still talking to same-origin when split deployment was intended

### 2. Dashboard session list

Expected:

- Session table renders rows when backend has active or cached sessions
- `callId`, `caller`, `state`, `startedAt`, and latest transcript are readable
- Clicking a row opens `/sessions/:id`

Failure checks:

- backend returns empty list unexpectedly
- stale proxy cache
- session payload no longer matches the adapter assumptions in `bridge-contract.ts`

### 3. Dashboard WebSocket observation

Expected:

- Browser establishes a WebSocket connection to `/ws`
- New `session.created` and `session.updated` events change the session list without refresh
- Transcript and TTS events append readable items to the realtime log panel

Failure checks:

- `VITE_WS_URL` points to the wrong scheme or host
- reverse proxy not forwarding WebSocket upgrade headers
- event payload changed but adapter was not updated

### 4. Session detail fetch

Expected:

- Opening `/sessions/:id` issues `GET /api/sessions/{id}`
- Summary section shows caller, bridge node, playback state, stream info, and providers
- Transcript timeline renders existing transcript entries when available
- Provider runtime cards render STT / OpenClaw / TTS binding, status, detail, and latency
- When `providerLatencies` is empty, the page falls back to `/api/health.services[].status/detail/latencyMs` instead of staying in a permanent placeholder state
- Empty states remain readable if both detail and health responses omit optional metrics

Failure checks:

- session ID route does not match backend lookup ID
- backend returns `404 session not found`
- detail payload shape diverged from `normalizeSessionDetail`
- `/api/health` is failing, so runtime cards can only show bindings and no live provider metrics

### 5. Interrupt flow

Expected:

- Clicking `Interrupt TTS` sends `POST /api/sessions/{id}/interrupt`
- Button enters pending state while the request is in flight
- On success, detail view refreshes and playback state returns to listening/idle behavior
- Related WebSocket events appear in the session log

Failure checks:

- session already closed
- backend interrupt path returns `404` or `500`
- TTS stop event is emitted but not reflected in detail state

### 6. WebSocket event walkthrough

For a real streamed call, verify this sequence from browser DevTools and the page:

1. `session.created` appears after stream start
2. `session.updated` reflects state changes
3. `session.transcript.partial` updates live transcript/log context
4. `session.transcript.final` updates the latest transcript
5. `session.tts.started` marks the session as speaking
6. `session.tts.stopped` returns the session to listening after interrupt
7. `session.closed` eventually closes the session after hangup

For the current frontend behavior:

- Dashboard still trusts `session.transcript.final` as the complete utterance and does not aggregate partials
- SessionDetail aggregates repeated partial frames so incremental STT updates remain readable while recognition is in progress
- SessionDetail provider runtime cards prefer session-level latency data, and otherwise fall back to the latest health snapshot for status/detail/latency
- Verifying what OpenClaw actually received still requires backend log or request inspection; the frontend can only confirm the UI-side transcript rendering

### 7. Settings page verification

Expected:

- Opening `/settings` loads provider data through the current backend health/config path
- Saving settings posts back to `POST /api/settings/providers`
- Vendor / endpoint / model / API key hint values round-trip correctly

Failure checks:

- backend field naming drift between `vendor/apiKeyHint/openclaw` and legacy aliases
- proxy strips request body
- settings save succeeds but health snapshot still exposes old values

## Recommended Browser Checks

In DevTools:

- Network > verify `GET /api/health`, `GET /api/sessions`, `GET /api/sessions/{id}`
- Network > verify `POST /api/sessions/{id}/interrupt`
- Network > inspect the `/ws` frame stream
- Console > confirm no client-side exceptions during route changes

## Minimum Sign-Off For Frontend Staging

Before considering the frontend ready for staging verification:

1. `npm run build` passes
2. Dashboard renders against the target backend
3. Session detail renders against a real session ID
4. Interrupt request returns success against an active speaking session
5. WebSocket events are visible in the browser and reflected in the UI
