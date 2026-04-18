import { NavLink, Route, Routes } from "react-router-dom";
import Dashboard from "./pages/Dashboard";
import SessionDetail from "./pages/SessionDetail";
import Settings from "./pages/Settings";

const navigationItems = [
  { to: "/", label: "Dashboard", end: true },
  { to: "/settings", label: "Settings" },
];

function App() {
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

export default App;
