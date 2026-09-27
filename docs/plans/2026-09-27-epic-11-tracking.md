# Epic 11 — Tracking (outcomes and calibration) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a judged subject gets a real-world outcome attached as a later revision of its judgement
document, and the predicted probabilities are scored against those outcomes with a Brier score
and a reliability table that refuse to print a number the sample cannot support.

**Architecture:** Three pure-core use cases in `internal/core/app`, each over `Docs` and `Index`
only:
- `AttachOutcome` / `AttachOutcomeForSubject`;
- `Calibrate` / `CalibrateByEngine`;
- `AgreementRate`.

Three thin tools expose them to packs: `judge.outcome`, `judge.calibrate` and
`decision.agreement`. The tools only parse, call and render; no rule lives in them. Nothing new is
persisted except the outcome itself; a calibration report is derived on every run.

**Tech Stack:** Go 1.27, the standard library, the existing `gitdocs`/`sqlindex` adapters. No
model engine is needed except in the one manual acceptance step (Task 7), which uses Ollama at
`http://localhost:11434/v1` with `qwen2.5-coder:7b`.

**Spec:** `docs/specs/2026-09-27-epic-11-tracking.md`. Scope is 11.3 (outcomes) and 11.4
(calibration), plus the spec's agreement rate. 11.1 and 11.2 wait for Epic 10, per the spec.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows an outcome's meaning.** `Outcome.State`, `Prediction.Positive`
  and `Prediction.Negative` are free strings a pack defines. No Go identifier, constant or string
  literal names `offer`, `rejected`, `ghosted`, `not_applied` or `withdrawn`. Go test fixtures
  use weather: the question is `rain`; the states are `wet`, `dry`, `fog`.
  `internal/arch/vocabulary_test.go` enforces the job-hunt half.
- **No core package imports an adapter.** `internal/arch/arch_test.go` enforces it. Core
  *test* files may use the real `gitdocs`/`sqlindex` adapters, as `rebuild_test.go` already does.
- **The core is a narrow waist** (`../CLAUDE.md`, "The narrow waist"). Every rule lives in
  `internal/core/app`. The tools parse `with`, call the core, and render the result; they own
  no business rule.
- `ctx context.Context` is the first parameter of every blocking function.
- Every wrapped error carries its component prefix:
  - core: `outcome: `, `calibrate: `, `agreement: `;
  - tools: `judge.outcome: `, `judge.calibrate: `, `decision.agreement: `.
- `MinSample = 30` for a headline Brier score and `MinBinSample = 10` for one reliability row,
  verbatim from the spec. Below either, the count is reported and the number is `nil`.
- Reliability bins are fixed-width deciles, `[0.0,0.1)` through `[0.9,1.0]`.
- A calibration report is never written to git.
- No emojis. Comments describe current behaviour only. Tests assert behaviour. Prove a guard by
  mutation and confirm the mutation applied.
- Every test runs with no network.

## Rulings on what the spec leaves open or gets wrong

**R1. The prediction's options are separate from the outcome states.**
- **The problem:** the spec's `Prediction` uses `Positive` for two different things. It is
  both the outcome states that count as the prediction coming true (matched against
  `Outcome.State`) and the answer options whose `Distribution` mass is the predicted
  probability ("`Positive: []string{"apply"}` … using `Distribution["apply"]`").
- **Why that fails:** those vocabularies differ. An answer option is `yes` or `apply`; an
  outcome state is what happened in the world. A pack could not score `stretch = yes` against
  an outcome state without renaming one of them.
- **Ruling:** `Prediction` gains `Options []string`, the answer options whose summed mass is the
  predicted probability of a positive outcome. `Positive` and `Negative` stay outcome states.
- **Validation:** all three must be non-empty, and `Positive` and `Negative` must not overlap.

**R2. An option the question never declared excludes that judgement, and is counted.**
- **The problem:** a typo in `Options` (`yse`) would otherwise score every judgement at
  probability 0, a confidently wrong number with nothing to flag it.
- **Ruling:** a judgement whose recorded question does not declare every entry in `Options` is
  excluded and counted in a fourth exclusion, `OptionMismatch`. A noul question's declared
  options are `yes`/`no`, as `app.OptionsFor` already defines.

**R3. Attaching an outcome replaces only the `outcome` value, byte for byte everywhere else.**
- **What exists:** Epic 7, planned in parallel, adds optional document keys (`fingerprint`,
  `rules`) and index fields (`verdict`, `tripped`, `fingerprint`).
- **The trap:** round-tripping the document through `judgementDoc` would drop any key that
  struct does not know yet. Rebuilding the row from `judgementFields` would drop Epic 7's fields.
- **Ruling:** `AttachOutcome` splices the new value into the `outcome` key and keeps every other
  key, its value and its order. It copies the existing index row's fields and changes only
  `outcome`.
- **Evidence:** measured while planning, splicing `null` back in reproduces a `MarshalIndent`
  document byte for byte, HTML escapes and unknown keys included.
- **What doesn't change:** `judgementDoc` is not modified, so there is no merge conflict with
  Epic 7.

**R4. `AttachOutcomeForSubject` returns paths, not revisions.** Deviation from the spec's
signature: a path is what a caller can act on or show, and each revision stays in git history.

**R5. A judgement that never asked the scored question is not part of the sample.** It is not
counted as an exclusion either: it is a different measurement, not a missing point. The row's
`questions` field decides this without reading the document.

**R6. Judgements recorded before coverage existed are excluded as zero coverage.** Their
`coverage` reads `{0, 0}`, so `Represented == 0`. They cannot be shown to have measured real
competition, which is the spec's own reason for the exclusion. The note records this.

**R7. The tool always reports pooled and per-engine results.**
- **What the spec asks:** the default report is grouped by (Provider, Model), and a pooled
  number is labelled as pooled.
- **Ruling:** `judge.calibrate` returns both `pooled` and `by_engine`, and takes no
  provider/model filter.
- **What stays in the core:** `CalibrateOptions` remains, for `Calibrate`'s callers.

**R8. The agreement rate compares strings, on existing fields only.**
- **How:** `AgreementRate` reads the decision rows' existing `decision` and
  `verdict_at_decision` fields. It does not read Epic 7's `agrees`, so neither plan depends on
  the other's merge order.
- **When it means anything:** the rate only means something when a pack's decisions and its
  verdict question share option names; the doc comment says so.
- **Not built:** the spec's optional breakdown by `Confidence`.

**R9. The shipped calibration pack classifies nothing by default.**
- `packs/judge-calibrate.yaml` has no defaults for `question`, `options`, `positive` or
  `negative`. Its header shows a job-hunt example that leaves `ghosted` unclassified.
- Which question to score, and what `ghosted` means, are the spec's open questions for the
  human. Epic 7 will name the verdict question.

## Review Focus

1. **The same outcome attached twice.** Expected: the second attach is a byte-identical write,
   so no new revision is made and the index row is unchanged. Pinned in Task 1.
2. **An outcome attached to a document that is not a judgement** (a decision or a profile
   path). Expected: refused before anything is read or written. Pinned in Task 1.
3. **A calibration run over an empty record, or before any outcome is attached.** Expected:
   `N = 0`, `Brier` nil, ten empty bins, and every judgement counted as pending. Never an error,
   never a number. Pinned in Task 4.
4. **A probability of exactly 1.0 or exactly 0.0.** Expected: 1.0 lands in the last bin (the top
   decile is closed) and 0.0 in the first. Pinned in Task 3.
5. **A misspelt option or overlapping states in a prediction.** Expected: the misspelt option
   excludes the affected judgements and counts them under `option_mismatch`; overlapping
   positive and negative states are rejected. Neither yields a silent number. Pinned in
   Task 4.

## What "done" means

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./... -race
```

Then the epic's claim, with pasted output (Task 7):

> Judgements recorded through `judge.ask` get outcomes attached through `judge.outcome` as new
> revisions of the same documents, with the original still in `git log`, and `judge.calibrate`
> reports the sample honestly: counts always, a Brier score and bin rates only where the sample
> supports them, pooled and per engine.

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/app/outcome.go` | `Outcome`, `AttachOutcome`, `AttachOutcomeForSubject`, `spliceKey`, `currentRow` |
| `internal/core/app/calibrate.go` | `Prediction`, `Calibration`, the exclusions, `score`, `Calibrate`, `CalibrateByEngine` |
| `internal/core/app/agreement.go` | `Agreement`, `AgreementRate` |
| `internal/adapters/outbound/tools/outcome.go` | `judge.outcome` |
| `internal/adapters/outbound/tools/calibrate.go` | `judge.calibrate` and its rendering |
| `internal/adapters/outbound/tools/agreement.go` | `decision.agreement` |
| `cmd/atlas/main.go`, `main_test.go` | Register the three tools |
| `packs/judge-outcome.yaml`, `packs/judge-calibrate.yaml` | The packs |
| `docs/design/the-judge.md` | The outcome slot, as it now works |
| `docs/notes/2026-09-27-epic-11.md` | The increment note |

---

### Task 1: `AttachOutcome` — a later revision of the same judgement document (11.3)

**Files:**
- Create: `internal/core/app/outcome.go`, `internal/core/app/outcome_test.go`

**Interfaces:**
- Consumes (existing, `internal/core/app`):
  - `judgementDoc`, `judgementQuestion`, `recordDocTimeFormat`, `RecordDocument`, `Document`,
    `RecordJudgement`;
  - the `ports.Docs` and `ports.Index` ports.
- Produces:

```go
type Outcome struct {
	State string
	When  time.Time
	Note  string
}
func AttachOutcome(ctx context.Context, docs ports.Docs, index ports.Index, judgementPath string, o Outcome) (ports.Revision, error)
func spliceKey(body []byte, key string, value json.RawMessage) ([]byte, error)
```

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/outcome_test.go`:

```go
package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// record opens a real git record and SQLite index over temp directories.
func record(t *testing.T) (*gitdocs.Store, *sqlindex.Index) {
	t.Helper()
	ctx := context.Background()
	docs, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	index, err := sqlindex.Open(ctx, t.TempDir()+"/index.db")
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return docs, index
}

func rainQuestion() []ports.Question {
	return []ports.Question{{ID: "rain", Kind: ports.KindNoul, Ask: "Will it rain on the day?"}}
}

// forecast is a judgement of subject that it will rain with probability p.
func forecast(subject string, p float64, when time.Time) ports.Judgement {
	return ports.Judgement{
		Subject:  subject,
		Model:    "a-model",
		Provider: "a-provider",
		When:     when,
		Answers: []ports.Answer{{
			ID: "rain", Kind: ports.KindNoul, Chosen: "yes",
			Distribution: map[string]float64{"yes": p, "no": 1 - p},
			Coverage:     ports.Coverage{Represented: 2, Declared: 2},
		}},
	}
}

var day = time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)

func wet() app.Outcome { return app.Outcome{State: "wet", When: day.Add(48 * time.Hour), Note: "umbrella needed"} }

func judged(t *testing.T, docs ports.Docs, index ports.Index, subject string, p float64, when time.Time) string {
	t.Helper()
	path, err := app.RecordJudgement(context.Background(), docs, index, subject, rainQuestion(), forecast(subject, p, when))
	if err != nil {
		t.Fatalf("record judgement: %v", err)
	}
	return path
}

func row(t *testing.T, index ports.Index, subject, path string) ports.Record {
	t.Helper()
	rows, err := index.Find(context.Background(), ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": subject}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var found []ports.Record
	for _, r := range rows {
		if r.Path == path {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("rows for %s = %d, want exactly 1", path, len(found))
	}
	return found[0]
}

func TestAttachingAnOutcomeIsANewRevisionOfTheSameDocument(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	original, _ := docs.Get(ctx, path)

	rev, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rev == "" {
		t.Error("no revision returned")
	}

	body, _ := docs.Get(ctx, path)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not json: %v", err)
	}
	outcome, ok := doc["outcome"].(map[string]any)
	if !ok || outcome["state"] != "wet" || outcome["when"] != "2026-09-29T09:00:00.000Z" || outcome["note"] != "umbrella needed" {
		t.Errorf("outcome = %v", doc["outcome"])
	}

	history, err := docs.History(ctx, path)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d revisions, err %v; want the judgement then the attachment", len(history), err)
	}
	first, err := docs.GetAt(ctx, path, history[1].Rev)
	if err != nil || string(first) != string(original) {
		t.Errorf("the original judgement is not retrievable unchanged: %v", err)
	}

	r := row(t, index, "harbour-fete", path)
	if r.Fields["outcome"] != "wet" {
		t.Errorf("index outcome = %q, want wet", r.Fields["outcome"])
	}
	if !r.When.Equal(day) {
		t.Errorf("row When = %v, want the judgement's own time %v", r.When, day)
	}
}

func TestACorrectionIsAThirdRevisionAndTheLatestWins(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "fog", When: day}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, wet()); err != nil {
		t.Fatal(err)
	}
	history, _ := docs.History(ctx, path)
	if len(history) != 3 {
		t.Fatalf("history = %d revisions, want 3", len(history))
	}
	earlier, _ := docs.GetAt(ctx, path, history[1].Rev)
	if !strings.Contains(string(earlier), `"state": "fog"`) {
		t.Errorf("the first attachment is not retrievable: %s", earlier)
	}
	if r := row(t, index, "harbour-fete", path); r.Fields["outcome"] != "wet" {
		t.Errorf("index outcome = %q, want the latest, wet", r.Fields["outcome"])
	}
}

func TestAttachingTheSameOutcomeTwiceChangesNothing(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	first, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("revisions %q then %q; a byte-identical attach must be a no-op", first, second)
	}
	if history, _ := docs.History(ctx, path); len(history) != 2 {
		t.Errorf("history = %d revisions, want 2", len(history))
	}
}

func TestAttachingKeepsEveryOtherKeyAndFieldAsItWas(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)

	// A later increment may add document keys and index fields this code
	// does not know. Simulate one: rewrite the document with an extra key,
	// and the row with an extra field.
	body, _ := docs.Get(ctx, path)
	extended := strings.Replace(string(body), "\n}", ",\n  \"fingerprint\": \"abc123\"\n}", 1)
	if _, err := docs.Put(ctx, path, []byte(extended), "extend"); err != nil {
		t.Fatal(err)
	}
	r := row(t, index, "harbour-fete", path)
	r.Fields["verdict"] = "yes"
	if err := index.Upsert(ctx, r); err != nil {
		t.Fatal(err)
	}

	if _, err := app.AttachOutcome(ctx, docs, index, path, wet()); err != nil {
		t.Fatal(err)
	}
	after, _ := docs.Get(ctx, path)
	nullOutcome := strings.Replace(string(after), `"outcome": {
    "state": "wet",
    "when": "2026-09-29T09:00:00.000Z",
    "note": "umbrella needed"
  }`, `"outcome": null`, 1)
	if nullOutcome != extended {
		t.Errorf("attaching changed more than the outcome value:\nbefore:\n%s\nafter:\n%s", extended, after)
	}
	got := row(t, index, "harbour-fete", path)
	if got.Fields["verdict"] != "yes" || got.Fields["model"] != "a-model" || got.Fields["outcome"] != "wet" {
		t.Errorf("row fields = %v; every field but outcome must survive", got.Fields)
	}
}

func TestAttachingRefusesWhatIsNotAJudgement(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	if _, err := docs.Put(ctx, "decisions/harbour-fete/x.json", []byte(`{"outcome": null}`), "a decision"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		o    app.Outcome
	}{
		"a decision path":  {"decisions/harbour-fete/x.json", wet()},
		"escaping path":    {"judgements/../decisions/harbour-fete/x.json", wet()},
		"no such document": {"judgements/nobody/2026.json", wet()},
		"empty state":      {path, app.Outcome{State: "  ", When: day}},
		"zero when":        {path, app.Outcome{State: "wet"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.AttachOutcome(ctx, docs, index, tc.path, tc.o); err == nil || !strings.HasPrefix(err.Error(), "outcome: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
	if history, _ := docs.History(ctx, path); len(history) != 1 {
		t.Errorf("a refused attach wrote to the judgement: %d revisions", len(history))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/core/app/ -run 'Attach|Correction'`
Expected: FAIL to compile, `app.Outcome` undefined.

- [ ] **Step 3: Implement**

Create `internal/core/app/outcome.go`:

```go
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Outcome is the real-world result of a judged subject. State is a free
// string a pack defines the meaning of; When is when the state became true,
// not when it was recorded. A judgement whose outcome is still null has no
// Outcome yet.
type Outcome struct {
	State string
	When  time.Time
	Note  string
}

// outcomeDoc is an Outcome as it appears in a judgement document.
type outcomeDoc struct {
	State string `json:"state"`
	When  string `json:"when"`
	Note  string `json:"note"`
}

// AttachOutcome writes o into the outcome slot of the judgement document at
// judgementPath as a new revision of that document, and sets its index
// row's outcome field to o.State. Every other key of the document and every
// other field of the row stays as it was. Attaching again replaces the
// outcome, and history keeps the earlier one; attaching the same outcome
// again changes nothing.
func AttachOutcome(ctx context.Context, docs ports.Docs, index ports.Index, judgementPath string, o Outcome) (ports.Revision, error) {
	if path.Clean(judgementPath) != judgementPath || !strings.HasPrefix(judgementPath, "judgements/") || !strings.HasSuffix(judgementPath, ".json") {
		return "", fmt.Errorf("outcome: %q is not a judgement document", judgementPath)
	}
	if strings.TrimSpace(o.State) == "" {
		return "", errors.New("outcome: state is empty")
	}
	if o.When.IsZero() {
		return "", errors.New("outcome: when is not set")
	}

	body, err := docs.Get(ctx, judgementPath)
	if err != nil {
		return "", fmt.Errorf("outcome: read %s: %w", judgementPath, err)
	}
	var doc judgementDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("outcome: decode %s: %w", judgementPath, err)
	}
	if doc.SubjectID == "" {
		return "", fmt.Errorf("outcome: %s has no subject id", judgementPath)
	}

	value, err := json.Marshal(outcomeDoc{State: o.State, When: o.When.UTC().Format(recordDocTimeFormat), Note: o.Note})
	if err != nil {
		return "", fmt.Errorf("outcome: encode: %w", err)
	}
	updated, err := spliceKey(body, "outcome", value)
	if err != nil {
		return "", fmt.Errorf("outcome: %s: %w", judgementPath, err)
	}
	fields, when, err := currentRow(ctx, index, judgementPath, doc)
	if err != nil {
		return "", err
	}
	fields["outcome"] = o.State

	rev, err := RecordDocument(ctx, docs, index, Document{
		Path:    judgementPath,
		Body:    updated,
		Message: "Attach outcome for " + doc.SubjectID,
		Kind:    "judgement",
		Fields:  fields,
		When:    when,
	})
	if err != nil {
		return rev, fmt.Errorf("outcome: %w", err)
	}
	return rev, nil
}

// currentRow returns a copy of the fields and the time of path's index row.
// When the index has no row for path, it returns the fields a judgement is
// recorded with, read from its document.
func currentRow(ctx context.Context, index ports.Index, path string, doc judgementDoc) (map[string]string, time.Time, error) {
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": doc.SubjectID}})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("outcome: find %s: %w", path, err)
	}
	for _, r := range rows {
		if r.Path == path {
			fields := maps.Clone(r.Fields)
			if fields == nil {
				fields = map[string]string{}
			}
			return fields, r.When, nil
		}
	}
	when, err := time.Parse(recordDocTimeFormat, doc.When)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("outcome: %s: when %q: %w", path, doc.When, err)
	}
	ids := make([]string, len(doc.Questions))
	for i, q := range doc.Questions {
		ids[i] = q.ID
	}
	return map[string]string{
		"subject_id": doc.SubjectID,
		"model":      doc.Model,
		"provider":   doc.Provider,
		"questions":  strings.Join(ids, ","),
	}, when, nil
}

// spliceKey returns the JSON object body with key's value replaced by value.
// Every other key keeps its value and its place, and the object is indented
// two spaces, the way judgement documents are written. It fails when body is
// not an object or has no such key.
func spliceKey(body []byte, key string, value json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out bytes.Buffer
	out.WriteString("{")
	found := false
	for i := 0; dec.More(); i++ {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if k == key {
			raw, found = value, true
		}
		name, _ := json.Marshal(k)
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString("\n  ")
		out.Write(name)
		out.WriteString(": ")
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, err
		}
		if err := json.Indent(&out, compact.Bytes(), "  ", "  "); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("no %q key", key)
	}
	out.WriteString("\n}")
	return out.Bytes(), nil
}
```

- [ ] **Step 4: Run to verify pass, then prove two guards by mutation**

Run: `go test ./internal/core/app/ -race -run 'Attach|Correction' -v`. Expected: PASS.

For each mutation below, confirm with `grep` that it applied, see the named test fail, then
revert. Paste the output of each run.
- In `AttachOutcome`, replace the `spliceKey` call with a round trip through `judgementDoc`:
  set `doc.Outcome = outcomeDoc{...}`, then `updated, err = json.MarshalIndent(doc, "", "  ")`.
  Expected: `TestAttachingKeepsEveryOtherKeyAndFieldAsItWas` fails, because `fingerprint` is
  lost.
- In `currentRow`, return fresh fields instead of `maps.Clone(r.Fields)`. Expected: the same test
  fails, because `verdict` is lost.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/outcome.go internal/core/app/outcome_test.go
git commit -m "Attach an outcome as a later revision of the judgement it resolves"
```

---

### Task 2: `AttachOutcomeForSubject` — every judgement of one subject

**Files:**
- Modify: `internal/core/app/outcome.go`, `internal/core/app/outcome_test.go`

**Interfaces:**
- Consumes: `AttachOutcome`, plus the `record`, `judged`, `wet`, `day` and `row` test helpers
  from Task 1; the existing `checkSubjectID`.
- Produces: `func AttachOutcomeForSubject(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, o Outcome) ([]string, error)`,
  which returns the paths attached to, sorted.

- [ ] **Step 1: Write the failing tests**

Append to `internal/core/app/outcome_test.go`:

```go
func TestAnOutcomeForASubjectReachesEveryJudgementOfIt(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	first := judged(t, docs, index, "harbour-fete", 0.7, day)
	second := judged(t, docs, index, "harbour-fete", 0.4, day.Add(time.Hour))
	other := judged(t, docs, index, "hill-race", 0.2, day)

	paths, err := app.AttachOutcomeForSubject(ctx, docs, index, "harbour-fete", wet())
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(paths) != 2 || paths[0] != first || paths[1] != second {
		t.Errorf("paths = %v, want [%s %s]", paths, first, second)
	}
	for _, p := range []string{first, second} {
		if r := row(t, index, "harbour-fete", p); r.Fields["outcome"] != "wet" {
			t.Errorf("%s outcome = %q", p, r.Fields["outcome"])
		}
	}
	if r := row(t, index, "hill-race", other); r.Fields["outcome"] != "pending" {
		t.Errorf("another subject's judgement changed: %q", r.Fields["outcome"])
	}
}

func TestAnOutcomeForASubjectNobodyJudgedIsAnError(t *testing.T) {
	docs, index := record(t)
	for _, subject := range []string{"", "nobody", "../escape", "a/b"} {
		if _, err := app.AttachOutcomeForSubject(context.Background(), docs, index, subject, wet()); err == nil || !strings.HasPrefix(err.Error(), "outcome: ") {
			t.Errorf("subject %q: err = %v", subject, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/core/app/ -run ForASubject`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Append to `internal/core/app/outcome.go` (add `"slices"` to its imports):

```go
// AttachOutcomeForSubject attaches o to every judgement recorded for
// subjectID, since the real-world result belongs to the subject rather than
// to one judge call. It returns the paths attached to, in order. A subject
// with no recorded judgement is an error.
func AttachOutcomeForSubject(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, o Outcome) ([]string, error) {
	if subjectID == "" {
		return nil, errors.New("outcome: subject id is empty")
	}
	if err := checkSubjectID(subjectID); err != nil {
		return nil, fmt.Errorf("outcome: %w", err)
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": subjectID}})
	if err != nil {
		return nil, fmt.Errorf("outcome: find %s: %w", subjectID, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("outcome: no judgement recorded for %q", subjectID)
	}
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	slices.Sort(paths)
	for i, p := range paths {
		if _, err := AttachOutcome(ctx, docs, index, p, o); err != nil {
			return paths[:i], err
		}
	}
	return paths, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/core/app/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/outcome.go internal/core/app/outcome_test.go
git commit -m "Attach an outcome to every judgement of a subject"
```

---

### Task 3: Scoring maths — Brier score and reliability bins (11.4)

**Files:**
- Create: `internal/core/app/calibrate.go`, `internal/core/app/calibrate_internal_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (Task 4 extends the file):

```go
const (
	MinSample    = 30
	MinBinSample = 10
)
type Calibration struct {
	N        int
	Excluded ExclusionCounts
	Brier    *float64
	Bins     []ReliabilityBin
}
type ExclusionCounts struct {
	Pending, ZeroCoverage, Unclassified, OptionMismatch int
}
type ReliabilityBin struct {
	Low, High     float64
	N             int
	MeanPredicted float64
	ObservedRate  *float64
}
type scoredPoint struct {
	predicted float64
	happened  bool
}
func score(points []scoredPoint) Calibration // sets N, Brier and Bins; leaves Excluded zero
```

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/calibrate_internal_test.go` (package `app`, because `score` is
unexported):

```go
package app

import (
	"math"
	"testing"
)

func repeat(n int, p float64, happened bool) []scoredPoint {
	out := make([]scoredPoint, n)
	for i := range out {
		out[i] = scoredPoint{predicted: p, happened: happened}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTheBrierScoreIsTheMeanSquaredErrorOfTheProbabilities(t *testing.T) {
	// 20 at 0.8 that happened, 10 at 0.8 that did not, 5 at 0.2 that did not:
	// (20*0.04 + 10*0.64 + 5*0.04) / 35 = 7.4/35.
	points := append(append(repeat(20, 0.8, true), repeat(10, 0.8, false)...), repeat(5, 0.2, false)...)
	c := score(points)
	if c.N != 35 {
		t.Fatalf("N = %d", c.N)
	}
	if c.Brier == nil || !near(*c.Brier, 7.4/35) {
		t.Errorf("Brier = %v, want %v", c.Brier, 7.4/35)
	}
}

func TestBelowThirtyPointsThereIsNoBrierScore(t *testing.T) {
	c := score(repeat(MinSample-1, 0.8, true))
	if c.N != MinSample-1 || c.Brier != nil {
		t.Errorf("N = %d Brier = %v; below %d the count is reported and the number is not", c.N, c.Brier, MinSample)
	}
	if score(repeat(MinSample, 0.8, true)).Brier == nil {
		t.Error("exactly the minimum sample has no Brier score")
	}
}

func TestBinsAreFixedDecilesAndReadOnlyWithTenPoints(t *testing.T) {
	// The 0.8 bin: 12 points, 7 happened -> 7/12. The 0.3 bin: 9 points -> too few.
	points := append(append(repeat(7, 0.83, true), repeat(5, 0.81, false)...), repeat(9, 0.3, true)...)
	c := score(points)
	if len(c.Bins) != 10 {
		t.Fatalf("bins = %d, want 10", len(c.Bins))
	}
	for i, b := range c.Bins {
		if !near(b.Low, float64(i)/10) || !near(b.High, float64(i+1)/10) {
			t.Errorf("bin %d = [%v,%v)", i, b.Low, b.High)
		}
	}
	eight := c.Bins[8]
	if eight.N != 12 || eight.ObservedRate == nil || !near(*eight.ObservedRate, 7.0/12) || !near(eight.MeanPredicted, (7*0.83+5*0.81)/12) {
		t.Errorf("0.8 bin = %+v", eight)
	}
	three := c.Bins[3]
	if three.N != 9 || three.ObservedRate != nil {
		t.Errorf("0.3 bin = %+v; nine points must not print a rate", three)
	}
}

func TestTheEdgesOfTheProbabilityRangeLandInTheEndBins(t *testing.T) {
	c := score([]scoredPoint{{0.0, false}, {1.0, true}, {0.1, false}, {0.9999, true}})
	if c.Bins[0].N != 1 || c.Bins[9].N != 2 || c.Bins[1].N != 1 {
		t.Errorf("bin counts = %d %d %d; 0.0 in the first, 1.0 and 0.9999 in the last, 0.1 in the second",
			c.Bins[0].N, c.Bins[9].N, c.Bins[1].N)
	}
}

func TestNoPointsIsAnEmptyReportNotAnError(t *testing.T) {
	c := score(nil)
	if c.N != 0 || c.Brier != nil || len(c.Bins) != 10 {
		t.Errorf("empty = %+v", c)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/core/app/ -run 'Brier|Bins|Edges|NoPoints'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Create `internal/core/app/calibrate.go`:

```go
package app

// MinSample is the fewest scored points a headline Brier score is reported
// for, and MinBinSample the fewest one reliability row is: below them the
// standard error of an observed proportion is too wide for the number to
// mean anything, so only the count is reported.
const (
	MinSample    = 30
	MinBinSample = 10
)

// Calibration is how a set of predicted probabilities fared against what
// happened. Brier is nil below MinSample; each bin's ObservedRate is nil
// below MinBinSample.
type Calibration struct {
	N        int
	Excluded ExclusionCounts
	Brier    *float64
	Bins     []ReliabilityBin
}

// ExclusionCounts is how many judgements were left out of a sample, and why.
type ExclusionCounts struct {
	Pending        int // no outcome attached yet
	ZeroCoverage   int // the scored answer's alternatives named none of its options
	Unclassified   int // an outcome the prediction names neither positive nor negative
	OptionMismatch int // a predicted option the judgement's question does not declare
}

// ReliabilityBin is one fixed-width decile of predicted probability: how
// many points fell in it, their mean prediction, and how often the
// predicted event happened.
type ReliabilityBin struct {
	Low, High     float64
	N             int
	MeanPredicted float64
	ObservedRate  *float64
}

// scoredPoint is one resolved prediction: the probability given, and
// whether the event happened.
type scoredPoint struct {
	predicted float64
	happened  bool
}

// score computes the Brier score and the ten reliability bins of points.
// The last bin is closed, so a probability of exactly 1 falls in it.
func score(points []scoredPoint) Calibration {
	c := Calibration{N: len(points), Bins: make([]ReliabilityBin, 10)}
	var sumSq float64
	hits := make([]int, 10)
	sums := make([]float64, 10)
	for _, p := range points {
		sumSq += (p.predicted - outcomeValue(p.happened)) * (p.predicted - outcomeValue(p.happened))
		i := min(max(int(p.predicted*10), 0), 9)
		c.Bins[i].N++
		sums[i] += p.predicted
		if p.happened {
			hits[i]++
		}
	}
	if c.N >= MinSample {
		brier := sumSq / float64(c.N)
		c.Brier = &brier
	}
	for i := range c.Bins {
		b := &c.Bins[i]
		b.Low, b.High = float64(i)/10, float64(i+1)/10
		if b.N > 0 {
			b.MeanPredicted = sums[i] / float64(b.N)
		}
		if b.N >= MinBinSample {
			rate := float64(hits[i]) / float64(b.N)
			b.ObservedRate = &rate
		}
	}
	return c
}

func outcomeValue(happened bool) float64 {
	if happened {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/core/app/ -race -run 'Brier|Bins|Edges|NoPoints' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/calibrate.go internal/core/app/calibrate_internal_test.go
git commit -m "Score predictions with a Brier score and decile reliability bins"
```

---

### Task 4: `Calibrate` over the record, with its exclusions (11.4)

**Files:**
- Modify: `internal/core/app/calibrate.go`
- Create: `internal/core/app/calibrate_test.go`

**Interfaces:**
- Consumes:
  - from Task 3: `score`, `scoredPoint`, `Calibration`, `ExclusionCounts`;
  - from Task 1: `AttachOutcome`, `Outcome`, `outcomeDoc`, plus the `record`, `judged`,
    `forecast` and `day` test helpers;
  - existing: `judgementQuestion`, `judgementAnswer`, `ports.NoulOptions`.
- Produces:

```go
type Prediction struct {
	QuestionID string
	Options    []string // answer options whose summed mass is the predicted probability
	Positive   []string // outcome states that count as the prediction coming true
	Negative   []string // outcome states that count as it coming false
}
type CalibrateOptions struct{ Provider, Model string }
type EngineCalibration struct {
	Provider, Model string
	Calibration
}
func Calibrate(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction, opts CalibrateOptions) (Calibration, error)
func CalibrateByEngine(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction) (Calibration, []EngineCalibration, error)
```

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/calibrate_test.go`:

```go
package app_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func rainPrediction() app.Prediction {
	return app.Prediction{QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry"}}
}

// resolved records a judgement with probability p and attaches state.
func resolved(t *testing.T, docs ports.Docs, index ports.Index, subject string, p float64, state string) {
	t.Helper()
	path := judged(t, docs, index, subject, p, day)
	if state == "" {
		return
	}
	if _, err := app.AttachOutcome(context.Background(), docs, index, path, app.Outcome{State: state, When: day}); err != nil {
		t.Fatalf("attach: %v", err)
	}
}

func TestCalibrateScoresResolvedJudgementsAndCountsTheRest(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range 25 {
		resolved(t, docs, index, fmt.Sprintf("wet-%d", i), 0.8, "wet")
	}
	for i := range 10 {
		resolved(t, docs, index, fmt.Sprintf("dry-%d", i), 0.8, "dry")
	}
	resolved(t, docs, index, "pending", 0.8, "")
	resolved(t, docs, index, "foggy", 0.8, "fog")

	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if c.N != 35 {
		t.Errorf("N = %d, want 35", c.N)
	}
	if c.Excluded.Pending != 1 || c.Excluded.Unclassified != 1 {
		t.Errorf("excluded = %+v", c.Excluded)
	}
	want := (25*0.04 + 10*0.64) / 35
	if c.Brier == nil || fmt.Sprintf("%.6f", *c.Brier) != fmt.Sprintf("%.6f", want) {
		t.Errorf("Brier = %v, want %v", c.Brier, want)
	}
	if r := c.Bins[8].ObservedRate; r == nil || fmt.Sprintf("%.4f", *r) != fmt.Sprintf("%.4f", 25.0/35) {
		t.Errorf("0.8 bin rate = %v, want 25/35", r)
	}
}

func TestAZeroCoverageAnswerIsExcludedEvenWhenItWasRight(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	j := forecast("blind", 1.0, day)
	j.Answers[0].Coverage = ports.Coverage{Represented: 0, Declared: 2}
	path, err := app.RecordJudgement(ctx, docs, index, "blind", rainQuestion(), j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
		t.Fatal(err)
	}
	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.N != 0 || c.Excluded.ZeroCoverage != 1 {
		t.Errorf("N = %d excluded = %+v; a probability that measured nothing is not scored", c.N, c.Excluded)
	}
}

func TestAMisspeltOptionExcludesAndCountsRatherThanScoringZero(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	resolved(t, docs, index, "one", 0.9, "wet")
	p := rainPrediction()
	p.Options = []string{"yse"}
	c, err := app.Calibrate(ctx, docs, index, p, app.CalibrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.N != 0 || c.Excluded.OptionMismatch != 1 {
		t.Errorf("N = %d excluded = %+v", c.N, c.Excluded)
	}
}

func TestAPredictionThatCannotMeanAnythingIsRefused(t *testing.T) {
	docs, index := record(t)
	for name, p := range map[string]app.Prediction{
		"no question": {Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry"}},
		"no options":  {QuestionID: "rain", Positive: []string{"wet"}, Negative: []string{"dry"}},
		"no positive": {QuestionID: "rain", Options: []string{"yes"}, Negative: []string{"dry"}},
		"no negative": {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}},
		"overlap":     {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry", "wet"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.Calibrate(context.Background(), docs, index, p, app.CalibrateOptions{}); err == nil || !strings.HasPrefix(err.Error(), "calibrate: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestAnEmptyRecordIsAnEmptyReport(t *testing.T) {
	docs, index := record(t)
	c, err := app.Calibrate(context.Background(), docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil || c.N != 0 || c.Brier != nil || len(c.Bins) != 10 {
		t.Errorf("c = %+v err = %v", c, err)
	}
}

func TestAJudgementThatNeverAskedTheQuestionIsNotInTheSample(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	q := []ports.Question{{ID: "wind", Kind: ports.KindNoul, Ask: "Will it be windy?"}}
	j := forecast("breezy", 0.5, day)
	j.Answers[0].ID = "wind"
	path, err := app.RecordJudgement(ctx, docs, index, "breezy", q, j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
		t.Fatal(err)
	}
	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil || c.N != 0 || c.Excluded != (app.ExclusionCounts{}) {
		t.Errorf("c = %+v err = %v", c, err)
	}
}

func TestCalibrationIsReportedPerEngineAndPooled(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i, engine := range []struct{ provider, model string }{{"local", "small"}, {"local", "small"}, {"remote", "large"}} {
		j := forecast(fmt.Sprintf("s-%d", i), 0.7, day.Add(time.Duration(i)*time.Minute))
		j.Provider, j.Model = engine.provider, engine.model
		path, err := app.RecordJudgement(ctx, docs, index, fmt.Sprintf("s-%d", i), rainQuestion(), j)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
			t.Fatal(err)
		}
	}
	pooled, byEngine, err := app.CalibrateByEngine(ctx, docs, index, rainPrediction())
	if err != nil {
		t.Fatal(err)
	}
	if pooled.N != 3 || len(byEngine) != 2 {
		t.Fatalf("pooled N = %d engines = %d", pooled.N, len(byEngine))
	}
	if byEngine[0].Provider != "local" || byEngine[0].Model != "small" || byEngine[0].N != 2 || byEngine[1].N != 1 {
		t.Errorf("by engine = %+v", byEngine)
	}
	one, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{Provider: "remote"})
	if err != nil || one.N != 1 {
		t.Errorf("remote only: N = %d err = %v", one.N, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/core/app/ -run 'Calibrat|ZeroCoverage|Misspelt|Prediction|EmptyRecord|NeverAsked'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Append to `internal/core/app/calibrate.go`. Add the imports `context`, `encoding/json`, `errors`,
`fmt`, `slices`, `strings`, and `github.com/tunedev/atlas/internal/core/ports`.

```go
// Prediction names what a calibration run scores. QuestionID is the
// recorded answer that predicts a real-world result; the summed mass of its
// Options is the predicted probability of a Positive outcome. Positive and
// Negative are outcome states a pack declares; an outcome in neither is
// left out and counted, never scored as a miss.
type Prediction struct {
	QuestionID string
	Options    []string
	Positive   []string
	Negative   []string
}

// CalibrateOptions narrows a run to one provider or one model; empty
// pools them.
type CalibrateOptions struct {
	Provider string
	Model    string
}

// EngineCalibration is a Calibration for one provider and model.
type EngineCalibration struct {
	Provider, Model string
	Calibration
}

func (p Prediction) validate() error {
	switch {
	case p.QuestionID == "":
		return errors.New("calibrate: no question id")
	case len(p.Options) == 0:
		return errors.New("calibrate: no predicted options")
	case len(p.Positive) == 0 || len(p.Negative) == 0:
		return errors.New("calibrate: positive and negative outcome states are both needed")
	}
	for _, s := range p.Positive {
		if slices.Contains(p.Negative, s) {
			return fmt.Errorf("calibrate: %q is both positive and negative", s)
		}
	}
	return nil
}

// Calibrate scores every judgement that asked p.QuestionID and has an
// outcome p classifies, against the mass it gave p.Options.
func Calibrate(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction, opts CalibrateOptions) (Calibration, error) {
	if err := p.validate(); err != nil {
		return Calibration{}, err
	}
	match := map[string]string{}
	if opts.Provider != "" {
		match["provider"] = opts.Provider
	}
	if opts.Model != "" {
		match["model"] = opts.Model
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: match})
	if err != nil {
		return Calibration{}, fmt.Errorf("calibrate: find: %w", err)
	}
	return calibrateRows(ctx, docs, rows, p)
}

// CalibrateByEngine scores p over every engine pooled, and over each
// provider and model on its own, ordered by provider then model.
func CalibrateByEngine(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction) (Calibration, []EngineCalibration, error) {
	if err := p.validate(); err != nil {
		return Calibration{}, nil, err
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement"})
	if err != nil {
		return Calibration{}, nil, fmt.Errorf("calibrate: find: %w", err)
	}
	pooled, err := calibrateRows(ctx, docs, rows, p)
	if err != nil {
		return Calibration{}, nil, err
	}
	groups := map[[2]string][]ports.Record{}
	for _, r := range rows {
		key := [2]string{r.Fields["provider"], r.Fields["model"]}
		groups[key] = append(groups[key], r)
	}
	keys := make([][2]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	var byEngine []EngineCalibration
	for _, k := range keys {
		c, err := calibrateRows(ctx, docs, groups[k], p)
		if err != nil {
			return Calibration{}, nil, err
		}
		byEngine = append(byEngine, EngineCalibration{Provider: k[0], Model: k[1], Calibration: c})
	}
	return pooled, byEngine, nil
}

// scoredDoc is the part of a judgement document a calibration reads.
type scoredDoc struct {
	Questions []judgementQuestion `json:"questions"`
	Answers   []judgementAnswer   `json:"answers"`
	Outcome   *outcomeDoc         `json:"outcome"`
}

func calibrateRows(ctx context.Context, docs ports.Docs, rows []ports.Record, p Prediction) (Calibration, error) {
	var points []scoredPoint
	var excluded ExclusionCounts
	for _, r := range rows {
		if !slices.Contains(strings.Split(r.Fields["questions"], ","), p.QuestionID) {
			continue
		}
		body, err := docs.Get(ctx, r.Path)
		if err != nil {
			return Calibration{}, fmt.Errorf("calibrate: read %s: %w", r.Path, err)
		}
		var doc scoredDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return Calibration{}, fmt.Errorf("calibrate: decode %s: %w", r.Path, err)
		}
		pt, reason, err := p.point(r.Path, doc)
		if err != nil {
			return Calibration{}, err
		}
		switch reason {
		case "":
			points = append(points, pt)
		case "pending":
			excluded.Pending++
		case "option_mismatch":
			excluded.OptionMismatch++
		case "zero_coverage":
			excluded.ZeroCoverage++
		case "unclassified":
			excluded.Unclassified++
		}
	}
	c := score(points)
	c.Excluded = excluded
	return c, nil
}

// point reads one judgement document as a scored point, or names why it is
// left out. A document that lists the question but has no answer for it is
// an error: the record contradicts itself.
func (p Prediction) point(path string, doc scoredDoc) (scoredPoint, string, error) {
	if doc.Outcome == nil {
		return scoredPoint{}, "pending", nil
	}
	i := slices.IndexFunc(doc.Answers, func(a judgementAnswer) bool { return a.ID == p.QuestionID })
	if i < 0 {
		return scoredPoint{}, "", fmt.Errorf("calibrate: %s has no answer %q", path, p.QuestionID)
	}
	answer := doc.Answers[i]
	if !p.declared(doc, answer) {
		return scoredPoint{}, "option_mismatch", nil
	}
	if answer.Coverage.Represented == 0 {
		return scoredPoint{}, "zero_coverage", nil
	}
	var happened bool
	switch {
	case slices.Contains(p.Positive, doc.Outcome.State):
		happened = true
	case slices.Contains(p.Negative, doc.Outcome.State):
		happened = false
	default:
		return scoredPoint{}, "unclassified", nil
	}
	var mass float64
	for _, o := range p.Options {
		mass += answer.Distribution[o]
	}
	return scoredPoint{predicted: mass, happened: happened}, "", nil
}

// declared reports whether the question answer came from declares every
// predicted option. A noul with no options of its own declares yes and no.
func (p Prediction) declared(doc scoredDoc, answer judgementAnswer) bool {
	var options []string
	for _, q := range doc.Questions {
		if q.ID == answer.ID {
			options = q.Options
			if len(options) == 0 && q.Kind == string(ports.KindNoul) {
				options = ports.NoulOptions()
			}
		}
	}
	for _, o := range p.Options {
		if !slices.Contains(options, o) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run to verify pass, then prove the coverage exclusion by mutation**

Run: `go test ./internal/core/app/ -race -v -run 'Calibrat|ZeroCoverage|Misspelt|Prediction|EmptyRecord|NeverAsked'`.
Expected: PASS.

Then delete the `answer.Coverage.Represented == 0` check, confirm with `grep` that the mutation
applied, and see `TestAZeroCoverageAnswerIsExcludedEvenWhenItWasRight` fail. Revert.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/calibrate.go internal/core/app/calibrate_test.go
git commit -m "Score recorded predictions against attached outcomes, counting every exclusion"
```

---

### Task 5: The agreement rate

**Files:**
- Create: `internal/core/app/agreement.go`, `internal/core/app/agreement_test.go`

**Interfaces:**
- Consumes: `RecordDecision`, `Decision` (existing), `MinSample` (Task 3), and the `record` and
  `day` test helpers (Task 1).
- Produces:

```go
type Agreement struct {
	N, Agreed int
	Rate      *float64
}
func AgreementRate(ctx context.Context, index ports.Index) (Agreement, error)
```

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/agreement_test.go`:

```go
package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestTheAgreementRateCountsDecisionsThatMatchedTheVerdict(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range 40 {
		verdict, choice := "yes", "yes"
		if i < 12 {
			choice = "no"
		}
		d := app.Decision{SubjectID: fmt.Sprintf("s-%d", i), Choice: choice, VerdictAtDecision: verdict, When: day.Add(time.Duration(i) * time.Minute)}
		if _, err := app.RecordDecision(ctx, docs, index, d); err != nil {
			t.Fatal(err)
		}
	}
	// A decision with no judgement before it is not a comparison.
	if _, err := app.RecordDecision(ctx, docs, index, app.Decision{SubjectID: "manual", Choice: "yes", When: day}); err != nil {
		t.Fatal(err)
	}
	a, err := app.AgreementRate(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	if a.N != 40 || a.Agreed != 28 || a.Rate == nil || *a.Rate != 28.0/40 {
		t.Errorf("agreement = %+v", a)
	}
}

func TestBelowThirtyDecisionsThereIsNoAgreementRate(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range app.MinSample - 1 {
		d := app.Decision{SubjectID: fmt.Sprintf("s-%d", i), Choice: "yes", VerdictAtDecision: "yes", When: day.Add(time.Duration(i) * time.Minute)}
		if _, err := app.RecordDecision(ctx, docs, index, d); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.AgreementRate(ctx, index)
	if err != nil || a.N != app.MinSample-1 || a.Rate != nil {
		t.Errorf("agreement = %+v err = %v", a, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/core/app/ -run Agreement`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Create `internal/core/app/agreement.go`:

```go
package app

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Agreement is how often a recorded decision matched the verdict the model
// had given when the person decided. Rate is nil below MinSample.
type Agreement struct {
	N, Agreed int
	Rate      *float64
}

// AgreementRate counts the decisions that followed a judgement and how many
// of them chose what the verdict said. The two are compared as strings, so
// the rate means something only when a pack's choices and its verdict
// question share option names.
func AgreementRate(ctx context.Context, index ports.Index) (Agreement, error) {
	rows, err := index.Find(ctx, ports.Query{Kind: "decision"})
	if err != nil {
		return Agreement{}, fmt.Errorf("agreement: find: %w", err)
	}
	var a Agreement
	for _, r := range rows {
		verdict := r.Fields["verdict_at_decision"]
		if verdict == "" {
			continue
		}
		a.N++
		if r.Fields["decision"] == verdict {
			a.Agreed++
		}
	}
	if a.N >= MinSample {
		rate := float64(a.Agreed) / float64(a.N)
		a.Rate = &rate
	}
	return a, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/core/app/ -race -run Agreement -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/agreement.go internal/core/app/agreement_test.go
git commit -m "Count how often a decision agreed with the verdict before it"
```

---

### Task 6: The three tools, registered

**Files:**
- Create: `internal/adapters/outbound/tools/outcome.go`, `calibrate.go`, `agreement.go`,
  `outcome_test.go`
- Modify: `cmd/atlas/main.go`, `cmd/atlas/main_test.go`

**Interfaces:**
- Consumes:
  - from Tasks 1–5: `app.AttachOutcome`, `app.AttachOutcomeForSubject`, `app.Outcome`,
    `app.CalibrateByEngine`, `app.Prediction`, `app.Calibration`, `app.EngineCalibration`,
    `app.MinSample`, `app.MinBinSample`, `app.AgreementRate`;
  - the existing `store(t)` test helper (real `gitdocs` and `sqlindex`) in
    `tools/judge_test.go`.
- Produces three tools:
  - **`judge.outcome`** (`tools.NewOutcome(docs, index)`).
    - Takes `with`: exactly one of `judgement_path` or `subject_id`; `state`; `when` (optional:
      RFC 3339 or `2006-01-02`; default now, UTC); and `note`.
    - Returns `{"attached": [paths], "state": state}`.
  - **`judge.calibrate`** (`tools.NewCalibrate(docs, index)`).
    - Takes `with`: `question`, plus `options`, `positive` and `negative` as comma lists.
    - Returns `{"prediction": {...}, "pooled": <report>, "by_engine": [<report> + provider and model]}`.
    - A report is `{"n", "excluded": {"pending", "zero_coverage", "unclassified", "option_mismatch"}, "brier" (null below MinSample), "headline", "bins": [{"low", "high", "n", "mean_predicted", "observed_rate" (null below MinBinSample), "reads"}]}`.
  - **`decision.agreement`** (`tools.NewAgreement(index)`) takes no `with` and returns
    `{"n", "agreed", "rate" (null below MinSample), "headline"}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/outbound/tools/outcome_test.go`:

```go
package tools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func weatherJudgement(subject string, p float64, when time.Time) ports.Judgement {
	return ports.Judgement{
		Subject: subject, Model: "m", Provider: "prov", When: when,
		Answers: []ports.Answer{{ID: "rain", Kind: ports.KindNoul, Chosen: "yes",
			Distribution: map[string]float64{"yes": p, "no": 1 - p},
			Coverage:     ports.Coverage{Represented: 2, Declared: 2}}},
	}
}

var rainQ = []ports.Question{{ID: "rain", Kind: ports.KindNoul, Ask: "Will it rain?"}}

// TestTheLoopClosesThroughTheTools records forecasts, attaches outcomes
// through judge.outcome, and reads them back through judge.calibrate.
func TestTheLoopClosesThroughTheTools(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	base := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	for i := range 32 {
		subject := fmt.Sprintf("fete-%d", i)
		if _, err := app.RecordJudgement(ctx, docs, index, subject, rainQ, weatherJudgement(subject, 0.8, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
		state := "wet"
		if i%4 == 0 {
			state = "dry"
		}
		out, err := tools.NewOutcome(docs, index).Invoke(ctx, map[string]string{"subject_id": subject, "state": state, "when": "2026-09-29"})
		if err != nil {
			t.Fatalf("judge.outcome: %v", err)
		}
		if got := out.(map[string]any)["attached"].([]string); len(got) != 1 {
			t.Fatalf("attached = %v", got)
		}
	}

	out, err := tools.NewCalibrate(docs, index).Invoke(ctx, map[string]string{
		"question": "rain", "options": "yes", "positive": "wet", "negative": "dry",
	})
	if err != nil {
		t.Fatalf("judge.calibrate: %v", err)
	}
	pooled := out.(map[string]any)["pooled"].(map[string]any)
	if pooled["n"] != 32 || pooled["brier"] == nil {
		t.Errorf("pooled = %v", pooled)
	}
	if h, _ := pooled["headline"].(string); !strings.Contains(h, "Brier") || !strings.Contains(h, "n=32") {
		t.Errorf("headline = %q", h)
	}
	bin := pooled["bins"].([]any)[8].(map[string]any)
	if reads, _ := bin["reads"].(string); !strings.Contains(reads, "80% judgements came true 75% of the time") {
		t.Errorf("0.8 bin reads %q", reads)
	}
	engines := out.(map[string]any)["by_engine"].([]any)
	if len(engines) != 1 || engines[0].(map[string]any)["provider"] != "prov" {
		t.Errorf("by_engine = %v", engines)
	}
}

func TestAReportTooSmallToScoreSaysSo(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	out, err := tools.NewCalibrate(docs, index).Invoke(ctx, map[string]string{
		"question": "rain", "options": "yes", "positive": "wet", "negative": "dry",
	})
	if err != nil {
		t.Fatal(err)
	}
	pooled := out.(map[string]any)["pooled"].(map[string]any)
	if pooled["brier"] != nil || !strings.Contains(pooled["headline"].(string), "too few") {
		t.Errorf("pooled = %v", pooled)
	}
}

func TestTheOutcomeToolRefusesAmbiguousOrIncompleteInput(t *testing.T) {
	docs, index := store(t)
	for name, with := range map[string]map[string]string{
		"neither target": {"state": "wet"},
		"both targets":   {"subject_id": "a", "judgement_path": "judgements/a/x.json", "state": "wet"},
		"no state":       {"subject_id": "a"},
		"bad when":       {"subject_id": "a", "state": "wet", "when": "last tuesday"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tools.NewOutcome(docs, index).Invoke(context.Background(), with)
			if err == nil || !strings.HasPrefix(err.Error(), "judge.outcome: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestTheAgreementToolReportsACount(t *testing.T) {
	docs, index := store(t)
	ctx := context.Background()
	if _, err := app.RecordDecision(ctx, docs, index, app.Decision{SubjectID: "a", Choice: "yes", VerdictAtDecision: "no", When: time.Now()}); err != nil {
		t.Fatal(err)
	}
	out, err := tools.NewAgreement(index).Invoke(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["n"] != 1 || m["agreed"] != 0 || m["rate"] != nil || !strings.Contains(m["headline"].(string), "too few") {
		t.Errorf("agreement = %v", m)
	}
}
```

In `cmd/atlas/main_test.go`, extend the registry name list with `"judge.outcome"`,
`"judge.calibrate"` and `"decision.agreement"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/adapters/outbound/tools/ -run 'Loop|TooSmall|OutcomeTool|AgreementTool'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Create `internal/adapters/outbound/tools/outcome.go`:

```go
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

	attached := []string{path}
	if subject != "" {
		attached, err = app.AttachOutcomeForSubject(ctx, t.docs, t.index, subject, o)
	} else {
		_, err = app.AttachOutcome(ctx, t.docs, t.index, path, o)
	}
	if err != nil {
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
```

Create `internal/adapters/outbound/tools/calibrate.go`:

```go
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Calibrate reports how recorded predictions fared against attached
// outcomes, pooled and per engine, in words a person can read aloud.
type Calibrate struct {
	docs  ports.Docs
	index ports.Index
}

func NewCalibrate(docs ports.Docs, index ports.Index) *Calibrate {
	return &Calibrate{docs: docs, index: index}
}

func (t *Calibrate) Name() string { return "judge.calibrate" }

func (t *Calibrate) Invoke(ctx context.Context, with map[string]string) (any, error) {
	p := app.Prediction{
		QuestionID: strings.TrimSpace(with["question"]),
		Options:    list(with["options"]),
		Positive:   list(with["positive"]),
		Negative:   list(with["negative"]),
	}
	pooled, byEngine, err := app.CalibrateByEngine(ctx, t.docs, t.index, p)
	if err != nil {
		return nil, fmt.Errorf("judge.calibrate: %w", err)
	}
	engines := make([]any, len(byEngine))
	for i, e := range byEngine {
		r := report(e.Calibration)
		r["provider"], r["model"] = e.Provider, e.Model
		engines[i] = r
	}
	return map[string]any{
		"prediction": map[string]any{"question": p.QuestionID, "options": p.Options, "positive": p.Positive, "negative": p.Negative},
		"pooled":     report(pooled),
		"by_engine":  engines,
	}, nil
}

// report renders a Calibration, saying "too few" wherever the core
// withheld a number.
func report(c app.Calibration) map[string]any {
	headline := fmt.Sprintf("n=%d, too few for a Brier score (needs %d)", c.N, app.MinSample)
	var brier any
	if c.Brier != nil {
		brier = *c.Brier
		headline = fmt.Sprintf("Brier %.3f over n=%d", *c.Brier, c.N)
	}
	bins := make([]any, len(c.Bins))
	for i, b := range c.Bins {
		reads := fmt.Sprintf("n=%d, too few (needs %d)", b.N, app.MinBinSample)
		var rate any
		if b.ObservedRate != nil {
			rate = *b.ObservedRate
			reads = fmt.Sprintf("your %.0f%% judgements came true %.0f%% of the time (n=%d)", b.Low*100, *b.ObservedRate*100, b.N)
		}
		bins[i] = map[string]any{"low": b.Low, "high": b.High, "n": b.N, "mean_predicted": b.MeanPredicted, "observed_rate": rate, "reads": reads}
	}
	return map[string]any{
		"n": c.N,
		"excluded": map[string]any{
			"pending":         c.Excluded.Pending,
			"zero_coverage":   c.Excluded.ZeroCoverage,
			"unclassified":    c.Excluded.Unclassified,
			"option_mismatch": c.Excluded.OptionMismatch,
		},
		"brier":    brier,
		"headline": headline,
		"bins":     bins,
	}
}

// list splits a comma list, dropping blanks.
func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
```

If `list` or `report` collides with an existing identifier in package `tools`, check with
`grep -n "func list\|func report" internal/adapters/outbound/tools/*.go`. If one does, rename
the new function `commaList` or `calibrationReport` and say so in the report.

Create `internal/adapters/outbound/tools/agreement.go`:

```go
package tools

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Agreement reports how often a decision agreed with the verdict before it.
type Agreement struct {
	index ports.Index
}

func NewAgreement(index ports.Index) *Agreement { return &Agreement{index: index} }

func (t *Agreement) Name() string { return "decision.agreement" }

func (t *Agreement) Invoke(ctx context.Context, _ map[string]string) (any, error) {
	a, err := app.AgreementRate(ctx, t.index)
	if err != nil {
		return nil, fmt.Errorf("decision.agreement: %w", err)
	}
	headline := fmt.Sprintf("n=%d, too few for a rate (needs %d)", a.N, app.MinSample)
	var rate any
	if a.Rate != nil {
		rate = *a.Rate
		headline = fmt.Sprintf("decisions agreed with the verdict %.0f%% of the time (n=%d)", *a.Rate*100, a.N)
	}
	return map[string]any{"n": a.N, "agreed": a.Agreed, "rate": rate, "headline": headline}, nil
}
```

In `cmd/atlas/main.go` `buildRegistry`, append to the `tools.NewRegistry(` list:

```go
		tools.NewOutcome(docs, index),
		tools.NewCalibrate(docs, index),
		tools.NewAgreement(index),
```

- [ ] **Step 4: Run to verify pass**

Run: `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./... -race`
Expected: PASS, including `internal/arch` (vocabulary and core imports).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/tools/outcome.go internal/adapters/outbound/tools/calibrate.go \
  internal/adapters/outbound/tools/agreement.go internal/adapters/outbound/tools/outcome_test.go cmd/atlas
git commit -m "Expose outcomes, calibration and agreement as pack tools"
```

---

### Task 7: The packs, proven end to end with the real binary

**Files:**
- Create: `packs/judge-outcome.yaml`, `packs/judge-calibrate.yaml`

**Interfaces:**
- Consumes: `judge.ask`, `judge.outcome`, `judge.calibrate`, `decision.agreement` and `-var`.
- Produces: the epic's acceptance evidence.

- [ ] **Step 1: Write the packs**

Create `packs/judge-outcome.yaml`:

```yaml
# Attaches what actually happened to a judged subject: every judgement of
# the subject, or one judgement by path. State is whatever the pack scoring
# it declares, e.g. for a job hunt: offer, rejected, withdrawn, ghosted,
# not_applied.
#   atlas -pack packs/judge-outcome.yaml -var subject_id=<id> -var state=rejected -var when=2026-11-20
name: judge-outcome
vars:
  subject_id: ""
  judgement_path: ""
  state: ""
  when: ""
  note: ""

steps:
  - id: outcome
    tool: judge.outcome
    with:
      subject_id: "{{ .vars.subject_id }}"
      judgement_path: "{{ .vars.judgement_path }}"
      state: "{{ .vars.state }}"
      when: "{{ .vars.when }}"
      note: "{{ .vars.note }}"
```

Create `packs/judge-calibrate.yaml`:

```yaml
# Scores one recorded question's probabilities against attached outcomes,
# pooled and per engine, and reports how often decisions agreed with the
# verdict. Nothing is classified by default: name the question, the answer
# options whose mass predicts a positive outcome, and which outcome states
# count as positive and negative. A job-hunt example, leaving ghosted,
# withdrawn and not_applied unscored:
#   atlas -pack packs/judge-calibrate.yaml -var question=<verdict question> \
#     -var options=<option> -var positive=offer -var negative=rejected
name: judge-calibrate
vars:
  question: ""
  options: ""
  positive: ""
  negative: ""

steps:
  - id: calibration
    tool: judge.calibrate
    with:
      question: "{{ .vars.question }}"
      options: "{{ .vars.options }}"
      positive: "{{ .vars.positive }}"
      negative: "{{ .vars.negative }}"

  - id: agreement
    tool: decision.agreement
```

- [ ] **Step 2: Prove it end to end, live**

This uses a scratch pack, not committed, to record real judgements through `judge.ask` against
the local Ollama.

```bash
export S=$(mktemp -d)
cat > $S/forecast.yaml <<'YAML'
name: forecast
vars:
  subject_id: ""
  subject: ""
steps:
  - id: judged
    tool: judge.ask
    with:
      subject_id: "{{ .vars.subject_id }}"
      subject: "{{ .vars.subject }}"
      questions: |
        - id: rain
          type: noul
          ask: Will it rain during this event?
YAML
run() { go run ./cmd/atlas -store-root $S/ws -store-index-path $S/index.db -store-history-path $S/h.duckdb \
  -feed-cache-path $S/feed -crawl-cache-dir $S/crawl "$@"; }
run -pack $S/forecast.yaml -var subject_id=fete -var subject="An outdoor fete in a coastal town in November, forecast: heavy cloud, 90% chance of showers."
run -pack $S/forecast.yaml -var subject_id=fete -var subject="An outdoor fete in a coastal town in November, forecast: heavy cloud, 90% chance of showers."
run -pack $S/forecast.yaml -var subject_id=picnic -var subject="A July picnic inland, forecast: clear skies, 0% chance of rain."
run -pack packs/judge-outcome.yaml -var subject_id=fete -var state=wet -var when=2026-11-02
run -pack packs/judge-outcome.yaml -var subject_id=picnic -var state=dry
run -pack packs/judge-calibrate.yaml -var question=rain -var options=yes -var positive=wet -var negative=dry
git -C $S/ws log --oneline | cat
git -C $S/ws log -p -1 -- $(git -C $S/ws ls-files 'judgements/fete/*' | head -1) | head -40
```

If a crawl flag does not exist on this branch's binary, drop it; flags differ by which epics
have merged.

Expected, pasted into the report:
- Three judgements, then three attachment commits: two for `fete` and one for `picnic`.
- The diff shows `"outcome": null` becoming `{"state": "wet", ...}` and nothing else.
- The calibration shows `n=3` with `headline` "too few for a Brier score (needs 30)",
  `by_engine` with one entry naming the provider and model, and agreement `n=0`.

- [ ] **Step 3: Commit**

```bash
git add packs/judge-outcome.yaml packs/judge-calibrate.yaml
git commit -m "Add the packs that attach outcomes and score predictions"
```

---

### Task 8: The design doc and the increment note

**Files:**
- Modify: `docs/design/the-judge.md`
- Create: `docs/notes/2026-09-27-epic-11.md`

- [ ] **Step 1: Update `docs/design/the-judge.md`**

Rewrite whatever it says about the outcome slot being unfilled, so it describes current
behaviour:
- `judge.outcome` fills the slot as a later revision of the same document.
- It changes only the `outcome` value and the index row's `outcome` field.
- `judge.calibrate` scores one question's probabilities against the attached outcomes.
- Its exclusions are pending, zero coverage, unclassified and option mismatch.
- It uses the `MinSample` and `MinBinSample` thresholds.
- It never persists a report.

- [ ] **Step 2: Write the note**

Use the same sections as earlier notes:
- **What the pattern was:** the outcome as a revision rather than a new document; calibration as
  a derived view; honesty thresholds.
- **What surprised me.** At minimum:
  - R1, the spec's `Positive` being used for two vocabularies;
  - R3, the byte-preserving splice and why the merge-order risk with Epic 7 required it;
  - R6, pre-coverage judgements being excluded;
  - what Task 7 showed live, with pasted output.
- **What I would do differently.**
- **What I still do not understand.**
- **What was deliberately not built:** the spec's list, plus the agreement rate's confidence
  breakdown (R8) and a provider/model filter on the tool (R7).

- [ ] **Step 3: Run the "done" checklist and paste its output into the note**

- [ ] **Step 4: Commit**

```bash
git add docs/design/the-judge.md docs/notes/2026-09-27-epic-11.md
git commit -m "Record what epic 11 taught, and describe the filled outcome slot"
```

---

## Self-review against the spec

| Spec requirement | Task |
|---|---|
| `Outcome` with a free State, When and Note; non-null is terminal | 1 |
| `AttachOutcome` as a later revision of the same document, the row replaced not appended | 1 |
| A correction is another revision, with history kept | 1 |
| `AttachOutcomeForSubject` across sibling judgements | 2 |
| Brier score, fixed deciles, MinSample 30, MinBinSample 10 | 3 |
| `Prediction`, generic over noul, choice and score | 4 (R1 amends it) |
| Exclusions counted: pending, zero coverage, unclassified | 4 (plus option mismatch, R2) |
| Grouped by provider and model; pooled labelled as pooled | 4, 6 |
| A report never persisted | 6 (tool output only) |
| Agreement rate as its own statistic, with MinSample honesty | 5, 6 |
| No job vocabulary in Go | Global Constraints; weather fixtures |
| Tested against the real gitdocs and sqlindex | 1, 2, 4, 5, 6 |
| 11.1 and 11.2 | Not in this epic, per the spec |
