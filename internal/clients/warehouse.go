package clients

import "context"

type Warehouse struct {
	baseClient
}

func NewWarehouse(baseURL, tokenURL, clientID, clientSecret string) *Warehouse {
	return &Warehouse{baseClient: newBase(baseURL, tokenURL, clientID, clientSecret, "iag.warehouse")}
}

func (c *Warehouse) Enabled() bool { return c != nil && c.enabled() }

type ProductionConsumeRequest struct {
	BatchBusinessID string `json:"batch_business_id"`
	FacilityCode    string `json:"facility_code"`
	Lines           []struct {
		ItemID  string  `json:"item_id"`
		Qty     float64 `json:"qty"`
		BinCode string  `json:"bin_code"`
		LotKey  string  `json:"lot_key"`
	} `json:"lines"`
}

type ProductionOutputRequest struct {
	BatchBusinessID string  `json:"batch_business_id"`
	SKU             string  `json:"sku"`
	ItemID          string  `json:"item_id"`
	Qty             float64 `json:"qty"`
	BinCode         string  `json:"bin_code"`
	LotKey          string  `json:"lot_key"`
	QCHold          bool    `json:"qc_hold"`
}

func (c *Warehouse) ProductionConsume(ctx context.Context, req ProductionConsumeRequest) (map[string]any, error) {
	var out map[string]any
	_, _, err := c.doJSON(ctx, "POST", "/api/v1/production/consume", req, &out)
	return out, err
}

func (c *Warehouse) ProductionOutput(ctx context.Context, req ProductionOutputRequest) (map[string]any, error) {
	var out map[string]any
	_, _, err := c.doJSON(ctx, "POST", "/api/v1/production/output", req, &out)
	return out, err
}

func (c *Warehouse) LowStockByAsset(ctx context.Context, assetTag string) ([]map[string]any, error) {
	var out struct {
		Items []map[string]any `json:"items"`
	}
	path := "/api/v1/spare-parts/by-asset/" + assetTag
	_, _, err := c.doJSON(ctx, "GET", path, nil, &out)
	return out.Items, err
}
