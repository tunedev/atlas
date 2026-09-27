package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// JudgeEach judges every item a pack selects from a ports.Source, sorted by
// item id, one Judge call per item, one item at a time. Each judgement is
// recorded with its verdict, the rules checked against its item, and a
// fingerprint of what it was asked and against what rule inputs. An item
// whose subject id and fingerprint are both already recorded is reused and
// not asked again.
//
// with carries prefix and match (as source.pull), subject_id and subject
// (per-item templates, [[ ]] delimiters, the item as .item), verdict (the
// id of the question whose answer is the verdict), questions (as
// judge.ask), and optionally rules (a Docs path to a rules document) and
// rule_fields (YAML: rule id to a dotted field path in the item).
//
// An item whose subject cannot be rendered or judged, or whose rendered
// subject id is unusable, becomes that item's error row rather than
// aborting the run. The run itself fails on a bad configuration (with
// checked before any item is asked), a selected item's body that will not
// decode as JSON, a failure to find, read or record a judgement, a failed
// LastRefreshed, or every selected item failing.
type JudgeEach struct {
	source     ports.Source
	judge      ports.Judge
	docs       ports.Docs
	index      ports.Index
	model      string
	staleAfter time.Duration
	log        *slog.Logger
}

func NewJudgeEach(source ports.Source, judge ports.Judge, docs ports.Docs, index ports.Index, model string, staleAfter time.Duration, log *slog.Logger) *JudgeEach {
	return &JudgeEach{source: source, judge: judge, docs: docs, index: index, model: model, staleAfter: staleAfter, log: log}
}

func (t *JudgeEach) Name() string { return "judge.each" }

// eachSpec is one run's configuration, parsed and checked before any item
// is asked. questions already includes one question per judged rule.
type eachSpec struct {
	subjectID string
	subject   string
	verdict   string
	questions []ports.Question
	rules     []app.Rule
	fields    map[string]string
}

func (t *JudgeEach) Invoke(ctx context.Context, with map[string]string) (any, error) {
	spec, err := t.parse(ctx, with)
	if err != nil {
		return nil, fmt.Errorf("judge.each: %w", err)
	}
	underPrefix, keep, err := filter(with["prefix"], with["match"])
	if err != nil {
		return nil, fmt.Errorf("judge.each: %w", err)
	}
	items, err := t.source.Pull(ctx)
	if err != nil {
		return nil, fmt.Errorf("judge.each: %w", err)
	}
	meta, err := freshness(ctx, t.source, t.staleAfter, t.log)
	if err != nil {
		return nil, fmt.Errorf("judge.each: %w", err)
	}
	slices.SortFunc(items, func(a, b ports.Item) int { return strings.Compare(a.ID, b.ID) })

	rows := []any{}
	var judged, reused, failed int
	var firstErr string
	for _, it := range items {
		if !underPrefix(it.ID) {
			continue
		}
		var doc any
		if err := json.Unmarshal(it.Body, &doc); err != nil {
			return nil, fmt.Errorf("judge.each: decode %s: %w", it.ID, err)
		}
		if !keep(doc) {
			continue
		}
		row, wasReused, err := t.one(ctx, spec, it.ID, doc)
		if err != nil {
			return nil, fmt.Errorf("judge.each: %w", err)
		}
		switch {
		case row["error"] != nil:
			failed++
			if firstErr == "" {
				firstErr = row["error"].(string)
			}
		case wasReused:
			reused++
		default:
			judged++
		}
		rows = append(rows, row)
	}
	if len(rows) > 0 && failed == len(rows) {
		return nil, fmt.Errorf("judge.each: every item failed; first: %s", firstErr)
	}

	meta["count"], meta["judged"], meta["reused"], meta["errors"] = len(rows), judged, reused, failed
	return map[string]any{"rows": rows, "_meta": meta}, nil
}

// parse reads and checks with, and is a full pre-flight: the rules document
// is read, the resulting question set is built into a schema, and both item
// templates are parsed, all here, so a bad option set, a malformed
// template, or a missing or malformed rules document fails the run before
// any item is asked.
func (t *JudgeEach) parse(ctx context.Context, with map[string]string) (eachSpec, error) {
	s := eachSpec{subjectID: with["subject_id"], subject: with["subject"], verdict: with["verdict"]}
	for name, v := range map[string]string{"subject_id": s.subjectID, "subject": s.subject, "verdict": s.verdict} {
		if v == "" {
			return eachSpec{}, fmt.Errorf("no %s", name)
		}
	}
	qs, err := parseQuestions(with["questions"])
	if err != nil {
		return eachSpec{}, err
	}
	hasVerdict := false
	for _, q := range qs {
		if strings.HasPrefix(q.ID, app.RuleQuestionID("")) {
			return eachSpec{}, fmt.Errorf("question id %q is reserved for rules", q.ID)
		}
		hasVerdict = hasVerdict || q.ID == s.verdict
	}
	if !hasVerdict {
		return eachSpec{}, fmt.Errorf("verdict %q is not one of the questions", s.verdict)
	}

	if path := with["rules"]; path != "" {
		body, err := t.docs.Get(ctx, path)
		if err != nil {
			return eachSpec{}, fmt.Errorf("rules at %s: %w", path, err)
		}
		if s.rules, err = app.ParseRules(body); err != nil {
			return eachSpec{}, err
		}
		if err := yaml.Unmarshal([]byte(with["rule_fields"]), &s.fields); err != nil {
			return eachSpec{}, fmt.Errorf("rule_fields: %w", err)
		}
		known := make(map[string]bool, len(s.rules))
		for _, r := range s.rules {
			known[r.ID] = true
		}
		for id := range s.fields {
			if !known[id] {
				return eachSpec{}, fmt.Errorf("rule_fields names %q, which is not a rule", id)
			}
		}
	}

	s.questions = append([]ports.Question{}, qs...)
	for _, r := range s.rules {
		if r.Kind == app.RuleJudged {
			s.questions = append(s.questions, app.RuleQuestion(r))
		}
	}

	if _, err := app.AnswerSchema(s.questions); err != nil {
		return eachSpec{}, err
	}
	if _, err := app.ParseItemTemplate(s.subjectID); err != nil {
		return eachSpec{}, err
	}
	if _, err := app.ParseItemTemplate(s.subject); err != nil {
		return eachSpec{}, err
	}
	return s, nil
}

// one judges a single item, or reuses its recorded judgement. Failing to
// render, ask or assess the item is reported in its row; only a failure to
// find or record a judgement is returned, since a board whose results
// cannot be kept has not partly succeeded.
func (t *JudgeEach) one(ctx context.Context, s eachSpec, itemID string, doc any) (map[string]any, bool, error) {
	subjectID, err := app.RenderItem(s.subjectID, doc)
	if err != nil {
		return errorRow(itemID, err), false, nil
	}
	if err := app.CheckSubjectID(subjectID); err != nil {
		return errorRow(subjectID, err), false, nil
	}
	subject, err := app.RenderItem(s.subject, doc)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}
	req, err := app.JudgeRequest(subject, s.questions)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}
	fp, err := app.Fingerprint(req, s.rules, app.RuleInputs(doc, s.fields), s.verdict, t.model)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}

	if row, found, err := t.reuse(ctx, s, subjectID, fp); err != nil || found {
		return row, found, err
	}

	j, err := t.judge.Ask(ctx, subject, s.questions)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}
	results, err := app.CheckRules(s.rules, doc, s.fields, j.Answers)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}
	a, err := app.Assess(s.verdict, j.Answers, results)
	if err != nil {
		return errorRow(subjectID, err), false, nil
	}
	path, err := app.RecordAssessedJudgement(ctx, t.docs, t.index, subjectID, s.questions, j,
		app.Assessed{Fingerprint: fp, Verdict: a.Verdict, Rules: results})
	if err != nil {
		return nil, false, err
	}
	return assessedRow(subjectID, path, a, false), false, nil
}

// reuse finds a judgement already recorded for subjectID under fp and
// rebuilds its row from the stored answers and rule results, through the
// same Assess a fresh judgement goes through. Matching subjectID as well as
// fp keeps two different items that render the same subject text from
// sharing one judgement. An indexed judgement whose document gitdocs can no
// longer find (object.ErrFileNotFound: the index row outlived the git
// history behind it) is treated as no reuse, so the item is judged fresh
// instead of aborting the run; any other read failure still aborts it.
func (t *JudgeEach) reuse(ctx context.Context, s eachSpec, subjectID, fp string) (map[string]any, bool, error) {
	recs, err := t.index.Find(ctx, ports.Query{Kind: "judgement", Match: map[string]string{"fingerprint": fp, "subject_id": subjectID}, Limit: 1})
	if err != nil {
		return nil, false, fmt.Errorf("find %s: %w", subjectID, err)
	}
	if len(recs) == 0 {
		return nil, false, nil
	}
	stored, err := app.ReadJudgement(ctx, t.docs, recs[0].Path)
	if errors.Is(err, object.ErrFileNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	a, err := app.Assess(s.verdict, stored.Answers, stored.Rules)
	if err != nil {
		return nil, false, err
	}
	return assessedRow(subjectID, recs[0].Path, a, true), true, nil
}

func assessedRow(subjectID, path string, a app.Assessment, reused bool) map[string]any {
	reasons := make([]any, len(a.Reasons))
	for i, r := range a.Reasons {
		reasons[i] = r
	}
	rules := make([]any, len(a.Rules))
	for i, r := range a.Rules {
		rules[i] = map[string]any{"id": r.ID, "state": r.State, "evidence": r.Evidence}
	}
	return map[string]any{
		"subject_id":     subjectID,
		"verdict":        a.Verdict,
		"p":              a.P,
		"reasons":        reasons,
		"rules":          rules,
		"judgement_path": path,
		"reused":         reused,
	}
}

func errorRow(subjectID string, err error) map[string]any {
	return map[string]any{"subject_id": subjectID, "error": err.Error()}
}
