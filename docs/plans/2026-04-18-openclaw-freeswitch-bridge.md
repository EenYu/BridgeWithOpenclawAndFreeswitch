# OpenClaw FreeSWITCH Bridge Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a frontend/backend separated AI voice bridge so FreeSWITCH can stream call audio into a bridge service, the bridge service can run STT -> OpenClaw -> TTS, and the generated audio can be streamed back to FreeSWITCH.

**Architecture:** Use a standalone backend bridge service as the media orchestration layer. FreeSWITCH sends PCM audio to the backend through a streaming adapter, the backend maintains per-call sessions and provider adapters, and a separate frontend dashboard consumes REST/WebSocket APIs for monitoring, configuration, and debugging.

**Tech Stack:** Go backend, React + TypeScript frontend, REST + WebSocket, FreeSWITCH media streaming adapter, pluggable STT/TTS/OpenClaw clients.

---

## Recommended Approach

### Option A: FreeSWITCH streams PCM over WebSocket to the backend bridge

Recommended. Keep FreeSWITCH thin and move orchestration into a Go service. This avoids writing a heavy native FreeSWITCH module up front and keeps STT/TTS/OpenClaw integration isolated from telephony concerns.

### Option B: Write a native FreeSWITCH module with media bug callbacks

Lower latency and deeper FS control, but much higher implementation and maintenance cost. This should only be used if the WebSocket streaming approach cannot satisfy latency or codec constraints.

### Option C: Build an external SIP B2BUA

Most flexible but unnecessary for v1. Too much complexity for the stated requirement.

## Target Repository Layout

```text
backend/
  cmd/bridge-server/main.go
  go.mod
  internal/config/config.go
  internal/session/session.go
  internal/session/manager.go
  internal/freeswitch/stream_server.go
  internal/freeswitch/esl_client.go
  internal/pipeline/orchestrator.go
  internal/stt/client.go
  internal/tts/client.go
  internal/openclaw/client.go
  internal/httpapi/router.go
  internal/httpapi/handlers.go
  internal/ws/hub.go
  internal/metrics/metrics.go
  tests/
frontend/
  package.json
  src/main.tsx
  src/App.tsx
  src/pages/Dashboard.tsx
  src/pages/SessionDetail.tsx
  src/pages/Settings.tsx
  src/components/SessionTable.tsx
  src/components/EventLogPanel.tsx
  src/components/HealthCards.tsx
  src/lib/api.ts
  src/lib/ws.ts
  src/types/session.ts
docs/
  plans/2026-04-18-openclaw-freeswitch-bridge.md
  api/openapi.yaml
  freeswitch/integration.md
deploy/
  docker-compose.yml
  backend.Dockerfile
  frontend.Dockerfile
```

## Contract Summary

- FreeSWITCH -> backend: streaming PCM audio, call lifecycle events, hangup notifications.
- Backend -> STT: streaming audio frames, partial/final transcripts.
- Backend -> OpenClaw: user transcript plus session context.
- Backend -> TTS: response text plus interrupt/cancel support.
- Backend -> FreeSWITCH: synthesized audio frames or stream URL depending on integration mode.
- Frontend -> backend: REST for config/status, WebSocket for live session updates and logs.

## Core Non-Functional Requirements

- Per-call state isolation and cleanup on hangup.
- Barge-in support: stop TTS playback when caller starts speaking again.
- Backpressure handling for streaming providers.
- Provider abstraction so STT/TTS/OpenClaw implementations can be swapped.
- Full structured logs by `call_id` / `session_id`.

### Task 1: Architecture And API Contract

**Files:**
- Create: `docs/api/openapi.yaml`
- Create: `docs/freeswitch/integration.md`
- Create: `backend/internal/session/session.go`
- Create: `backend/internal/pipeline/orchestrator.go`

**Step 1: Define the session model**

Document and implement the initial session state machine:

```go
type SessionState string

const (
    StateIdle        SessionState = "idle"
    StateListening   SessionState = "listening"
    StateRecognizing SessionState = "recognizing"
    StateThinking    SessionState = "thinking"
    StateSpeaking    SessionState = "speaking"
    StateClosed      SessionState = "closed"
)
```

**Step 2: Define backend APIs**

Create endpoints for:
- `GET /api/health`
- `GET /api/sessions`
- `GET /api/sessions/{id}`
- `POST /api/settings/providers`
- `POST /api/sessions/{id}/interrupt`

**Step 3: Define WebSocket events**

Create event names and payloads for:
- `session.created`
- `session.updated`
- `session.transcript.partial`
- `session.transcript.final`
- `session.tts.started`
- `session.tts.stopped`
- `session.closed`

**Step 4: Commit**

```bash
git add docs/api/openapi.yaml docs/freeswitch/integration.md backend/internal/session/session.go backend/internal/pipeline/orchestrator.go
git commit -m "docs: define bridge architecture and contracts"
```

### Task 2: Backend Bridge Skeleton

**Files:**
- Create: `backend/go.mod`
- Create: `backend/cmd/bridge-server/main.go`
- Create: `backend/internal/config/config.go`
- Create: `backend/internal/session/manager.go`
- Create: `backend/internal/freeswitch/stream_server.go`
- Create: `backend/internal/httpapi/router.go`
- Test: `backend/tests/session_manager_test.go`

**Step 1: Write the failing test**

```go
func TestSessionManagerCreateAndClose(t *testing.T) {
    mgr := session.NewManager()
    s := mgr.Create("call-123")
    if s.CallID != "call-123" {
        t.Fatalf("expected call id call-123, got %s", s.CallID)
    }
    mgr.Close("call-123")
    if _, ok := mgr.Get("call-123"); ok {
        t.Fatal("expected session to be removed")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tests/...`

**Step 3: Implement minimal backend skeleton**

Stand up:
- config loading
- session manager
- HTTP router
- FreeSWITCH stream listener interface

**Step 4: Run tests**

Run: `go test ./...`

**Step 5: Commit**

```bash
git add backend
git commit -m "feat: scaffold backend bridge service"
```

### Task 3: Frontend Console Skeleton

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/pages/Dashboard.tsx`
- Create: `frontend/src/pages/SessionDetail.tsx`
- Create: `frontend/src/pages/Settings.tsx`
- Create: `frontend/src/components/SessionTable.tsx`
- Create: `frontend/src/components/EventLogPanel.tsx`
- Create: `frontend/src/lib/api.ts`
- Create: `frontend/src/lib/ws.ts`

**Step 1: Create typed API contracts**

Define frontend types for:

```ts
export interface SessionSummary {
  id: string;
  callId: string;
  state: "idle" | "listening" | "recognizing" | "thinking" | "speaking" | "closed";
  caller: string;
  startedAt: string;
  lastTranscript?: string;
}
```

**Step 2: Build dashboard skeleton**

Implement:
- session list
- active state cards
- live log panel
- provider settings form

**Step 3: Connect WebSocket updates**

Subscribe to session lifecycle and transcript events, and patch the dashboard state incrementally.

**Step 4: Verify**

Run: `npm run build`

**Step 5: Commit**

```bash
git add frontend
git commit -m "feat: scaffold frontend operations console"
```

### Task 4: Provider Adapters And Streaming Pipeline

**Files:**
- Create: `backend/internal/stt/client.go`
- Create: `backend/internal/tts/client.go`
- Create: `backend/internal/openclaw/client.go`
- Create: `backend/internal/pipeline/orchestrator.go`
- Test: `backend/tests/orchestrator_test.go`

**Step 1: Write failing orchestration test**

Test the sequence:
- receive final transcript
- call OpenClaw
- request TTS
- emit playback event

**Step 2: Implement provider interfaces**

Define narrow interfaces:

```go
type STTClient interface {
    PushAudio(ctx context.Context, sessionID string, pcm []byte) error
}

type OpenClawClient interface {
    Reply(ctx context.Context, sessionID string, transcript string) (string, error)
}

type TTSClient interface {
    Synthesize(ctx context.Context, sessionID string, text string) ([]byte, error)
}
```

**Step 3: Implement interrupt logic**

If new caller speech is detected while TTS playback is active, stop output and move session state back to `listening`.

**Step 4: Run tests**

Run: `go test ./...`

**Step 5: Commit**

```bash
git add backend
git commit -m "feat: add streaming ai pipeline"
```

### Task 5: Frontend Observability And Control

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`
- Modify: `frontend/src/pages/SessionDetail.tsx`
- Modify: `frontend/src/components/EventLogPanel.tsx`
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/lib/ws.ts`

**Step 1: Add session drill-down**

Display:
- transcript timeline
- provider latency
- playback status
- manual interrupt control

**Step 2: Add settings validation**

Validate required provider credentials and endpoint URLs before save.

**Step 3: Verify**

Run:
- `npm run lint`
- `npm run build`

**Step 4: Commit**

```bash
git add frontend
git commit -m "feat: add dashboard observability and control flows"
```

### Task 6: End-To-End Integration, Deployment, And Runbook

**Files:**
- Create: `deploy/docker-compose.yml`
- Create: `deploy/backend.Dockerfile`
- Create: `deploy/frontend.Dockerfile`
- Create: `docs/freeswitch/runbook.md`
- Test: `backend/tests/e2e_call_flow_test.go`

**Step 1: Define the e2e acceptance test**

Successful v1 flow:
- incoming call creates session
- caller speech reaches STT
- transcript reaches OpenClaw
- TTS response is produced
- audio is returned to FreeSWITCH
- dashboard shows final session log

**Step 2: Containerize services**

Package backend and frontend separately and expose required environment variables for STT/TTS/OpenClaw providers.

**Step 3: Document FreeSWITCH hookup**

Document:
- dialplan changes
- stream adapter configuration
- codec / sample rate expectations
- health checks
- troubleshooting steps

**Step 4: Run verification**

Run:
- `go test ./...`
- `npm run build`
- manual call test against staging FreeSWITCH

**Step 5: Commit**

```bash
git add deploy docs backend/tests
git commit -m "chore: add deployment and e2e verification"
```

## Member Assignment For This Channel

- Frontend member `01KPFR9H6BWFGDQ29RAQK1Q0A3`: start with Task 3, then Task 5.
- Backend member `01KPFRABRDC85JK7HAPMTYM7DD`: start with Task 1 backend portions and Task 2, then Task 4 and Task 6 backend portions.
- Assistant `01KPFRB0ZQAZ6QR13AF25QAAC8`: maintain roadmap, enforce contracts, and coordinate API/event alignment between frontend and backend.

## Immediate Decisions To Hold Constant

- Use frontend/backend separation from day one.
- Use Go for backend service because FreeSWITCH integration and streaming control are easier to keep efficient and deployable.
- Use React + TypeScript for frontend dashboard.
- Prefer WebSocket PCM streaming from FreeSWITCH into the backend service for v1.
- Keep provider integrations behind interfaces so STT/TTS/OpenClaw vendors stay swappable.

Plan complete and saved to `docs/plans/2026-04-18-openclaw-freeswitch-bridge.md`. Two execution options:

1. Subagent-Driven (this session) - I dispatch fresh subagent per task, review between tasks, fast iteration
2. Parallel Session (separate) - Open new session with executing-plans, batch execution with checkpoints

Which approach?
