// Package tools holds the harness's generic tools and the registry that
// resolves their names. A tool here knows nothing about any use case: what it
// fetches, and what is done with the result, come from a pack.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTP fetches a URL. A JSON object body is returned parsed so packs can
// select into it by path; anything else is returned as text under "body".
// A response body over maxBytes fails the call rather than being truncated:
// a downstream step must never act on partial data believing it complete.
type HTTP struct {
	client   *http.Client
	maxBytes int64
}

func NewHTTP(timeout time.Duration, maxBytes int64) *HTTP {
	return &HTTP{client: &http.Client{Timeout: timeout}, maxBytes: maxBytes}
}

func (h *HTTP) Name() string { return "http.request" }

func (h *HTTP) Invoke(ctx context.Context, with map[string]string) (any, error) {
	url := with["url"]
	if url == "" {
		return nil, fmt.Errorf("http.request: no url")
	}
	method := with["method"]
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http.request: build request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http.request: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("http.request: %s returned %d", url, resp.StatusCode)
	}

	body, err := readLimited(resp.Body, h.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("http.request: read body: %w", err)
	}

	var parsed map[string]any
	if json.Unmarshal(body, &parsed) == nil {
		return parsed, nil
	}
	// Not JSON, or JSON that is not an object. Either way a pack can still
	// reach it, as text, rather than the run failing over a content type.
	return map[string]any{"body": string(body)}, nil
}
