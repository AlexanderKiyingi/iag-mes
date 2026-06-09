import { useCallback, useEffect, useState } from "react";
import {
  fetchBootstrap,
  loadToken,
  saveToken,
  type BootstrapPayload,
} from "./api";
import "./app.css";

export default function App() {
  const [token, setToken] = useState(loadToken);
  const [draft, setDraft] = useState(token);
  const [data, setData] = useState<BootstrapPayload | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async (accessToken: string) => {
    if (!accessToken.trim()) {
      setError("Paste a JWT with mes.view_overview and platform.access_mes");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const payload = await fetchBootstrap(accessToken.trim());
      setData(payload);
    } catch (e) {
      setData(null);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (token) {
      void load(token);
    }
  }, [token, load]);

  return (
    <div className="layout">
      <header>
        <h1>IAG MES / CMMS</h1>
        <p className="muted">Bootstrap shell — loads /bootstrap via gateway</p>
      </header>

      <section className="panel">
        <label htmlFor="token">Access token</label>
        <textarea
          id="token"
          rows={3}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Bearer JWT from iag-authentication"
        />
        <div className="actions">
          <button
            type="button"
            onClick={() => {
              saveToken(draft);
              setToken(draft);
            }}
            disabled={loading}
          >
            {loading ? "Loading…" : "Connect"}
          </button>
        </div>
        {error && <p className="error">{error}</p>}
      </section>

      {data && (
        <>
          <section className="stats">
            <Stat label="Plants" value={data.plants?.length ?? 0} />
            <Stat label="Assets" value={data.assets?.length ?? 0} />
            <Stat label="Active runs" value={data.active_runs?.length ?? 0} />
            <Stat
              label="Open WOs"
              value={data.open_work_orders?.length ?? 0}
            />
            <Stat label="New alerts" value={data.new_alerts?.length ?? 0} />
          </section>

          {data.integrations && (
            <section className="panel">
              <h2>Integrations</h2>
              <ul className="chips">
                {Object.entries(data.integrations.upstreams ?? {}).map(
                  ([name, ok]) => (
                    <li key={name} className={ok ? "ok" : "down"}>
                      {name}: {ok ? "up" : "down"}
                    </li>
                  ),
                )}
              </ul>
            </section>
          )}

          <section className="panel">
            <h2>Assets</h2>
            <table>
              <thead>
                <tr>
                  <th>Tag</th>
                  <th>Name</th>
                  <th>Plant</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {(data.assets ?? []).slice(0, 20).map((a) => (
                  <tr key={a.tag}>
                    <td>{a.tag}</td>
                    <td>{a.name}</td>
                    <td>{a.plant_code ?? "—"}</td>
                    <td>{a.status ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        </>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="stat">
      <span className="stat-value">{value}</span>
      <span className="stat-label">{label}</span>
    </div>
  );
}
