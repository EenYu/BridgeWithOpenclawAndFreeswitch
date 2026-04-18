import type {
  SessionDetail,
  SessionLogEntry,
  SessionState,
  SessionSummary,
  TranscriptEntry,
} from "./session";

export type BridgeEventName =
  | "session.created"
  | "session.updated"
  | "session.transcript.partial"
  | "session.transcript.final"
  | "session.tts.started"
  | "session.tts.stopped"
  | "session.closed"
  | "log.entry";

export interface SessionLifecycleEvent {
  type: "session.created" | "session.updated" | "session.closed";
  payload: SessionSummary & {
    bridgeNode?: string;
  };
}

export interface TranscriptEvent {
  type: "session.transcript.partial" | "session.transcript.final";
  payload: {
    sessionId: string;
    transcript: TranscriptEntry;
  };
}

export interface TtsEvent {
  type: "session.tts.started" | "session.tts.stopped";
  payload: {
    sessionId: string;
    state: Extract<SessionState, "speaking" | "listening">;
    updatedAt: string;
    text?: string;
    reason?: string;
    audioBytes?: number;
  };
}

export interface LogEvent {
  type: "log.entry";
  payload: {
    sessionId?: string;
    entry: SessionLogEntry;
  };
}

export type BridgeEvent = SessionLifecycleEvent | TranscriptEvent | TtsEvent | LogEvent;

export interface BridgeEventMap {
  "session.created": SessionLifecycleEvent;
  "session.updated": SessionLifecycleEvent;
  "session.transcript.partial": TranscriptEvent;
  "session.transcript.final": TranscriptEvent;
  "session.tts.started": TtsEvent;
  "session.tts.stopped": TtsEvent;
  "session.closed": SessionLifecycleEvent;
  "log.entry": LogEvent;
}

export interface SessionDetailPatch {
  summary?: Partial<SessionSummary>;
  detail?: Partial<SessionDetail>;
}
