package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Decision is one choice a person made about a subject. Choice is a free
// string a pack defines the meaning of. JudgementPath and VerdictAtDecision
// are empty when no judgement preceded the decision. A decision never holds
// an outcome; the judgement document owns that slot.
type Decision struct {
	SubjectID         string
	Choice            string
	Reason            string
	JudgementPath     string
	VerdictAtDecision string
	When              time.Time
}

// decisionDoc is a decision as it is written to the record.
type decisionDoc struct {
	SubjectID         string `json:"subject_id"`
	Decision          string `json:"decision"`
	When              string `json:"when"`
	Reason            string `json:"reason"`
	JudgementPath     string `json:"judgement_path"`
	VerdictAtDecision string `json:"verdict_at_decision"`
}

// RecordDecision writes d under its subject, keyed by time so a later
// decision never overwrites an earlier one, and indexes it. It returns the
// path written.
func RecordDecision(ctx context.Context, docs ports.Docs, index ports.Index, d Decision) (string, error) {
	if d.SubjectID == "" {
		return "", errors.New("decision: subject id is empty")
	}
	if d.Choice == "" {
		return "", errors.New("decision: choice is empty")
	}

	path := fmt.Sprintf("decisions/%s/%s.json", d.SubjectID, d.When.UTC().Format(recordTimeFormat))
	body, err := json.MarshalIndent(decisionDoc{
		SubjectID:         d.SubjectID,
		Decision:          d.Choice,
		When:              d.When.UTC().Format(recordDocTimeFormat),
		Reason:            d.Reason,
		JudgementPath:     d.JudgementPath,
		VerdictAtDecision: d.VerdictAtDecision,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("decision: encode: %w", err)
	}

	rev, err := RecordDocument(ctx, docs, index, Document{
		Path:    path,
		Body:    body,
		Message: "Record decision for " + d.SubjectID,
		Kind:    "decision",
		Fields: map[string]string{
			"subject_id":          d.SubjectID,
			"decision":            d.Choice,
			"verdict_at_decision": d.VerdictAtDecision,
		},
		When: d.When,
	})
	if err != nil {
		if rev == "" {
			return "", fmt.Errorf("decision: %w", err)
		}
		return path, fmt.Errorf("decision: %w", err)
	}
	return path, nil
}

// VerdictAt returns the option the judgement at judgementPath chose for
// questionID, so a decision can snapshot what the model said when the
// person chose. A missing judgement or question is an error.
func VerdictAt(ctx context.Context, docs ports.Docs, judgementPath, questionID string) (string, error) {
	body, err := docs.Get(ctx, judgementPath)
	if err != nil {
		return "", fmt.Errorf("decision: read judgement %s: %w", judgementPath, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("decision: no judgement at %s", judgementPath)
	}
	var doc judgementDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("decision: decode judgement %s: %w", judgementPath, err)
	}
	for _, a := range doc.Answers {
		if a.ID == questionID {
			return a.Chosen, nil
		}
	}
	return "", fmt.Errorf("decision: judgement %s has no answer %q", judgementPath, questionID)
}
