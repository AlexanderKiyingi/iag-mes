package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"
)

type baseClient struct {
	baseURL string
	http    *http.Client
	auth    *platformserviceauth.Client
}

func newBase(baseURL, tokenURL, clientID, clientSecret, audience string) baseClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	var auth *platformserviceauth.Client
	if tokenURL != "" && clientID != "" && clientSecret != "" {
		auth = platformserviceauth.NewClient(platformserviceauth.Options{
			TokenURL:     tokenURL,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Audience:     audience,
		})
	}
	return baseClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
		auth:    auth,
	}
}

func (b baseClient) enabled() bool { return b.baseURL != "" }

func (b baseClient) doJSON(ctx context.Context, method, path string, in any, out any) (int, []byte, error) {
	if !b.enabled() {
		return 0, nil, fmt.Errorf("client disabled")
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.auth != nil {
		if err := b.auth.AuthorizeRequest(ctx, req); err != nil {
			return 0, nil, err
		}
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, err
		}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, raw, fmt.Errorf("%s %s: %s", method, path, strings.TrimSpace(string(raw)))
	}
	return resp.StatusCode, raw, nil
}
