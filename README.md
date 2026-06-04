# iag-mes (Manufacturing Execution)

Production and mill operations for IAG Coffee — publishes processing milestones to Kafka for traceability and SCM.

| Field | Value |
|-------|-------|
| **Port** | `4003` |
| **Gateway prefix** | `/api/v1/mes` |
| **Kafka topic** | `iag.production` |
| **Remote** | [iag-mes](https://github.com/AlexanderKiyingi/iag-mes) |

## Role

Records **wet mill**, **drying**, and **dry mill** completion against a `batch_business_id`. Does not own batches or inventory — **`iag-supply-chain`** remains the operational system of record. Events are consumed by **`iag-traceability`** to build the public coffee story and chain-of-custody read model.

## Quick start

```bash
cd services/operations/mes
cp config/.env.example .env
go run ./cmd/server
curl http://localhost:4003/health
```

Via gateway (when `UPSTREAM_MES` is set):

```bash
curl http://localhost:8080/api/v1/mes/health
```

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness |
| GET | `/ready` | Readiness |
| POST | `/api/v1/production-orders` | Publish a mill-stage completion event |

### `POST /api/v1/production-orders`

```json
{
  "batch_business_id": "BAT-2026-035",
  "stage": "wetmill",
  "facility": "Mbale wet mill",
  "kg_in": 1200,
  "kg_out": 980
}
```

Accepted `stage` values: `wetmill` / `wet_mill`, `drying` / `dry`, `drymill` / `dry_mill`.

## Kafka events

| Event type | When |
|------------|------|
| `mes.wetmill.completed` | Wet mill stage finished |
| `mes.drying.completed` | Drying stage finished |
| `mes.drymill.completed` | Dry mill / hulling finished |

Payload includes `batch_business_id`, optional `facility`, `kg_in`, `kg_out`.

## Integration

- **Consumers:** `iag-traceability` (story projections, chain nodes)
- **Correlation:** always pass SCM `batch_business_id`
- **Plan:** [TRACEABILITY_AND_SUPPLIER_PLATFORM.md](../../../docs/planning/TRACEABILITY_AND_SUPPLIER_PLATFORM.md)

Registry: [`subrepos.json`](../../../subrepos.json)
