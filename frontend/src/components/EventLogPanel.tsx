import type { SessionLogEntry } from "../types/session";

interface EventLogPanelProps {
  entries: SessionLogEntry[];
  title?: string;
  description?: string;
}

function EventLogPanel({
  entries,
  title = "Realtime log",
  description = "WebSocket 事件和桥接日志会按时间顺序追加到这里。",
}: EventLogPanelProps) {
  return (
    <section className="surface">
      <header className="surface-header">
        <div>
          <h3>{title}</h3>
          <p>{description}</p>
        </div>
        <span className="meta-pill">{entries.length} events</span>
      </header>

      {entries.length === 0 ? (
        <p className="empty-state">日志面板已就绪，等待实时事件写入。</p>
      ) : (
        <div className="log-list">
          {entries.map((entry) => (
            <article className="log-item" key={entry.id}>
              <time dateTime={entry.createdAt}>
                {new Date(entry.createdAt).toLocaleString()} | {entry.source} | {entry.level}
              </time>
              <strong>{entry.message}</strong>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

export default EventLogPanel;
