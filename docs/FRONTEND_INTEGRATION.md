# Frontend integration — MES / CMMS + Production

## Services

| UI domain | Service | Gateway base |
|-----------|---------|--------------|
| CMMS, assets, telemetry, KPIs, alerts | **iag-mes** | `http://localhost:8080/api/v1/mes/api/v1` |
| Production runs, orders, schedule, shifts | **iag-production** | `http://localhost:8080/api/v1/production/api/v1` |

All requests require `Authorization: Bearer <JWT>` except `/health` and `/ready`.

## Production pane (inspire-cmms-v6)

| UI pane | Primary endpoints (`iag-production`) |
|---------|-------------------------------------|
| Production runs | `GET /production-runs`, `POST /production-runs`, `POST .../:id/advance`, `POST .../:id/complete` |
| Production schedule | `GET /production-orders/schedule`, `GET /schedule-blocks` |
| Shifts | `GET /shift-logs`, `POST /shift-logs`, `GET /operators` |
| ERP sync | `POST /admin/integrations/erp/sync` (`production.admin.write`) |

Production run complete (warehouse + QC):

```json
POST /production-runs/{id}/complete
{
  "kg_out": 980,
  "moisture": 11.2,
  "submit_qc_sample": true,
  "warehouse_output": {
    "batch_business_id": "BAT-2026-035",
    "item_id": "uuid-from-warehouse",
    "qty": 980,
    "bin_code": "STG-01",
    "lot_key": "BAT-2026-035",
    "qc_hold": true
  }
}
```

## CMMS pane (`iag-mes`)

| UI pane | Primary endpoints |
|---------|-------------------|
| Plants & sites | `GET /plants`, `GET /plants/:code`, `GET /sections?plant=` |
| Asset registry | `GET /assets`, `GET /assets/:tag`, `PATCH /assets/:tag` |
| Work orders | `GET /work-orders`, `POST /work-orders`, `POST /work-orders/:num/complete` |
| Downtime | `GET /downtime-events`, `POST /downtime-events` |
| Maintenance / PM | `GET /pm-templates`, `GET /pm-schedules`, `GET /maintenance/calendar` |
| Live telemetry | `GET /assets/:tag/telemetry`, `POST /telemetry/ingest` |
| KPIs / alerts | `GET /kpis/snapshots`, `GET /alerts` |

## Permissions

- Production: `production.view_*`, `production.add_run`, … + gateway `platform.access_production`
- MES/CMMS: `mes.view_*`, … + gateway `platform.access_mes`
