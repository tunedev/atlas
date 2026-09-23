// Package openaiprov adapts any OpenAI-compatible chat completions endpoint
// to ports.Provider. Ollama, vLLM and a hosted key all speak this protocol,
// so choosing among them is a base URL and model name, not a code change.
package openaiprov

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// errorSnippetMaxBytes bounds how much of a non-2xx response body is copied
// into an error message. A body longer than this is truncated to its first
// errorSnippetMaxBytes bytes; the snippet is diagnostic, not data.
const errorSnippetMaxBytes = 512

// Config configures a Client. Name identifies which engine answered; it is
// reported through Client.Name and carried into every error message this
// client produces.
type Config struct {
	Name     string
	BaseURL  string
	Model    string
	APIKey   string
	Timeout  time.Duration
	MaxBytes int64
}

// Client speaks the OpenAI-compatible chat completions protocol to a single
// configured engine.
type Client struct {
	name     string
	baseURL  string
	model    string
	apiKey   string
	http     *http.Client
	maxBytes int64
}

// New builds a Client from cfg. It never uses http.DefaultClient, so the
// configured timeout always applies.
func New(cfg Config) *Client {
	return &Client{
		name:     cfg.Name,
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		http:     &http.Client{Timeout: cfg.Timeout},
		maxBytes: cfg.MaxBytes,
	}
}

// Name identifies which engine answered.
func (c *Client) Name() string { return c.name }

// Complete posts p to the configured chat completions endpoint and maps the
// response onto the port's vocabulary. Nothing vendor-shaped survives past
// this method.
func (c *Client) Complete(ctx context.Context, p ports.Prompt) (ports.Completion, error) {
	reqBody, err := c.buildRequest(p)
	if err != nil {
		return ports.Completion{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return ports.Completion{}, fmt.Errorf("openaiprov: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return ports.Completion{}, fmt.Errorf("openaiprov: post: %w", err)
	}
	defer resp.Body.Close()
	latency := time.Since(start)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorSnippetMaxBytes))
		return ports.Completion{}, fmt.Errorf("openaiprov: %s returned status %d: %s", c.name, resp.StatusCode, snippet)
	}

	body, err := readLimited(resp.Body, c.maxBytes)
	if err != nil {
		return ports.Completion{}, fmt.Errorf("openaiprov: read response: %w", err)
	}

	var chat chatResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return ports.Completion{}, fmt.Errorf("openaiprov: decode response: %w", err)
	}
	if len(chat.Choices) == 0 {
		return ports.Completion{}, fmt.Errorf("openaiprov: %s returned no choices", c.name)
	}

	return toCompletion(chat, latency), nil
}

// buildRequest maps a Prompt onto the wire request, adding logprobs and a
// JSON schema only when the prompt asks for them.
func (c *Client) buildRequest(p ports.Prompt) ([]byte, error) {
	req := chatRequest{
		Model:     c.model,
		Messages:  toMessages(p),
		MaxTokens: p.MaxTokens,
	}

	if p.TopLogProbs > 0 {
		req.LogProbs = true
		req.TopLogProbs = p.TopLogProbs
	}

	if len(p.Schema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(p.Schema, &schema); err != nil {
			return nil, fmt.Errorf("openaiprov: schema: %w", err)
		}
		req.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: &namedSchema{
				Name:   "response",
				Strict: true,
				Schema: schema,
			},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaiprov: encode request: %w", err)
	}
	return body, nil
}

func toMessages(p ports.Prompt) []chatMessage {
	var messages []chatMessage
	if p.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: p.System})
	}
	return append(messages, chatMessage{Role: "user", Content: p.User})
}

// toCompletion maps the wire response onto the port's Completion. Tokens is
// left empty when the response carries no per-token logprobs.
func toCompletion(chat chatResponse, latency time.Duration) ports.Completion {
	choice := chat.Choices[0]

	completion := ports.Completion{
		Text:  choice.Message.Content,
		Model: chat.Model,
		Usage: ports.Usage{
			PromptTokens:     chat.Usage.PromptTokens,
			CompletionTokens: chat.Usage.CompletionTokens,
		},
		Latency: latency,
	}

	if choice.LogProbs == nil {
		return completion
	}

	for _, entry := range choice.LogProbs.Content {
		tok := ports.Token{Text: entry.Token, LogProb: entry.LogProb}
		for _, alt := range entry.TopLogProbs {
			tok.Alternatives = append(tok.Alternatives, ports.Alternative{
				Text:    alt.Token,
				LogProb: alt.LogProb,
			})
		}
		completion.Tokens = append(completion.Tokens, tok)
	}

	return completion
}
