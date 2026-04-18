import type { HealthStatus } from "../types/health";

interface HealthCardsProps {
  health: HealthStatus | null;
}

function HealthCards({ health }: HealthCardsProps) {
  if (!health) {
    return <p className="inline-message">等待 `/api/health` 返回桥接服务状态。</p>;
  }

  return (
    <div className="health-grid">
      <article className="health-card">
        <p className="eyebrow">Bridge</p>
        <strong>{health.status}</strong>
        <span className="status-indicator">
          <span className={`status-dot status-${health.status}`} />
          {health.version}
        </span>
      </article>
      <article className="health-card">
        <p className="eyebrow">Checked</p>
        <strong>{new Date(health.checkedAt).toLocaleTimeString()}</strong>
        <span>{new Date(health.checkedAt).toLocaleDateString()}</span>
      </article>
      <article className="health-card">
        <p className="eyebrow">Active Sessions</p>
        <strong>{health.activeSessions}</strong>
        <span>当前桥上仍在跟踪的会话数</span>
      </article>
      <article className="health-card">
        <p className="eyebrow">Dependencies</p>
        <strong>{health.services.length}</strong>
        <span>
          {health.services.map((service) => `${service.name}:${service.status}`).join(" / ")}
        </span>
      </article>
    </div>
  );
}

export default HealthCards;
