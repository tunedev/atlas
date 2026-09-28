package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// loopCase is the apply-loop scenario, read from testdata so the packs it
// runs and the subjects it names stay out of the Go tree. Paths are
// relative to the repository root.
type loopCase struct {
	Pack     string                     `json:"pack"`
	Vars     map[string]string          `json:"vars"`
	Children map[string]string          `json:"children"`
	Docs     map[string]json.RawMessage `json:"docs"`
	Items    []struct {
		ID   string          `json:"id"`
		Body json.RawMessage `json:"body"`
	} `json:"items"`
	Verdicts   map[string]string     `json:"verdicts"`
	Expect     map[string]loopExpect `json:"expect"`
	DraftFails struct {
		Children map[string]string     `json:"children"`
		Expect   map[string]loopExpect `json:"expect"`
	} `json:"draft_fails"`
	Suggest struct {
		Pack string            `json:"pack"`
		Vars map[string]string `json:"vars"`
	} `json:"suggest"`
	Declare struct {
		Pack  string            `json:"pack"`
		Vars  map[string]string `json:"vars"`
		Stage string            `json:"stage"`
	} `json:"declare"`
}

// loopExpect is what one subject should show after a run.
type loopExpect struct {
	Stage     string   `json:"stage"`
	Note      string   `json:"note"`
	Decisions []string `json:"decisions"`
	Listed    struct {
		Step     string `json:"step"`
		Decision string `json:"decision"`
	} `json:"listed"`
}

// loopSource holds the case's items, refreshed now.
type loopSource struct{ items []ports.Item }

func (s loopSource) Pull(context.Context) ([]ports.Item, error) { return s.items, nil }
func (s loopSource) LastRefreshed(context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}

// loopJudge answers the verdict question with the case's verdict for the
// first key found in the subject, and every other question with its first
// option. A subject no key names fails.
type loopJudge struct {
	verdicts map[string]string
	verdict  string
}

func (j loopJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	var chosen string
	for key, v := range j.verdicts {
		if strings.Contains(subject, key) {
			chosen = v
		}
	}
	if chosen == "" {
		return ports.Judgement{}, fmt.Errorf("no verdict for this subject")
	}
	answers := make([]ports.Answer, len(qs))
	for i, q := range qs {
		pick := q.Options[0]
		if q.ID == j.verdict {
			pick = chosen
		}
		answers[i] = ports.Answer{ID: q.ID, Kind: q.Kind, Chosen: pick, Distribution: map[string]float64{pick: 0.9}}
	}
	return ports.Judgement{Subject: subject, Model: "stub", When: time.Now().UTC(), Answers: answers}, nil
}

// loop is one wired store and registry the case's packs run against.
type loop struct {
	docs      ports.Docs
	index     ports.Index
	registry  ports.Registry
	childRuns *int
	storeRoot string
	outDir    string
}

// readLoopCase reads the case, from the repository root.
func readLoopCase(t *testing.T) loopCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("cmd", "atlas", "testdata", "apply-loop", "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c loopCase
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// newLoop seeds a real store with the case's documents and wires the
// shipped registry with a stub source and judge, and pack.each over a child
// runner that swaps each of the case's children for its stand-in after
// checking the real pack accepts the same vars.
func newLoop(t *testing.T, c loopCase) loop {
	t.Helper()
	ctx := context.Background()
	docs, index := testStore(t)
	for path, body := range c.Docs {
		if _, err := docs.Put(ctx, path, body, "seed "+path); err != nil {
			t.Fatal(err)
		}
	}
	var items []ports.Item
	for _, it := range c.Items {
		items = append(items, ports.Item{ID: it.ID, Body: it.Body, When: time.Now().UTC()})
	}
	src := loopSource{items: items}
	judge := loopJudge{verdicts: c.Verdicts, verdict: "verdict"}

	tracer := noop.NewTracerProvider().Tracer("")
	registry, _ := buildRegistry(config.Config{}, docs, index, src, testCrawler(t))
	base := registry.With(tools.NewJudgeEach(src, judge, docs, index, "stub", time.Hour, slog.Default()))
	runs := 0
	real := childRunner(base, tracer, io.Discard)
	child := func(ctx context.Context, path string, vars map[string]string) error {
		runs++
		standIn, ok := c.Children[path]
		if !ok {
			return real(ctx, path, vars)
		}
		b, err := packfile.Load(path)
		if err != nil {
			return err
		}
		if _, err := b.WithVars(vars); err != nil {
			return err
		}
		return real(ctx, standIn, vars)
	}
	reg := base.With(tools.NewPackEach(child, stageOf(index)))
	return loop{docs: docs, index: index, registry: reg, childRuns: &runs, storeRoot: filepath.Join(t.TempDir(), "a 'quoted' root"), outDir: filepath.Join(t.TempDir(), "a 'quoted' dir")}
}

// run runs the pack at path with vars through the real runner and returns
// its outputs as decoded JSON.
func (l loop) run(t *testing.T, path string, vars map[string]string) map[string]any {
	t.Helper()
	out, err := l.runErr(t, path, vars)
	if err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	return out
}

// runErr is run, returning the run's error instead of failing on it.
func (l loop) runErr(t *testing.T, path string, vars map[string]string) (map[string]any, error) {
	t.Helper()
	b, err := packfile.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, err = b.WithVars(vars); err != nil {
		t.Fatal(err)
	}
	state, err := app.NewRunner(l.registry).Run(context.Background(), b)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(state.Outputs())
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// revisions counts every document's revisions in the store.
func (l loop) revisions(t *testing.T) map[string]int {
	t.Helper()
	ctx := context.Background()
	paths, err := l.docs.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int, len(paths))
	for _, p := range paths {
		h, err := l.docs.History(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		counts[p] = len(h)
	}
	return counts
}

// stageNote reads the note on a subject's current stage document.
func (l loop) stageNote(t *testing.T, subjectID string) string {
	t.Helper()
	ctx := context.Background()
	recs, err := l.index.Find(ctx, ports.Query{Kind: "stage", Match: map[string]string{"subject_id": subjectID}, Limit: 1})
	if err != nil || len(recs) == 0 {
		t.Fatalf("%s: stage row: %v, %d rows", subjectID, err, len(recs))
	}
	body, err := l.docs.Get(ctx, recs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Note
}

// listedDecision is the decision a step's output rows give subjectID, ""
// when no row names it.
func listedDecision(outputs map[string]any, step, subjectID string) string {
	out, _ := outputs[step].(map[string]any)
	rows, _ := out["rows"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["subject_id"] == subjectID {
			d, _ := row["decision"].(string)
			return d
		}
	}
	return ""
}

// choices lists the choices recorded for a subject, sorted.
func (l loop) choices(t *testing.T, subjectID string) []string {
	t.Helper()
	recs, err := l.index.Find(context.Background(), ports.Query{Kind: "decision", Match: map[string]string{"subject_id": subjectID}})
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range recs {
		out = append(out, r.Fields["decision"])
	}
	slices.Sort(out)
	return out
}

// check compares each expected subject's stage, recorded choices, stage note
// and listed decision with the store and the run's outputs.
func (l loop) check(t *testing.T, outputs map[string]any, expect map[string]loopExpect) {
	t.Helper()
	for id, want := range expect {
		stage, err := app.CurrentStage(context.Background(), l.index, id)
		if err != nil {
			t.Fatal(err)
		}
		if stage != want.Stage {
			t.Errorf("%s: stage = %q; want %q", id, stage, want.Stage)
		}
		if got := l.choices(t, id); !slices.Equal(got, want.Decisions) {
			t.Errorf("%s: decisions = %v; want %v", id, got, want.Decisions)
		}
		if want.Note != "" {
			if got := l.stageNote(t, id); got != want.Note {
				t.Errorf("%s: stage note = %q; want %q", id, got, want.Note)
			}
		}
		if want.Listed.Step != "" {
			if got := listedDecision(outputs, want.Listed.Step, id); got != want.Listed.Decision {
				t.Errorf("%s: step %s lists decision %q; want %q", id, want.Listed.Step, got, want.Listed.Decision)
			}
		}
	}
}

// TestApplyLoopDraftsAllowedSkipsDeniedAndLeavesTheRestAlone runs the
// shipped loop pack over a stub source and judge: an allowed row reaches
// its draft stage, a denied row gets a skip decision and its skip stage, an
// asked row and an error row are untouched, and a second run changes no
// document and runs no child. The automatic skips propose no policy rule,
// and the person can then declare a later stage for the drafted row.
func TestApplyLoopDraftsAllowedSkipsDeniedAndLeavesTheRestAlone(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	c := readLoopCase(t)
	l := newLoop(t, c)
	vars := map[string]string{"store_root": l.storeRoot, "out_dir": l.outDir}
	for k, v := range c.Vars {
		vars[k] = v
	}

	first := l.run(t, c.Pack, vars)
	l.check(t, first, c.Expect)

	suggested := 0
	for step, out := range l.run(t, c.Suggest.Pack, c.Suggest.Vars) {
		cands, ok := out.(map[string]any)["candidates"].([]any)
		if !ok {
			continue
		}
		suggested++
		if len(cands) != 0 {
			t.Errorf("step %s proposed %v from the loop's automatic skips; want none", step, cands)
		}
	}
	if suggested == 0 {
		t.Error("the suggest pack produced no candidates list")
	}

	before, runsBefore := l.revisions(t), *l.childRuns
	l.run(t, c.Pack, vars)
	if after := l.revisions(t); !maps.Equal(before, after) {
		t.Errorf("a second run changed the store:\nbefore %v\nafter  %v", before, after)
	}
	if runs := *l.childRuns - runsBefore; runs != 0 {
		t.Errorf("a second run ran %d child packs; want 0", runs)
	}

	l.run(t, c.Declare.Pack, c.Declare.Vars)
	id := c.Declare.Vars["subject_id"]
	if stage, err := app.CurrentStage(context.Background(), l.index, id); err != nil || stage != c.Declare.Stage {
		t.Errorf("%s after declaring: stage = %q, err = %v; want %q", id, stage, err, c.Declare.Stage)
	}
}

// TestAFailedDraftDoesNotBlockTheSkips runs the loop with a child that always
// fails in place of the drafting pack: the run fails, and every denied row
// is still skipped.
func TestAFailedDraftDoesNotBlockTheSkips(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	c := readLoopCase(t)
	c.Children = c.DraftFails.Children
	l := newLoop(t, c)
	vars := map[string]string{"store_root": l.storeRoot, "out_dir": l.outDir}
	for k, v := range c.Vars {
		vars[k] = v
	}

	_, err := l.runErr(t, c.Pack, vars)
	if err == nil {
		t.Fatal("the loop succeeded although every draft failed")
	}
	t.Logf("run failed as expected: %v", err)
	l.check(t, nil, c.DraftFails.Expect)
}
