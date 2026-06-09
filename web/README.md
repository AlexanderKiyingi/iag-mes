# IAG MES web shell

Minimal React app that loads `GET /bootstrap` and renders plants, assets, and integration status.

## Run

```bash
cd services/operations/mes/web
pnpm install
pnpm dev
```

Open http://localhost:5173 and paste a JWT with `platform.access_mes` and `mes.view_overview`.

## Env

| Variable | Default | Purpose |
|----------|---------|---------|
| `VITE_MES_API_BASE` | `/api/v1/mes/api/v1` | API prefix (via gateway proxy) |
| `VITE_ACCESS_TOKEN` | — | Optional dev token |
| `VITE_API_PROXY` | `http://localhost:8080` | Vite dev proxy target |

See [../docs/FRONTEND_INTEGRATION.md](../docs/FRONTEND_INTEGRATION.md) for full API mapping.
