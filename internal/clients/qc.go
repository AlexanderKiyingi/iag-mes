package clients

import "context"

type QualityControl struct {
	baseClient
}

func NewQualityControl(baseURL, tokenURL, clientID, clientSecret string) *QualityControl {
	return &QualityControl{baseClient: newBase(baseURL, tokenURL, clientID, clientSecret, "iag.quality-control")}
}

func (c *QualityControl) Enabled() bool { return c != nil && c.enabled() }

func (c *QualityControl) SubmitSample(ctx context.Context, batchBusinessID, sampleID string) (map[string]any, error) {
	body := map[string]string{
		"batch_business_id": batchBusinessID,
		"sample_id":         sampleID,
	}
	var out map[string]any
	_, _, err := c.doJSON(ctx, "POST", "/api/v1/samples", body, &out)
	return out, err
}
