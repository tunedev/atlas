# Epic 10 — The apply loop — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** one run of `packs/job-hunt.yaml` scores a board, applies the user's policy, drafts a
package for every allowed posting, logs a skip for every denied one, lists the asked ones, and
never submits; the user declares a send, which starts the clock Epic 11 reads.

**Architecture:** pure core functions (`app.Decide`, `app.DeclareStage`, `app.Suggest`) behind
small generic tools (`policy.decide`, `policy.suggest`, `policy.add`, `stage.declare`,
`stage.attach`, `pack.each`). `pack.each` runs a child pack per row through a runner injected at
the composition root. Every use-case word stays in `packs/`.

**Tech Stack:** Go 1.27, existing dependencies only; `typst` for the live run; Ollama
`qwen2.5-coder:7b` at `http://localhost:11434/v1`.

**Spec:** `docs/specs/2026-09-28-epic-10-apply-loop.md`. The Rulings below override it where they
disagree.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a posting, application, CV or letter is.** Go names are
  generic: policy, decision, stage, row, pack. `internal/arch/vocabulary_test.go` enforces it,
  fixtures included (use neutral subjects: a bakery's orders, a lighthouse log).
- **No core package imports an adapter or a driver.** `internal/arch/arch_test.go` enforces it.
- Every wrapped error carries its component prefix: `policy: `, `stage: `, `suggest: `,
  `policy.decide: `, `policy.suggest: `, `policy.add: `, `stage.declare: `, `stage.attach: `,
  `pack.each: `, `http.request: `.
- `ctx` first on every blocking call, never stored in a struct.
- The `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no literal in
  `buildRegistry`, `startAgent` or `startRender`, and none in any new composition function.
- Comments describe current behaviour only. No emojis.
- Tests assert behaviour; every guard is proven by mutation, and the mutation is confirmed applied.
- Every test runs with no network.
- **Atlas never submits.** No new code sends anything but GET or HEAD to a remote host.

## What "done" means

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

and the live run in Task 9, with pasted output: one allowed posting drafted (four PDFs, stage
`drafted`), one denied (skip decision, stage `not_applied`), one asked (untouched); a second run
changes nothing; `sent.yaml` declares a send and the index shows `sent_at`.

## Rulings

| # | Point | Ruling |
|---|---|---|
| R1 | The spec's `packs/apply.yaml` would repeat `packs/tailor.yaml`'s twenty steps. | No `apply.yaml`. `tailor.yaml` is the one-action package pack (10.1): it gains a `url` var, an `instructions.typ` render, and a final `stage.declare drafted`. Run by hand or by `pack.each`. |
| R2 | How `pack.each` gets the posting's text into `tailor.yaml`. | `judge.each` rows gain `item` (the source document), beside `answers` and `source_id`. `pack.each` renders `posting` and `url` from `[[ .item.item.* ]]`. No second fetch, no nesting. |
| R3 | What `pack.each` returns per child. | `{"vars": {...}, "ok": true}` or `{"vars": {...}, "error": "..."}` — not the child's state, which would repeat every package in the parent's output. |
| R4 | The spec groups `policy.suggest` by verdict and tripped deal-breakers. | By verdict alone. A tripped deal-breaker already always denies, and an unknown one caps at ask, so neither can be promoted into an allow or deny rule that changes anything. It reads the index's `decision` rows, which carry `decision` and `verdict_at_decision`. |
| R5 | Where the policy is read from. | A record path, read through `ports.Docs`, like `judge.each` reads its rules. Empty path = no policy rules (tripped still denies, unknown caps at ask, everything else asks). |
| R6 | How `stage.declare` knows whether a stage document exists. | `Docs.List` on the subject's directory, not a not-found error: the core has no not-found sentinel, and reading one from an adapter would leak it. |
| R7 | The architecture test for "no tool writes to a remote host". | A test that reads every non-test `.go` file in `internal/adapters/outbound/tools` and `internal/adapters/outbound/crawlsource` and fails on `MethodPost`, `MethodPut`, `MethodPatch` or `MethodDelete`, or a string literal of those methods. `openaiprov` posts to the model endpoint and is not a tool; `mcpserve` receives rather than sends. |
| R8 | `pack.each` output when every child failed. | An error for the step, like `judge.each`. Some failed → the step succeeds with error rows. |

## Review Focus

1. **The same posting reaches `pack.each` twice in one run** (a feed with a duplicate id): the
   second child must not redraft. `stage.attach` runs once before `pack.each`, so both rows carry
   an empty stage. Task 5, `TestPackEachSkipsARowWhoseStageBecameSetDuringTheRun` — the child is
   re-checked: `pack.each` re-reads each row's stage through an injected lookup just before
   running it.
2. **A backdated send earlier than `drafted`**: refused with both times in the message. Task 4,
   `TestDeclareRefusesATimeBeforeTheCurrentStage`.
3. **A policy file with a rule whose condition names an unknown operator or a missing value**:
   fails before any posting is scored. Task 3, `TestParsePolicyRejectsAMalformedRule`.
4. **A row with an error from `judge.each`**: gets no decision and is never drafted or skipped.
   Task 3, `TestDecideLeavesAnErrorRowUndecided`, and Task 8's end-to-end test.
5. **`sent.yaml` run for a subject with no stage at all** (the user applied by hand): `sent` is
   accepted as the first stage — the clock still starts. Task 4, `TestSentCanBeTheFirstStage`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/adapters/outbound/tools/http.go` | GET and HEAD only |
| `internal/arch/submit_test.go` | No tool code sends a write method |
| `internal/adapters/outbound/tools/each.go` | Rows gain `answers`, `source_id`, `item` |
| `internal/core/app/policy.go` | `Policy`, `ParsePolicy`, `Decide` |
| `internal/adapters/outbound/tools/policy.go` | `policy.decide` |
| `internal/core/app/stage.go` | `DeclareStage`, `StageRow` |
| `internal/adapters/outbound/tools/stage.go` | `stage.declare`, `stage.attach` |
| `internal/adapters/outbound/tools/packeach.go` | `pack.each` |
| `internal/core/app/suggest.go` | `Suggest` |
| `internal/adapters/outbound/tools/suggest.go` | `policy.suggest`, `policy.add` |
| `cmd/atlas/main.go` | Registering the tools; the child runner for `pack.each` |
| `packs/tailor.yaml`, `packs/tailor/instructions.typ` | The package pack (R1) |
| `packs/sent.yaml`, `packs/skip.yaml`, `packs/policy-promote.yaml`, `packs/job-hunt.yaml` | The loop |
| `packs/profile/policy.example.json` | An example policy |
| `docs/notes/2026-09-28-epic-10.md` | The note |

---

### Task 1: Refuse to write to a remote host

**Files:**
- Modify: `internal/adapters/outbound/tools/http.go`, `internal/adapters/outbound/tools/http_test.go`
- Create: `internal/arch/submit_test.go`

- [ ] **Step 1: Failing tests**

In `http_test.go` (follow its existing server helper):

```go
func TestHTTPRefusesEveryMethodButGetAndHead(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "post", "OPTIONS"} {
		_, err := tools.NewHTTP(time.Second, 1<<20).Invoke(context.Background(), map[string]string{"url": srv.URL, "method": m})
		if err == nil || !strings.Contains(err.Error(), "only GET and HEAD") {
			t.Errorf("%s: err = %v", m, err)
		}
	}
	if hit {
		t.Error("a refused method reached the server")
	}
	for _, m := range []string{"", "GET", "HEAD"} {
		if _, err := tools.NewHTTP(time.Second, 1<<20).Invoke(context.Background(), map[string]string{"url": srv.URL, "method": m}); err != nil {
			t.Errorf("%q: %v", m, err)
		}
	}
}
```

`internal/arch/submit_test.go`:

```go
package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Atlas drafts and never submits. No tool, and nothing a tool fetches
// through, may send a method that writes to a remote host.
func TestNoToolSendsAWriteMethod(t *testing.T) {
	write := regexp.MustCompile(`Method(Post|Put|Patch|Delete)\b|"(POST|PUT|PATCH|DELETE)"`)
	for _, dir := range []string{"../adapters/outbound/tools", "../adapters/outbound/crawlsource"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no files in %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if loc := write.Find(b); loc != nil {
				t.Errorf("%s names a write method (%s); atlas never submits", f, loc)
			}
		}
	}
}
```

If `crawlsource` legitimately names a write method only to refuse it (read `conduct.go`'s
`refuse`), express that refusal without naming the constant (e.g. `req.Method != http.MethodGet
&& req.Method != http.MethodHead`) rather than weakening the test, and say so in the report.

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/adapters/outbound/tools/ -run HTTPRefuses ./internal/arch/ -v`

- [ ] **Step 3: Implement**

In `http.go`'s `Invoke`, after reading `method`:

```go
	method := strings.ToUpper(with["method"])
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return nil, fmt.Errorf("http.request: %s refused: only GET and HEAD are allowed; atlas never submits", method)
	}
```

Update the tool's doc comment to say so.

- [ ] **Step 4: Run, mutate, commit**

Run the package tests and the arch test. Mutation: allow `POST` through; confirm the HTTP test
fails. Revert.

```bash
git commit -m "Refuse every HTTP method but GET and HEAD, so nothing can submit"
```

(Every commit message ends with a blank line and `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.)

---

### Task 2: judge.each rows carry what the loop needs

**Files:**
- Modify: `internal/adapters/outbound/tools/each.go` and its test file

**Interfaces:**
- Produces: every non-error row gains `answers` (map question id → chosen option, every question
  including rule questions), `source_id` (the item's id as the source gave it) and `item` (the
  decoded source document). An error row gains `source_id` and `item` too. Reused rows carry the
  same fields, read from the stored judgement's answers.

- [ ] **Step 1: Failing test** — in the existing `judge.each` test file, extend the test that
  checks a fresh row and the one that checks a reused row: assert `row["answers"]` holds each
  question's chosen option, `row["source_id"]` equals the item's id, and `row["item"]` is the item
  document (compare one field such as its title). Assert an error row carries `source_id`.

- [ ] **Step 2: Implement** — pass `itemID` and `doc` into `assessedRow` and `errorRow`:

```go
func assessedRow(subjectID, itemID, path string, doc any, answers []ports.Answer, a app.Assessment, reused bool) map[string]any {
	chosen := make(map[string]any, len(answers))
	for _, ans := range answers {
		chosen[ans.ID] = ans.Chosen
	}
	// ... existing reasons/rules ...
	return map[string]any{
		"subject_id": subjectID, "source_id": itemID, "item": doc,
		"verdict": a.Verdict, "p": a.P, "answers": chosen,
		"reasons": reasons, "rules": rules, "judgement_path": path, "reused": reused,
	}
}
```

Fresh rows pass `j.Answers`; reused rows pass `stored.Answers` (so `reuse` also takes `itemID`
and `doc`). `errorRow(subjectID, itemID string, doc any, err error)`.

- [ ] **Step 3: Run the `judge.each` tests and `packs/job-hunt.yaml`'s packfile validation test;
  commit** — "Carry each item's answers, source id and document on its judged row".

---

### Task 3: The policy

**Files:**
- Create: `internal/core/app/policy.go`, `internal/core/app/policy_test.go`
- Create: `internal/adapters/outbound/tools/policy.go`, `internal/adapters/outbound/tools/policy_test.go`
- Create: `packs/profile/policy.example.json`

**Interfaces:**
- Produces:
  - `app.PolicyRule{ID, Decision string; When []app.Condition}`, `app.Condition{Field, Op string; Value any}` (JSON `id`, `decision`, `when`, `field`, `op`, `value`)
  - `app.ParsePolicy(body []byte) ([]app.PolicyRule, error)` — rejects: no id, repeated id, decision not `allow`/`deny` (ask is the default, not a rule), empty `when`, an invalid operator or value (reuse `validOp` and the value checks `checkComparableValue` applies, adapted to a `Condition`).
  - `app.Decision{Decision string; Matched []string; Because string}` (JSON `decision`, `matched`, `because`)
  - `app.Decide(row map[string]any, rules []app.PolicyRule) (app.Decision, bool)` — `false` for an error row.
  - `tools.NewPolicyDecide(docs ports.Docs) *tools.PolicyDecide`, name `policy.decide`. Inputs: `rows` (JSON array), `policy` (record path, may be empty). Output: `{"rows": [...]}`, each row with `decision`, `matched`, `because` added (error rows unchanged).

- [ ] **Step 1: Failing core tests** (`policy_test.go`, neutral fixtures):

```go
func row(verdict string, p float64, rules ...string) map[string]any {
	rs := []any{}
	for i := 0; i+1 < len(rules); i += 2 {
		rs = append(rs, map[string]any{"id": rules[i], "state": rules[i+1]})
	}
	return map[string]any{"subject_id": "s", "verdict": verdict, "p": p,
		"answers": map[string]any{"size": "large"}, "rules": rs}
}

var allowStrong = app.PolicyRule{ID: "strong", Decision: "allow", When: []app.Condition{{Field: "verdict", Op: "==", Value: "take"}, {Field: "p", Op: ">=", Value: 0.7}}}
var denyLarge = app.PolicyRule{ID: "no-large", Decision: "deny", When: []app.Condition{{Field: "answers.size", Op: "==", Value: "large"}}}

func TestDecidePrecedence(t *testing.T) {
	cases := []struct {
		name  string
		row   map[string]any
		rules []app.PolicyRule
		want  string
	}{
		{"tripped beats allow", row("take", 0.9, "nuts", "tripped"), []app.PolicyRule{allowStrong}, "deny"},
		{"deny beats allow, allow first", row("take", 0.9), []app.PolicyRule{allowStrong, denyLarge}, "deny"},
		{"deny beats allow, deny first", row("take", 0.9), []app.PolicyRule{denyLarge, allowStrong}, "deny"},
		{"unknown caps at ask", row("take", 0.9, "nuts", "unknown"), []app.PolicyRule{allowStrong}, "ask"},
		{"allow", row("take", 0.9, "nuts", "clear"), []app.PolicyRule{allowStrong}, "allow"},
		{"allow condition not met", row("take", 0.5), []app.PolicyRule{allowStrong}, "ask"},
		{"default ask", row("take", 0.9), nil, "ask"},
	}
	for _, c := range cases {
		d, ok := app.Decide(c.row, c.rules)
		if !ok || d.Decision != c.want {
			t.Errorf("%s: %+v, %v; want %s", c.name, d, ok, c.want)
		}
	}
}

func TestDecideNamesWhatMatched(t *testing.T) {
	d, _ := app.Decide(row("take", 0.9, "nuts", "tripped"), []app.PolicyRule{allowStrong})
	if !strings.Contains(d.Because, "nuts") || len(d.Matched) != 1 || d.Matched[0] != "strong" {
		t.Errorf("%+v", d)
	}
}

func TestDecideLeavesAnErrorRowUndecided(t *testing.T) {
	if _, ok := app.Decide(map[string]any{"subject_id": "s", "error": "boom"}, nil); ok {
		t.Error("an error row was decided")
	}
}

func TestParsePolicyRejectsAMalformedRule(t *testing.T) {
	for name, body := range map[string]string{
		"no id":       `{"rules":[{"decision":"deny","when":[{"field":"p","op":">","value":1}]}]}`,
		"repeated id": `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":1}]},{"id":"a","decision":"allow","when":[{"field":"p","op":">","value":1}]}]}`,
		"ask rule":    `{"rules":[{"id":"a","decision":"ask","when":[{"field":"p","op":">","value":1}]}]}`,
		"no when":     `{"rules":[{"id":"a","decision":"deny","when":[]}]}`,
		"bad op":      `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":"~","value":1}]}]}`,
		"no value":    `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">"}]}]}`,
		"string order":`{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":"x"}]}]}`,
		"not json":    `{`,
	} {
		if _, err := app.ParsePolicy([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
```

- [ ] **Step 2: Implement** `policy.go`:

```go
package app

// PolicyRule allows or denies a row when every one of its conditions holds.
type PolicyRule struct {
	ID       string      `json:"id"`
	Decision string      `json:"decision"`
	When     []Condition `json:"when"`
}

// Condition compares the value at a dotted path of a row with Value, using
// the comparable-rule operators.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// Decision is what a policy decided about one row, which rules matched, and
// why, in words a person can check.
type Decision struct {
	Decision string   `json:"decision"`
	Matched  []string `json:"matched"`
	Because  string   `json:"because"`
}

const (
	DecisionAllow = "allow"
	DecisionAsk   = "ask"
	DecisionDeny  = "deny"
)

// ParsePolicy decodes {"rules": [...]} and rejects a rule it could not apply.
func ParsePolicy(body []byte) ([]PolicyRule, error) {
	var doc struct {
		Rules []PolicyRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("policy: decode: %w", err)
	}
	seen := map[string]bool{}
	for i, r := range doc.Rules {
		switch {
		case r.ID == "":
			return nil, fmt.Errorf("policy: rule %d has no id", i)
		case seen[r.ID]:
			return nil, fmt.Errorf("policy: %s is listed twice", r.ID)
		case r.Decision != DecisionAllow && r.Decision != DecisionDeny:
			return nil, fmt.Errorf("policy: %s: decision must be allow or deny, got %q; ask is the default", r.ID, r.Decision)
		case len(r.When) == 0:
			return nil, fmt.Errorf("policy: %s: no conditions", r.ID)
		}
		seen[r.ID] = true
		for _, c := range r.When {
			if err := checkCondition(r.ID, c); err != nil {
				return nil, err
			}
		}
	}
	return doc.Rules, nil
}

// checkCondition applies the comparable-rule checks to one condition.
func checkCondition(ruleID string, c Condition) error {
	if c.Field == "" {
		return fmt.Errorf("policy: %s: a condition has no field", ruleID)
	}
	if !validOp(c.Op) {
		return fmt.Errorf("policy: %s: unknown operator %q", ruleID, c.Op)
	}
	return checkComparableValue(Rule{ID: ruleID, Op: c.Op, Value: c.Value})
}

// Decide applies rules to row with a fixed precedence: a tripped rule in
// the row's "rules" denies; a matching deny rule denies; an unknown rule
// caps the result at ask; a matching allow rule allows; otherwise ask. A
// row carrying "error" is not decided.
func Decide(row map[string]any, rules []PolicyRule) (Decision, bool) {
	if _, failed := row["error"]; failed {
		return Decision{}, false
	}
	tripped, unknown := ruleStates(row)
	var deny, allow []string
	for _, r := range rules {
		if !holds(row, r.When) {
			continue
		}
		if r.Decision == DecisionDeny {
			deny = append(deny, r.ID)
		} else {
			allow = append(allow, r.ID)
		}
	}
	matched := append(append([]string{}, deny...), allow...)
	switch {
	case len(tripped) > 0:
		return Decision{DecisionDeny, matched, "tripped: " + strings.Join(tripped, ", ")}, true
	case len(deny) > 0:
		return Decision{DecisionDeny, matched, "denied by: " + strings.Join(deny, ", ")}, true
	case len(unknown) > 0:
		return Decision{DecisionAsk, matched, "unknown: " + strings.Join(unknown, ", ")}, true
	case len(allow) > 0:
		return Decision{DecisionAllow, matched, "allowed by: " + strings.Join(allow, ", ")}, true
	}
	return Decision{DecisionAsk, matched, "no rule matched"}, true
}

// ruleStates lists the ids of the row's tripped and unknown rules, as
// judge.each writes them: "rules": [{"id", "state", ...}].
func ruleStates(row map[string]any) (tripped, unknown []string) {
	list, _ := row["rules"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		id, _ := m["id"].(string)
		switch m["state"] {
		case RuleTripped:
			tripped = append(tripped, id)
		case RuleUnknown:
			unknown = append(unknown, id)
		}
	}
	return tripped, unknown
}

// holds reports whether every condition holds for row; a missing field
// does not hold.
func holds(row map[string]any, when []Condition) bool {
	for _, c := range when {
		got, ok := lookupPath(row, c.Field)
		if !ok {
			return false
		}
		if ok, comparable := compare(got, c.Op, c.Value); !comparable || !ok {
			return false
		}
	}
	return true
}
```

A condition holds when `lookupPath(row, c.Field)` finds a value and `compare(got, c.Op, c.Value)`
holds; a missing field means the condition does not hold. `Because` lists, in order: tripped rule
ids ("tripped: nuts"), matched deny rules, unknown rule ids, matched allow rules, or "no rule
matched" for the default. `Matched` holds the policy rule ids that matched, deny and allow alike.
Row `rules` entries are maps with `id` and `state` as `judge.each` writes them.

- [ ] **Step 3: The tool, test first** — `policy_test.go` in tools: with a `gitdocs` store holding
  a policy document, `policy.decide` decides three rows (allow, deny, error) and leaves the error
  row without a `decision` key; with `policy: ""` every non-tripped row is ask; a malformed policy
  document is an error naming the path. Implement: read `policy` via `docs.Get` when non-empty,
  `ParsePolicy`, then `Decide` per row, copying the row and adding the three keys.

- [ ] **Step 4: `packs/profile/policy.example.json`** — the spec's example (allow on
  `verdict == apply` and `p >= 0.7`; deny on `answers.focus == frontend`).

- [ ] **Step 5: Run, mutate (make deny lose to allow; confirm "deny beats allow" fails), commit** —
  "Decide allow, ask or deny for each scored row, with a tripped rule always denying".

---

### Task 4: Stages

**Files:**
- Create: `internal/core/app/stage.go`, `internal/core/app/stage_test.go`
- Create: `internal/adapters/outbound/tools/stage.go`, `internal/adapters/outbound/tools/stage_test.go`

**Interfaces:**
- Produces:
  - `app.Stage{SubjectID, Stage, Note, DeclaredBy string; When time.Time}`
  - `app.DeclareStage(ctx, docs ports.Docs, index ports.Index, s app.Stage, now time.Time) (path string, changed bool, err error)`
  - `app.CurrentStage(ctx, index ports.Index, subjectID string) (string, error)` — "" when none.
  - `tools.NewStageDeclare(docs, index)` name `stage.declare` (inputs `subject_id`, `stage`, `when` optional RFC 3339, `note`, `declared_by`; output `{"path", "stage", "changed"}`)
  - `tools.NewStageAttach(index)` name `stage.attach` (input `rows` JSON; output `{"rows"}` each with `stage` set, "" when none)

The document at `applications/<subject_id>/stage.json`:

```json
{"subject_id": "s", "stage": "sent", "reached": {"drafted": "2026-09-28T10:00:00Z", "sent": "2026-09-29T09:00:00Z"}, "note": "", "declared_by": "user"}
```

Index row: kind `stage`, fields `subject_id`, `stage`, `declared_by`, and `<stage>_at` for every
entry in `reached` (RFC 3339).

- [ ] **Step 1: Failing core tests** (`stage_test.go`, fake docs/index already in package or a
  gitdocs+sqlindex store — follow the file's neighbours): first declaration writes the document and
  row; a later stage adds to `reached` and sets `<stage>_at`; `TestDeclareRefusesATimeBeforeTheCurrentStage`
  (error names both times); a future `when` is refused; re-declaring the current stage returns
  `changed == false` and makes no new revision (`Docs.History` length unchanged); a backdated
  `sent` after `drafted` is accepted; `TestSentCanBeTheFirstStage`; a subject id with `/` is refused
  (`CheckSubjectID`); `CurrentStage` returns the latest and "" for an unknown subject.

- [ ] **Step 2: Implement** — existence via `docs.List(ctx, "applications/"+id)` (R6); read and
  decode when present; validate; write with `RecordDocument` (kind `stage`, message
  `"Declare <stage> for <id>"`). `CurrentStage` uses `index.Find(ctx, ports.Query{Kind: "stage", Match: {"subject_id": id}, Limit: 1})`.

- [ ] **Step 3: The tools, test first** — `stage.declare` parses `when` (empty = now; bad format =
  error naming the value); `stage.attach` sets `stage` on every row by `subject_id` and leaves rows
  without a `subject_id` unchanged. Test through a real gitdocs+sqlindex store.

- [ ] **Step 4: Run, mutate (drop the before-current check; confirm the test fails), commit** —
  "Declare an application's stage as a dated revision, and attach it to rows".

---

### Task 5: pack.each

**Files:**
- Create: `internal/adapters/outbound/tools/packeach.go`, `internal/adapters/outbound/tools/packeach_test.go`

**Interfaces:**
- Consumes: `app.RenderItem` / `app.ParseItemTemplate` (existing).
- Produces:
  - `tools.RunPack func(ctx context.Context, path string, vars map[string]string) error` — injected; runs one pack with vars overriding its declared vars.
  - `tools.StageOf func(ctx context.Context, subjectID string) (string, error)` — injected; the current stage.
  - `tools.NewPackEach(run tools.RunPack, stageOf tools.StageOf) *tools.PackEach`, name `pack.each`. Inputs: `pack` (path), `rows` (JSON array), `match` (comma-separated `field=value`, all must hold, missing field = ""), `vars` (YAML map of child var → `[[ ]]` template over `.item`, the row). Output `{"rows": [...], "_meta": {"count", "ok", "errors"}}` (R3, R8).

For each row, in `subject_id` order: skip unless every match condition holds (`fmt.Sprint` of the
value, missing = ""); if the match includes `stage=`, re-read the stage through `stageOf` just
before running and skip if it is no longer empty (Review Focus 1); render every var; call `run`.

- [ ] **Step 1: Failing tests** with a fake `RunPack` recording calls and a fake `StageOf`:
  vars rendered per row; match with two conditions and a missing field; id order; a child error
  becomes an error row and later rows still run; all children failing is a step error;
  `TestPackEachSkipsARowWhoseStageBecameSetDuringTheRun` (the fake `StageOf` reports `drafted` for
  a subject after the first call's run — two rows with the same `subject_id`, second is skipped);
  a malformed `vars` template fails before any child runs.

- [ ] **Step 2: Implement.** Parse `vars` YAML with `gopkg.in/yaml.v3` (already a dependency), then
  `app.ParseItemTemplate` every value before running anything.

- [ ] **Step 3: Run, mutate (drop the stage re-check; confirm the Review Focus test fails), commit**
  — "Run a pack once per matching row, one at a time, each failure isolated".

---

### Task 6: Suggesting and promoting a rule

**Files:**
- Create: `internal/core/app/suggest.go`, `internal/core/app/suggest_test.go`
- Create: `internal/adapters/outbound/tools/suggest.go`, `internal/adapters/outbound/tools/suggest_test.go`

**Interfaces:**
- Produces:
  - `app.Candidate{Rule app.PolicyRule; Evidence []string}` (JSON `rule`, `evidence`)
  - `app.Suggest(decisions []ports.Record, choiceToDecision map[string]string, minRepeats int) []app.Candidate` — groups `decision` rows by `verdict_at_decision` (skipping empty), counts each choice; where one choice for a verdict reaches `minRepeats` and maps to `allow` or `deny` in `choiceToDecision`, emits a rule `{id: "<decision>-when-verdict-<verdict>", decision, when: [{verdict == <verdict>}]}` with the subject ids as evidence. Output sorted by rule id.
  - `tools.NewPolicySuggest(index)` name `policy.suggest`: inputs `choices` (YAML map, e.g. `apply: allow`, `skip: deny`), `min_repeats` (integer ≥ 2); reads `index.Find(kind decision)`; output `{"candidates": [...]}`. Writes nothing.
  - `tools.NewPolicyAdd(docs, index)` name `policy.add`: inputs `policy` (record path), `rule` (JSON of one rule), `evidence` (text); reads the current policy if present (existence via `Docs.List`), rejects a duplicate id, validates the combined document with `app.ParsePolicy`, commits with `RecordDocument` (kind `profile`, field `section: policy`), message `"Promote policy rule <id>: <evidence>"`.

- [ ] **Step 1: Failing tests** — `Suggest`: three `skip`s on verdict `reach` with `minRepeats` 3 →
  one deny candidate with three subject ids; two → none; a choice absent from the map → none;
  decisions with no verdict ignored. `policy.suggest` leaves the store unchanged (compare
  `Docs.List("")` before and after). `policy.add`: creates the document, appends a second rule,
  refuses a duplicate id and a malformed rule, and the commit message names the evidence.

- [ ] **Step 2: Implement, run, mutate (lower the threshold check by one; confirm the test fails),
  commit** — "Suggest a rule from repeated decisions, and promote one only when asked".

---

### Task 7: Composition root

**Files:**
- Modify: `cmd/atlas/main.go`, `cmd/atlas/main_test.go`

- [ ] **Step 1: Failing tests** — extend the registry test to require `policy.decide`,
  `policy.suggest`, `policy.add`, `stage.declare`, `stage.attach`, `pack.each`; add
  `TestPackEachChildCannotRunPackEach`: the child registry built by `run`'s wiring has no
  `pack.each` (test the helper that builds it). Add the new composition function to
  `TestCompositionPassesNoLiterals`.

- [ ] **Step 2: Implement.** Register the five non-`pack.each` tools in `buildRegistry`. In `run()`,
  after render and before the agent: build the child registry from the current registry (it has no
  `pack.each` yet), then

```go
// childRunner runs one pack over registry, with vars overriding its own, so
// pack.each can run a pack per row; registry never holds pack.each.
func childRunner(registry ports.Registry, tracer trace.Tracer) tools.RunPack {
	return func(ctx context.Context, path string, vars map[string]string) error {
		b, err := packfile.Load(path)
		if err != nil {
			return err
		}
		if b, err = b.WithVars(vars); err != nil {
			return err
		}
		_, err = app.NewRunner(registry).WithTracer(tracer).Run(ctx, b)
		return err
	}
}
```

and `registry = registry.With(tools.NewPackEach(childRunner(registry, tracer), stageOf(index)))`
where `stageOf` wraps `app.CurrentStage`. Obtain `tracer` before this (it is created later in `run`
today — move its creation up, keeping it after `telemetry.Init`).

- [ ] **Step 3: Run the full suite; commit** — "Register the apply-loop tools, and give pack.each a
  child runner without itself".

---

### Task 8: The packs, and the end-to-end test

**Files:**
- Modify: `packs/tailor.yaml`; create `packs/tailor/instructions.typ`, fixtures in `packs/tailor/testdata/`
- Create: `packs/sent.yaml`, `packs/skip.yaml`, `packs/policy-promote.yaml`
- Modify: `packs/job-hunt.yaml`
- Create: `cmd/atlas/apply_loop_test.go` (end to end)

- [ ] **Step 1: `tailor.yaml` (R1).** Add vars `url: ""` and `declared_by: "tailor"`. After the
  `review` render, add:
  - `instructions` — `render.run` with `packs/tailor/instructions.typ`, data
    `{"url", "subject_id", "files": ["cv.pdf","letter.pdf"], "answers": <kept answer sentences>, "gaps": <gaps_all>}`,
    output `{{ .vars.out_dir }}/instructions.pdf`, `expect` the url when non-empty.
  - `drafted` — `stage.declare` with `subject_id`, `stage: drafted`, `declared_by`.
  `instructions.typ` renders: where to submit (the url, or "No link in the record: find it on the
  board"), which file goes where, each answer under its question to paste, the review sheet's gaps
  to check first, and the literal command
  `atlas -pack packs/sent.yaml -var subject_id=<id> [-var when=<RFC 3339>]`. Add fixtures for it
  (with and without url).

- [ ] **Step 2: `sent.yaml`, `skip.yaml`, `policy-promote.yaml`.**
  - `sent.yaml`: vars `subject_id`, `when: ""`, `note: ""`; one `stage.declare` step with
    `stage: sent`, `declared_by: user`.
  - `skip.yaml`: vars `subject_id`, `reason: ""`, `judgement_path: ""`, `verdict_question: verdict`,
    `declared_by: user`; `decision.record` (choice `skip`) then `stage.declare not_applied`.
  - `policy-promote.yaml`: vars `rule`, `evidence`, `policy: profile/policy.json`; one `policy.add`
    step. Header comment shows running `policy.suggest` first (a two-step pack
    `packs/policy-suggest.yaml` with `choices: {apply: allow, skip: deny}`, `min_repeats: "3"`).

- [ ] **Step 3: `job-hunt.yaml`.** Add vars `policy: ""`, `store_root`, `out_dir`,
  `templates: packs/tailor`. After `board` (judge.each): `decide` (policy.decide over
  `{{ json .steps.board.rows }}`), `attach` (stage.attach), `draft` (pack.each over
  `decision=allow,stage=`, pack `packs/tailor.yaml`, vars: `subject_id: '[[ .item.subject_id ]]'`,
  `url: '[[ .item.item.url ]]'`, `posting` from `[[ .item.item.title ]]`, company and
  `description_text`, `store_root`, `out_dir: '<out_dir>/[[ .item.subject_id ]]'`,
  `declared_by: job-hunt`), `skip` (pack.each over `decision=deny,stage=`, pack `packs/skip.yaml`,
  vars `subject_id`, `reason: '[[ .item.because ]]'`, `judgement_path`, `declared_by: job-hunt`).
  Header comment: asked postings are in the `decide` step's rows with `decision: ask`; run
  `tailor.yaml` or `skip.yaml` on them by hand.

- [ ] **Step 4: End-to-end test** (`cmd/atlas/apply_loop_test.go`, no network, no model, no typst):
  build a registry with a stub `ports.Source` holding three items, a stub `ports.Judge` answering
  `apply` / `apply` + tripped rule / `reach`, a real gitdocs+sqlindex store, and a child runner
  whose `tailor.yaml` is replaced by a fixture pack under `cmd/atlas/testdata/` that only runs
  `stage.declare drafted` (the real tailoring pipeline is covered by Epic 9's tests and the live
  run). Run a job-hunt-shaped fixture pack through the real runner. Assert: the allowed subject is
  `drafted`, the tripped one is `not_applied` with a skip decision, the reach one has no stage; a
  second run changes no document (`Docs.History` lengths unchanged).

- [ ] **Step 5: Run everything, including the packfile validation test over every pack; commit** —
  "Draft the allowed, skip the denied, list the asked, and let the user declare a send".

---

### Task 9: Live run and note

**Files:**
- Create: `docs/notes/2026-09-28-epic-10.md`

- [ ] **Step 1:** In the scratchpad, a fabricated record (profile ingested from a fabricated CV),
  a `profile/dealbreakers.json` and `profile/policy.json`, and three fabricated postings in a
  local feed (use whatever local-feed mechanism Epic 5's tests or config allow; do not touch the
  real feed or the user's store). Run `packs/job-hunt.yaml` with `ATLAS_RENDER_TYPST=typst` on
  `qwen2.5-coder:7b`. Paste: each posting's decision and because; the four PDFs' pdftotext for the
  drafted one; the stage rows. Run it again and show nothing changed. Run `packs/sent.yaml` with a
  backdated `when` and show `sent_at` in the index. Run `packs/policy-suggest.yaml` after recording
  three skips on one verdict and show the candidate. If Ollama returns a CUDA error, retry once and
  record it.

- [ ] **Step 2: The note** in the house shape (what the pattern was, what surprised you, what you
  would do differently, what you still do not understand), with the live-run output, the
  refusal's enforcement, the contract Epic 11 reads, and every ruling made during execution. No
  real CV content.

- [ ] **Step 3: Commit** — "Run the apply loop end to end on a fabricated record, and record it".

## Deliberately not in this increment

As the spec's table, unchanged.
