export type SessionState =
  | "idle"
  | "listening"
  | "recognizing"
  | "thinking"
  | "speaking"
  | "closed";

export interface SessionProviderBindings {
  stt: string;
  openclaw: string;
  tts: string;
}

export interface StreamMeta {
  encoding: string;
  sampleRateHz: number;
  channels: number;
}

export interface SessionSummary {
  id: string;
  callId: string;
  state: SessionState;
  caller: string;
  startedAt: string;
  updatedAt: string;
  closedAt?: string;
  lastTranscript?: string;
  providers: SessionProviderBindings;
  stream: StreamMeta;
}

export interface TranscriptEntry {
  id: string;
  text: string;
  kind: "partial" | "final" | "assistant";
  createdAt: string;
}

export interface ProviderLatency {
  provider: "stt" | "openclaw" | "tts";
  latencyMs: number;
  updatedAt: string;
}

export interface SessionLogEntry {
  id: string;
  level: "debug" | "info" | "warn" | "error";
  message: string;
  source: "bridge" | "freeswitch" | "stt" | "openclaw" | "tts" | "system";
  createdAt: string;
}

export interface SessionDetail extends SessionSummary {
  bridgeNode: string;
  providerStatus: {
    stt: string;
    openclaw: string;
    tts: string;
  };
  playbackActive: boolean;
  transcripts: TranscriptEntry[];
  recentLogs: SessionLogEntry[];
  providerLatencies: ProviderLatency[];
}
