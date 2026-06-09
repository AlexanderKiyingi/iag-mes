package clients

import "context"

type ERP struct {
	baseClient
}

func NewERP(baseURL, tokenURL, clientID, clientSecret string) *ERP {
	return &ERP{baseClient: newBase(baseURL, tokenURL, clientID, clientSecret, "iag.erp")}
}

func (c *ERP) Enabled() bool { return c != nil && c.enabled() }

type ERPProductionOrder struct {
	PONum      string  `json:"po_num"`
	Customer   string  `json:"customer"`
	Product    string  `json:"product"`
	QtyKg      float64 `json:"qty_kg"`
	OriginLot  string  `json:"origin_lot"`
	AssetTag   string  `json:"asset_tag"`
	Status     string  `json:"status"`
	DueAt      string  `json:"due_at"`
	ERPRef     string  `json:"erp_ref"`
}

func (c *ERP) ListProductionOrders(ctx context.Context) ([]ERPProductionOrder, error) {
	var out struct {
		Items []ERPProductionOrder `json:"items"`
	}
	_, _, err := c.doJSON(ctx, "GET", "/api/v1/production-orders", nil, &out)
	if err != nil {
		return nil, err
	}
	return out.Items, nil
}
