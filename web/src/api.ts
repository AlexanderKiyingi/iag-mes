export type BootstrapPayload = {
  service: string;
  gateway?: string;
  plants: Array<{ code: string; name: string; region?: string }>;
  sections: unknown[];
  assets: Array<{ tag: string; name: string; status?: string; plant_code?: string }>;
  active_runs: unknown[];
  open_work_orders: unknown[];
  new_alerts: unknown[];
  integrations?: {
    enabled: boolean;
    upstreams?: Record<string, boolean>;
  };
};

const apiBase =
  import.meta.env.VITE_MES_API_BASE ?? "/api/v1/mes/api/v1";

export async function fetchBootstrap(token: string): Promise<BootstrapPayload> {
  const res = await fetch(`${apiBase}/bootstrap`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${res.status}: ${text}`);
  }
  return res.json() as Promise<BootstrapPayload>;
}

export function loadToken(): string {
  return (
    import.meta.env.VITE_ACCESS_TOKEN ??
    localStorage.getItem("iag_access_token") ??
    ""
  );
}

export function saveToken(token: string) {
  localStorage.setItem("iag_access_token", token);
}
