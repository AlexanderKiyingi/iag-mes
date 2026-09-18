package integrations

import (
	"context"
	"encoding/json"

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
		"scm":       b.SCM != nil && b.SCM.Enabled(),
	}
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
