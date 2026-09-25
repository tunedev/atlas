package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// extractSystemMessage asks for what the text states and a verbatim quote
// for each value, since a quote is what grounding checks.
const extractSystemMessage = "Extract only what the text states. Fill every field from the text alone. " +
	"For every quote field, copy a verbatim substring of the text that supports the values beside it. " +
	"Never invent a value the text does not state."

// ExtractorConfig pins how an Extractor samples: the same text and schema
// should give the same value, and MaxTokens bounds the reply.
type ExtractorConfig struct {
	Temperature float64
	MaxTokens   int
}

// Extractor extracts a schema-shaped value from text with one call to a
// Provider, the schema constraining the reply.
type Extractor struct {
	provider ports.Provider
	cfg      ExtractorConfig
}

// NewExtractor builds an Extractor that asks p, sampling as cfg directs.
func NewExtractor(p ports.Provider, cfg ExtractorConfig) *Extractor {
	return &Extractor{provider: p, cfg: cfg}
}

// Extract sends text with schema as the reply's constraint and returns the
// reply. A reply that is not JSON is an error, never a best-effort parse.
func (e *Extractor) Extract(ctx context.Context, text string, schema []byte) (json.RawMessage, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("extract: no text")
	}
	if !json.Valid(schema) {
		return nil, errors.New("extract: schema is not valid json")
	}
	temperature := e.cfg.Temperature
	c, err := e.provider.Complete(ctx, ports.Prompt{
		System:      extractSystemMessage,
		User:        text,
		MaxTokens:   e.cfg.MaxTokens,
		Schema:      schema,
		Temperature: &temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if !json.Valid([]byte(c.Text)) {
		return nil, fmt.Errorf("extract: reply is not json: %.200q", c.Text)
	}
	return json.RawMessage(c.Text), nil
}
