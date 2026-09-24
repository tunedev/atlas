package tools

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Judge asks a ports.Judge the questions a pack supplies as YAML, then
// records the resulting judgement.
type Judge struct {
	judge ports.Judge
	docs  ports.Docs
	index ports.Index
}

func NewJudge(j ports.Judge, docs ports.Docs, index ports.Index) *Judge {
	return &Judge{judge: j, docs: docs, index: index}
}

func (t *Judge) Name() string { return "judge.ask" }

// questionSpec is one question as it arrives in a pack's YAML block.
type questionSpec struct {
	ID      string              `yaml:"id"`
	Type    string              `yaml:"type"`
	Ask     string              `yaml:"ask"`
	Options []string            `yaml:"options"`
	Levels  []string            `yaml:"levels"`
	Forms   map[string][]string `yaml:"forms"`
}

func (t *Judge) Invoke(ctx context.Context, with map[string]string) (any, error) {
	subjectID := with["subject_id"]
	if subjectID == "" {
		return nil, fmt.Errorf("judge.ask: no subject id")
	}
	if with["subject"] == "" {
		return nil, fmt.Errorf("judge.ask: no subject")
	}

	qs, err := parseQuestions(with["questions"])
	if err != nil {
		return nil, fmt.Errorf("judge.ask: %w", err)
	}

	j, err := t.judge.Ask(ctx, with["subject"], qs)
	if err != nil {
		return nil, fmt.Errorf("judge.ask: %w", err)
	}

	path, err := app.RecordJudgement(ctx, t.docs, t.index, subjectID, qs, j)
	if err != nil {
		return nil, fmt.Errorf("judge.ask: %w", err)
	}

	return judgeResult(j, path), nil
}

// parseQuestions decodes a pack's YAML questions block into ports.Question,
// rejecting anything a judge could not act on: an empty block, a missing id,
// an unknown kind, or a question naming both options and levels.
func parseQuestions(raw string) ([]ports.Question, error) {
	var specs []questionSpec
	if err := yaml.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, fmt.Errorf("parse questions: %w", err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no questions")
	}

	qs := make([]ports.Question, len(specs))
	for i, s := range specs {
		q, err := s.question()
		if err != nil {
			return nil, err
		}
		qs[i] = q
	}
	return qs, nil
}

// question converts one questionSpec into a ports.Question.
func (s questionSpec) question() (ports.Question, error) {
	if s.ID == "" {
		return ports.Question{}, fmt.Errorf("question missing id")
	}
	if s.ID == "path" {
		return ports.Question{}, fmt.Errorf("question id %q is reserved for the tool's own result key", s.ID)
	}
	if len(s.Options) > 0 && len(s.Levels) > 0 {
		return ports.Question{}, fmt.Errorf("question %q names both options and levels", s.ID)
	}

	options := s.Options
	if len(s.Levels) > 0 {
		options = s.Levels
	}

	switch ports.Kind(s.Type) {
	case ports.KindNoul:
	case ports.KindChoice, ports.KindScore:
		if len(options) < 2 {
			return ports.Question{}, fmt.Errorf("question %q needs at least two options", s.ID)
		}
	default:
		return ports.Question{}, fmt.Errorf("question %q has unknown type %q", s.ID, s.Type)
	}

	return ports.Question{
		ID:      s.ID,
		Kind:    ports.Kind(s.Type),
		Ask:     s.Ask,
		Options: options,
		Forms:   s.Forms,
	}, nil
}

// judgeResult builds the tool's output: each answer keyed by question id,
// plus the path the judgement was recorded at.
func judgeResult(j ports.Judgement, path string) map[string]any {
	out := make(map[string]any, len(j.Answers)+1)
	for _, a := range j.Answers {
		entry := map[string]any{
			"chosen":       a.Chosen,
			"p":            a.Distribution[a.Chosen],
			"distribution": a.Distribution,
			"confidence":   a.Confidence,
			"coverage": map[string]any{
				"represented": a.Coverage.Represented,
				"declared":    a.Coverage.Declared,
			},
		}
		if a.Kind == ports.KindScore {
			entry["expected"] = a.Expected
		}
		out[a.ID] = entry
	}
	out["path"] = path
	return out
}
