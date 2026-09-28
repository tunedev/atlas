package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

// shelfJudge answers every choice with its first option at 0.8, and every
// yes/no with p(yes) from yesP (default 0.1). A subject containing FAIL
// errors. It counts calls.
type shelfJudge struct {
	calls int
	yesP  map[string]float64
}

func (s *shelfJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	s.calls++
	if strings.Contains(subject, "FAIL") {
		return ports.Judgement{}, errors.New("engine refused")
	}
	var answers []ports.Answer
	for _, q := range qs {
		if q.Kind == ports.KindNoul {
			p, ok := s.yesP[q.ID]
			if !ok {
				p = 0.1
			}
			chosen := "no"
			if p >= 0.5 {
				chosen = "yes"
			}
			answers = append(answers, ports.Answer{ID: q.ID, Kind: q.Kind, Chosen: chosen,
				Distribution: map[string]float64{"yes": p, "no": 1 - p}, Coverage: ports.Coverage{Represented: 2, Declared: 2}})
			continue
		}
		answers = append(answers, ports.Answer{ID: q.ID, Kind: q.Kind, Chosen: q.Options[0],
			Distribution: map[string]float64{q.Options[0]: 0.8}, Coverage: ports.Coverage{Represented: 1, Declared: len(q.Options)}})
	}
	return ports.Judgement{Subject: subject, Model: "m", When: time.Now().UTC(), Answers: answers}, nil
}

func book(id, title string, pages float64, state string) ports.Item {
	body, _ := json.Marshal(map[string]any{"id": id, "title": title, "pages": pages, "state": state})
	return ports.Item{ID: "shelf/" + id, Body: body}
}

const eachQuestions = `
- id: verdict
  type: choice
  options: [keep, lend, skip]
  ask: Should the reader keep, lend or skip this book?
- id: genre
  type: choice
  options: [fiction, history]
  ask: Which genre is it?
`

func eachWith() map[string]string {
	return map[string]string{
		"prefix":     "shelf",
		"match":      "state=open",
		"subject_id": `[[ replace .item.id "/" "~" ]]`,
		"subject":    "Book: [[ .item.title ]]",
		"verdict":    "verdict",
		"questions":  eachQuestions,
	}
}

type run struct {
	rows []map[string]any
	meta map[string]any
}

func invokeEach(t *testing.T, tool *tools.JudgeEach, with map[string]string) run {
	t.Helper()
	out, err := tool.Invoke(context.Background(), with)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	res := out.(map[string]any)
	var rows []map[string]any
	for _, r := range res["rows"].([]any) {
		rows = append(rows, r.(map[string]any))
	}
	return run{rows, res["_meta"].(map[string]any)}
}

func newEach(t *testing.T, items []ports.Item, judge ports.Judge) (*tools.JudgeEach, ports.Docs, ports.Index) {
	t.Helper()
	docs, index := store(t)
	src := fakeSource{items: items, refreshed: time.Now().Add(-time.Hour)}
	return tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), docs, index
}

func TestJudgeEachJudgesEverySelectedItemInIDOrder(t *testing.T) {
	items := []ports.Item{
		book("c", "Ulysses", 730, "open"), book("b", "Emma", 300, "lent"),
		{ID: "elsewhere/x", Body: []byte(`not json`)}, book("a", "Dune", 412, "open"),
	}
	judge := &shelfJudge{}
	tool, _, _ := newEach(t, items, judge)
	got := invokeEach(t, tool, eachWith())
	if len(got.rows) != 2 || got.rows[0]["subject_id"] != "a" || got.rows[1]["subject_id"] != "c" {
		t.Fatalf("rows = %v", got.rows)
	}
	r := got.rows[0]
	if r["verdict"] != "keep" || r["p"] != 0.8 || r["reused"] != false || r["judgement_path"] == "" {
		t.Errorf("row = %v", r)
	}
	if r["source_id"] != "shelf/a" {
		t.Errorf("source_id = %v, want shelf/a", r["source_id"])
	}
	answers, ok := r["answers"].(map[string]any)
	if !ok || answers["verdict"] != "keep" || answers["genre"] != "fiction" {
		t.Errorf("answers = %v", r["answers"])
	}
	item, ok := r["item"].(map[string]any)
	if !ok || item["title"] != "Dune" {
		t.Errorf("item = %v", r["item"])
	}
	if got.meta["count"] != 2 || got.meta["judged"] != 2 || got.meta["reused"] != 0 || got.meta["errors"] != 0 || judge.calls != 2 {
		t.Errorf("meta = %v, calls = %d", got.meta, judge.calls)
	}
}

func TestASecondRunReusesEveryJudgementWithoutAsking(t *testing.T) {
	judge := &shelfJudge{}
	tool, _, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "open"), book("c", "Ulysses", 730, "open")}, judge)
	first := invokeEach(t, tool, eachWith())
	second := invokeEach(t, tool, eachWith())
	if judge.calls != 2 {
		t.Fatalf("calls = %d, want 2: the second run must ask nothing", judge.calls)
	}
	wantSourceID := []string{"shelf/a", "shelf/c"}
	wantTitle := []string{"Dune", "Ulysses"}
	for i, r := range second.rows {
		if r["reused"] != true || r["verdict"] != first.rows[i]["verdict"] || r["judgement_path"] != first.rows[i]["judgement_path"] {
			t.Errorf("row %d = %v, want the first run's judgement reused", i, r)
		}
		if strings.Join(anyStrings(r["reasons"]), "|") != strings.Join(anyStrings(first.rows[i]["reasons"]), "|") {
			t.Errorf("row %d reasons changed on reuse", i)
		}
		if r["source_id"] != wantSourceID[i] {
			t.Errorf("row %d source_id = %v, want %s", i, r["source_id"], wantSourceID[i])
		}
		answers, ok := r["answers"].(map[string]any)
		if !ok || answers["verdict"] != "keep" || answers["genre"] != "fiction" {
			t.Errorf("row %d answers = %v", i, r["answers"])
		}
		item, ok := r["item"].(map[string]any)
		if !ok || item["title"] != wantTitle[i] {
			t.Errorf("row %d item = %v, want title %s", i, r["item"], wantTitle[i])
		}
	}
	if second.meta["reused"] != 2 || second.meta["judged"] != 0 {
		t.Errorf("meta = %v", second.meta)
	}
}

func TestChangingOneItemRejudgesOnlyThatItem(t *testing.T) {
	judge := &shelfJudge{}
	docs, index := store(t)
	src := fakeSource{items: []ports.Item{book("a", "Dune", 412, "open"), book("c", "Ulysses", 730, "open")}, refreshed: time.Now()}
	invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), eachWith())
	src.items[1] = book("c", "Ulysses, annotated", 730, "open")
	got := invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), eachWith())
	if judge.calls != 3 || got.rows[0]["reused"] != true || got.rows[1]["reused"] != false {
		t.Errorf("calls = %d, rows = %v; want only the changed item asked", judge.calls, got.rows)
	}
}

func TestOneFailingItemBecomesAnErrorRow(t *testing.T) {
	tool, _, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "open"), book("b", "FAIL", 1, "open"), book("c", "Ulysses", 730, "open")}, &shelfJudge{})
	got := invokeEach(t, tool, eachWith())
	if len(got.rows) != 3 || got.rows[1]["subject_id"] != "b" || !strings.Contains(got.rows[1]["error"].(string), "engine refused") {
		t.Fatalf("rows = %v", got.rows)
	}
	if got.rows[1]["source_id"] != "shelf/b" {
		t.Errorf("error row source_id = %v, want shelf/b", got.rows[1]["source_id"])
	}
	if item, ok := got.rows[1]["item"].(map[string]any); !ok || item["title"] != "FAIL" {
		t.Errorf("error row item = %v, want title FAIL", got.rows[1]["item"])
	}
	if got.rows[0]["verdict"] != "keep" || got.rows[2]["verdict"] != "keep" || got.meta["errors"] != 1 {
		t.Errorf("rows = %v, meta = %v", got.rows, got.meta)
	}
}

func TestEveryItemFailingIsAnError(t *testing.T) {
	tool, _, _ := newEach(t, []ports.Item{book("a", "FAIL", 1, "open"), book("b", "FAIL too", 1, "open")}, &shelfJudge{})
	if _, err := tool.Invoke(context.Background(), eachWith()); err == nil || !strings.Contains(err.Error(), "engine refused") {
		t.Errorf("err = %v, want every item failing to fail the run", err)
	}
}

func TestASubjectIDThatCannotBeRecordedIsThatItemsError(t *testing.T) {
	judge := &shelfJudge{}
	tool, _, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "open"), book("b", "Bad/Title", 300, "open")}, judge)
	with := eachWith()
	with["subject_id"] = "[[ .item.title ]]"
	got := invokeEach(t, tool, with)
	if len(got.rows) != 2 {
		t.Fatalf("rows = %v", got.rows)
	}
	good, bad := got.rows[0], got.rows[1]
	if good["subject_id"] != "Dune" || good["verdict"] != "keep" {
		t.Errorf("good row = %v", good)
	}
	if bad["subject_id"] != "Bad/Title" || !strings.Contains(bad["error"].(string), "not a plain name") {
		t.Errorf("bad row = %v", bad)
	}
	if judge.calls != 1 {
		t.Errorf("calls = %d, want 1: only the good item should be asked", judge.calls)
	}
	if got.meta["judged"] != 1 || got.meta["errors"] != 1 {
		t.Errorf("meta = %v", got.meta)
	}
}

func TestTwoItemsWithIdenticalSubjectsAreBothJudged(t *testing.T) {
	judge := &shelfJudge{}
	tool, _, _ := newEach(t, []ports.Item{book("a", "Same Title", 412, "open"), book("b", "Same Title", 412, "open")}, judge)
	with := eachWith()
	with["subject"] = "Book: same subject text regardless of item"
	got := invokeEach(t, tool, with)
	if judge.calls != 2 {
		t.Fatalf("calls = %d, want 2: identical subjects on different items must both be asked", judge.calls)
	}
	for _, r := range got.rows {
		id := r["subject_id"].(string)
		path := r["judgement_path"].(string)
		if !strings.Contains(path, id) {
			t.Errorf("row %v: judgement_path %q does not belong to subject_id %q", r, path, id)
		}
	}
}

// TestAReusedJudgementWhoseDocumentIsMissingIsJudgedFresh records a
// judgement, then reuses the same index against a Docs store that never
// saw that judgement written (the realistic shape of the document going
// missing: the index row survives, the git history behind it does not).
// Get on a path never committed fails with the real gitdocs not-found
// error, so the item must be judged again rather than aborting the run.
func TestAReusedJudgementWhoseDocumentIsMissingIsJudgedFresh(t *testing.T) {
	judge := &shelfJudge{}
	docs, index := store(t)
	src := fakeSource{items: []ports.Item{book("a", "Dune", 412, "open")}, refreshed: time.Now()}
	invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), eachWith())
	if judge.calls != 1 {
		t.Fatalf("calls = %d, want 1", judge.calls)
	}

	emptyDocs, err := gitdocs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	if _, err := emptyDocs.Put(context.Background(), "unrelated.json", []byte(`{}`), "unrelated"); err != nil {
		t.Fatalf("put unrelated: %v", err)
	}
	got := invokeEach(t, tools.NewJudgeEach(src, judge, emptyDocs, index, "m", 24*time.Hour, slog.Default()), eachWith())
	if judge.calls != 2 {
		t.Errorf("calls = %d, want 2: a reused judgement whose document is missing must be judged fresh", judge.calls)
	}
	if got.rows[0]["reused"] != false {
		t.Errorf("row = %v, want reused false", got.rows[0])
	}
}

const shelfRulesDoc = `{"rules": [
  {"id": "min-pages", "statement": "At least 500 pages.", "kind": "comparable", "op": ">=", "value": 500},
  {"id": "no-spoilers", "statement": "No spoilers in the blurb.", "kind": "judged", "threshold": 0.6, "ask": "Does the blurb reveal the ending?"},
  {"id": "in-print", "statement": "Still in print.", "kind": "comparable", "op": "==", "value": "yes"}
]}`

func TestRulesAreCheckedNamedFirstAndIndexed(t *testing.T) {
	judge := &shelfJudge{yesP: map[string]float64{"rule_no-spoilers": 0.71}}
	tool, docs, index := newEach(t, []ports.Item{book("a", "Dune", 412, "open")}, judge)
	ctx := context.Background()
	if _, err := docs.Put(ctx, "profile/shelf-rules.json", []byte(shelfRulesDoc), "rules"); err != nil {
		t.Fatalf("put rules: %v", err)
	}
	with := eachWith()
	with["rules"] = "profile/shelf-rules.json"
	with["rule_fields"] = "min-pages: pages\nin-print: print.status\n"
	r := invokeEach(t, tool, with).rows[0]
	reasons := anyStrings(r["reasons"])
	if len(reasons) < 3 ||
		reasons[0] != "min-pages tripped: pages is 412, rule needs >= 500" ||
		reasons[1] != "no-spoilers tripped: p(yes) 0.71 >= 0.60" ||
		reasons[2] != "in-print unknown: print.status not in the subject" {
		t.Errorf("reasons = %q", reasons)
	}
	if r["verdict"] != "keep" {
		t.Errorf("verdict = %v: rules flag, they never override", r["verdict"])
	}
	recs, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: map[string]string{"tripped": "min-pages,no-spoilers"}})
	if err != nil || len(recs) != 1 {
		t.Errorf("find by tripped: %v, %v", recs, err)
	}
}

func ruleState(t *testing.T, row map[string]any, id string) string {
	t.Helper()
	for _, r := range row["rules"].([]any) {
		rule := r.(map[string]any)
		if rule["id"] == id {
			return rule["state"].(string)
		}
	}
	t.Fatalf("no rule %q in row %v", id, row)
	return ""
}

func TestChangingAMappedFieldValueRejudgesTheItem(t *testing.T) {
	judge := &shelfJudge{}
	docs, index := store(t)
	items := []ports.Item{book("a", "Dune", 412, "open")}
	src := fakeSource{items: items, refreshed: time.Now()}
	if _, err := docs.Put(context.Background(), "profile/shelf-rules.json", []byte(shelfRulesDoc), "rules"); err != nil {
		t.Fatalf("put rules: %v", err)
	}
	with := eachWith()
	with["rules"] = "profile/shelf-rules.json"
	with["rule_fields"] = "min-pages: pages\n"

	first := invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), with)
	if state := ruleState(t, first.rows[0], "min-pages"); state != "tripped" {
		t.Fatalf("initial min-pages state = %q, want tripped (412 < 500)", state)
	}

	src.items[0] = book("a", "Dune", 600, "open")
	second := invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), with)
	if judge.calls != 2 {
		t.Errorf("calls = %d, want 2: a changed mapped value must re-judge", judge.calls)
	}
	if state := ruleState(t, second.rows[0], "min-pages"); state != "clear" {
		t.Errorf("min-pages state after change = %q, want clear (600 >= 500)", state)
	}
}

func TestChangingRuleFieldsAloneRejudgesTheItem(t *testing.T) {
	judge := &shelfJudge{}
	docs, index := store(t)
	src := fakeSource{items: []ports.Item{book("a", "Dune", 412, "open")}, refreshed: time.Now()}
	if _, err := docs.Put(context.Background(), "profile/shelf-rules.json", []byte(shelfRulesDoc), "rules"); err != nil {
		t.Fatalf("put rules: %v", err)
	}
	with := eachWith()
	with["rules"] = "profile/shelf-rules.json"
	with["rule_fields"] = "min-pages: pages\n"
	invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), with)

	with["rule_fields"] = "min-pages: pages\nin-print: print.status\n"
	invokeEach(t, tools.NewJudgeEach(src, judge, docs, index, "m", 24*time.Hour, slog.Default()), with)
	if judge.calls != 2 {
		t.Errorf("calls = %d, want 2: changing rule_fields alone must re-judge", judge.calls)
	}
}

func TestBadConfigurationFailsBeforeAnyItemIsAsked(t *testing.T) {
	judge := &shelfJudge{}
	tool, docs, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "open")}, judge)
	if _, err := docs.Put(context.Background(), "profile/shelf-rules.json", []byte(shelfRulesDoc), "rules"); err != nil {
		t.Fatalf("put rules: %v", err)
	}
	for name, change := range map[string]map[string]string{
		"verdict not a question":    {"verdict": "mood"},
		"no subject":                {"subject": ""},
		"rule_ question id":         {"questions": eachQuestions + "- id: rule_x\n  type: noul\n  ask: x?\n"},
		"rule_fields names no rule": {"rules": "profile/shelf-rules.json", "rule_fields": "nonsense: pages\n"},
	} {
		with := eachWith()
		for k, v := range change {
			with[k] = v
		}
		if _, err := tool.Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if judge.calls != 0 {
		t.Errorf("calls = %d: configuration errors must stop the run before asking", judge.calls)
	}
}

// TestConfigurationIsCheckedEvenWithNoMatchingItems proves the checks are a
// pre-flight, not a side effect of asking the first item: both a
// shared-initial option set and a malformed item template fail Invoke here
// even though no item matches, so nothing about the fix depends on an item
// reaching t.one.
func TestConfigurationIsCheckedEvenWithNoMatchingItems(t *testing.T) {
	for name, change := range map[string]map[string]string{
		"shared-initial options":        {"questions": strings.Replace(eachQuestions, "[fiction, history]", "[fiction, frontier]", 1)},
		"malformed subject_id template": {"subject_id": "[[ .item.id"},
	} {
		judge := &shelfJudge{}
		tool, _, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "lent")}, judge)
		with := eachWith()
		for k, v := range change {
			with[k] = v
		}
		if _, err := tool.Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: accepted even though no item matched", name)
		}
		if judge.calls != 0 {
			t.Errorf("%s: calls = %d, want 0", name, judge.calls)
		}
	}
}

func TestZeroSelectedItemsSucceedsWithEmptyRows(t *testing.T) {
	tool, _, _ := newEach(t, []ports.Item{book("a", "Dune", 412, "lent"), book("b", "Emma", 300, "lent")}, &shelfJudge{})
	got := invokeEach(t, tool, eachWith())
	if len(got.rows) != 0 {
		t.Errorf("rows = %v, want empty", got.rows)
	}
	if got.meta["count"] != 0 || got.meta["judged"] != 0 || got.meta["reused"] != 0 || got.meta["errors"] != 0 {
		t.Errorf("meta = %v, want all zero", got.meta)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}
