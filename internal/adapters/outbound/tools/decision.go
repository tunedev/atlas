package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Decision records a person's choice about a subject, snapshotting the
// verdict of the judgement it follows when there is one.
type Decision struct {
	docs  ports.Docs
	index ports.Index
}

func NewDecision(docs ports.Docs, index ports.Index) *Decision {
	return &Decision{docs: docs, index: index}
}

func (t *Decision) Name() string { return "decision.record" }

func (t *Decision) Invoke(ctx context.Context, with map[string]string) (any, error) {
	jpath, question := with["judgement_path"], with["verdict_question"]
	if (jpath == "") != (question == "") {
		return nil, fmt.Errorf("decision.record: judgement_path and verdict_question go together")
	}

	var verdict string
	if jpath != "" {
		v, err := app.VerdictAt(ctx, t.docs, with["subject_id"], jpath, question)
		if err != nil {
			return nil, fmt.Errorf("decision.record: %w", err)
		}
		verdict = v
	}

	path, err := app.RecordDecision(ctx, t.docs, t.index, app.Decision{
		SubjectID:         with["subject_id"],
		Choice:            with["choice"],
		Reason:            with["reason"],
		JudgementPath:     jpath,
		VerdictAtDecision: verdict,
		When:              time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("decision.record: %w", err)
	}
	return map[string]any{"path": path, "verdict_at_decision": verdict}, nil
}
