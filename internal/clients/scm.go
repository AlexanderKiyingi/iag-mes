package clients

import (
	"context"
	"fmt"
	"net/http"
)

type SCM struct {
	baseClient
}

func NewSCM(baseURL, tokenURL, clientID, clientSecret string) *SCM {
	return &SCM{baseClient: newBase(baseURL, tokenURL, clientID, clientSecret, "iag.supply-chain")}
}

func (c *SCM) Enabled() bool { return c != nil && c.enabled() }

func (c *SCM) ValidateBatch(ctx context.Context, batchBusinessID string) (bool, error) {
	if !c.Enabled() {
		return false, fmt.Errorf("scm client disabled")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/batches/"+batchBusinessID, nil)
	if err != nil {
		return false, err
	}
	if c.auth != nil {
		if err := c.auth.AuthorizeRequest(ctx, req); err != nil {
			return false, err
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("scm validate: %s", resp.Status)
	}
	return true, nil
}
