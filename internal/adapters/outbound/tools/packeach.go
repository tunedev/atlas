package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/app"
)

// RunPack runs the pack at path once, with vars overriding its declared vars.
type RunPack func(ctx context.Context, path string, vars map[string]string) error

// StageOf returns a subject's current stage, "" when it has none.
type StageOf func(ctx context.Context, subjectID string) (string, error)

// PackEach runs a pack once per matching row, one row at a time, in
// subject_id order.
//
// with carries pack (the child pack's path), rows (a JSON array of
// objects), match (comma-separated field=value conditions, all of which
// must hold; a missing field is ""), and vars (YAML: child var to a [[ ]]
// template over the row, bound as .item). Every var template is parsed
// before any child runs. When match has a stage condition, the row's
// current stage is read through StageOf just before its run, and the row
// is skipped unless it still holds.
//
// Each run becomes a row {vars, ok: true} or {vars, error}. The step fails
// when every run failed.
type PackEach struct {
	run     RunPack
	stageOf StageOf
}

func NewPackEach(run RunPack, stageOf StageOf) *PackEach {
	return &PackEach{run: run, stageOf: stageOf}
}

func (t *PackEach) Name() string { return "pack.each" }

// condition is one field=value term of match.
type condition struct{ field, value string }

func (t *PackEach) Invoke(ctx context.Context, with map[string]string) (any, error) {
	path, rows, conds, vars, err := parsePackEach(with)
	if err != nil {
		return nil, fmt.Errorf("pack.each: %w", err)
	}
	slices.SortStableFunc(rows, func(a, b map[string]any) int {
		return cmp.Compare(fieldString(a, "subject_id"), fieldString(b, "subject_id"))
	})

	out := []any{}
	var failed int
	var firstErr string
	for _, row := range rows {
		if !matches(row, conds) {
			continue
		}
		still, err := t.stageStillHolds(ctx, row, conds)
		if err != nil {
			return nil, fmt.Errorf("pack.each: %w", err)
		}
		if !still {
			continue
		}
		result := t.one(ctx, path, vars, row)
		if msg, isErr := result["error"].(string); isErr {
			failed++
			if firstErr == "" {
				firstErr = msg
			}
		}
		out = append(out, result)
	}
	if len(out) > 0 && failed == len(out) {
		return nil, fmt.Errorf("pack.each: every child failed; first: %s", firstErr)
	}
	meta := map[string]any{"count": len(out), "ok": len(out) - failed, "errors": failed}
	return map[string]any{"rows": out, "_meta": meta}, nil
}

// parsePackEach reads and checks with, parsing every var template.
func parsePackEach(with map[string]string) (string, []map[string]any, []condition, map[string]string, error) {
	path := with["pack"]
	if path == "" {
		return "", nil, nil, nil, errors.New("no pack")
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(with["rows"]), &rows); err != nil {
		return "", nil, nil, nil, fmt.Errorf("rows: %w", err)
	}
	conds, err := parseMatch(with["match"])
	if err != nil {
		return "", nil, nil, nil, err
	}
	var vars map[string]string
	if err := yaml.Unmarshal([]byte(with["vars"]), &vars); err != nil {
		return "", nil, nil, nil, fmt.Errorf("vars: %w", err)
	}
	for name, tmpl := range vars {
		if _, err := app.ParseItemTemplate(tmpl); err != nil {
			return "", nil, nil, nil, fmt.Errorf("var %s: %w", name, err)
		}
	}
	return path, rows, conds, vars, nil
}

// parseMatch splits a comma-separated list of field=value conditions.
func parseMatch(match string) ([]condition, error) {
	if match == "" {
		return nil, nil
	}
	var conds []condition
	for term := range strings.SplitSeq(match, ",") {
		field, value, ok := strings.Cut(strings.TrimSpace(term), "=")
		if !ok || field == "" {
			return nil, fmt.Errorf("match term %q must be field=value", term)
		}
		conds = append(conds, condition{field: field, value: value})
	}
	return conds, nil
}

// matches reports whether every condition holds for row.
func matches(row map[string]any, conds []condition) bool {
	for _, c := range conds {
		if fieldString(row, c.field) != c.value {
			return false
		}
	}
	return true
}

// fieldString is a row field as text, "" when the field is missing.
func fieldString(row map[string]any, field string) string {
	v, ok := row[field]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// stageStillHolds re-reads the row's current stage when conds has a stage
// condition, and reports whether that condition still holds.
func (t *PackEach) stageStillHolds(ctx context.Context, row map[string]any, conds []condition) (bool, error) {
	for _, c := range conds {
		if c.field != "stage" {
			continue
		}
		stage, err := t.stageOf(ctx, fieldString(row, "subject_id"))
		if err != nil {
			return false, err
		}
		return stage == c.value, nil
	}
	return true, nil
}

// one renders every var against row and runs the child pack once.
func (t *PackEach) one(ctx context.Context, path string, tmpls map[string]string, row map[string]any) map[string]any {
	vars := make(map[string]string, len(tmpls))
	for name, tmpl := range tmpls {
		v, err := app.RenderItem(tmpl, row)
		if err != nil {
			return map[string]any{"vars": vars, "error": fmt.Sprintf("var %s: %v", name, err)}
		}
		vars[name] = v
	}
	if err := t.run(ctx, path, vars); err != nil {
		return map[string]any{"vars": vars, "error": err.Error()}
	}
	return map[string]any{"vars": vars, "ok": true}
}
