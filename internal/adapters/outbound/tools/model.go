package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Model calls an OpenAI-compatible chat completions endpoint. Ollama serves
// this locally, so a pack runs offline and free; a routed gateway speaks the
// same protocol, so routing through it is a base URL change.
//
// With expect=json the reply is parsed into fields a pack can select by path.
// Without it the reply is returned as text under "text".
//
// A response body over maxBytes fails the call rather than being truncated:
// a downstream step must never act on partial data believing it complete.
type Model struct {
	baseURL  string
	model    string
	client   *http.Client
	maxBytes int64
}

func NewModel(baseURL, model string, timeout time.Duration, maxBytes int64) *Model {
	return &Model{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		model:    model,
		client:   &http.Client{Timeout: timeout},
		maxBytes: maxBytes,
	}
}

func (m *Model) Name() string { return "model.complete" }

func (m *Model) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if with["user"] == "" {
		return nil, fmt.Errorf("model.complete: no user message")
	}

	messages := []map[string]string{}
	if s := with["system"]; s != "" {
		messages = append(messages, map[string]string{"role": "system", "content": s})
	}
	messages = append(messages, map[string]string{"role": "user", "content": with["user"]})

	payload, err := json.Marshal(map[string]any{
		"model":    m.model,
		"messages": messages,
		"stream":   false,
	})
	if err != nil {
		return nil, fmt.Errorf("model.complete: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("model.complete: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model.complete: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model.complete: model %s returned %d", m.model, resp.StatusCode)
	}

	body, err := readLimited(resp.Body, m.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("model.complete: read body: %w", err)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("model.complete: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("model.complete: model %s returned no choices", m.model)
	}

	text := out.Choices[0].Message.Content
	if with["expect"] != "json" {
		return map[string]any{"text": text}, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(unfence(text)), &fields); err != nil {
		return nil, fmt.Errorf("model.complete: expected json, got %q: %w", text, err)
	}
	return fields, nil
}

// unfence strips a markdown code fence. Small local models wrap JSON in one
// routinely, and failing on formatting rather than on substance would be the
// wrong reason to fail.
func unfence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "```"))
}
