package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Outcome attaches a real-world outcome to one judgement, by path, or to
// every judgement of a subject.
type Outcome struct {
	docs  ports.Docs
	index ports.Index
}

func NewOutcome(docs ports.Docs, index ports.Index) *Outcome {
	return &Outcome{docs: docs, index: index}
}

func (t *Outcome) Name() string { return "judge.outcome" }

func (t *Outcome) Invoke(ctx context.Context, with map[string]string) (any, error) {
	path, subject := with["judgement_path"], with["subject_id"]
	if (path == "") == (subject == "") {
		return nil, errors.New("judge.outcome: give exactly one of judgement_path or subject_id")
	}
	when, err := outcomeTime(with["when"])
	if err != nil {
		return nil, fmt.Errorf("judge.outcome: %w", err)
	}
	o := app.Outcome{State: with["state"], When: when, Note: with["note"]}

	var attached []string
	if subject != "" {
		attached, err = app.AttachOutcomeForSubject(ctx, t.docs, t.index, subject, o)
	} else if _, err = app.AttachOutcome(ctx, t.docs, t.index, path, o); err == nil {
		attached = []string{path}
	}
	if err != nil {
		if len(attached) > 0 {
			return nil, fmt.Errorf("judge.outcome: attached to %v before failing: %w", attached, err)
		}
		return nil, fmt.Errorf("judge.outcome: %w", err)
	}
	return map[string]any{"attached": attached, "state": o.State}, nil
}

// outcomeTime reads when an outcome became true: RFC 3339 or a date, and
// now when empty.
func outcomeTime(s string) (time.Time, error) {
	if s == "" {
		return time.Now().UTC(), nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("when %q is neither RFC 3339 nor a date", s)
}
