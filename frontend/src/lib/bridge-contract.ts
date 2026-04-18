import type { HealthStatus, ServiceHealth, ServiceStatus } from "../types/health";
import type { ProviderConfig, ProviderSettings } from "../types/providers";
import type {
  ProviderLatency,
  SessionDetail,
  SessionLogEntry,
  SessionProviderBindings,
  SessionState,
  SessionSummary,
  StreamMeta,
  TranscriptEntry,
} from "../types/session";
import type { BridgeEvent } from "../types/ws";

export interface BackendProviderConfig {
  vendor?: string;
  name?: string;
  endpoint?: string;
  model?: string;
  enabled?: boolean;
  apiKeyHint?: string;
  apiKeyEnv?: string;
}

export interface BackendProviders {
  stt?: BackendProviderConfig;
  openClaw?: BackendProviderConfig;
  openclaw?: BackendProviderConfig;
  tts?: BackendProviderConfig;
}

export interface BackendHealthResponse {
  status?: string;
  version?: string;
  checkedAt?: string;
  sessions?: number;
  activeSessions?: number;
  listen?: string;
  providers?: BackendProviders;
  services?: Array<{
    name?: string;
    status?: string;
    detail?: string;
    latencyMs?: number;
  }>;
}

export interface BackendSession {
  id?: string;
  callId?: string;
  caller?: string;
  state?: string;
  startedAt?: string;
  updatedAt?: string;
  closedAt?: string | null;
  lastTranscript?: string;
  providers?: {
    stt?: string;
    openClaw?: string;
    openclaw?: string;
    tts?: string;
  };
  stream?: Partial<StreamMeta>;
  bridgeNode?: string;
  playbackActive?: boolean;
  transcripts?: TranscriptEntry[];
  recentLogs?: SessionLogEntry[];
  providerLatencies?: ProviderLatency[];
  providerStatus?: Partial<SessionDetail["providerStatus"]>;
}

interface BackendTranscriptPayload {
  id?: string;
  text?: string;
  kind?: string;
  createdAt?: string;
}

export interface BackendSessionsResponse {
  items?: BackendSession[];
}

export interface BackendEventEnvelope {
  type?: string;
  sessionId?: string;
  timestamp?: string;
  data?: Record<string, unknown>;
}

const DEFAULT_STREAM: StreamMeta = {
  encoding: "unknown",
  sampleRateHz: 0,
  channels: 0,
};

const DEFAULT_PROVIDER_BINDINGS: SessionProviderBindings = {
  stt: "unknown",
  openclaw: "unknown",
  tts: "unknown",
};

const DEFAULT_PROVIDER_CONFIG: ProviderConfig = {
  vendor: "",
  endpoint: "",
  model: "",
  apiKeyHint: "",
  enabled: false,
};

export function normalizeProviderSettings(raw?: BackendProviders): ProviderSettings {
  return {
    stt: normalizeProviderConfig(raw?.stt),
    openclaw: normalizeProviderConfig(raw?.openClaw ?? raw?.openclaw),
    tts: normalizeProviderConfig(raw?.tts),
  };
}

export function serializeProviderSettings(settings: ProviderSettings): BackendProviders {
  return {
    stt: serializeProviderConfig(settings.stt),
    openClaw: serializeProviderConfig(settings.openclaw),
    tts: serializeProviderConfig(settings.tts),
  };
}

export function normalizeHealthResponse(raw: BackendHealthResponse): HealthStatus {
  const providerSettings = normalizeProviderSettings(raw.providers);
  const checkedAt = normalizeTimestamp(raw.checkedAt);
  const services =
    raw.services && raw.services.length > 0
      ? raw.services.map((service) => ({
          name: service.name ?? "unknown",
          status: normalizeServiceStatus(service.status),
          detail: service.detail ?? "未提供详情",
          latencyMs: service.latencyMs,
        }))
      : buildProviderServices(providerSettings);

  return {
    status: normalizeServiceStatus(raw.status),
    version: raw.version ?? raw.listen ?? "unknown",
    checkedAt,
    activeSessions: raw.activeSessions ?? raw.sessions ?? 0,
    services,
  };
}

export function normalizeSessionListResponse(
  raw: BackendSessionsResponse | BackendSession[],
): SessionSummary[] {
  const items = Array.isArray(raw) ? raw : raw.items ?? [];
  return items.map((item) => normalizeSessionSummary(item));
}

export function normalizeSessionSummary(raw: BackendSession): SessionSummary {
  const id = raw.id ?? raw.callId ?? "unknown-session";
  const updatedAt = normalizeTimestamp(raw.updatedAt ?? raw.startedAt);

  return {
    id,
    callId: raw.callId ?? id,
    caller: raw.caller ?? "未知来电",
    state: normalizeSessionState(raw.state),
    startedAt: normalizeTimestamp(raw.startedAt),
    updatedAt,
    closedAt: raw.closedAt ? normalizeTimestamp(raw.closedAt) : undefined,
    lastTranscript: raw.lastTranscript,
    providers: normalizeProviderBindings(raw.providers),
    stream: normalizeStream(raw.stream),
  };
}

export function normalizeSessionDetail(raw: BackendSession): SessionDetail {
  const summary = normalizeSessionSummary(raw);
  const normalizedProviders: SessionProviderBindings = {
    stt: raw.providers?.stt ?? raw.providerStatus?.stt ?? summary.providers.stt,
    openclaw:
      raw.providers?.openClaw ??
      raw.providers?.openclaw ??
      raw.providerStatus?.openclaw ??
      summary.providers.openclaw,
    tts: raw.providers?.tts ?? raw.providerStatus?.tts ?? summary.providers.tts,
  };

  const seededTranscript =
    summary.lastTranscript && summary.lastTranscript.trim().length > 0
      ? [
          {
            id: `${summary.id}-last-transcript`,
            text: summary.lastTranscript,
            kind: "final" as const,
            createdAt: summary.updatedAt,
          },
        ]
      : [];

  return {
    ...summary,
    providers: normalizedProviders,
    bridgeNode: raw.bridgeNode ?? "待后端补充",
    providerStatus: {
      stt: raw.providerStatus?.stt ?? describeProvider(normalizedProviders.stt),
      openclaw: raw.providerStatus?.openclaw ?? describeProvider(normalizedProviders.openclaw),
      tts: raw.providerStatus?.tts ?? describeProvider(normalizedProviders.tts),
    },
    playbackActive:
      typeof raw.playbackActive === "boolean"
        ? raw.playbackActive
        : summary.state === "speaking",
    transcripts:
      raw.transcripts && raw.transcripts.length > 0
        ? raw.transcripts.map((entry) => normalizeTranscriptEntry(entry, summary.updatedAt))
        : seededTranscript,
    recentLogs:
      raw.recentLogs?.map((entry) => normalizeLogEntry(entry, summary.updatedAt)) ?? [],
    providerLatencies:
      raw.providerLatencies?.map((entry) =>
        normalizeProviderLatency(entry, summary.updatedAt),
      ) ?? [],
  };
}

export function normalizeBridgeEvent(raw: unknown): BridgeEvent | null {
  if (!isRecord(raw) || typeof raw.type !== "string") {
    return null;
  }

  const envelope = raw as BackendEventEnvelope;
  const timestamp = normalizeTimestamp(envelope.timestamp);
  const data = isRecord(envelope.data) ? envelope.data : {};
  const sessionId = resolveSessionId(envelope.sessionId, data);

  switch (envelope.type) {
    case "session.created":
    case "session.updated": {
      const session = normalizeSessionSummary(extractEventSession(data, sessionId, timestamp));
      return {
        type: envelope.type,
        payload: session,
      };
    }
    case "session.closed": {
      const session = normalizeSessionSummary({
        ...extractEventSession(data, sessionId, timestamp),
        state: "closed",
        closedAt: timestamp,
      });
      return {
        type: envelope.type,
        payload: { ...session, state: "closed", closedAt: timestamp },
      };
    }
    case "session.transcript.partial":
    case "session.transcript.final": {
      const transcriptPayload = normalizeTranscriptPayload(
        data.transcript,
        sessionId,
        envelope.type === "session.transcript.final" ? "final" : "partial",
        timestamp,
      );
      return {
        type: envelope.type,
        payload: {
          sessionId,
          transcript: transcriptPayload,
        },
      };
    }
    case "session.tts.started":
    case "session.tts.stopped":
      return {
        type: envelope.type,
        payload: {
          sessionId,
          state: envelope.type === "session.tts.started" ? "speaking" : "listening",
          updatedAt: timestamp,
          text: typeof data.text === "string" ? data.text : undefined,
          reason: typeof data.reason === "string" ? data.reason : undefined,
          audioBytes: typeof data.audioBytes === "number" ? data.audioBytes : undefined,
        },
      };
    case "log.entry":
      return {
        type: "log.entry",
        payload: {
          sessionId: sessionId || undefined,
          entry: normalizeLogEntry(
            {
              id: `${sessionId}-log-${timestamp}`,
              level:
                typeof data.level === "string"
                  ? (data.level as SessionLogEntry["level"])
                  : "info",
              message:
                typeof data.message === "string" ? data.message : "收到未命名日志事件",
              source:
                typeof data.source === "string"
                  ? (data.source as SessionLogEntry["source"])
                  : "system",
              createdAt: timestamp,
            },
            timestamp,
          ),
        },
      };
    default:
      return null;
  }
}

function normalizeProviderConfig(raw?: BackendProviderConfig): ProviderConfig {
  return {
    vendor: raw?.vendor ?? raw?.name ?? DEFAULT_PROVIDER_CONFIG.vendor,
    endpoint: raw?.endpoint ?? DEFAULT_PROVIDER_CONFIG.endpoint,
    model: raw?.model ?? DEFAULT_PROVIDER_CONFIG.model,
    apiKeyHint: raw?.apiKeyHint ?? raw?.apiKeyEnv ?? DEFAULT_PROVIDER_CONFIG.apiKeyHint,
    enabled: raw?.enabled ?? DEFAULT_PROVIDER_CONFIG.enabled,
  };
}

function serializeProviderConfig(raw: ProviderConfig): BackendProviderConfig {
  return {
    vendor: raw.vendor,
    name: raw.vendor,
    endpoint: raw.endpoint,
    model: raw.model || undefined,
    enabled: raw.enabled,
    apiKeyHint: raw.apiKeyHint || undefined,
    apiKeyEnv: raw.apiKeyHint || undefined,
  };
}

function buildProviderServices(settings: ProviderSettings): ServiceHealth[] {
  return [
    buildProviderService("stt", settings.stt),
    buildProviderService("openclaw", settings.openclaw),
    buildProviderService("tts", settings.tts),
  ];
}

function buildProviderService(name: string, config: ProviderConfig): ServiceHealth {
  return {
    name,
    status: config.enabled ? "ok" : "degraded",
    detail: config.endpoint || (config.enabled ? "已启用" : "未启用"),
  };
}

function normalizeProviderBindings(raw?: BackendSession["providers"]): SessionProviderBindings {
  return {
    stt: raw?.stt ?? DEFAULT_PROVIDER_BINDINGS.stt,
    openclaw: raw?.openClaw ?? raw?.openclaw ?? DEFAULT_PROVIDER_BINDINGS.openclaw,
    tts: raw?.tts ?? DEFAULT_PROVIDER_BINDINGS.tts,
  };
}

function normalizeStream(raw?: Partial<StreamMeta>): StreamMeta {
  return {
    encoding: raw?.encoding ?? DEFAULT_STREAM.encoding,
    sampleRateHz: raw?.sampleRateHz ?? DEFAULT_STREAM.sampleRateHz,
    channels: raw?.channels ?? DEFAULT_STREAM.channels,
  };
}

function normalizeSessionState(raw: string | undefined): SessionState {
  switch (raw) {
    case "idle":
    case "listening":
    case "recognizing":
    case "thinking":
    case "speaking":
    case "closed":
      return raw;
    default:
      return "idle";
  }
}

function normalizeServiceStatus(raw: string | undefined): ServiceStatus {
  switch (raw) {
    case "ok":
    case "degraded":
    case "error":
      return raw;
    default:
      return "degraded";
  }
}

function normalizeTimestamp(raw?: string | null): string {
  if (!raw) {
    return new Date().toISOString();
  }

  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}

function normalizeTranscriptEntry(
  raw: TranscriptEntry,
  fallbackTimestamp: string,
): TranscriptEntry {
  return {
    id: raw.id,
    text: raw.text,
    kind: raw.kind,
    createdAt: normalizeTimestamp(raw.createdAt ?? fallbackTimestamp),
  };
}

function normalizeTranscriptPayload(
  raw: unknown,
  sessionId: string,
  fallbackKind: TranscriptEntry["kind"],
  fallbackTimestamp: string,
): TranscriptEntry {
  if (typeof raw === "string") {
    return {
      id: `${sessionId}-${fallbackKind}-${fallbackTimestamp}`,
      text: raw,
      kind: fallbackKind,
      createdAt: fallbackTimestamp,
    };
  }

  if (isRecord(raw)) {
    const payload = raw as BackendTranscriptPayload;
    return {
      id: payload.id ?? `${sessionId}-${fallbackKind}-${fallbackTimestamp}`,
      text: payload.text ?? "",
      kind:
        payload.kind === "partial" || payload.kind === "final" || payload.kind === "assistant"
          ? payload.kind
          : fallbackKind,
      createdAt: normalizeTimestamp(payload.createdAt ?? fallbackTimestamp),
    };
  }

  return {
    id: `${sessionId}-${fallbackKind}-${fallbackTimestamp}`,
    text: "",
    kind: fallbackKind,
    createdAt: fallbackTimestamp,
  };
}

function normalizeLogEntry(raw: SessionLogEntry, fallbackTimestamp: string): SessionLogEntry {
  return {
    id: raw.id,
    level: raw.level,
    message: raw.message,
    source: raw.source,
    createdAt: normalizeTimestamp(raw.createdAt ?? fallbackTimestamp),
  };
}

function normalizeProviderLatency(
  raw: ProviderLatency,
  fallbackTimestamp: string,
): ProviderLatency {
  return {
    provider: raw.provider,
    latencyMs: raw.latencyMs,
    updatedAt: normalizeTimestamp(raw.updatedAt ?? fallbackTimestamp),
  };
}

function extractEventSession(
  data: Record<string, unknown>,
  sessionId: string,
  timestamp: string,
): BackendSession {
  if (isRecord(data.session)) {
    return data.session as BackendSession;
  }

  return {
    id: sessionId,
    callId: typeof data.callId === "string" ? data.callId : sessionId,
    caller: typeof data.caller === "string" ? data.caller : "未知来电",
    state: typeof data.state === "string" ? data.state : "idle",
    startedAt: typeof data.startedAt === "string" ? data.startedAt : timestamp,
    updatedAt: timestamp,
    lastTranscript: typeof data.lastTranscript === "string" ? data.lastTranscript : undefined,
    providers: DEFAULT_PROVIDER_BINDINGS,
    stream: DEFAULT_STREAM,
  };
}

function resolveSessionId(
  envelopeSessionId: string | undefined,
  data: Record<string, unknown>,
): string {
  if (typeof envelopeSessionId === "string" && envelopeSessionId.length > 0) {
    return envelopeSessionId;
  }

  if (isRecord(data.session) && typeof data.session.id === "string") {
    return data.session.id;
  }

  return "unknown-session";
}

function describeProvider(name: string): string {
  return name && name !== "unknown" ? `已绑定 ${name}` : "待后端补充";
}

function isRecord(value: unknown): value is Record<string, any> {
  return typeof value === "object" && value !== null;
}
