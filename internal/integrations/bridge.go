package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"iag-mes/backend/internal/clients"
	"iag-mes/backend/internal/store"
)

type Config struct {
	AutoWarehouseOnComplete bool
	AutoQCOnComplete        bool
	AutoValidateBatch       bool
}

type Bridge struct {
	Warehouse *clients.Warehouse
	QC        *clients.QualityControl
	ERP       *clients.ERP
	SCM       *clients.SCM
	Store     *store.Store
	Cfg       Config
}

func (b *Bridge) Status() map[string]bool {
	if b == nil {
		return map[string]bool{}
	}
	return map[string]bool{
		"warehouse": b.Warehouse != nil && b.Warehouse.Enabled(),
		"qc":        b.QC != nil && b.QC.Enabled(),
		"erp":       b.ERP != nil && b.ERP.Enabled(),
		"scm":       b.SCM != nil && b.SCM.Enabled(),
	}
}

type CompleteHooks struct {
	WarehouseOutput *clients.ProductionOutputRequest
	WarehouseConsume *clients.ProductionConsumeRequest
	SubmitQCSample  bool
	SampleID        string
}

func (b *Bridge) ValidateBatch(ctx context.Context, batchBusinessID string) error {
	if b == nil || !b.Cfg.AutoValidateBatch || b.SCM == nil || !b.SCM.Enabled() {
		_ = b.Store.UpsertBatchRef(ctx, batchBusinessID, "manual")
		return nil
	}
	ok, err := b.SCM.ValidateBatch(ctx, batchBusinessID)
	b.logCall(ctx, "scm", "validate_batch", batchBusinessID, err == nil && ok, map[string]any{"batch": batchBusinessID}, nil, err)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrBadInput
	}
	return b.Store.UpsertBatchRef(ctx, batchBusinessID, "scm")
}

func (b *Bridge) AfterRunComplete(ctx context.Context, run *store.ProductionRun, hooks CompleteHooks) {
	if b == nil || run == nil {
		return
	}
	if b.Cfg.AutoWarehouseOnComplete && hooks.WarehouseOutput != nil && b.Warehouse != nil && b.Warehouse.Enabled() {
		resp, err := b.Warehouse.ProductionOutput(ctx, *hooks.WarehouseOutput)
		status := "ok"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
			slog.Warn("warehouse output failed", "batch", run.BatchBusinessID, "err", err)
		}
		_ = b.Store.RecordWarehouseHandoff(ctx, run.BatchBusinessID, "output", hooks.WarehouseOutput, status, resp, errMsg)
		b.logCall(ctx, "warehouse", "production_output", run.BatchBusinessID, err == nil, hooks.WarehouseOutput, resp, err)
	}
	if b.Cfg.AutoWarehouseOnComplete && hooks.WarehouseConsume != nil && b.Warehouse != nil && b.Warehouse.Enabled() {
		resp, err := b.Warehouse.ProductionConsume(ctx, *hooks.WarehouseConsume)
		status := "ok"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
		}
		_ = b.Store.RecordWarehouseHandoff(ctx, run.BatchBusinessID, "consume", hooks.WarehouseConsume, status, resp, errMsg)
		b.logCall(ctx, "warehouse", "production_consume", run.BatchBusinessID, err == nil, hooks.WarehouseConsume, resp, err)
	}
	if (b.Cfg.AutoQCOnComplete || hooks.SubmitQCSample) && b.QC != nil && b.QC.Enabled() {
		sampleID := hooks.SampleID
		if sampleID == "" {
			sampleID = fmt.Sprintf("SMP-%s-%s", run.BatchBusinessID, time.Now().UTC().Format("20060102"))
		}
		resp, err := b.QC.SubmitSample(ctx, run.BatchBusinessID, sampleID)
		b.logCall(ctx, "qc", "submit_sample", run.BatchBusinessID, err == nil, map[string]any{"sample_id": sampleID}, resp, err)
		if err == nil {
			_ = b.Store.RecordQCHandoff(ctx, run.BatchBusinessID, sampleID, run.ID)
		}
	}
}

func (b *Bridge) SyncERPProductionOrders(ctx context.Context) (int, error) {
	if b == nil || b.ERP == nil || !b.ERP.Enabled() {
		n, err := b.Store.ApplyERPSyncQueue(ctx)
		return n, err
	}
	orders, err := b.ERP.ListProductionOrders(ctx)
	b.logCall(ctx, "erp", "list_production_orders", "", err == nil, nil, map[string]any{"count": len(orders)}, err)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, o := range orders {
		po := store.ProductionOrder{
			PONum:    o.PONum,
			Customer: o.Customer,
			Product:  o.Product,
			QtyKg:    o.QtyKg,
			Status:   o.Status,
		}
		if o.OriginLot != "" {
			po.OriginLot = &o.OriginLot
		}
		if o.AssetTag != "" {
			po.AssetTag = &o.AssetTag
		}
		if o.ERPRef != "" {
			po.ERPRef = &o.ERPRef
		}
		if o.DueAt != "" {
			if t, e := time.Parse(time.RFC3339, o.DueAt); e == nil {
				po.DueAt = &t
			}
		}
		if _, err := b.Store.UpsertProductionOrder(ctx, po); err == nil {
			applied++
		}
	}
	return applied, nil
}

func (b *Bridge) IngestERPWebhook(ctx context.Context, payload map[string]any) error {
	if b == nil {
		return store.ErrBadInput
	}
	raw, _ := json.Marshal(payload)
	poNum, _ := payload["po_num"].(string)
	if poNum == "" {
		return store.ErrBadInput
	}
	return b.Store.EnqueueERPSync(ctx, poNum, raw)
}

func (b *Bridge) logCall(ctx context.Context, target, op, correlation string, ok bool, req, resp any, err error) {
	if b == nil || b.Store == nil {
		return
	}
	status := "ok"
	errMsg := ""
	if err != nil {
		status = "error"
		errMsg = err.Error()
	} else if !ok && correlation != "" && target == "scm" {
		status = "error"
		errMsg = "validation failed"
	}
	var reqB, respB json.RawMessage
	if req != nil {
		reqB, _ = json.Marshal(req)
	}
	if resp != nil {
		respB, _ = json.Marshal(resp)
	}
	_ = b.Store.LogIntegrationCall(ctx, target, op, correlation, status, reqB, respB, errMsg)
}

// RunID is a helper for optional run correlation.
func RunID(id uuid.UUID) *uuid.UUID { return &id }
