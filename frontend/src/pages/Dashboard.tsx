import { useEffect, useMemo, useState } from "react";
import EventLogPanel from "../components/EventLogPanel";
import HealthCards from "../components/HealthCards";
import ProviderConfigPanel from "../components/ProviderConfigPanel";
import SessionTable from "../components/SessionTable";
import { apiClient } from "../lib/api";
import { buildLogEntryFromEvent, shouldRecordDashboardLog } from "../lib/event-log";
import { bridgeSocket } from "../lib/ws";
import type { HealthStatus } from "../types/health";
import type { ProviderSettings } from "../types/providers";
import type { SessionLogEntry, SessionSummary } from "../types/session";

const defaultProviderSettings: ProviderSettings = {
  stt: { vendor: "", endpoint: "", model: "", apiKeyHint: "", enabled: true },
  openclaw: { vendor: "", endpoint: "", model: "", apiKeyHint: "", enabled: true },
  tts: { vendor: "", endpoint: "", model: "", apiKeyHint: "", enabled: true },
};

function Dashboard() {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [logs, setLogs] = useState<SessionLogEntry[]>([]);
  const [settings, setSettings] = useState<ProviderSettings>(defaultProviderSettings);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let disposed = false;

    const load = async () => {
      try {
        const snapshot = await apiClient.getDashboardSnapshot();

        if (disposed) {
          return;
        }

        setHealth(snapshot.health);
        setSessions(snapshot.sessions);
        setSettings(snapshot.providerSettings);
        setError(null);
      } catch (loadError) {
        if (!disposed) {
          setError(loadError instanceof Error ? loadError.message : "加载 Dashboard 失败");
        }
      }
    };

    void load();
    bridgeSocket.connect();

    const offAny = bridgeSocket.onAny((event) => {
      if (!shouldRecordDashboardLog(event)) {
        return;
      }

      setLogs((current) => [buildLogEntryFromEvent(event), ...current].slice(0, 50));
    });
    const offCreated = bridgeSocket.on("session.created", (event) => {
      setSessions((current) => upsertSession(current, event.payload));
    });
    const offUpdated = bridgeSocket.on("session.updated", (event) => {
      setSessions((current) => upsertSession(current, event.payload));
    });
    const offClosed = bridgeSocket.on("session.closed", (event) => {
      setSessions((current) => removeSession(current, event.payload.id));
    });
    const offTranscript = bridgeSocket.on("session.transcript.final", (event) => {
      setSessions((current) =>
        current.map((session) =>
          session.id === event.payload.sessionId
            ? {
                ...session,
                lastTranscript: event.payload.transcript.text,
                updatedAt: event.payload.transcript.createdAt,
              }
            : session,
        ),
      );
    });

    return () => {
      disposed = true;
      offAny();
      offCreated();
      offUpdated();
      offClosed();
      offTranscript();
      bridgeSocket.disconnect();
    };
  }, []);

  const speakingCount = useMemo(
    () => sessions.filter((session) => session.state === "speaking").length,
    [sessions],
  );

  const liveHealth = useMemo(() => {
    if (!health) {
      return null;
    }

    return {
      ...health,
      activeSessions: sessions.length,
    };
  }, [health, sessions]);

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <p className="eyebrow">Operations Dashboard</p>
          <h2>Observe bridge pressure, active calls, and provider readiness.</h2>
          <p>Dashboard 已兼容当前后端契约，覆盖健康状态、会话列表、实时日志和 Provider 配置预览。</p>
        </div>
        <div className="meta-pill">{speakingCount} sessions speaking</div>
      </header>

      {error ? <p className="inline-message">{error}</p> : null}

      <section className="surface">
        <header className="surface-header">
          <div>
            <h3>Bridge health</h3>
            <p>显示桥接进程、依赖服务和活动会话的当前快照。</p>
          </div>
          <span className="meta-pill">GET /api/health</span>
        </header>
        <HealthCards health={liveHealth} />
      </section>

      <div className="dashboard-grid">
        <div className="section-stack">
          <section className="surface">
            <header className="surface-header">
              <div>
                <h3>Session list</h3>
                <p>用于确认通话生命周期、最近识别文本和会话钻取入口。</p>
              </div>
              <span className="meta-pill">GET /api/sessions</span>
            </header>
            <SessionTable sessions={sessions} />
          </section>

          <EventLogPanel entries={logs} />
        </div>

        <div className="section-stack">
          <ProviderConfigPanel settings={settings} onChange={setSettings} />
        </div>
      </div>
    </div>
  );
}

function upsertSession(current: SessionSummary[], next: SessionSummary): SessionSummary[] {
  const existing = current.find((item) => item.id === next.id);
  if (!existing) {
    return [next, ...current];
  }

  return current.map((item) => (item.id === next.id ? { ...item, ...next } : item));
}

function removeSession(current: SessionSummary[], id: string): SessionSummary[] {
  return current.filter((item) => item.id !== id);
}

export default Dashboard;
