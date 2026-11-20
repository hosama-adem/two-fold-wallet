package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the local (or remote) provider HTTP API and verifies
// inbound webhooks with the shared HMAC secret.
type Client struct {
	name   string
	base   string
	secret string
	http   *http.Client
	now    func() time.Time
}

// ClientConfig wires an HTTP Provider adapter.
type ClientConfig struct {
	Name       string // e.g. "simulator", "telebirr"
	BaseURL    string
	HMACSecret string
	HTTPClient *http.Client
}

// NewClient builds a Provider over HTTP.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("provider: BaseURL is required")
	}
	if cfg.HMACSecret == "" {
		return nil, fmt.Errorf("provider: HMACSecret is required")
	}
	name := cfg.Name
	if name == "" {
		name = "simulator"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		name:   name,
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		secret: cfg.HMACSecret,
		http:   hc,
		now:    time.Now,
	}, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) InitiateCollection(ctx context.Context, req CollectionRequest) (CollectionRef, error) {
	var out CollectionRef
	if err := c.postJSON(ctx, "/v1/collections", req, &out); err != nil {
		return CollectionRef{}, err
	}
	return out, nil
}

func (c *Client) InitiatePayout(ctx context.Context, req PayoutRequest) (PayoutRef, error) {
	var out PayoutRef
	if err := c.postJSON(ctx, "/v1/payouts", req, &out); err != nil {
		return PayoutRef{}, err
	}
	return out, nil
}

func (c *Client) GetStatus(ctx context.Context, ref string) (PaymentStatus, error) {
	var out PaymentStatus
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/payments/"+ref, nil)
	if err != nil {
		return PaymentStatus{}, err
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return PaymentStatus{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return PaymentStatus{}, fmt.Errorf("provider: get status %s: %s", res.Status, truncate(body))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return PaymentStatus{}, err
	}
	return out, nil
}

func (c *Client) VerifyWebhook(headers http.Header, body []byte) error {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	return VerifyWebhookSignature(c.secret, headers, body, now)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("provider: %s %s: %s", path, res.Status, truncate(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func truncate(b []byte) string {
	const n = 200
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Compile-time check: Client is a Provider.
var _ Provider = (*Client)(nil)
