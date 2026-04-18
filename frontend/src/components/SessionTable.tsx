import { Link } from "react-router-dom";
import type { SessionSummary } from "../types/session";

interface SessionTableProps {
  sessions: SessionSummary[];
}

function SessionTable({ sessions }: SessionTableProps) {
  if (sessions.length === 0) {
    return <p className="empty-state">当前没有活动或已缓存的会话。</p>;
  }

  return (
    <table className="session-table">
      <thead>
        <tr>
          <th>Session</th>
          <th>Caller</th>
          <th>State</th>
          <th>Started</th>
          <th>Last Transcript</th>
        </tr>
      </thead>
      <tbody>
        {sessions.map((session) => (
          <tr key={session.id}>
            <td>
              <Link className="session-link" to={`/sessions/${session.id}`}>
                {session.callId}
              </Link>
            </td>
            <td>{session.caller}</td>
            <td>
              <span className="state-pill">{session.state}</span>
            </td>
            <td>{new Date(session.startedAt).toLocaleTimeString()}</td>
            <td>{session.lastTranscript ?? "暂无转写"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default SessionTable;
