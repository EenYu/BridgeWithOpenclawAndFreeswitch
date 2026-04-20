import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import EventLogPanel from "../components/EventLogPanel";
import { apiClient } from "../lib/api";
import { buildLogEntryFromEvent } from "../lib/event-log";
import { bridgeSocket } from "../lib/ws";
import type {
  SessionDetail as SessionDetailType,
  SessionLogEntry,
  SessionProviderMetric,
  TranscriptEntry,
} from "../types/session";
import type { BridgeEvent } from "../types/ws";

const PARTIAL_TRANSCRIPT_FLUSH_MS = 150;

function SessionDetail() {
  const { sessionId = "" } = useParams();
  const [session, setSession] = useState<SessionDetailType | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingInterrupt, setPendingInterrupt] = useState(false);
  const partialTranscriptRef = useRef<TranscriptEntry | null>(null);
  const partialFlushTimerRef = useRef<number | null>(null);

  useEffect(() => {
    let disposed = false;

    const loadSession = async () => {
      if (!sessionId) {
        return;
      }

      try {
        const detail = await apiClient.getSession(sessionId);
        if (!disposed) {
          setSession(detail);
          setError(null);
        }
      } catch (loadError) {
        if (!disposed) {
          setError(
            loadError instanceof Error ? loadError.message : "Failed to load session detail",
          );
        }
      }
    };

    const flushPartialTranscript = () => {
      const pendingPartial = partialTranscriptRef.current;
      partialTranscriptRef.current = null;

      if (!pendingPartial) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              transcripts: upsertLatestPartialTranscript(current.transcripts, pendingPartial),
              updatedAt: pendingPartial.createdAt,
            }
          : current,
      );
    };

    const schedulePartialTranscriptFlush = () => {
      if (partialFlushTimerRef.current !== null) {
        return;
      }

      partialFlushTimerRef.current = window.setTimeout(() => {
        partialFlushTimerRef.current = null;
        flushPartialTranscript();
      }, PARTIAL_TRANSCRIPT_FLUSH_MS);
    };

    void loadSession();

    const offAny = bridgeSocket.onAny((event) => {
      const eventSessionId = resolveEventSessionId(event);
      if (eventSessionId !== sessionId) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              recentLogs: upsertRecentLog(current.recentLogs, buildLogEntryFromEvent(event)),
            }
          : current,
      );
    });

    const offTranscriptPartial = bridgeSocket.on("session.transcript.partial", (event) => {
      if (event.payload.sessionId !== sessionId) {
        return;
      }

      partialTranscriptRef.current = event.payload.transcript;
      schedulePartialTranscriptFlush();
    });

    const offTranscriptFinal = bridgeSocket.on("session.transcript.final", (event) => {
      if (event.payload.sessionId !== sessionId) {
        return;
      }

      if (partialFlushTimerRef.current !== null) {
        window.clearTimeout(partialFlushTimerRef.current);
        partialFlushTimerRef.current = null;
      }
      partialTranscriptRef.current = null;

      setSession((current) =>
        current
          ? {
              ...current,
              lastTranscript: event.payload.transcript.text,
              transcripts: prependFinalTranscript(current.transcripts, event.payload.transcript),
              updatedAt: event.payload.transcript.createdAt,
            }
          : current,
      );
    });

    const offUpdated = bridgeSocket.on("session.updated", (event) => {
      if (event.payload.id !== sessionId) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              ...event.payload,
              playbackActive: event.payload.state === "speaking",
            }
          : current,
      );
    });

    const offClosed = bridgeSocket.on("session.closed", (event) => {
      if (event.payload.id !== sessionId) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              ...event.payload,
              state: "closed",
              playbackActive: false,
            }
          : current,
      );
    });

    const offTtsStarted = bridgeSocket.on("session.tts.started", (event) => {
      if (event.payload.sessionId !== sessionId) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              state: "speaking",
              playbackActive: true,
              updatedAt: event.payload.updatedAt,
            }
          : current,
      );
    });

    const offTtsStopped = bridgeSocket.on("session.tts.stopped", (event) => {
      if (event.payload.sessionId !== sessionId) {
        return;
      }

      setSession((current) =>
        current
          ? {
              ...current,
              state: "listening",
              playbackActive: false,
              updatedAt: event.payload.updatedAt,
            }
          : current,
      );
    });

    const releaseSocket = bridgeSocket.retain();

    return () => {
      disposed = true;
      if (partialFlushTimerRef.current !== null) {
        window.clearTimeout(partialFlushTimerRef.current);
        partialFlushTimerRef.current = null;
      }
      partialTranscriptRef.current = null;
      offAny();
      offTranscriptPartial();
      offTranscriptFinal();
      offUpdated();
      offClosed();
      offTtsStarted();
      offTtsStopped();
      releaseSocket();
    };
  }, [sessionId]);

  const interrupt = async () => {
    if (!sessionId) {
      return;
    }

    try {
      setPendingInterrupt(true);
      await apiClient.interruptSession(sessionId);
      const nextDetail = await apiClient.getSession(sessionId);
      setSession(nextDetail);
      setError(null);
    } catch (interruptError) {
      setError(
        interruptError instanceof Error ? interruptError.message : "Failed to interrupt session",
      );
    } finally {
      setPendingInterrupt(false);
    }
  };

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <p className="eyebrow">Session Detail</p>
          <h2>{session?.callId ?? sessionId}</h2>
          <p>
            Read the live session record, aggregate transcript events, and surface provider
            runtime metrics without relying on long-lived placeholder text.
          </p>
        </div>
        <div className="button-row">
          <button
            className="button button-primary"
            disabled={pendingInterrupt}
            onClick={interrupt}
            type="button"
          >
            {pendingInterrupt ? "Interrupting..." : "Interrupt TTS"}
          </button>
        </div>
      </header>

      {error ? <p className="inline-message">{error}</p> : null}

      {!session ? (
        <section className="surface">
          <p className="empty-state">
            Waiting for `GET /api/sessions/{'{id}'}` to return session detail.
          </p>
        </section>
      ) : (
        <div className="session-detail-grid">
          <section className="surface">
            <header className="surface-header">
              <div>
                <h3>Session summary</h3>
                <p>
                  Call identity, bridge node, playback state, and current stream bindings for the
                  selected session.
                </p>
              </div>
              <span className="meta-pill">{session.state}</span>
            </header>

            <div className="kv-grid">
              <div className="surface">
                <p className="eyebrow">Caller</p>
                <strong>{session.caller}</strong>
              </div>
              <div className="surface">
                <p className="eyebrow">Bridge Node</p>
                <strong>{session.bridgeNode}</strong>
              </div>
              <div className="surface">
                <p className="eyebrow">Playback</p>
                <strong>{session.playbackActive ? "Active" : "Idle"}</strong>
              </div>
              <div className="surface">
                <p className="eyebrow">Started</p>
                <strong>{new Date(session.startedAt).toLocaleString()}</strong>
              </div>
              <div className="surface">
                <p className="eyebrow">Stream</p>
                <strong>
                  {session.stream.encoding} / {session.stream.sampleRateHz}Hz /{" "}
                  {session.stream.channels}ch
                </strong>
              </div>
              <div className="surface">
                <p className="eyebrow">Providers</p>
                <strong>
                  {session.providers.stt} / {session.providers.openclaw} / {session.providers.tts}
                </strong>
              </div>
            </div>

            <section className="surface" style={{ marginTop: "1rem" }}>
              <header className="surface-header">
                <div>
                  <h4>Transcript timeline</h4>
                  <p>
                    Shows partial, final, and assistant transcript entries in reverse chronological
                    order.
                  </p>
                </div>
                <span className="meta-pill">{session.transcripts.length} entries</span>
              </header>

              {session.transcripts.length === 0 ? (
                <p className="empty-state">
                  No transcript events have been recorded for this session yet.
                </p>
              ) : (
                <div className="log-list">
                  {session.transcripts.map((entry) => (
                    <article className="log-item" key={entry.id}>
                      <time dateTime={entry.createdAt}>
                        {new Date(entry.createdAt).toLocaleString()} | {entry.kind}
                      </time>
                      <strong>{entry.text}</strong>
                    </article>
                  ))}
                </div>
              )}
            </section>
          </section>

          <div className="section-stack">
            <section className="surface">
              <header className="surface-header">
                <div>
                  <h3>Provider runtime</h3>
                  <p>
                    Session bindings merged with backend health status, latency, and runtime
                    detail for STT, OpenClaw, and TTS.
                  </p>
                </div>
                <span className="meta-pill">{session.providerMetrics.length} providers</span>
              </header>

              <div className="provider-runtime-grid">
                {session.providerMetrics.map((metric) => (
                  <article className="surface provider-runtime-card" key={metric.provider}>
                    <div className="split-header">
                      <div>
                        <p className="eyebrow">{metric.provider.toUpperCase()}</p>
                        <strong>{metric.binding}</strong>
                      </div>
                      <span className="status-indicator">
                        <span
                          className={`status-dot ${resolveProviderMetricStatusClassName(metric.status)}`}
                        />
                        {resolveProviderMetricStatusLabel(metric.status)}
                      </span>
                    </div>

                    <p className="inline-message">{metric.detail}</p>

                    <div className="provider-runtime-meta">
                      <div>
                        <p className="eyebrow">Latency</p>
                        <strong>{formatProviderLatency(metric)}</strong>
                      </div>
                      <div>
                        <p className="eyebrow">Source</p>
                        <strong>{formatLatencySource(metric)}</strong>
                      </div>
                    </div>

                    {metric.updatedAt ? (
                      <p className="inline-message">
                        Updated {new Date(metric.updatedAt).toLocaleString()}
                      </p>
                    ) : null}
                  </article>
                ))}
              </div>
            </section>

            <EventLogPanel
              description="Focused event stream for the selected session, including bridge, FreeSWITCH, and provider activity."
              entries={session.recentLogs}
              title="Session log"
            />
          </div>
        </div>
      )}
    </div>
  );
}

function resolveEventSessionId(event: BridgeEvent): string | undefined {
  switch (event.type) {
    case "session.created":
    case "session.updated":
    case "session.closed":
      return event.payload.id;
    case "session.transcript.partial":
    case "session.transcript.final":
    case "session.tts.started":
    case "session.tts.stopped":
      return event.payload.sessionId;
    case "log.entry":
      return event.payload.sessionId;
    default:
      return undefined;
  }
}

function upsertLatestPartialTranscript(
  current: TranscriptEntry[],
  next: TranscriptEntry,
): TranscriptEntry[] {
  if (current.length === 0) {
    return [next];
  }

  const [latest, ...rest] = current;
  if (latest.kind === "partial") {
    return [
      {
        ...next,
        text: mergeTranscriptText(latest.text, next.text),
      },
      ...rest,
    ];
  }

  return [next, ...current].slice(0, 50);
}

function prependFinalTranscript(
  current: TranscriptEntry[],
  next: TranscriptEntry,
): TranscriptEntry[] {
  const rest = current[0]?.kind === "partial" ? current.slice(1) : current;
  return [next, ...rest].slice(0, 50);
}

function upsertRecentLog(current: SessionLogEntry[], next: SessionLogEntry): SessionLogEntry[] {
  const existingIndex = current.findIndex((entry) => entry.id === next.id);
  const merged =
    existingIndex >= 0 ? mergePartialLogEntry(current[existingIndex], next) : next;

  if (existingIndex === 0) {
    return [merged, ...current.slice(1)];
  }

  if (existingIndex > 0) {
    return [
      merged,
      ...current.slice(0, existingIndex),
      ...current.slice(existingIndex + 1),
    ].slice(0, 50);
  }

  return [next, ...current].slice(0, 50);
}

function mergePartialLogEntry(previous: SessionLogEntry, next: SessionLogEntry): SessionLogEntry {
  if (previous.id !== next.id || !next.id.endsWith("-stt-partial")) {
    return next;
  }

  const prefix = resolveLogPrefix(next.message);
  const mergedText = mergeTranscriptText(
    extractLogText(previous.message),
    extractLogText(next.message),
  );

  return {
    ...next,
    message: prefix ? `${prefix} ${mergedText}` : mergedText,
  };
}

function resolveLogPrefix(message: string): string {
  const separatorIndex = message.indexOf(":");
  return separatorIndex >= 0 ? message.slice(0, separatorIndex + 1) : "";
}

function extractLogText(message: string): string {
  const separatorIndex = message.indexOf(":");
  return separatorIndex >= 0 ? message.slice(separatorIndex + 1).trimStart() : message;
}

function mergeTranscriptText(previous: string, incoming: string): string {
  if (!previous) {
    return incoming;
  }

  if (!incoming || incoming === previous) {
    return previous;
  }

  if (incoming.startsWith(previous)) {
    return incoming;
  }

  if (previous.endsWith(incoming)) {
    return previous;
  }

  const normalizedPrevious = normalizeTranscriptText(previous);
  const normalizedIncoming = normalizeTranscriptText(incoming);
  if (normalizedIncoming && normalizedPrevious) {
    if (normalizedIncoming.startsWith(normalizedPrevious)) {
      return incoming;
    }

    const normalizedPrefixLength = findCommonPrefixLength(
      normalizedPrevious,
      normalizedIncoming,
    );
    if (
      normalizedPrefixLength >= 2 &&
      normalizedIncoming.length >= Math.floor(normalizedPrevious.length * 0.8)
    ) {
      return incoming;
    }
  }

  const overlapLength = findOverlapLength(previous, incoming);
  if (overlapLength > 0) {
    return `${previous}${incoming.slice(overlapLength)}`;
  }

  return `${previous}${incoming}`;
}

function findOverlapLength(previous: string, incoming: string): number {
  const maxLength = Math.min(previous.length, incoming.length);

  for (let length = maxLength; length > 0; length -= 1) {
    if (previous.slice(-length) === incoming.slice(0, length)) {
      return length;
    }
  }

  return 0;
}

function findCommonPrefixLength(previous: string, incoming: string): number {
  const maxLength = Math.min(previous.length, incoming.length);

  for (let index = 0; index < maxLength; index += 1) {
    if (previous[index] !== incoming[index]) {
      return index;
    }
  }

  return maxLength;
}

function normalizeTranscriptText(value: string): string {
  return value.replace(/[\s,.!?;:，。！；：]/g, "");
}

function resolveProviderMetricStatusClassName(
  status: SessionProviderMetric["status"],
): string {
  switch (status) {
    case "ok":
      return "status-ok";
    case "degraded":
      return "status-degraded";
    case "error":
      return "status-error";
    default:
      return "status-idle";
  }
}

function resolveProviderMetricStatusLabel(status: SessionProviderMetric["status"]): string {
  switch (status) {
    case "ok":
      return "Healthy";
    case "degraded":
      return "Degraded";
    case "error":
      return "Error";
    default:
      return "Unknown";
  }
}

function formatProviderLatency(metric: SessionProviderMetric): string {
  return metric.latencyMs !== undefined ? `${metric.latencyMs} ms` : "Not reported";
}

function formatLatencySource(metric: SessionProviderMetric): string {
  switch (metric.latencySource) {
    case "session":
      return "Session detail";
    case "health":
      return "Health snapshot";
    default:
      return "No latency source";
  }
}

export default SessionDetail;
