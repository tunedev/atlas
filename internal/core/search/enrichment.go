package search

import (
	"strings"
	"time"
)

// Enrichment is what a model read out of one subject. Values nests the
// pack's declared fields by path segment; a null means the subject does not
// say. Values must have the shapes encoding/json decodes into an any:
// map[string]any, []any, string, float64 and bool. Any other Go type
// compares as null. Evidence maps a field path to a verbatim quote.
type Enrichment struct {
	Version     int               `json:"version"`
	SubjectID   string            `json:"subject_id"`
	ContentHash string            `json:"content_hash"`
	Enricher    Enricher          `json:"enricher"`
	EnrichedAt  time.Time         `json:"enriched_at"`
	Values      map[string]any    `json:"values"`
	Evidence    map[string]string `json:"evidence"`
}

type Enricher struct {
	Engine        string `json:"engine"`
	Model         string `json:"model"`
	PromptVersion int    `json:"prompt_version"`
	Error         string `json:"error,omitempty"`
}

// Value returns the value at a dotted path, or nil when it is absent or null.
func (e *Enrichment) Value(path string) any {
	var v any = e.Values
	for seg := range strings.SplitSeq(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[seg]
	}
	return v
}
