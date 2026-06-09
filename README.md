# iag-mes (CMMS + plant operations)

Plant maintenance, assets, telemetry, KPIs, and alerts. **Manufacturing execution** (production runs, orders, schedule) lives in **`iag-production`** (`:4002`).

| Field | Value |
|-------|-------|
| **Port** | `4003` |
| **Gateway prefix** | `/api/v1/mes` |
| **Kafka topics** | `iag.operations` (CMMS events) |
| **Related** | Production runs → [`iag-production`](../production/) on `iag.production` |
| **Audience** | `iag.mes` |
| **Remote** | [iag-mes](https://github.com/AlexanderKiyingi/iag-mes) |

## Role

- **CMMS:** work orders, PM schedules, downtime events, asset registry
- **Performance:** KPI snapshots, alerts, energy, AI recommendations
- **Integrations:** telemetry ingest; consumes cross-domain Kafka events

Production runs, orders, ERP sync, and traceability mill events → **`iag-production`**.

## Quick start

```bash
cd services/operations/mes
cp config/.env.example .env
go run ./cmd/server
curl http://localhost:4003/health
curl http://localhost:4003/api/v1/bootstrap   # requires JWT with mes.view_overview
```

Scheduled jobs (KPI rollup, telemetry alerts):

```bash
go run ./cmd/mes-jobs -plant=kampala          # all jobs once
go run ./cmd/mes-jobs -alerts                 # single job
go run ./cmd/mes-jobs -daemon                 # local scheduler (15m alerts, 1h kpi/erp, 24h ai/energy)
```

Docker Compose (requires stack up):

```bash
docker compose --profile jobs run --rm mes-jobs-alerts
docker compose --profile jobs up mes-jobs-daemon   # dev scheduler
pwsh deploy/scripts/run-mes-jobs.ps1 all
```

| Job | CLI flag | Suggested cron |
|-----|----------|----------------|
| Telemetry alerts | `-alerts` | `*/15 * * * *` |
| KPI rollup | `-kpi` | `0 * * * *` |
| ERP sync | `-erp-sync` | `0 * * * *` |
| AI recommendations | `-ai` | `0 6 * * *` |
| Energy insights | `-energy` | `0 6 * * *` |
| Preventive maintenance sync | `-preventive-maintenance` | `0 * * * *` |

The API server also runs **in-process**: Kafka consumer (SCM/QC/warehouse events), outbox publisher, and JWKS refresh. See [deploy/RAILWAY.md](../../../deploy/RAILWAY.md) for Railway cron services.

## API overview

| Area | Examples |
|------|----------|
| Bootstrap | `GET /api/v1/bootstrap` |
| Plants | `GET/POST /api/v1/plants`, `GET /api/v1/plants/:code/sections` |
| Assets | `GET/POST /api/v1/assets`, `PATCH /api/v1/assets/:tag` |
| Production | `POST /api/v1/production-runs`, `POST .../:id/advance`, `POST .../:id/complete` |
| Legacy traceability | `POST /api/v1/production-orders` |
| CMMS | `GET/POST/PATCH /api/v1/work-orders`, `POST .../:num/start`, `POST .../:num/complete` |
| Preventive maintenance | `GET/POST /api/v1/pm-templates`, `GET/POST /api/v1/pm-schedules` |
| Reliability / reports | `GET /api/v1/reliability/summary`, `GET /api/v1/shift-analysis`, `GET /api/v1/reports/library` |
| Quality (read-model) | `GET /api/v1/quality/summary` |
| Downtime | `GET/POST /api/v1/downtime-events`, `POST .../:id/end` |
| Scheduling | `GET/POST /api/v1/production-orders/schedule`, `GET/POST /api/v1/schedule-blocks` |
| Shifts | `GET/POST /api/v1/shift-logs`, `GET /api/v1/operators` |
| KPIs / alerts | `GET /api/v1/kpis/definitions`, `GET /api/v1/alerts`, `POST /api/v1/alerts/:id/ack` |
| Telemetry | `GET/POST /api/v1/assets/:tag/telemetry` |
| Admin | `GET /api/v1/admin/audit-logs` |

## Kafka events

| Event | Topic |
|-------|-------|
| `mes.wetmill.*`, `mes.drying.*`, `mes.drymill.completed`, `mes.roast.*`, `mes.stage.advanced`, `mes.ccp.recorded` | `iag.production` |
| `mes.downtime.*`, `mes.workorder.completed` | `iag.operations` |

## Admin API

Requires `mes.admin.read` (GET) or `mes.admin.write` (POST). Gateway prefix: `/api/v1/mes/api/v1/admin`.

| Method | Path | Permission | Purpose |
|--------|------|------------|---------|
| GET | `/audit-logs` | `mes.admin.read` | API request audit trail |
| GET | `/monitoring/summary` | `mes.admin.read` | 24h request/error stats |
| GET | `/monitoring/activity` | `mes.admin.read` | Recent API activity |
| GET | `/config` | `mes.admin.read` | Runtime config (non-secret) |
| GET | `/integrations/calls` | `mes.admin.read` | Outbound integration call log |
| POST | `/integrations/erp/sync` | `mes.admin.write` | Pull ERP production orders |
| POST | `/integrations/erp/webhook` | `mes.admin.write` | Ingest ERP webhook payload |
| POST | `/integrations/warehouse/consume` | `mes.admin.write` | Manual warehouse consume |
| POST | `/integrations/warehouse/output` | `mes.admin.write` | Manual warehouse output |
| POST | `/integrations/qc/sample` | `mes.admin.write` | Manual QC sample submit |
| POST | `/jobs/:job` | `mes.admin.write` | Run background job inline |

Job names: `erp-sync`, `ai-recommendations`, `energy-insights`, `kpi-rollup`, `telemetry-alerts`, `preventive-maintenance-sync`. Optional `?plant=` for plant-scoped jobs.

Operators still use `GET /integrations/status` (requires `mes.view_overview`).

## Integrations (Phase 5)

| Upstream | Env | Handlers |
|----------|-----|----------|
| Warehouse | `UPSTREAM_WAREHOUSE` | `POST /integrations/warehouse/consume`, `/output`; auto on run complete |
| Quality control | `UPSTREAM_QUALITY_CONTROL` | `POST /integrations/qc/sample`; auto on run complete |
| Supply chain | `UPSTREAM_SUPPLY_CHAIN` | Batch validation; Kafka consumer |
| ERP | `UPSTREAM_ERP` or webhook | `POST /admin/integrations/erp/sync`, `/admin/integrations/erp/webhook` |

Kafka consumer topics: `iag.supply-chain`, `iag.quality`, `iag.operations`.

## Intelligence (Phase 6)

- `GET /ai/recommendations` — predictive maintenance, energy, performance
- `GET /energy/summary` — kWh by tariff band
- `cmd/mes-jobs` flags: `-ai`, `-energy`, `-erp-sync`

Frontend guide: [docs/FRONTEND_INTEGRATION.md](docs/FRONTEND_INTEGRATION.md)

React shell (bootstrap UI): [web/](web/)

Gateway edge RBAC: `mesViewPermissions` / `mesMutatePermissions` in `shared/services/api-gateway/src/service-permissions.ts` (plus `platform.access_mes`).

## Migrations

| File | Purpose |
|------|---------|
| `001_api_audit.sql` | API audit log |
| `002_schema.sql` | Full CMMS+MES schema |
| `003_seed.sql` | Reference plants/assets from UI prototype |
| `004_integrations_telemetry.sql` | Telemetry history, integration audit, energy, ERP queue |

Registry: [`subrepos.json`](../../../subrepos.json)
