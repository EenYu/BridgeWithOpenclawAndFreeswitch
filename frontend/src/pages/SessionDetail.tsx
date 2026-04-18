import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import EventLogPanel from "../components/EventLogPanel";
import { apiClient } from "../lib/api";
import { buildLogEntryFromEvent } from "../lib/event-log";
import { bridgeSocket } from "../lib/ws";
import type { SessionDetail as SessionDetailType, SessionLogEntry, TranscriptEntry } from "../types/session";
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
          setError(loadError instanceof Error ? loadError.message : "加载会话详情失败");
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
    bridgeSocket.connect();

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
      bridgeSocket.disconnect();
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
      setError(interruptError instanceof Error ? interruptError.message : "中断会话失败");
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
          <p>详情页直接读取当前会话，并对未落地字段做前端占位降级。</p>
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
          <p className="empty-state">等待 `GET /api/sessions/{'{id}'}` 返回详情数据。</p>
        </section>
      ) : (
        <div className="session-detail-grid">
          <section className="surface">
            <header className="surface-header">
              <div>
                <h3>Session summary</h3>
                <p>聚合通话标识、状态、最近转写、桥接节点与实时播放状态。</p>
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
                  {session.stream.encoding} / {session.stream.sampleRateHz}Hz / {session.stream.channels}ch
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
                  <p>按 partial / final / assistant 分类展示对话片段。</p>
                </div>
                <span className="meta-pill">{session.transcripts.length} entries</span>
              </header>

              {session.transcripts.length === 0 ? (
                <p className="empty-state">当前还没有转写事件。</p>
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
                  <h3>Provider latency</h3>
                  <p>为 STT、OpenClaw 和 TTS 预留毫秒级耗时展示位。</p>
                </div>
              </header>

              {session.providerLatencies.length === 0 ? (
                <p className="empty-state">后端暂未提供延迟指标，页面保持稳定降级展示。</p>
              ) : (
                <div className="section-stack">
                  {session.providerLatencies.map((latency) => (
                    <div className="surface" key={latency.provider}>
                      <p className="eyebrow">{latency.provider.toUpperCase()}</p>
                      <strong>{latency.latencyMs} ms</strong>
                      <p className="inline-message">
                        Last updated {new Date(latency.updatedAt).toLocaleString()}
                      </p>
                    </div>
                  ))}
                </div>
              )}
            </section>

            <EventLogPanel
              description="聚焦当前会话的桥接、FreeSWITCH 与 Provider 事件。"
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
    return [next, ...rest];
  }

  return [next, ...current].slice(0, 50);
}

function prependFinalTranscript(current: TranscriptEntry[], next: TranscriptEntry): TranscriptEntry[] {
  const rest = current[0]?.kind === "partial" ? current.slice(1) : current;
  return [next, ...rest].slice(0, 50);
}

function upsertRecentLog(current: SessionLogEntry[], next: SessionLogEntry): SessionLogEntry[] {
  const existingIndex = current.findIndex((entry) => entry.id === next.id);
  if (existingIndex === 0) {
    return [next, ...current.slice(1)];
  }

  if (existingIndex > 0) {
    return [next, ...current.slice(0, existingIndex), ...current.slice(existingIndex + 1)].slice(0, 50);
  }

  return [next, ...current].slice(0, 50);
}

export default SessionDetail;
