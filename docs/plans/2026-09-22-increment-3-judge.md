# Increment 3 — The `Judge` port — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ask several typed questions about one subject in a single call, return each answer as
a value with the probability mass it actually holds, and record every judgement beside an empty
outcome slot so a later increment can check it.

**Architecture:** A `Judge` port owned by the core. Its local implementation lives in
`internal/core/app` beside `Chain`, because it composes `Provider` with a mass read and performs
no I/O. A JSON schema whose properties are question ids, each an enum of that question's options,
makes a `choice` unable to answer outside its options. Per-question probability comes from
locating each field's answer token in the completion's token stream and reading mass there.
A judgement is a git document with `"outcome": null`, indexed by subject id; recording is a
separate use case, not the port's job. A `judge.ask` tool puts the whole path behind a pack step.

**Tech Stack:** Go 1.27, the standard library, `gopkg.in/yaml.v3` (already a dependency),
Ollama on `http://localhost:11434/v1` and vLLM on `http://localhost:8000/v1`.

**Spec:** `docs/specs/2026-09-22-judge-design.md`

**Harness spec:** `docs/specs/2026-09-17-job-hunt-harness-design.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md` (Epic 3, stories 3.1-3.6)

**Design reference:** `docs/design/the-provider.md` for the Provider port and `MassPerClass`,
`docs/design/the-record.md` for `Docs` and `Index`.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL or string
  constant naming a use-case concept outside `packs/`. `internal/arch/vocabulary_test.go`
  enforces it, **test fixtures included** — question text in tests judges books, films or
  weather, never roles or candidates.
- **No core package imports an adapter or a driver.** `internal/core/...` compiles in neither
  `net/http` nor `crypto/tls`. `internal/arch/arch_test.go` enforces both.
- **No adapter type in a port signature.** A JSON schema crossing a port is a `[]byte` of
  standard JSON Schema.
- `ctx context.Context` first parameter of every blocking or remote call. Never stored in a struct.
- Every remote call has a timeout. Every response body is bounded.
- Every wrapped error carries its component prefix, as `openaiprov: `, `chain: ` and `mass: `
  already do. The judge's prefix is `judge: `, the recorder's is `judgement: `.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis. Comments describe current behaviour only — no history, no dates, no narrative.
- Tests assert behaviour. A test that cannot fail is a defect. Prove a guard by mutation, and
  **confirm the mutation applied** before reading anything into the result.
- The three `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no
  literal in `buildRegistry`. `cmd/atlas/main_test.go` enforces them.
- Every test runs with no network. Live tests are skipped unless `ATLAS_LIVE_PROVIDER` is set.

## What "done" means

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Then the increment's own claim, with pasted output:

> A pack step asks several typed questions in one call, each answer carries the probability
> mass its class actually holds, and the judgement is on disk in git with an empty outcome slot.

## Measured facts this plan depends on

Both measured in increment 2 against the running engines, not assumed. Re-measure if anything
surprises you.

**Returned log probabilities are the model's pre-constraint distribution.** On a
schema-constrained yes/no question the emitted token was `yes` at p=0.266 while `Yes` held
p=0.672. Reading the emitted token's own probability reports 27% where the model is near 94%.

**`MassPerClass` reads one position per completion** — the last token whose own text is a class
member. Many questions in one completion therefore need a token located per question. That is
Task 4, and it is the one genuinely fiddly piece of this increment.

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/ports/provider.go` | Gains `Temperature` and `Seed` on `Prompt` |
| `internal/core/ports/judge.go` | `Judge`, `Kind`, `Question`, `Answer`, `Judgement` |
| `internal/adapters/outbound/openaiprov/client.go` | Sends temperature and seed when set |
| `internal/core/app/mass.go` | Gains `MassAtToken`; `MassPerClass` is expressed in terms of it |
| `internal/core/app/answerschema.go` | Builds the JSON schema from questions |
| `internal/core/app/answertokens.go` | Locates each field's answer token in a completion |
| `internal/core/app/judge.go` | The local `Judge` over a `Provider` |
| `internal/core/app/judgementrecord.go` | Writes a judgement to `Docs` and `Index` |
| `internal/adapters/outbound/tools/judge.go` | The `judge.ask` tool |
| `internal/config/config.go`, `layers.go` | `JudgeConfig` and the store wiring it needs |
| `cmd/atlas/main.go` | Opens the store and index, registers `judge.ask` |
| `packs/job-hunt.yaml` | A real step that uses it |
| `docs/notes/2026-09-22-increment-3.md` | The increment note |

---

### Task 1: Sampling controls on `Prompt`

**Files:**
- Modify: `internal/core/ports/provider.go`
- Modify: `internal/adapters/outbound/openaiprov/client.go`, `internal/adapters/outbound/openaiprov/wire.go`
- Test: `internal/adapters/outbound/openaiprov/client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ports.Prompt` gains `Temperature *float64` and `Seed *int`. Both nil-able: nil
  means the field is absent from the request and the engine's own default applies.

This exists because increment 2 measured vLLM and Ollama disagreeing on the same input under the
same nominal prompt, traced to vLLM applying its own generation config. A probability read under
unknown sampling is not a calibrated number.

- [ ] **Step 1: Write the failing tests**

Add to `internal/adapters/outbound/openaiprov/client_test.go`:

```go
func TestSamplingIsSentOnlyWhenSet(t *testing.T) {
	temp := 0.0
	seed := 7

	for _, tc := range []struct {
		name   string
		prompt ports.Prompt
		want   bool
	}{
		{"unset", ports.Prompt{User: "q"}, false},
		{"set", ports.Prompt{User: "q", Temperature: &temp, Seed: &seed}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured http.Request
			s := serve(t, http.StatusOK, recorded, &captured)
			if _, err := client(t, s.URL).Complete(context.Background(), tc.prompt); err != nil {
				t.Fatalf("complete: %v", err)
			}
			body, _ := io.ReadAll(captured.Body)
			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}
			_, hasTemp := sent["temperature"]
			_, hasSeed := sent["seed"]
			if hasTemp != tc.want || hasSeed != tc.want {
				t.Errorf("temperature present = %v, seed present = %v, want both %v; request was %s",
					hasTemp, hasSeed, tc.want, body)
			}
		})
	}
}

func TestATemperatureOfZeroIsSentRatherThanOmitted(t *testing.T) {
	temp := 0.0
	var captured http.Request
	s := serve(t, http.StatusOK, recorded, &captured)
	if _, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", Temperature: &temp}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	body, _ := io.ReadAll(captured.Body)
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	v, ok := sent["temperature"]
	if !ok || v.(float64) != 0 {
		t.Errorf("temperature = %v present=%v, want 0 present; a zero value must not be dropped: %s", v, ok, body)
	}
}
```

The second test is the one that matters: `omitempty` on a plain `float64` would silently drop
temperature 0, which is the exact value a judge wants.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/adapters/outbound/openaiprov/ -run Sampling -v` and
`go test ./internal/adapters/outbound/openaiprov/ -run Temperature -v`
Expected: FAIL — `unknown field Temperature in struct literal`.

- [ ] **Step 3: Add the fields to the port**

In `internal/core/ports/provider.go`, add to `Prompt`:

```go
	// Temperature and Seed pin how the engine samples. Both are nil when the
	// caller has no opinion, and the engine's own default applies.
	Temperature *float64
	Seed        *int
```

- [ ] **Step 4: Carry them on the wire**

In `wire.go`, add to `chatRequest`:

```go
	Temperature *float64 `json:"temperature,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
```

Pointers, so `omitempty` drops an absent value rather than a zero one. In `client.go`'s request
builder, assign `p.Temperature` and `p.Seed` straight across.

- [ ] **Step 5: Run them and watch them pass**

Run: `go test ./internal/adapters/outbound/openaiprov/ -v`
Expected: PASS, including every test from increment 2.

- [ ] **Step 6: Prove the zero-value guard by mutation**

Change the wire fields to `float64`/`int` with `omitempty` (not pointers), confirm the mutation
applied with `git diff`, and confirm `TestATemperatureOfZeroIsSentRatherThanOmitted` fails.
Revert, and confirm it passes. Record this in your report.

- [ ] **Step 7: Commit**

```bash
git add internal/core/ports/provider.go internal/adapters/outbound/openaiprov
git commit -m "Let a caller state how the engine samples, rather than inheriting it"
```

---

### Task 2: The `Judge` port and its types

**Files:**
- Create: `internal/core/ports/judge.go`
- Test: `internal/core/ports/judge_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ports.Kind` with constants `KindNoul = "noul"`, `KindChoice = "choice"`,
  `KindScore = "score"`; `ports.Question{ID string, Kind Kind, Ask string, Options []string,
  Forms map[string][]string}`; `ports.Answer{ID string, Kind Kind, Chosen string,
  Distribution map[string]float64, Expected float64}`; `ports.Judgement{Subject string,
  Model string, When time.Time, Answers []Answer}`; `ports.Judge` with
  `Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)`;
  `ports.NoulOptions() []string` returning `[]string{"yes", "no"}` and
  `ports.NoulForms() map[string][]string` returning `{"yes": {"yes", "Yes", "YES", "true"},
  "no": {"no", "No", "NO", "false"}}`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/ports/judge_test.go`:

```go
package ports_test

import (
	"context"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// stubJudge exists to prove the port is implementable from outside the package
// with no adapter type in any signature.
type stubJudge struct{}

func (stubJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	answers := make([]ports.Answer, 0, len(qs))
	for _, q := range qs {
		answers = append(answers, ports.Answer{
			ID:           q.ID,
			Kind:         q.Kind,
			Chosen:       "yes",
			Distribution: map[string]float64{"yes": 0.9, "no": 0.1},
		})
	}
	return ports.Judgement{Subject: subject, Model: "stub-model", When: time.Now(), Answers: answers}, nil
}

func TestAJudgeCanBeImplementedOutsideTheCore(t *testing.T) {
	var j ports.Judge = stubJudge{}

	got, err := j.Ask(context.Background(), "a short book", []ports.Question{
		{ID: "worth_reading", Kind: ports.KindNoul, Ask: "Is it worth reading?"},
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got.Model == "" {
		t.Error("Judgement does not record which model answered")
	}
	if len(got.Answers) != 1 || got.Answers[0].ID != "worth_reading" {
		t.Fatalf("answers are not keyed by question id: %+v", got.Answers)
	}
	if got.Answers[0].Distribution["yes"] == 0 {
		t.Error("Answer carries no distribution")
	}
}

func TestANoulKnowsItsOwnOptionsAndForms(t *testing.T) {
	if len(ports.NoulOptions()) != 2 {
		t.Fatalf("noul options = %v, want yes and no", ports.NoulOptions())
	}
	forms := ports.NoulForms()
	if len(forms["yes"]) < 2 || len(forms["no"]) < 2 {
		t.Errorf("noul forms do not cover surface variants: %+v", forms)
	}
	// A caller must not be able to mutate the shared defaults.
	forms["yes"] = nil
	if ports.NoulForms()["yes"] == nil {
		t.Error("NoulForms returns a shared map; a caller can empty it for everyone")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/core/ports/ -v`
Expected: FAIL — `undefined: ports.Judge`.

- [ ] **Step 3: Write the port**

Create `internal/core/ports/judge.go`. `NoulOptions` and `NoulForms` return freshly built values
each call, so no caller can mutate a shared default. Doc comments state the three kinds and what
`Distribution` and `Expected` mean, in the core's vocabulary — no use-case words.

```go
package ports

import (
	"context"
	"time"
)

// Kind is what shape an answer takes.
type Kind string

const (
	// KindNoul is a probability of yes.
	KindNoul Kind = "noul"
	// KindChoice is one option, with the distribution over all of them.
	KindChoice Kind = "choice"
	// KindScore is a probability-weighted position on ordered levels.
	KindScore Kind = "score"
)

// Question is one typed question. Options are the allowed answers: the options
// themselves for a choice, the ordered levels for a score, and yes and no for a
// noul. Forms names extra surface forms that count as an option, so that Yes
// counts as yes.
type Question struct {
	ID      string
	Kind    Kind
	Ask     string
	Options []string
	Forms   map[string][]string
}

// Answer is what a model answered and how much probability mass each option
// held. Distribution sums to one. Expected is set for a score alone: the
// levels' positions weighted by their mass.
type Answer struct {
	ID           string
	Kind         Kind
	Chosen       string
	Distribution map[string]float64
	Expected     float64
}

// Judgement is every answer about one subject, and which model produced them.
type Judgement struct {
	Subject string
	Model   string
	When    time.Time
	Answers []Answer
}

// Judge answers typed questions about a subject. Every question is answered in
// one call, so the answers cannot contradict each other.
type Judge interface {
	Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)
}

// NoulOptions is the option list a noul is asked with.
func NoulOptions() []string { return []string{"yes", "no"} }

// NoulForms is the surface forms that count as each noul option.
func NoulForms() map[string][]string {
	return map[string][]string{
		"yes": {"yes", "Yes", "YES", "true"},
		"no":  {"no", "No", "NO", "false"},
	}
}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/core/ports/ -v`
Expected: PASS.

- [ ] **Step 5: Confirm the core stayed clean**

Run: `go test ./internal/arch/ -v`
Expected: PASS. `internal/core/ports` still imports only the standard library, and no use-case
vocabulary entered the tree.

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports/judge.go internal/core/ports/judge_test.go
git commit -m "Name the three shapes a judgement can take"
```

---

### Task 3: The answer schema

**Files:**
- Create: `internal/core/app/answerschema.go`
- Test: `internal/core/app/answerschema_test.go`

**Interfaces:**
- Consumes: `ports.Question`, `ports.Kind`, `ports.NoulOptions`.
- Produces: `app.AnswerSchema(qs []ports.Question) ([]byte, error)` returning standard JSON
  Schema bytes; `app.OptionsFor(q ports.Question) []string` returning a question's effective
  options (its own, or the noul defaults).

The schema is what makes story 3.3 true: a `choice` cannot return anything outside its options,
because the enum forbids it.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/answerschema_test.go`:

```go
package app_test

import (
	"encoding/json"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func questions() []ports.Question {
	return []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "genre", Kind: ports.KindChoice, Ask: "Which genre?", Options: []string{"fiction", "history", "poetry"}},
		{ID: "length", Kind: ports.KindScore, Ask: "How long?", Options: []string{"short", "medium", "long"}},
	}
}

func decodeSchema(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("schema is not valid json: %v", err)
	}
	return m
}

func TestTheSchemaConstrainsEveryQuestionToItsOptions(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := decodeSchema(t, b)
	props, ok := s["properties"].(map[string]any)
	if !ok || len(props) != 3 {
		t.Fatalf("properties = %+v, want one per question", s["properties"])
	}
	genre, ok := props["genre"].(map[string]any)
	if !ok {
		t.Fatalf("no property for the choice question: %+v", props)
	}
	enum, ok := genre["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Fatalf("choice has no enum of its options: %+v", genre)
	}
}

func TestANoulIsConstrainedToYesAndNo(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	props := decodeSchema(t, b)["properties"].(map[string]any)
	enum := props["readable"].(map[string]any)["enum"].([]any)
	if len(enum) != 2 || enum[0] != "yes" || enum[1] != "no" {
		t.Errorf("noul enum = %v, want yes and no", enum)
	}
}

func TestEveryQuestionIsRequiredAndNothingElseIsAllowed(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := decodeSchema(t, b)
	req, ok := s["required"].([]any)
	if !ok || len(req) != 3 {
		t.Errorf("required = %v, want every question id", s["required"])
	}
	if s["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false; the model may otherwise answer questions nobody asked", s["additionalProperties"])
	}
}

func TestAChoiceWithoutOptionsIsAnError(t *testing.T) {
	_, err := app.AnswerSchema([]ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Which genre?"}})
	if err == nil {
		t.Fatal("a choice with no options produced a schema that constrains nothing")
	}
}

func TestADuplicateQuestionIDIsAnError(t *testing.T) {
	qs := []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "readable", Kind: ports.KindNoul, Ask: "Really?"},
	}
	if _, err := app.AnswerSchema(qs); err == nil {
		t.Fatal("two questions share an id and no error was returned; one answer would silently overwrite the other")
	}
}

func TestAnUnknownKindIsAnError(t *testing.T) {
	if _, err := app.AnswerSchema([]ports.Question{{ID: "x", Kind: "vibes", Ask: "?"}}); err == nil {
		t.Fatal("an unknown kind produced a schema")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Schema -v`
Expected: FAIL — `undefined: app.AnswerSchema`.

- [ ] **Step 3: Implement it**

Create `internal/core/app/answerschema.go`. `OptionsFor` returns `q.Options`, or
`ports.NoulOptions()` when the kind is noul and no options were given. `AnswerSchema` builds

```json
{"type":"object","properties":{"<id>":{"type":"string","enum":["..."]}},
 "required":["<id>"],"additionalProperties":false}
```

marshalled with `encoding/json`. Errors, each prefixed `judge: `: an empty question list, a
duplicate id, an unknown kind, a choice or score with fewer than two options, and an empty id.
Keep the function short; a helper that returns one property is fine.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/core/app/ -run Schema -v`
Expected: PASS, all six.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/answerschema.go internal/core/app/answerschema_test.go
git commit -m "Let the schema forbid an answer nobody offered"
```

---

### Task 4: Locating each question's answer token

**Files:**
- Create: `internal/core/app/answertokens.go`
- Test: `internal/core/app/answertokens_test.go`

**Interfaces:**
- Consumes: `ports.Completion`, `ports.Token`.
- Produces: `app.AnswerTokens(c ports.Completion, ids []string) (map[string]ports.Token, error)`.

This is the mechanism the whole increment turns on. `MassPerClass` reads the **last**
class-bearing token of a completion; with several questions in one completion that would read a
later question's answer as an earlier one's. Here each field's answer token is located
separately.

**The rule, and it goes in the doc comment:** reconstruct the completion's text from its tokens,
recording where each token starts. Walk the JSON with `encoding/json`'s `Decoder`, which reports
an input offset after each token it reads. For each top-level key, the value occupies the bytes
between the offset after the key and the offset after the value. The question's answer token is
the **first token starting in that range whose text carries a character other than whitespace,
`"`, `:` and `,`** — the punctuation around a value is structure, and the first token with
content is where the alternatives distinguish one option from another.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/answertokens_test.go`:

```go
package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// toks builds a completion whose text is the concatenation of the given token
// texts, which is how a real completion arrives.
func toks(texts ...string) ports.Completion {
	c := ports.Completion{}
	for _, t := range texts {
		c.Text += t
		c.Tokens = append(c.Tokens, ports.Token{Text: t, LogProb: -0.1})
	}
	return c
}

func TestEachFieldGetsItsOwnAnswerToken(t *testing.T) {
	c := toks(`{"`, `readable`, `":`, ` "`, `yes`, `",`, ` "`, `genre`, `":`, ` "`, `poetry`, `"}`)

	got, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["readable"].Text != "yes" {
		t.Errorf("readable answer token = %q, want yes", got["readable"].Text)
	}
	if got["genre"].Text != "poetry" {
		t.Errorf("genre answer token = %q, want poetry", got["genre"].Text)
	}
}

func TestTheFirstTokenOfAMultiTokenValueIsTheAnswerToken(t *testing.T) {
	// "medium" arrives as two tokens. The distinction between options is made
	// at the first of them.
	c := toks(`{"`, `length`, `":`, ` "`, `med`, `ium`, `"}`)

	got, err := app.AnswerTokens(c, []string{"length"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["length"].Text != "med" {
		t.Errorf("answer token = %q, want med; a later token cannot distinguish options that share a prefix", got["length"].Text)
	}
}

func TestFieldsAnsweredOutOfOrderAreStillKeyedCorrectly(t *testing.T) {
	c := toks(`{"`, `genre`, `":`, ` "`, `history`, `",`, ` "`, `readable`, `":`, ` "`, `no`, `"}`)

	got, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err != nil {
		t.Fatalf("answer tokens: %v", err)
	}
	if got["readable"].Text != "no" || got["genre"].Text != "history" {
		t.Errorf("answers are positional rather than keyed: %+v", map[string]string{
			"readable": got["readable"].Text, "genre": got["genre"].Text,
		})
	}
}

func TestAMissingFieldIsAnErrorNamingIt(t *testing.T) {
	c := toks(`{"`, `readable`, `":`, ` "`, `yes`, `"}`)

	_, err := app.AnswerTokens(c, []string{"readable", "genre"})
	if err == nil {
		t.Fatal("a question the model never answered produced no error")
	}
	if !contains(err.Error(), "genre") {
		t.Errorf("error does not name the missing question: %v", err)
	}
}

func TestACompletionWithNoTokensIsAnError(t *testing.T) {
	c := ports.Completion{Text: `{"readable": "yes"}`}
	if _, err := app.AnswerTokens(c, []string{"readable"}); err == nil {
		t.Fatal("a completion carrying no per-token data returned no error")
	}
}

func TestATokenStreamThatIsNotJSONIsAnError(t *testing.T) {
	c := toks(`I `, `think `, `yes`)
	if _, err := app.AnswerTokens(c, []string{"readable"}); err == nil {
		t.Fatal("prose was accepted as an answer object")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

Use `strings.Contains` instead of the helpers above if the test file already imports `strings`;
the helpers exist so the file compiles standalone.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run AnswerToken -v`
Expected: FAIL — `undefined: app.AnswerTokens`.

- [ ] **Step 3: Implement it**

Create `internal/core/app/answertokens.go`, implementing the rule stated above. Sketch of the
mechanism, which you should follow rather than invent an alternative:

```go
// starts[i] is the byte offset where token i begins in the reconstructed text.
text, starts := reconstruct(c.Tokens)

dec := json.NewDecoder(strings.NewReader(text))
// Read the opening brace, then alternate key, value. json.Decoder.InputOffset
// reports the offset just after the most recently read token, so the value of
// a key lies between the offset after the key and the offset after the value.
```

For each wanted id, find the first token index whose start is within its value's byte range and
whose text has a character other than whitespace, `"`, `:` and `,`. Errors, prefixed `judge: `:
empty `Tokens`, text that does not parse as a JSON object, a wanted id with no field, and a
field whose value range contains no such token. Ignore fields nobody asked about rather than
failing on them — `additionalProperties: false` already forbids them, and a model that emits one
anyway should not lose the answers that are present.

Keep `reconstruct` and the per-field scan as separate short functions.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/core/app/ -run AnswerToken -v`
Expected: PASS, all six.

- [ ] **Step 5: Prove the location rule by mutation**

Change the scan to take the **last** qualifying token in the range rather than the first, confirm
the mutation applied with `git diff`, and confirm `TestTheFirstTokenOfAMultiTokenValueIsTheAnswerToken`
fails. Revert and confirm it passes. Record it in your report.

- [ ] **Step 6: Commit**

```bash
git add internal/core/app/answertokens.go internal/core/app/answertokens_test.go
git commit -m "Find where in a completion each question was actually answered"
```

---

### Task 5: Reading mass at one token

**Files:**
- Modify: `internal/core/app/mass.go`
- Test: `internal/core/app/mass_test.go`

**Interfaces:**
- Consumes: `ports.Token`, `ports.Alternative`.
- Produces: `app.MassAtToken(tok ports.Token, classes map[string][]string) (map[string]float64, error)`.
  `MassPerClass` keeps its signature and is expressed in terms of `MassAtToken`, so one rule
  exists in one place.

- [ ] **Step 1: Write the failing test**

Add to `internal/core/app/mass_test.go`:

```go
func TestMassAtTokenReadsTheTokenItIsGiven(t *testing.T) {
	// The same fixture the package already uses, read at a chosen position
	// rather than by scanning for the last class-bearing token.
	c := recordedYesNo()
	structural := c.Tokens[0] // the brace the schema forced
	answer := c.Tokens[2]     // the answer-bearing token

	got, err := app.MassAtToken(answer, yesNo)
	if err != nil {
		t.Fatalf("mass at token: %v", err)
	}
	if got["yes"] < 0.90 {
		t.Errorf("yes = %.4f at the answer token, want at least 0.90", got["yes"])
	}

	// Reading the structural token is a different answer entirely, which is why
	// the caller must choose the position.
	structuralMass, err := app.MassAtToken(structural, yesNo)
	if err != nil {
		t.Fatalf("mass at structural token: %v", err)
	}
	if structuralMass["yes"] == got["yes"] {
		t.Error("the structural token and the answer token gave the same distribution; the position is being ignored")
	}
}

func TestMassAtATokenWithNoMatchingAlternativeIsAnError(t *testing.T) {
	tok := ports.Token{Text: "banana", Alternatives: []ports.Alternative{{Text: "apple", LogProb: -1}}}
	if _, err := app.MassAtToken(tok, yesNo); err == nil {
		t.Fatal("a token matching no class returned no error")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/core/app/ -run MassAtToken -v`
Expected: FAIL — `undefined: app.MassAtToken`.

- [ ] **Step 3: Extract it**

Move the per-token body of `MassPerClass` into `MassAtToken`: sum `math.Exp(logprob)` over
alternatives whose text matches a class surface form, case-sensitively; add the token's own
probability only when its own text is absent from its alternatives; normalise so the result sums
to one; error when nothing matches. `MassPerClass` keeps finding the last class-bearing token
and then calls `MassAtToken`. Its doc comment describes what it does now; do not narrate the
extraction.

- [ ] **Step 4: Run the whole package**

Run: `go test ./internal/core/app/ -race -v`
Expected: PASS, including every increment 2 mass test unchanged. If any of them needed editing,
the extraction changed behaviour — stop and report that rather than adjusting the tests.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/mass.go internal/core/app/mass_test.go
git commit -m "Read mass where the caller says, not only where the scan lands"
```

---

### Task 6: The local `Judge`

**Files:**
- Create: `internal/core/app/judge.go`
- Test: `internal/core/app/judge_test.go`

**Interfaces:**
- Consumes: `ports.Provider`, `ports.Prompt`, `ports.Completion`, `ports.Question`,
  `ports.Answer`, `ports.Judgement`, `app.AnswerSchema`, `app.OptionsFor`, `app.AnswerTokens`,
  `app.MassAtToken`.
- Produces: `app.NewJudge(p ports.Provider, cfg app.JudgeConfig) *app.Judge` implementing
  `ports.Judge`; `app.JudgeConfig{Temperature float64, Seed int, TopLogProbs int, MaxTokens int}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/judge_test.go`. `scriptedProvider` already exists in
`chain_test.go`; this file needs one that returns a fixed completion and records the prompt, so
give it a distinct name.

```go
package app_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type recordingProvider struct {
	completion ports.Completion
	calls      int
	last       ports.Prompt
}

func (p *recordingProvider) Name() string { return "recording" }

func (p *recordingProvider) Complete(_ context.Context, pr ports.Prompt) (ports.Completion, error) {
	p.calls++
	p.last = pr
	return p.completion, nil
}

// twoAnswers is a completion answering two questions, with alternatives that
// split the same answer across surface forms.
func twoAnswers() ports.Completion {
	tok := func(text string, alts ...ports.Alternative) ports.Token {
		return ports.Token{Text: text, LogProb: math.Log(0.2), Alternatives: alts}
	}
	plain := func(text string) ports.Token { return ports.Token{Text: text, LogProb: math.Log(0.9)} }

	c := ports.Completion{Model: "a-model"}
	add := func(t ports.Token) { c.Text += t.Text; c.Tokens = append(c.Tokens, t) }

	add(plain(`{"`))
	add(plain(`readable`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`yes`,
		ports.Alternative{Text: "Yes", LogProb: math.Log(0.70)},
		ports.Alternative{Text: "yes", LogProb: math.Log(0.25)},
		ports.Alternative{Text: "no", LogProb: math.Log(0.05)}))
	add(plain(`",`))
	add(plain(` "`))
	add(plain(`length`))
	add(plain(`":`))
	add(plain(` "`))
	add(tok(`long`,
		ports.Alternative{Text: "long", LogProb: math.Log(0.60)},
		ports.Alternative{Text: "medium", LogProb: math.Log(0.30)},
		ports.Alternative{Text: "short", LogProb: math.Log(0.10)}))
	add(plain(`"}`))
	return c
}

func judgeQuestions() []ports.Question {
	return []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "length", Kind: ports.KindScore, Ask: "How long is it?", Options: []string{"short", "medium", "long"}},
	}
}

func testJudgeConfig() app.JudgeConfig {
	return app.JudgeConfig{Temperature: 0, Seed: 7, TopLogProbs: 5, MaxTokens: 128}
}

func TestEveryQuestionIsAnsweredInOneCall(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}

	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("provider called %d times, want 1; questions asked separately invite contradictions", p.calls)
	}
	if len(got.Answers) != 2 {
		t.Fatalf("answers = %d, want 2", len(got.Answers))
	}
	if got.Model != "a-model" {
		t.Errorf("judgement does not record which model answered: %q", got.Model)
	}
}

func TestANoulCarriesSummedMassNotTheEmittedTokensProbability(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	var readable ports.Answer
	for _, a := range got.Answers {
		if a.ID == "readable" {
			readable = a
		}
	}
	if readable.Distribution["yes"] < 0.90 {
		t.Errorf("yes = %.4f, want at least 0.90; Yes and yes are the same answer", readable.Distribution["yes"])
	}
	if math.Abs(readable.Distribution["yes"]-0.25) < 0.01 {
		t.Error("the emitted token's own probability was reported instead of the summed mass")
	}
	if readable.Chosen != "yes" {
		t.Errorf("chosen = %q, want yes", readable.Chosen)
	}
}

func TestAScoreCarriesAProbabilityWeightedPosition(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	got, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	var length ports.Answer
	for _, a := range got.Answers {
		if a.ID == "length" {
			length = a
		}
	}
	// short=0, medium=1, long=2 weighted by 0.10, 0.30, 0.60.
	want := 0*0.10 + 1*0.30 + 2*0.60
	if math.Abs(length.Expected-want) > 0.01 {
		t.Errorf("expected = %.4f, want %.4f", length.Expected, want)
	}
	if length.Chosen != "long" {
		t.Errorf("chosen = %q, want long", length.Chosen)
	}
}

func TestTheRequestCarriesTheSchemaLogprobsAndPinnedSampling(t *testing.T) {
	p := &recordingProvider{completion: twoAnswers()}
	if _, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions()); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if len(p.last.Schema) == 0 || !strings.Contains(string(p.last.Schema), "enum") {
		t.Errorf("no enum schema was sent; a choice could answer anything: %s", p.last.Schema)
	}
	if p.last.TopLogProbs < 2 {
		t.Errorf("TopLogProbs = %d; without alternatives there is no mass to sum", p.last.TopLogProbs)
	}
	if p.last.Temperature == nil || *p.last.Temperature != 0 {
		t.Errorf("temperature = %v, want a pinned 0", p.last.Temperature)
	}
	if p.last.Seed == nil || *p.last.Seed != 7 {
		t.Errorf("seed = %v, want the configured 7", p.last.Seed)
	}
	if !strings.Contains(p.last.User, "a short book") {
		t.Error("the subject did not reach the prompt")
	}
	for _, q := range judgeQuestions() {
		if !strings.Contains(p.last.User, q.Ask) {
			t.Errorf("question %q did not reach the prompt", q.ID)
		}
	}
}

func TestAnAnswerTokenWithoutAlternativesIsAnErrorNamingTheQuestion(t *testing.T) {
	c := twoAnswers()
	for i := range c.Tokens {
		c.Tokens[i].Alternatives = nil
	}
	p := &recordingProvider{completion: c}

	_, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions())
	if err == nil {
		t.Fatal("a completion with no alternatives produced a distribution; that number would be invented")
	}
	if !strings.Contains(err.Error(), "readable") {
		t.Errorf("error does not name the question: %v", err)
	}
}

func TestAProviderFailureIsReturnedNotSwallowed(t *testing.T) {
	p := &failingProvider{}
	if _, err := app.NewJudge(p, testJudgeConfig()).Ask(context.Background(), "a short book", judgeQuestions()); err == nil {
		t.Fatal("a failing provider produced a judgement")
	}
}
```

Write `failingProvider` in the same file: a `ports.Provider` returning a fixed error.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Judge -v` and `go test ./internal/core/app/ -run Answer -v`
Expected: FAIL — `undefined: app.NewJudge`.

- [ ] **Step 3: Implement it**

Create `internal/core/app/judge.go`. `Ask`:

1. `AnswerSchema(qs)` for the schema.
2. Build the user message: the subject, then each question's id and text. State in the system
   message that every field must be answered with one of its allowed values and nothing else.
3. Call `Complete` once with `Schema`, `TopLogProbs`, `MaxTokens`, and pointers to the configured
   temperature and seed.
4. `AnswerTokens(c, ids)` for the positions.
5. For each question, `MassAtToken` against classes built from `OptionsFor(q)` and `q.Forms`
   (an option with no forms is its own only surface form). `Chosen` is the option holding the
   most mass. For a score, `Expected` is the sum over options of index times mass.
6. Return `ports.Judgement{Subject, Model: c.Model, When: time.Now().UTC(), Answers}`.

Every error is prefixed `judge: ` and names the question id where one applies. Do not store the
context. Keep `Ask` short by giving steps 5 and 6 their own functions.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/core/app/ -race -v`
Expected: PASS, the whole package.

- [ ] **Step 5: Prove the summed-mass guard by mutation**

Change the mass read to use the emitted token's own probability instead of `MassAtToken`, confirm
the mutation applied, and confirm `TestANoulCarriesSummedMassNotTheEmittedTokensProbability`
fails. Revert and confirm it passes.

- [ ] **Step 6: Commit**

```bash
git add internal/core/app/judge.go internal/core/app/judge_test.go
git commit -m "Answer every question in one call, with the mass each answer holds"
```

---

### Task 7: Recording a judgement

**Files:**
- Create: `internal/core/app/judgementrecord.go`
- Test: `internal/core/app/judgementrecord_test.go`

**Interfaces:**
- Consumes: `ports.Docs`, `ports.Index`, `ports.Judgement`, `ports.Question`.
- Produces: `app.RecordJudgement(ctx context.Context, docs ports.Docs, index ports.Index,
  subjectID string, qs []ports.Question, j ports.Judgement) (string, error)` returning the
  document path it wrote.

Story 3.5, and it lands with this epic rather than later: a judgement made before the record
exists can never be checked. Git is the system of record, so the judgement is a document; the
index row is derived and holds flat strings.

The core owns this use case and the core may not import an adapter, so its tests use in-memory
fakes implementing `ports.Docs` and `ports.Index`. Task 8 exercises it against the real
`gitdocs` and `sqlindex`.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/app/judgementrecord_test.go`:

```go
package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type fakeDocs struct {
	put map[string][]byte
}

func newFakeDocs() *fakeDocs { return &fakeDocs{put: map[string][]byte{}} }

func (d *fakeDocs) Put(_ context.Context, path string, body []byte, _ string) (ports.Revision, error) {
	d.put[path] = body
	return ports.Revision("rev-" + path), nil
}
func (d *fakeDocs) Get(_ context.Context, path string) ([]byte, error) { return d.put[path], nil }
func (d *fakeDocs) List(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (d *fakeDocs) History(_ context.Context, _ string) ([]ports.DocMeta, error) { return nil, nil }
func (d *fakeDocs) GetAt(_ context.Context, path string, _ ports.Revision) ([]byte, error) {
	return d.put[path], nil
}

type fakeIndex struct{ rows []ports.Record }

func (i *fakeIndex) Upsert(_ context.Context, r ports.Record) error {
	i.rows = append(i.rows, r)
	return nil
}
func (i *fakeIndex) Find(_ context.Context, _ ports.Query) ([]ports.Record, error) { return i.rows, nil }
func (i *fakeIndex) Reset(_ context.Context) error                                 { return nil }
func (i *fakeIndex) Close() error                                                  { return nil }

func aJudgement() ports.Judgement {
	return ports.Judgement{
		Subject: "a short book",
		Model:   "a-model",
		When:    time.Date(2026, 9, 22, 11, 4, 2, 0, time.UTC),
		Answers: []ports.Answer{{
			ID:           "readable",
			Kind:         ports.KindNoul,
			Chosen:       "yes",
			Distribution: map[string]float64{"yes": 0.94, "no": 0.06},
		}},
	}
}

func recordQuestions() []ports.Question {
	return []ports.Question{{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"}}
}

func TestTheDocumentCarriesTheQuestionTheAnswerAndAnEmptyOutcome(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}

	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	body, ok := docs.put[path]
	if !ok {
		t.Fatalf("nothing was written at the returned path %q", path)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}
	if doc["subject_id"] != "subject-1" {
		t.Errorf("document does not carry the subject id: %+v", doc["subject_id"])
	}
	outcome, present := doc["outcome"]
	if !present {
		t.Error("the document has no outcome slot; a later increment has nowhere to record what happened")
	}
	if outcome != nil {
		t.Errorf("outcome = %v, want null until an outcome is known", outcome)
	}
	if _, present := doc["questions"]; !present {
		t.Error("the document does not record the questions as asked")
	}
	if _, present := doc["answers"]; !present {
		t.Error("the document does not record the answers")
	}
}

func TestThePathIsKeyedBySubjectSoJudgementsOfOneSubjectSitTogether(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !strings.HasPrefix(path, "judgements/subject-1/") {
		t.Errorf("path = %q, want it under judgements/subject-1/", path)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("path = %q, want a .json document", path)
	}
}

func TestJudgingTheSameSubjectTwiceDoesNotOverwriteTheFirst(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	first := aJudgement()
	second := aJudgement()
	second.When = first.When.Add(time.Minute)

	p1, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	p2, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both judgements landed at %q; the first is gone", p1)
	}
	if len(docs.put) != 2 {
		t.Errorf("documents written = %d, want 2", len(docs.put))
	}
}

func TestTheIndexRowMakesAPendingJudgementFindable(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(index.rows) != 1 {
		t.Fatalf("index rows = %d, want 1", len(index.rows))
	}
	row := index.rows[0]
	if row.Kind != "judgement" {
		t.Errorf("kind = %q, want judgement", row.Kind)
	}
	if row.Fields["subject_id"] != "subject-1" {
		t.Errorf("fields do not carry the subject id: %+v", row.Fields)
	}
	if row.Fields["outcome"] != "pending" {
		t.Errorf("outcome field = %q, want pending; an awaiting judgement must be findable by query", row.Fields["outcome"])
	}
	if row.Rev == "" {
		t.Error("the index row does not carry the revision the document was written at")
	}
}

func TestAnEmptySubjectIDIsAnError(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordJudgement(context.Background(), docs, index, "", recordQuestions(), aJudgement()); err == nil {
		t.Fatal("a judgement with no subject id was recorded; nothing could ever attach an outcome to it")
	}
}

func TestAJudgementWithNoAnswersIsAnError(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	empty := aJudgement()
	empty.Answers = nil
	if _, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), empty); err == nil {
		t.Fatal("an empty judgement was recorded")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Judgement -v`
Expected: FAIL — `undefined: app.RecordJudgement`.

- [ ] **Step 3: Implement it**

Create `internal/core/app/judgementrecord.go`.

The document is `encoding/json` with indentation, carrying `subject_id`, `subject`, `model`,
`when` (RFC 3339, UTC), `questions` (id, kind, ask, options), `answers` (id, kind, chosen,
distribution, and expected for a score), and `outcome` explicitly `null`. Use a struct with a
`*string` or `any` outcome field so the key is present and null rather than omitted.

The path is `judgements/<subject-id>/<when formatted as 2006-01-02T15-04-05Z>.json`. Colons are
not portable in file names, so the timestamp uses dashes.

The index row is `ports.Record{Path: path, Rev: rev, Kind: "judgement", When: j.When, Fields:
map[string]string{"subject_id": ..., "model": ..., "questions": comma-joined ids,
"outcome": "pending"}}`. `Index` fields are strings, so a distribution stays in the document
alone; the row exists to find a judgement, not to hold its numbers.

Errors are prefixed `judgement: `: an empty subject id, an empty answer list, a `Put` failure, an
`Upsert` failure. A failed `Upsert` after a successful `Put` returns the error and names the path
that was written, since git already holds the record and the index is rebuildable.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/core/app/ -run Judgement -v`
Expected: PASS, all six.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/judgementrecord.go internal/core/app/judgementrecord_test.go
git commit -m "Record a judgement beside the slot its outcome will fill"
```

---

### Task 8: The `judge.ask` tool

**Files:**
- Create: `internal/adapters/outbound/tools/judge.go`
- Test: `internal/adapters/outbound/tools/judge_test.go`

**Interfaces:**
- Consumes: `ports.Judge`, `ports.Docs`, `ports.Index`, `app.RecordJudgement`.
- Produces: `tools.NewJudge(j ports.Judge, docs ports.Docs, index ports.Index) *tools.Judge`
  implementing `ports.Tool` with `Name() == "judge.ask"`.

`with` arrives fully rendered and is `map[string]string`, so the questions come as a YAML block
the tool parses. The pack format itself does not change.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/outbound/tools/judge_test.go`. This test uses the real `gitdocs` and
`sqlindex` adapters over `t.TempDir()`, so the record is proven end to end; only the `Judge` is a
stub, because a real one needs a model.

```go
package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

type stubJudge struct {
	calls    int
	subject  string
	asked    []ports.Question
	fail     error
}

func (s *stubJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	s.calls++
	s.subject = subject
	s.asked = qs
	if s.fail != nil {
		return ports.Judgement{}, s.fail
	}
	answers := make([]ports.Answer, 0, len(qs))
	for _, q := range qs {
		answers = append(answers, ports.Answer{
			ID: q.ID, Kind: q.Kind, Chosen: "yes",
			Distribution: map[string]float64{"yes": 0.94, "no": 0.06},
			Expected:     1.5,
		})
	}
	return ports.Judgement{Subject: subject, Model: "a-model", When: time.Now().UTC(), Answers: answers}, nil
}

func store(t *testing.T) (*gitdocs.Store, *sqlindex.Index) {
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

const questionsYAML = `
- id: readable
  type: noul
  ask: Is it readable?
- id: genre
  type: choice
  options: [fiction, history, poetry]
  ask: Which genre is it?
- id: length
  type: score
  levels: [short, medium, long]
  ask: How long is it?
`

func TestTheToolParsesEveryQuestionKind(t *testing.T) {
	j := &stubJudge{}
	docs, index := store(t)

	_, err := tools.NewJudge(j, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1",
		"subject":    "a short book",
		"questions":  questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if j.calls != 1 {
		t.Errorf("judge called %d times, want 1", j.calls)
	}
	if len(j.asked) != 3 {
		t.Fatalf("questions asked = %d, want 3", len(j.asked))
	}
	byID := map[string]ports.Question{}
	for _, q := range j.asked {
		byID[q.ID] = q
	}
	if byID["readable"].Kind != ports.KindNoul {
		t.Errorf("readable kind = %q", byID["readable"].Kind)
	}
	if len(byID["genre"].Options) != 3 {
		t.Errorf("choice options did not reach the judge: %+v", byID["genre"])
	}
	if len(byID["length"].Options) != 3 {
		t.Errorf("score levels did not reach the judge as options: %+v", byID["length"])
	}
	if byID["genre"].Ask == "" {
		t.Error("the question text did not reach the judge")
	}
}

func TestTheResultIsKeyedByQuestionIDForLaterSteps(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %T, want map[string]any", out)
	}
	readable, ok := m["readable"].(map[string]any)
	if !ok {
		t.Fatalf("no answer keyed by question id: %+v", m)
	}
	if readable["chosen"] != "yes" {
		t.Errorf("chosen = %v", readable["chosen"])
	}
	p, ok := readable["p"].(float64)
	if !ok || p < 0.9 {
		t.Errorf("p = %v, want the chosen option's mass", readable["p"])
	}
	if _, ok := readable["distribution"]; !ok {
		t.Error("the distribution is not available to a later step")
	}
	if m["path"] == nil || m["path"] == "" {
		t.Error("the tool does not report where the judgement was recorded")
	}
}

func TestTheJudgementIsOnDiskWithAnEmptyOutcome(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	path := out.(map[string]any)["path"].(string)

	body, err := docs.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}
	if outcome, present := doc["outcome"]; !present || outcome != nil {
		t.Errorf("outcome = %v present = %v, want a present null slot", outcome, present)
	}

	rows, err := index.Find(context.Background(), ports.Query{Kind: "judgement", Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 || rows[0].Fields["subject_id"] != "subject-1" {
		t.Errorf("the judgement is not findable in the index: %+v", rows)
	}
}

func TestAMissingSubjectIDIsAnError(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject": "a short book", "questions": questionsYAML,
	})
	if err == nil {
		t.Fatal("a judgement with no subject id was accepted; nothing could attach an outcome to it later")
	}
	if !strings.Contains(err.Error(), "judge.ask: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
}

func TestMalformedQuestionsAreAnErrorNotAnEmptyJudgement(t *testing.T) {
	docs, index := store(t)
	for _, tc := range []struct{ name, questions string }{
		{"not yaml", "id: [unclosed"},
		{"no questions", ""},
		{"missing id", "- type: noul\n  ask: Is it readable?"},
		{"unknown type", "- id: x\n  type: vibes\n  ask: Well?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
				"subject_id": "subject-1", "subject": "a short book", "questions": tc.questions,
			})
			if err == nil {
				t.Fatal("malformed questions produced a judgement")
			}
		})
	}
}

func TestAJudgeFailureRecordsNothing(t *testing.T) {
	docs, index := store(t)
	j := &stubJudge{fail: errFailed}
	_, err := tools.NewJudge(j, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err == nil {
		t.Fatal("a failing judge produced a result")
	}
	rows, findErr := index.Find(context.Background(), ports.Query{Kind: "judgement", Limit: 10})
	if findErr != nil {
		t.Fatalf("find: %v", findErr)
	}
	if len(rows) != 0 {
		t.Errorf("a failed judgement was recorded anyway: %+v", rows)
	}
}
```

Declare `errFailed` in the same file with `errors.New("judge is down")`.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/adapters/outbound/tools/ -run Judge -v`
Expected: FAIL — `undefined: tools.NewJudge`.

- [ ] **Step 3: Implement it**

Create `internal/adapters/outbound/tools/judge.go`. `Invoke`:

1. Require `subject_id` and `subject`; each missing one is an error prefixed `judge.ask: `.
2. Parse `with["questions"]` with `gopkg.in/yaml.v3` into a local unexported struct
   `{ID string `yaml:"id"`; Type string `yaml:"type"`; Ask string `yaml:"ask"`; Options []string
   `yaml:"options"`; Levels []string `yaml:"levels"`; Forms map[string][]string `yaml:"forms"`}`.
   Map it to `[]ports.Question`: `options` and `levels` both fill `Options`, a question naming
   both is an error, and an unknown type is an error naming it.
3. Call `Ask` once.
4. `app.RecordJudgement` with the subject id and the questions as asked.
5. Return `map[string]any` keyed by question id, each `{"chosen", "p", "distribution"}` plus
   `"expected"` for a score, and a top-level `"path"` with the document path. `p` is the chosen
   option's mass.

An adapter importing `internal/core/app` is fine: dependencies point inward.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/adapters/outbound/tools/ -race -v`
Expected: PASS, including the increment 2 model tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/tools/judge.go internal/adapters/outbound/tools/judge_test.go
git commit -m "Put typed judgement behind a tool a pack can name"
```

---

### Task 9: Config and the composition root

**Files:**
- Modify: `internal/config/config.go`, `internal/config/layers.go`, `internal/config/config_test.go`
- Modify: `cmd/atlas/main.go`, `cmd/atlas/main_test.go`
- Test: `cmd/atlas/main_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `app.NewJudge`, `app.JudgeConfig`, `tools.NewJudge`, `gitdocs.Open`, `sqlindex.Open`.
- Produces: `config.JudgeConfig{Temperature float64, Seed int, TopLogProbs int, MaxTokens int}`
  on `config.Config` as `Judge`; `buildRegistry(cfg config.Config, docs ports.Docs,
  index ports.Index) tools.Registry`.

`buildRegistry` today builds the HTTP and model tools from config alone. `judge.ask` is the
first tool needing the store, and nothing opens the store today, so `run()` opens both and hands
them in. The three guard tests still hold afterwards: no `defer` in `main()`, `os.Exit` only in
`main()`, no literal in `buildRegistry`.

- [ ] **Step 1: Write the failing config tests**

Add to `internal/config/config_test.go`, following the existing env and flag test patterns
exactly:

```go
func TestJudgeDefaultsArePinnedForReproducibility(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.Temperature != 0 {
		t.Errorf("Judge.Temperature = %v, want 0; a judged probability must not move between runs", cfg.Judge.Temperature)
	}
	if cfg.Judge.TopLogProbs < 2 {
		t.Errorf("Judge.TopLogProbs = %d, want at least 2; without alternatives there is no mass to sum", cfg.Judge.TopLogProbs)
	}
	if cfg.Judge.MaxTokens <= 0 {
		t.Errorf("Judge.MaxTokens = %d, want a positive default", cfg.Judge.MaxTokens)
	}
}

func TestJudgeTopLogProbsEnvVar(t *testing.T) {
	t.Setenv("ATLAS_JUDGE_TOP_LOGPROBS", "9")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.TopLogProbs != 9 {
		t.Errorf("Judge.TopLogProbs = %d, want 9", cfg.Judge.TopLogProbs)
	}
}

func TestJudgeSeedFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_JUDGE_SEED", "3")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-judge-seed", "11"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.Seed != 11 {
		t.Errorf("Judge.Seed = %d, want 11; flags are the last layer", cfg.Judge.Seed)
	}
}

func TestAnInvalidJudgeTopLogProbsIsRejectedAtStartup(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-judge-top-logprobs", "0"}); err == nil {
		t.Fatal("zero top logprobs was accepted; the judge would have no alternatives to sum")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/config/ -run Judge -v`
Expected: FAIL — `cfg.Judge undefined`.

- [ ] **Step 3: Add the config**

Add `Judge JudgeConfig` to `config.Config` and `JudgeConfig` beside `ModelConfig`. Defaults in
`defaults()`: `Temperature: 0`, `Seed: 1`, `TopLogProbs: 5`, `MaxTokens: 256`. Env vars
`ATLAS_JUDGE_TEMPERATURE`, `ATLAS_JUDGE_SEED`, `ATLAS_JUDGE_TOP_LOGPROBS`,
`ATLAS_JUDGE_MAX_TOKENS`; flags `-judge-temperature`, `-judge-seed`, `-judge-top-logprobs`,
`-judge-max-tokens`. Follow the existing layering exactly. `validate()` rejects `TopLogProbs < 2`,
`MaxTokens <= 0` and a negative temperature, each with a message naming the setting.

- [ ] **Step 4: Write the failing composition-root test**

Add to `cmd/atlas/main_test.go`, following `TestBuildRegistryRegistersBothShippedTools`:

```go
func TestBuildRegistryRegistersTheJudgeTool(t *testing.T) {
	docs, index := testStore(t)
	r := buildRegistry(config.Config{}, docs, index)
	for _, name := range []string{"http.request", "model.complete", "judge.ask"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}
```

Write `testStore` in the same file, opening `gitdocs` and `sqlindex` over `t.TempDir()` and
closing the index in `t.Cleanup`.

- [ ] **Step 5: Run it and watch it fail**

Run: `go test ./cmd/atlas/ -run BuildRegistry -v`
Expected: FAIL — `too many arguments in call to buildRegistry`.

- [ ] **Step 6: Wire it**

In `cmd/atlas/main.go`:

- `buildRegistry(cfg config.Config, docs ports.Docs, index ports.Index) tools.Registry` builds
  the provider as it does now, then `app.NewJudge(provider, app.JudgeConfig{...from cfg.Judge})`,
  then registers `tools.NewJudge(judge, docs, index)` alongside the existing two. No literal
  enters the function — the guard test walks its AST.
- `run()` opens `gitdocs.Open(ctx, cfg.Store.Root)` and `sqlindex.Open(ctx, cfg.Store.IndexPath)`,
  defers the index's `Close`, and passes both in. `defer` in `run()` is fine; the guard forbids it
  in `main()` only.

- [ ] **Step 7: Run everything**

```bash
go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

Expected: PASS, and the pack still runs unaided on default config, which proves opening the store
at startup broke nothing. Paste the output. If `hn-summary` needs a model, leave it to Task 10
and say so.

- [ ] **Step 8: Commit**

```bash
git add internal/config cmd/atlas
git commit -m "Open the record at startup and hand the judge its store"
```

---

### Task 10: A real pack step, the live proof, and the note

**Files:**
- Modify: `packs/job-hunt.yaml`
- Create: `docs/notes/2026-09-22-increment-3.md`
- Modify: `docs/design/the-provider.md`
- Create: `internal/adapters/outbound/openaiprov/judge_live_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing further.

- [ ] **Step 1: Add the live check**

Create `internal/adapters/outbound/openaiprov/judge_live_test.go`, matching the existing live
tests exactly: skipped unless `ATLAS_LIVE_PROVIDER` is set, reading `ATLAS_LIVE_BASE_URL`
(default `http://localhost:11434/v1`) and `ATLAS_LIVE_MODEL` (default `qwen2.5-coder:7b`). It
asks a real engine three questions in one call about a neutral subject — no use-case vocabulary —
one of each kind, and asserts:

- every question is answered and keyed by its id;
- the `choice` answer is one of its options **even though the system message tells the model to
  answer with something else entirely**. This is story 3.3, and only a real engine proves it;
- the obvious `noul` holds more than 0.8;
- it logs each answer, its chosen option, its mass and, for the score, its expected position.

An adapter test may import `internal/core/app`, so build the judge with `app.NewJudge` over a
real `openaiprov.Client`.

- [ ] **Step 2: Add the pack step**

In `packs/job-hunt.yaml`, add a step after `role` that judges the posting with `judge.ask`, using
the real board id as `subject_id`:

```yaml
  - id: judged
    tool: judge.ask
    with:
      subject_id: "{{ .steps.role.id }}"
      subject: |
        Candidate: {{ .vars.profile }}

        Role: {{ .steps.role.title }} at {{ .steps.role.company_name }}

        {{ .steps.role.content }}
      questions: |
        - id: stretch
          type: noul
          ask: Is this role a stretch for the candidate?
        - id: seniority
          type: score
          levels: [junior, mid, senior, staff]
          ask: How senior is this role?
        - id: focus
          type: choice
          options: [backend, frontend, platform, data, other]
          ask: What is the main focus of this role?
```

Leave the existing `verdict` and `documents` steps alone: the prose verdict is epic 7's to
replace, and this increment proves the typed path beside it.

- [ ] **Step 3: Run it against a live engine**

The machine owner starts one engine. On this laptop vLLM needs its measured flags, recorded in
`docs/notes/2026-09-21-increment-2.md`; Ollama needs `sudo systemctl start ollama`. Then:

```bash
ATLAS_LIVE_PROVIDER=1 go test ./internal/adapters/outbound/openaiprov/ -run Live -v
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Paste both outputs, and paste the judgement document from the store root
(`~/.atlas/workspace/judgements/<id>/<timestamp>.json`) and the index row. **This is the
increment's headline evidence.** If no engine can be started, say so plainly and record the block
rather than describing what would have happened.

- [ ] **Step 4: Write the increment note**

Create `docs/notes/2026-09-22-increment-3.md` with four sections: what the pattern was, what
surprised you, what you would do differently, what you still do not understand.

Record honestly:

- What the answer-token location rule did against a real completion, and whether any answer
  arrived as several tokens.
- What each answer's mass was, and how it compares with the emitted token's own probability —
  the comparison increment 2 could not make because its live question was too easy.
- Whether the constrained `choice` actually held when the model was told to misbehave.
- Whether `Question` and `Answer` are the right shapes, or whether epic 7 will want something
  they cannot express.
- Whether pinned sampling made two engines agree, if both were run.

A note saying everything went fine is worthless. If everything did go fine, say what that
suggests is under-tested.

- [ ] **Step 5: Update the design reference**

Add the `Judge` port, the answer-token rule and the judgement record to a design document —
`docs/design/the-provider.md` if it stays coherent, or a sibling if that file is the wrong home.
Say which you chose and why in your report. Update that file's Known gaps: the entry saying
`MassPerClass` reads one position per completion is now stale.

- [ ] **Step 6: Run everything and commit**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
git add packs docs internal/adapters/outbound/openaiprov
git commit -m "Judge a real subject through a pack, and record what it cost"
```

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| Checking a probability against an outcome | Needs applications with results. Epic 11 closes the loop; this increment leaves the slot |
| The fit judgement, deal-breakers and disagreement | Epic 7, which is this epic pointed at real postings |
| TypeSafe | Hosted, opt-in, and a second adapter behind the port this increment defines |
| Error classification and wiring `Chain` | Carried from increment 2; it belongs with the increment that gives the chain a production caller |
| Judging many subjects in one call | Story 3.2 is many questions about one subject; a board is epic 7 |
| Rebuilding the index from judgement documents | `Rebuild` exists and has no production caller; that gap is the record's, not the judge's |
