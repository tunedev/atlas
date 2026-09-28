package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// StageDeclare records the stage a subject reached, as a new revision of
// its stage document.
type StageDeclare struct {
	docs  ports.Docs
	index ports.Index
}

func NewStageDeclare(docs ports.Docs, index ports.Index) *StageDeclare {
	return &StageDeclare{docs: docs, index: index}
}

func (t *StageDeclare) Name() string { return "stage.declare" }

func (t *StageDeclare) Invoke(ctx context.Context, with map[string]string) (any, error) {
	s := app.Stage{SubjectID: with["subject_id"], Stage: with["stage"], Note: with["note"], DeclaredBy: with["declared_by"]}
	if w := with["when"]; w != "" {
		when, err := time.Parse(time.RFC3339, w)
		if err != nil {
			return nil, fmt.Errorf("stage.declare: when %q is not RFC 3339", w)
		}
		s.When = when
	}
	path, changed, err := app.DeclareStage(ctx, t.docs, t.index, s, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("stage.declare: %w", err)
	}
	return map[string]any{"path": path, "stage": s.Stage, "changed": changed}, nil
}

// StageAttach sets each row's current stage, found by its subject_id, and
// "" when the subject has none. A row without a subject_id is unchanged.
type StageAttach struct {
	index ports.Index
}

func NewStageAttach(index ports.Index) *StageAttach {
	return &StageAttach{index: index}
}

func (t *StageAttach) Name() string { return "stage.attach" }

func (t *StageAttach) Invoke(ctx context.Context, with map[string]string) (any, error) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(with["rows"]), &rows); err != nil {
		return nil, fmt.Errorf("stage.attach: rows: %w", err)
	}
	out := make([]any, len(rows))
	for i, row := range rows {
		copied := maps.Clone(row)
		if id, _ := row["subject_id"].(string); id != "" {
			stage, err := app.CurrentStage(ctx, t.index, id)
			if err != nil {
				return nil, fmt.Errorf("stage.attach: %w", err)
			}
			copied["stage"] = stage
		}
		out[i] = copied
	}
	return map[string]any{"rows": out}, nil
}
