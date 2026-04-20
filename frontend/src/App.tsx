import { NavLink, Route, Routes } from "react-router-dom";
import { useBridgeConnection } from "./lib/use-bridge-connection";
import Dashboard from "./pages/Dashboard";
import SessionDetail from "./pages/SessionDetail";
import Settings from "./pages/Settings";

const navigationItems = [
  { to: "/", label: "Dashboard", end: true },
  { to: "/settings", label: "Settings" },
];

function App() {
  const connection = useBridgeConnection();

  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <div className="brand-lockup">
          <span className="brand-mark">OC</span>
          <div>
            <p className="eyebrow">Voice Bridge Console</p>
            <h1>OpenClaw / FreeSWITCH</h1>
          </div>
        </div>
        <nav className="app-nav" aria-label="Primary">
          {navigationItems.map((item) => (
            <NavLink
              key={item.to}
              className={({ isActive }) =>
                isActive ? "nav-link nav-link-active" : "nav-link"
              }
              end={item.end}
              to={item.to}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <section className="sidebar-note">
          <p className="eyebrow">Realtime Socket</p>
          <div className="status-indicator status-line">
            <span className={`status-dot ${resolveConnectionStatusClassName(connection.state)}`} />
            <strong>{resolveConnectionLabel(connection.state)}</strong>
          </div>
          <p>{buildConnectionSummary(connection.state, connection.activeConsumers, connection.reconnectAttempt)}</p>
          {connection.lastError ? <p className="inline-message">{connection.lastError}</p> : null}
        </section>
        <section className="sidebar-note">
          <p className="eyebrow">Current Focus</p>
          <strong>Bridge health, call sessions, provider wiring.</strong>
          <p>
            Skeleton routes are wired to typed REST and WebSocket adapters so the
            backend contract can be plugged in without rewriting the UI shell.
          </p>
        </section>
      </aside>

      <main className="app-main">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/sessions/:sessionId" element={<SessionDetail />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}

function resolveConnectionLabel(state: ReturnType<typeof useBridgeConnection>["state"]): string {
  switch (state) {
    case "connected":
      return "Connected";
    case "connecting":
      return "Connecting";
    case "reconnecting":
      return "Reconnecting";
    default:
      return "Idle";
  }
}

function resolveConnectionStatusClassName(state: ReturnType<typeof useBridgeConnection>["state"]): string {
  switch (state) {
    case "connected":
      return "status-ok";
    case "connecting":
      return "status-warning";
    case "reconnecting":
      return "status-warning";
    default:
      return "status-idle";
  }
}

function buildConnectionSummary(
  state: ReturnType<typeof useBridgeConnection>["state"],
  activeConsumers: number,
  reconnectAttempt: number,
): string {
  switch (state) {
    case "connected":
      return `${activeConsumers} page subscriptions are sharing the live event stream.`;
    case "connecting":
      return `Opening the shared event stream for ${activeConsumers} active page subscriptions.`;
    case "reconnecting":
      return `Retry ${reconnectAttempt} is in flight while ${activeConsumers} page subscriptions stay attached.`;
    default:
      return "No page is actively consuming realtime events right now.";
  }
}

export default App;
