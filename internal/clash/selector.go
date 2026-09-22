package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to sing-box's Clash API directly (not via the reverse proxy)
// for the few calls the backend itself needs: reading and switching a
// selector group.
type Client struct {
	Addr   string // external_controller, e.g. 127.0.0.1:9090
	Secret string
	HTTP   *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	addr := c.Addr
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(addr, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	return hc.Do(req)
}

// Now returns the outbound a group (selector or urltest) currently points at.
func (c *Client) Now(ctx context.Context, group string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(group), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clash: GET %s: %s", group, resp.Status)
	}
	var body struct {
		Now string `json:"now"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Now, nil
}

// Select points a selector group at the named outbound. sing-box persists the
// choice in its cache_file, so it survives restarts.
func (c *Client) Select(ctx context.Context, selector, name string) error {
	resp, err := c.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(selector), map[string]string{"name": name})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clash: select %q in %s: %s", name, selector, resp.Status)
	}
	return nil
}
