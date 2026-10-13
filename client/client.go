// Package client is the typed internal API client for game backend adapters.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/connectivity"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

// New permits HTTP for private deployment networks; use HTTPS outside them.
// Redirects are refused so backend credentials cannot move to another host.
func New(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(token) < 32 {
		return nil, fmt.Errorf("invalid connectivity client configuration")
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("connectivity API returned %d", e.Status) }

func (c *Client) call(ctx context.Context, method, path string, req, out any) error {
	var body io.Reader
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("connectivity request failed")
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if req != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return fmt.Errorf("connectivity service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return &APIError{resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(out); err != nil {
			return fmt.Errorf("invalid connectivity response")
		}
	}
	return nil
}

func (c *Client) Register(ctx context.Context, req connectivity.RegisterRequest) (connectivity.Participant, error) {
	var out connectivity.Participant
	err := c.call(ctx, "POST", "/v1/participants", req, &out)
	return out, err
}
func (c *Client) Get(ctx context.Context, id string) (connectivity.Participant, error) {
	var out connectivity.Participant
	err := c.call(ctx, "GET", "/v1/participants/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) Release(ctx context.Context, id string) error {
	return c.call(ctx, "DELETE", "/v1/participants/"+url.PathEscape(id), nil, nil)
}
func (c *Client) AllocateRelay(ctx context.Context, req connectivity.RelayRequest) (connectivity.Relay, error) {
	var out connectivity.Relay
	err := c.call(ctx, "POST", "/v1/relays", req, &out)
	return out, err
}
func (c *Client) BindRelay(ctx context.Context, id string, req connectivity.BindRequest) (connectivity.Relay, error) {
	var out connectivity.Relay
	err := c.call(ctx, "POST", "/v1/relays/"+url.PathEscape(id)+"/bind", req, &out)
	return out, err
}
func (c *Client) ReleaseRelay(ctx context.Context, id string) error {
	return c.call(ctx, "DELETE", "/v1/relays/"+url.PathEscape(id), nil, nil)
}
func (c *Client) ICE(ctx context.Context, req connectivity.ICERequest) (connectivity.ICEResponse, error) {
	var out connectivity.ICEResponse
	err := c.call(ctx, "POST", "/v1/ice", req, &out)
	return out, err
}
func (c *Client) Report(ctx context.Context, req connectivity.ReportRequest) error {
	return c.call(ctx, "POST", "/v1/reports", req, nil)
}
