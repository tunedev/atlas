# Increment 1a — Step Model Tracer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fetch one real job posting, score it with a real model, and draft an application package — through a uniform step model, with a trace covering every step.

**Architecture:** Three steps implement one `Step` port and are composed into a `Blueprint` that runs them in order over a typed `State`. The blueprint runner is the single place that opens a span per step, so instrumentation is not repeated per step. Adapters reach Greenhouse's public board API and an OpenAI-compatible model endpoint. ADK is deliberately absent — it arrives in increment 1b so that only one unknown is in play here.

**Tech Stack:** Go 1.24.6, OpenTelemetry OTLP gRPC, Greenhouse public board API, Ollama's OpenAI-compatible endpoint.

**Spec:** `docs/specs/2026-08-24-agent-substrate-design.md`

## Global Constraints

- Go 1.24.6. Module path `forge/atlas`, matching `forge/hello`.
- Hexagonal layout per `../../../CLAUDE.md` Tenet 1: `internal/core/{domain,ports,app}`, `internal/adapters/{inbound,outbound}`, `cmd/atlas/main.go` as the only file knowing every concrete type.
- Ports are written in the core's vocabulary. No core package imports an adapter or a driver, enforced by `internal/arch`.
- `ctx context.Context` is the first parameter of every blocking or remote function. No `Context` stored in a struct.
- Every remote call has a timeout. The default is not "no timeout".
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis in code, log lines, or output.
- Comments describe current behaviour only. No history, no bug narratives, no dates.

## Deviations from the spec's increment 1

The spec describes increment 1 as one posting end to end *on ADK*. This plan is 1a and
stops short of three things it lists, all deliberately.

| Spec row | Status in 1a | Why |
|---|---|---|
| "by a blueprint running on ADK" | Deferred to 1b | ADK Go's OpenAI-compatible path is undocumented; every Go example uses `gemini.NewModel`. Carrying it here would put two unknowns in one increment, and when it broke you would not know which one lied. |
| "Whatever storage works" | Absent | 1a prints its result. Persistence means a database, a schema and migrations — a third unknown for no gain, since nothing yet reads back what a previous run wrote. |
| "Sandbox running one trusted step" | Absent | The spec already lists this as an open question. A first-party step in-process needs no boundary; the boundary arrives with untrusted code. |

Everything else in the spec's increment 1 table is covered.

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module `forge/atlas` |
| `internal/config/config.go` | Typed config, validated at startup |
| `internal/config/layers.go` | defaults, file, env, flags — later wins |
| `internal/arch/arch_test.go` | Fails if core imports an adapter or driver |
| `internal/core/domain/posting.go` | `Posting` |
| `internal/core/domain/verdict.go` | `Verdict` |
| `internal/core/domain/package.go` | `Package`, `Profile` |
| `internal/core/domain/state.go` | `State` — what flows through a blueprint |
| `internal/core/ports/ports.go` | `Step`, `PostingSource`, `Model` |
| `internal/core/app/blueprint.go` | Ordered composition, one span per step |
| `internal/core/app/fetch.go` | Fetch step |
| `internal/core/app/score.go` | Score step |
| `internal/core/app/draft.go` | Draft step |
| `internal/adapters/outbound/greenhouse/client.go` | Greenhouse board API, HTML cleanup |
| `internal/adapters/outbound/llm/client.go` | OpenAI-compatible chat completions |
| `internal/telemetry/telemetry.go` | OTLP trace pipeline |
| `cmd/atlas/main.go` | Composition root |

---

### Task 1: Module scaffold, config, and the architecture guard

The guard comes first. A tenet you cannot fail is decoration, and it is much cheaper to add before there is anything to fix.

**Files:**
- Create: `go.mod`, `internal/config/config.go`, `internal/config/layers.go`
- Test: `internal/arch/arch_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `config.Config` with fields `Greenhouse GreenhouseConfig`, `Model ModelConfig`, `OTel OTelConfig`; `config.Load(args []string) (Config, error)`

- [ ] **Step 1: Initialise the module**

```bash
cd /home/tunedev/forge/atlas
go mod init forge/atlas
```

- [ ] **Step 2: Write the architecture test**

Create `internal/arch/arch_test.go`:

```go
package arch_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The core owns its interfaces and must not import any driver or adapter.
// This is the mechanical form of "dependencies point inward".
func TestCoreImportsNoAdapters(t *testing.T) {
	// Module-qualified, not dot-relative: go test runs this binary with cwd
	// set to this package's directory, never the module root.
	out, err := exec.Command("go", "list", "-deps", "forge/atlas/internal/core/...").Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}
	// Adapter tree and third-party drivers only. Never list stdlib packages:
	// go list -deps is transitive, so net/http would fail this for the wrong reason.
	forbidden := []string{
		"forge/atlas/internal/adapters",
		"go.opentelemetry.io/otel/exporters",
	}
	for _, dep := range strings.Split(string(out), "\n") {
		for _, bad := range forbidden {
			if strings.HasPrefix(strings.TrimSpace(dep), bad) {
				t.Errorf("core imports %q; dependencies must point inward", dep)
			}
		}
	}
}
```

- [ ] **Step 3: Write the failing config test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"testing"

	"forge/atlas/internal/config"
)

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Greenhouse.Board == "" {
		t.Error("Greenhouse.Board has no default")
	}
	if cfg.Greenhouse.Timeout == 0 {
		t.Error("Greenhouse.Timeout defaults to zero; every remote call needs a timeout")
	}
	if cfg.Model.Timeout == 0 {
		t.Error("Model.Timeout defaults to zero")
	}
}

func TestEnvOverridesDefault(t *testing.T) {
	t.Setenv("ATLAS_GREENHOUSE_BOARD", "someco")
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Greenhouse.Board != "someco" {
		t.Errorf("Board = %q, want someco", cfg.Greenhouse.Board)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_GREENHOUSE_BOARD", "fromenv")
	cfg, err := config.Load([]string{"-greenhouse-board", "fromflag"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Greenhouse.Board != "fromflag" {
		t.Errorf("Board = %q, want fromflag; flags are the last layer", cfg.Greenhouse.Board)
	}
}

func TestZeroTimeoutIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-model-timeout", "0s"}); err == nil {
		t.Error("Load accepted a zero model timeout; invalid config must fail at boot")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/...`
Expected: FAIL — `internal/config` does not exist.

- [ ] **Step 5: Write config.go**

Create `internal/config/config.go`:

```go
// Package config loads atlas's configuration in layers: defaults, then
// environment, then flags — later layers win. The result is validated once at
// startup; nothing here is read in a request path.
package config

import (
	"fmt"
	"time"
)

// Config is the fully resolved, validated configuration for the atlas binary.
type Config struct {
	Greenhouse GreenhouseConfig
	Model      ModelConfig
	OTel       OTelConfig
}

type GreenhouseConfig struct {
	BaseURL string
	Board   string
	Timeout time.Duration
}

type ModelConfig struct {
	BaseURL string
	Name    string
	Timeout time.Duration
}

type OTelConfig struct {
	Endpoint      string
	ExportTimeout time.Duration
	Enabled       bool
}

func (c Config) validate() error {
	if c.Greenhouse.Board == "" {
		return fmt.Errorf("config: greenhouse board is empty")
	}
	if c.Greenhouse.Timeout <= 0 {
		return fmt.Errorf("config: greenhouse timeout must be positive, got %s", c.Greenhouse.Timeout)
	}
	if c.Model.Timeout <= 0 {
		return fmt.Errorf("config: model timeout must be positive, got %s", c.Model.Timeout)
	}
	if c.Model.Name == "" {
		return fmt.Errorf("config: model name is empty")
	}
	return nil
}
```

- [ ] **Step 6: Write layers.go**

Create `internal/config/layers.go`:

```go
package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// Load resolves configuration from defaults, then environment, then flags.
// Pass nil args to skip the flag layer. A model call is slow on a laptop, so
// the model timeout default is generous where the HTTP one is not.
func Load(args []string) (Config, error) {
	cfg := defaults()
	applyEnv(&cfg)
	if err := applyFlags(&cfg, args); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		Greenhouse: GreenhouseConfig{
			BaseURL: "https://boards-api.greenhouse.io",
			Board:   "gitlab",
			Timeout: 15 * time.Second,
		},
		Model: ModelConfig{
			BaseURL: "http://localhost:11434/v1",
			Name:    "qwen3.5:9b",
			Timeout: 5 * time.Minute,
		},
		OTel: OTelConfig{
			Endpoint:      "localhost:4317",
			ExportTimeout: 10 * time.Second,
			Enabled:       false,
		},
	}
}

func applyEnv(c *Config) {
	if v := os.Getenv("ATLAS_GREENHOUSE_BOARD"); v != "" {
		c.Greenhouse.Board = v
	}
	if v := os.Getenv("ATLAS_MODEL_BASE_URL"); v != "" {
		c.Model.BaseURL = v
	}
	if v := os.Getenv("ATLAS_MODEL_NAME"); v != "" {
		c.Model.Name = v
	}
	if v := os.Getenv("ATLAS_OTEL_ENDPOINT"); v != "" {
		c.OTel.Endpoint = v
		c.OTel.Enabled = true
	}
}

func applyFlags(c *Config, args []string) error {
	if args == nil {
		return nil
	}
	fs := flag.NewFlagSet("atlas", flag.ContinueOnError)
	fs.StringVar(&c.Greenhouse.Board, "greenhouse-board", c.Greenhouse.Board, "greenhouse board token")
	fs.StringVar(&c.Model.BaseURL, "model-base-url", c.Model.BaseURL, "OpenAI-compatible base URL")
	fs.StringVar(&c.Model.Name, "model-name", c.Model.Name, "model identifier")
	fs.DurationVar(&c.Model.Timeout, "model-timeout", c.Model.Timeout, "model call timeout")
	fs.BoolVar(&c.OTel.Enabled, "otel", c.OTel.Enabled, "export traces over OTLP")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	return nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/...`
Expected: PASS. The arch test passes vacuously for now — `internal/core` does not exist yet, and `go list` on a missing pattern is what Task 2 makes meaningful.

- [ ] **Step 8: Commit**

```bash
git add go.mod internal/
git commit -m "Add atlas module scaffold, layered config, and the architecture guard

The guard lands before there is anything to guard, because a tenet you
cannot fail is decoration and retrofitting one means fixing violations
rather than preventing them.

Config is defaults, then environment, then flags, validated at boot. Every
timeout has a positive default and a zero is rejected at startup rather
than in a call path."
```

---

### Task 2: Domain types and the Step port

This is the increment's actual claim: three unlike operations share one interface. If they end up as three bespoke functions with a call chain, the substrate does not exist.

**Files:**
- Create: `internal/core/domain/posting.go`, `internal/core/domain/verdict.go`, `internal/core/domain/package.go`, `internal/core/domain/state.go`, `internal/core/ports/ports.go`
- Test: `internal/core/domain/state_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `domain.Posting{ID, Title, Company, Location, URL, Body string}`
  - `domain.Verdict{Decision string, Reasons []string}` with `domain.DecisionApply/Stretch/Skip`
  - `domain.Profile{Summary string, Skills []string}`
  - `domain.Package{CV, CoverLetter string}`
  - `domain.State{Profile Profile; Posting *Posting; Verdict *Verdict; Package *Package}`
  - `ports.Step` with `Name() string` and `Run(ctx, domain.State) (domain.State, error)`
  - `ports.PostingSource` with `Latest(ctx, board string) (domain.Posting, error)`
  - `ports.Model` with `Complete(ctx, system, user string) (string, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/core/domain/state_test.go`:

```go
package domain_test

import (
	"testing"

	"forge/atlas/internal/core/domain"
)

// State threads through a blueprint by value. A step returns a new State
// rather than mutating a shared one, so a failed step cannot leave half its
// output behind for the next step to read.
func TestWithPostingDoesNotMutateReceiver(t *testing.T) {
	original := domain.State{Profile: domain.Profile{Summary: "go engineer"}}

	next := original.WithPosting(domain.Posting{ID: "1", Title: "Backend Engineer"})

	if original.Posting != nil {
		t.Error("WithPosting mutated the receiver")
	}
	if next.Posting == nil || next.Posting.Title != "Backend Engineer" {
		t.Error("WithPosting did not set the posting on the returned State")
	}
	if next.Profile.Summary != "go engineer" {
		t.Error("WithPosting dropped the profile")
	}
}

func TestDecisionsAreClosedSet(t *testing.T) {
	for _, d := range []string{domain.DecisionApply, domain.DecisionStretch, domain.DecisionSkip} {
		if !domain.ValidDecision(d) {
			t.Errorf("ValidDecision(%q) = false, want true", d)
		}
	}
	if domain.ValidDecision("maybe") {
		t.Error("ValidDecision accepted a decision outside the closed set")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/domain/ -v`
Expected: FAIL — package `forge/atlas/internal/core/domain` does not exist.

- [ ] **Step 3: Write the domain types**

Create `internal/core/domain/posting.go`:

```go
package domain

// Posting is a job advertisement, normalised away from whatever portal it
// came from. Body is plain text: the adapter that fetched it is responsible
// for unwrapping any markup, so nothing downstream parses HTML.
type Posting struct {
	ID       string
	Title    string
	Company  string
	Location string
	URL      string
	Body     string
}
```

Create `internal/core/domain/verdict.go`:

```go
package domain

// Decisions are a closed set. A verdict the user cannot act on is a score,
// and the point of the product is not to be a score.
const (
	DecisionApply   = "apply"
	DecisionStretch = "stretch"
	DecisionSkip    = "skip"
)

// Verdict is a judgement about one posting, with the reasoning that produced
// it. Reasons are what the user reads; Decision is what the system routes on.
type Verdict struct {
	Decision string
	Reasons  []string
}

func ValidDecision(d string) bool {
	switch d {
	case DecisionApply, DecisionStretch, DecisionSkip:
		return true
	default:
		return false
	}
}
```

Create `internal/core/domain/package.go`:

```go
package domain

// Profile is who the user is, at the depth increment 1 needs. The four real
// profile inputs are a later epic; this is the hardcoded stand-in.
type Profile struct {
	Summary string
	Skills  []string
}

// Package is everything a user needs in order to apply. The platform drafts
// it; the user submits it.
type Package struct {
	CV          string
	CoverLetter string
}
```

Create `internal/core/domain/state.go`:

```go
package domain

// State is what flows through a blueprint. Each step reads the fields it
// needs and returns a State carrying what it produced.
//
// Pointers mark "not yet produced": a step can tell the difference between a
// posting that has not been fetched and one that came back empty.
type State struct {
	Profile Profile
	Posting *Posting
	Verdict *Verdict
	Package *Package
}

func (s State) WithPosting(p Posting) State {
	s.Posting = &p
	return s
}

func (s State) WithVerdict(v Verdict) State {
	s.Verdict = &v
	return s
}

func (s State) WithPackage(p Package) State {
	s.Package = &p
	return s
}
```

- [ ] **Step 4: Write the ports**

Create `internal/core/ports/ports.go`:

```go
package ports

import (
	"context"

	"forge/atlas/internal/core/domain"
)

// Step is one unit of work in a blueprint. Every step has the same shape
// regardless of whether it calls a job board, a model, or nothing at all —
// that uniformity is what lets a blueprint be a list rather than a call chain.
//
// Name is what the blueprint runner labels the step's span with, so it is
// read by an operator and should say what the step does.
type Step interface {
	Name() string
	Run(ctx context.Context, s domain.State) (domain.State, error)
}

// PostingSource is somewhere postings come from. Named for what the core
// needs, not for how any particular board serves them.
type PostingSource interface {
	Latest(ctx context.Context, board string) (domain.Posting, error)
}

// Model turns a system instruction and a user message into text. Deliberately
// narrower than any provider's API: the core needs completion, not sessions,
// streaming, or tool calls.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/...`
Expected: PASS, including the arch test, which now has a real `internal/core/...` tree to walk.

- [ ] **Step 6: Commit**

```bash
git add internal/core/
git commit -m "Add domain types and the Step port

Step is the increment's actual claim: fetching a posting, scoring it, and
drafting a package are unlike operations that share one interface, so a
blueprint is a list rather than a call chain.

State threads by value and each step returns a new one, so a failed step
cannot leave half its output for the next step to read. Pointer fields
distinguish not-yet-produced from produced-and-empty.

Ports are named for what the core needs. Model exposes completion only,
not sessions or streaming, because that is all the core has a use for."
```

---

### Task 3: The blueprint runner

One place opens a span per step. Instrumenting each step individually would put the same four lines in every step forever and guarantee the fifth step forgets them.

**Files:**
- Create: `internal/core/app/blueprint.go`
- Test: `internal/core/app/blueprint_test.go`

**Interfaces:**
- Consumes: `ports.Step`, `domain.State`
- Produces: `app.Blueprint{Name string, Steps []ports.Step}` with `Run(ctx, domain.State) (domain.State, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/core/app/blueprint_test.go`:

```go
package app_test

import (
	"context"
	"errors"
	"testing"

	"forge/atlas/internal/core/app"
	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
)

type recordingStep struct {
	name string
	log  *[]string
	err  error
}

func (s recordingStep) Name() string { return s.name }

func (s recordingStep) Run(ctx context.Context, st domain.State) (domain.State, error) {
	*s.log = append(*s.log, s.name)
	if s.err != nil {
		return domain.State{}, s.err
	}
	return st, nil
}

func TestRunExecutesStepsInOrder(t *testing.T) {
	var log []string
	b := app.Blueprint{
		Name: "test",
		Steps: []ports.Step{
			recordingStep{name: "first", log: &log},
			recordingStep{name: "second", log: &log},
			recordingStep{name: "third", log: &log},
		},
	}

	if _, err := b.Run(context.Background(), domain.State{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"first", "second", "third"}
	if len(log) != len(want) {
		t.Fatalf("ran %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Errorf("step %d = %q, want %q", i, log[i], want[i])
		}
	}
}

func TestRunStopsAtTheFailingStep(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	b := app.Blueprint{
		Name: "test",
		Steps: []ports.Step{
			recordingStep{name: "first", log: &log},
			recordingStep{name: "second", log: &log, err: boom},
			recordingStep{name: "third", log: &log},
		},
	}

	_, err := b.Run(context.Background(), domain.State{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom wrapped", err)
	}
	if len(log) != 2 {
		t.Errorf("ran %v; a failing step must stop the blueprint", log)
	}
}

func TestRunThreadsStateBetweenSteps(t *testing.T) {
	b := app.Blueprint{
		Name: "test",
		Steps: []ports.Step{
			stepFunc{name: "produce", fn: func(_ context.Context, s domain.State) (domain.State, error) {
				return s.WithPosting(domain.Posting{ID: "abc"}), nil
			}},
			stepFunc{name: "consume", fn: func(_ context.Context, s domain.State) (domain.State, error) {
				if s.Posting == nil || s.Posting.ID != "abc" {
					t.Error("second step did not see the first step's output")
				}
				return s, nil
			}},
		},
	}
	if _, err := b.Run(context.Background(), domain.State{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

type stepFunc struct {
	name string
	fn   func(context.Context, domain.State) (domain.State, error)
}

func (s stepFunc) Name() string { return s.name }
func (s stepFunc) Run(ctx context.Context, st domain.State) (domain.State, error) {
	return s.fn(ctx, st)
}
```


- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/app/ -v`
Expected: FAIL — package `forge/atlas/internal/core/app` does not exist.

- [ ] **Step 3: Write the blueprint**

Create `internal/core/app/blueprint.go`:

```go
// Package app composes steps into blueprints and holds the steps themselves.
package app

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
)

// tracer is fetched from the global provider, which telemetry.Init installs
// before any blueprint is constructed. When tracing is disabled the global is
// a no-op provider and every span below costs nothing.
var tracer = otel.Tracer("forge/atlas/blueprint")

// Blueprint runs steps in order, threading State from each into the next, and
// stops at the first failure.
//
// Adding a step is adding a list entry. Nothing here knows what any step does.
type Blueprint struct {
	Name  string
	Steps []ports.Step
}

// Run executes every step under its own span. Instrumenting here rather than
// inside each step is what keeps a new step from silently arriving untraced.
func (b Blueprint) Run(ctx context.Context, s domain.State) (domain.State, error) {
	ctx, span := tracer.Start(ctx, "blueprint."+b.Name)
	defer span.End()
	span.SetAttributes(attribute.Int("blueprint.steps", len(b.Steps)))

	for _, step := range b.Steps {
		var err error
		s, err = b.runStep(ctx, step, s)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return domain.State{}, err
		}
	}
	return s, nil
}

func (b Blueprint) runStep(ctx context.Context, step ports.Step, s domain.State) (domain.State, error) {
	ctx, span := tracer.Start(ctx, "step."+step.Name())
	defer span.End()

	next, err := step.Run(ctx, s)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return domain.State{}, fmt.Errorf("blueprint %s: step %s: %w", b.Name, step.Name(), err)
	}
	return next, nil
}
```

- [ ] **Step 4: Add the OTel dependency**

```bash
go get go.opentelemetry.io/otel@latest
go mod tidy
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS. The arch test must still pass: `go.opentelemetry.io/otel` is the API, not an exporter, and only exporters are forbidden inward.

- [ ] **Step 6: Commit**

```bash
git add internal/core/app/ go.mod go.sum
git commit -m "Add the blueprint runner

Runs steps in order, threads State from each into the next, and stops at
the first failure. Nothing here knows what any step does, so adding one is
adding a list entry.

Spans are opened here rather than inside each step. The alternative puts
the same four lines in every step forever and guarantees the fifth one
forgets them."
```

---

### Task 4: The Greenhouse adapter

Greenhouse serves `content` as HTML-entity-escaped HTML. Unwrapping it belongs in the adapter: the port promises plain text, and nothing downstream should parse markup.

**Files:**
- Create: `internal/adapters/outbound/greenhouse/client.go`
- Test: `internal/adapters/outbound/greenhouse/client_test.go`

**Interfaces:**
- Consumes: `domain.Posting`, `ports.PostingSource`
- Produces: `greenhouse.New(baseURL string, timeout time.Duration) *greenhouse.Client` implementing `ports.PostingSource`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/greenhouse/client_test.go`:

```go
package greenhouse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge/atlas/internal/adapters/outbound/greenhouse"
)

// Shape captured from boards-api.greenhouse.io. content arrives as
// HTML-entity-escaped HTML, which is the detail the adapter exists to absorb.
const jobsBody = `{"jobs":[{
  "id": 8503792002,
  "title": "Backend Engineer",
  "company_name": "GitLab",
  "absolute_url": "https://job-boards.greenhouse.io/gitlab/jobs/8503792002",
  "location": {"name": "Remote, Italy"},
  "content": "&lt;p&gt;We want &lt;strong&gt;Go&lt;/strong&gt; experience.&lt;/p&gt;",
  "updated_at": "2026-08-10T16:52:46-04:00"
}],"meta":{"total":1}}`

func TestLatestReturnsAPlainTextPosting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/boards/gitlab/jobs" {
			t.Errorf("path = %q", got)
		}
		if r.URL.Query().Get("content") != "true" {
			t.Error("content=true not requested; the body is what scoring reads")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jobsBody))
	}))
	defer srv.Close()

	c := greenhouse.New(srv.URL, 5*time.Second)
	p, err := c.Latest(context.Background(), "gitlab")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	if p.ID != "8503792002" {
		t.Errorf("ID = %q", p.ID)
	}
	if p.Title != "Backend Engineer" {
		t.Errorf("Title = %q", p.Title)
	}
	if p.Company != "GitLab" {
		t.Errorf("Company = %q", p.Company)
	}
	if p.Location != "Remote, Italy" {
		t.Errorf("Location = %q", p.Location)
	}
	if strings.Contains(p.Body, "<") || strings.Contains(p.Body, "&lt;") {
		t.Errorf("Body still contains markup: %q", p.Body)
	}
	if !strings.Contains(p.Body, "Go experience") {
		t.Errorf("Body lost its text: %q", p.Body)
	}
}

func TestLatestFailsWhenTheBoardHasNoJobs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[],"meta":{"total":0}}`))
	}))
	defer srv.Close()

	if _, err := greenhouse.New(srv.URL, 5*time.Second).Latest(context.Background(), "empty"); err == nil {
		t.Error("Latest succeeded on an empty board")
	}
}

func TestLatestFailsOnUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := greenhouse.New(srv.URL, 5*time.Second).Latest(context.Background(), "x"); err == nil {
		t.Error("Latest succeeded on a 503")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/outbound/greenhouse/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the client**

Create `internal/adapters/outbound/greenhouse/client.go`:

```go
// Package greenhouse reads job postings from Greenhouse's public board API.
// No authentication, no scraping.
package greenhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forge/atlas/internal/core/domain"
)

// Client reads one board. The port promises plain text, so unwrapping
// Greenhouse's escaped HTML happens here and nothing downstream sees markup.
type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

type jobsResponse struct {
	Jobs []job `json:"jobs"`
}

type job struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	CompanyName string `json:"company_name"`
	AbsoluteURL string `json:"absolute_url"`
	Content     string `json:"content"`
	Location    struct {
		Name string `json:"name"`
	} `json:"location"`
}

// Latest returns the first posting the board lists. Increment 1 fetches one
// posting; selection and paging belong to the discovery epic.
func (c *Client) Latest(ctx context.Context, board string) (domain.Posting, error) {
	url := fmt.Sprintf("%s/v1/boards/%s/jobs?content=true", c.baseURL, board)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return domain.Posting{}, fmt.Errorf("greenhouse: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return domain.Posting{}, fmt.Errorf("greenhouse: get %s: %w", board, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.Posting{}, fmt.Errorf("greenhouse: board %s returned %d", board, resp.StatusCode)
	}

	var body jobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return domain.Posting{}, fmt.Errorf("greenhouse: decode: %w", err)
	}
	if len(body.Jobs) == 0 {
		return domain.Posting{}, fmt.Errorf("greenhouse: board %s listed no jobs", board)
	}

	j := body.Jobs[0]
	return domain.Posting{
		ID:       strconv.FormatInt(j.ID, 10),
		Title:    j.Title,
		Company:  j.CompanyName,
		Location: j.Location.Name,
		URL:      j.AbsoluteURL,
		Body:     plainText(j.Content),
	}, nil
}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// plainText unescapes Greenhouse's entity-encoded HTML, strips the tags that
// unescaping reveals, and collapses the whitespace that removing them leaves.
func plainText(content string) string {
	unescaped := html.UnescapeString(content)
	stripped := tagPattern.ReplaceAllString(unescaped, " ")
	return strings.Join(strings.Fields(html.UnescapeString(stripped)), " ")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/... -v`
Expected: PASS, all three.

- [ ] **Step 5: Verify against the real API**

Run:
```bash
go run ./internal/adapters/outbound/greenhouse/... 2>/dev/null || \
  curl -s "https://boards-api.greenhouse.io/v1/boards/gitlab/jobs?content=true" | head -c 200
```
Expected: a 200 and JSON beginning `{"jobs":[{`. A test against a captured fixture proves the parsing; this proves the fixture still matches reality.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/outbound/greenhouse/
git commit -m "Add the Greenhouse board adapter

Reads the public board API: no authentication, no scraping, no browser.

Greenhouse serves content as HTML-entity-escaped HTML. Unwrapping it is the
adapter's job because the port promises plain text, so nothing downstream
parses markup. Tests run against a fixture captured from the live API."
```

---

### Task 5: The model adapter

OpenAI-compatible chat completions. Ollama serves this locally, so the tracer runs offline and free; synapse-gateway speaks the same protocol, so swapping it in later is configuration.

**Files:**
- Create: `internal/adapters/outbound/llm/client.go`
- Test: `internal/adapters/outbound/llm/client_test.go`

**Interfaces:**
- Consumes: `ports.Model`
- Produces: `llm.New(baseURL, model string, timeout time.Duration) *llm.Client` implementing `ports.Model`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/llm/client_test.go`:

```go
package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forge/atlas/internal/adapters/outbound/llm"
)

func TestCompleteSendsSystemAndUserAndReturnsContent(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"the answer"}}]}`))
	}))
	defer srv.Close()

	c := llm.New(srv.URL, "test-model", 5*time.Second)
	out, err := c.Complete(context.Background(), "be terse", "the question")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if out != "the answer" {
		t.Errorf("out = %q", out)
	}
	if got.Model != "test-model" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("sent %d messages, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Errorf("message 0 = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "the question" {
		t.Errorf("message 1 = %+v", got.Messages[1])
	}
}

func TestCompleteFailsOnUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := llm.New(srv.URL, "m", time.Second).Complete(context.Background(), "s", "u"); err == nil {
		t.Error("Complete succeeded on a 500")
	}
}

func TestCompleteFailsOnEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	if _, err := llm.New(srv.URL, "m", time.Second).Complete(context.Background(), "s", "u"); err == nil {
		t.Error("Complete returned no error for a response with no choices")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/outbound/llm/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the client**

Create `internal/adapters/outbound/llm/client.go`:

```go
// Package llm calls an OpenAI-compatible chat completions endpoint. Ollama
// serves this locally, so the tracer runs offline and free; synapse-gateway
// speaks the same protocol, so routing through it later is configuration.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

func New(baseURL, model string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type completionResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// Complete sends one system and one user message and returns the assistant's
// text. Streaming is off: the core consumes a whole answer, and a partial one
// is not useful to a step that has to return a value.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(completionRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: model %s returned %d", c.model, resp.StatusCode)
	}

	var out completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: model %s returned no choices", c.model)
	}
	return out.Choices[0].Message.Content, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/llm/
git commit -m "Add the OpenAI-compatible model adapter

Ollama serves this protocol locally, so the tracer needs no credential, no
network, and no budget. synapse-gateway speaks the same protocol, so
routing through it later is a base URL change rather than a rewrite.

Streaming is off: a step has to return a value, and a partial answer is not
one."
```

---

### Task 6: The three steps

**Files:**
- Create: `internal/core/app/fetch.go`, `internal/core/app/score.go`, `internal/core/app/draft.go`
- Test: `internal/core/app/steps_test.go`

**Interfaces:**
- Consumes: `ports.PostingSource`, `ports.Model`, `domain.State`
- Produces: `app.NewFetchStep(src ports.PostingSource, board string) ports.Step`, `app.NewScoreStep(m ports.Model) ports.Step`, `app.NewDraftStep(m ports.Model) ports.Step`

- [ ] **Step 1: Write the failing test**

Create `internal/core/app/steps_test.go`:

```go
package app_test

import (
	"context"
	"strings"
	"testing"

	"forge/atlas/internal/core/app"
	"forge/atlas/internal/core/domain"
)

type fakeSource struct{ p domain.Posting }

func (f fakeSource) Latest(context.Context, string) (domain.Posting, error) { return f.p, nil }

type fakeModel struct {
	reply  string
	prompt string
}

func (f *fakeModel) Complete(_ context.Context, _, user string) (string, error) {
	f.prompt = user
	return f.reply, nil
}

func TestFetchStepPutsThePostingInState(t *testing.T) {
	step := app.NewFetchStep(fakeSource{p: domain.Posting{ID: "1", Title: "Backend Engineer"}}, "gitlab")

	got, err := step.Run(context.Background(), domain.State{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Posting == nil || got.Posting.Title != "Backend Engineer" {
		t.Error("fetch step did not put the posting in State")
	}
	if step.Name() == "" {
		t.Error("step has no name; the blueprint labels its span with it")
	}
}

func TestScoreStepParsesTheVerdict(t *testing.T) {
	m := &fakeModel{reply: `{"decision":"apply","reasons":["go experience matches","remote"]}`}
	step := app.NewScoreStep(m)

	in := domain.State{
		Profile: domain.Profile{Summary: "go engineer", Skills: []string{"go", "kubernetes"}},
	}.WithPosting(domain.Posting{Title: "Backend Engineer", Body: "We want Go."})

	got, err := step.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Verdict == nil {
		t.Fatal("no verdict in State")
	}
	if got.Verdict.Decision != domain.DecisionApply {
		t.Errorf("Decision = %q", got.Verdict.Decision)
	}
	if len(got.Verdict.Reasons) != 2 {
		t.Errorf("Reasons = %v; a verdict without reasons is a score", got.Verdict.Reasons)
	}
	if !strings.Contains(m.prompt, "Backend Engineer") {
		t.Error("the posting title never reached the model")
	}
	if !strings.Contains(m.prompt, "go engineer") {
		t.Error("the profile never reached the model")
	}
}

func TestScoreStepRejectsADecisionOutsideTheClosedSet(t *testing.T) {
	step := app.NewScoreStep(&fakeModel{reply: `{"decision":"maybe","reasons":[]}`})
	in := domain.State{}.WithPosting(domain.Posting{Title: "x"})

	if _, err := step.Run(context.Background(), in); err == nil {
		t.Error("score step accepted a decision outside the closed set")
	}
}

func TestScoreStepToleratesAFencedCodeBlock(t *testing.T) {
	// Small local models routinely wrap JSON in a markdown fence.
	m := &fakeModel{reply: "```json\n{\"decision\":\"skip\",\"reasons\":[\"onsite\"]}\n```"}
	step := app.NewScoreStep(m)
	in := domain.State{}.WithPosting(domain.Posting{Title: "x"})

	got, err := step.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Verdict.Decision != domain.DecisionSkip {
		t.Errorf("Decision = %q", got.Verdict.Decision)
	}
}

func TestScoreStepFailsWithoutAPosting(t *testing.T) {
	step := app.NewScoreStep(&fakeModel{reply: "{}"})
	if _, err := step.Run(context.Background(), domain.State{}); err == nil {
		t.Error("score step ran without a posting")
	}
}

func TestDraftStepProducesBothDocuments(t *testing.T) {
	m := &fakeModel{reply: "CV BODY\n---\nCOVER LETTER BODY"}
	step := app.NewDraftStep(m)

	in := domain.State{Profile: domain.Profile{Summary: "go engineer"}}.
		WithPosting(domain.Posting{Title: "Backend Engineer"}).
		WithVerdict(domain.Verdict{Decision: domain.DecisionApply})

	got, err := step.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Package == nil {
		t.Fatal("no package in State")
	}
	if !strings.Contains(got.Package.CV, "CV BODY") {
		t.Errorf("CV = %q", got.Package.CV)
	}
	if !strings.Contains(got.Package.CoverLetter, "COVER LETTER BODY") {
		t.Errorf("CoverLetter = %q", got.Package.CoverLetter)
	}
}

func TestDraftStepFailsWithoutAVerdict(t *testing.T) {
	step := app.NewDraftStep(&fakeModel{reply: "x\n---\ny"})
	in := domain.State{}.WithPosting(domain.Posting{Title: "x"})
	if _, err := step.Run(context.Background(), in); err == nil {
		t.Error("draft step ran without a verdict")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/app/ -v`
Expected: FAIL — `NewFetchStep`, `NewScoreStep`, `NewDraftStep` undefined.

- [ ] **Step 3: Write the fetch step**

Create `internal/core/app/fetch.go`:

```go
package app

import (
	"context"
	"fmt"

	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
)

type fetchStep struct {
	source ports.PostingSource
	board  string
}

// NewFetchStep reads one posting from a source. Increment 1 reads a single
// board; the portal registry is a later epic.
func NewFetchStep(source ports.PostingSource, board string) ports.Step {
	return fetchStep{source: source, board: board}
}

func (s fetchStep) Name() string { return "fetch" }

func (s fetchStep) Run(ctx context.Context, st domain.State) (domain.State, error) {
	p, err := s.source.Latest(ctx, s.board)
	if err != nil {
		return domain.State{}, fmt.Errorf("fetch: %w", err)
	}
	return st.WithPosting(p), nil
}
```

- [ ] **Step 4: Write the score step**

Create `internal/core/app/score.go`:

```go
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
)

const scoreSystem = `You judge whether a job posting suits a candidate.
Reply with JSON only: {"decision":"apply|stretch|skip","reasons":["...","..."]}
decision must be exactly one of apply, stretch, or skip.
reasons must be short and specific to this posting and this candidate.`

type scoreStep struct{ model ports.Model }

// NewScoreStep asks a model for a verdict and the reasoning behind it. A
// verdict whose reasoning the user cannot read is a score, which is the thing
// the product exists not to be.
func NewScoreStep(m ports.Model) ports.Step { return scoreStep{model: m} }

func (s scoreStep) Name() string { return "score" }

func (s scoreStep) Run(ctx context.Context, st domain.State) (domain.State, error) {
	if st.Posting == nil {
		return domain.State{}, fmt.Errorf("score: no posting in state")
	}

	prompt := fmt.Sprintf(
		"Candidate: %s\nSkills: %s\n\nPosting: %s at %s (%s)\n\n%s",
		st.Profile.Summary,
		strings.Join(st.Profile.Skills, ", "),
		st.Posting.Title, st.Posting.Company, st.Posting.Location,
		st.Posting.Body,
	)

	raw, err := s.model.Complete(ctx, scoreSystem, prompt)
	if err != nil {
		return domain.State{}, fmt.Errorf("score: %w", err)
	}

	var v struct {
		Decision string   `json:"decision"`
		Reasons  []string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(unfence(raw)), &v); err != nil {
		return domain.State{}, fmt.Errorf("score: parse verdict from %q: %w", raw, err)
	}
	if !domain.ValidDecision(v.Decision) {
		return domain.State{}, fmt.Errorf("score: decision %q is not apply, stretch, or skip", v.Decision)
	}

	return st.WithVerdict(domain.Verdict{Decision: v.Decision, Reasons: v.Reasons}), nil
}

// unfence strips a markdown code fence. Small local models wrap JSON in one
// routinely, and the alternative is a step that fails on formatting rather
// than on substance.
func unfence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "```"))
}
```

- [ ] **Step 5: Write the draft step**

Create `internal/core/app/draft.go`:

```go
package app

import (
	"context"
	"fmt"
	"strings"

	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
)

const draftSystem = `You draft job application documents.
Output exactly two sections separated by a line containing only ---
Section one is a tailored CV. Section two is a cover letter.
Use only facts given about the candidate. State a gap plainly rather than inventing experience.`

const draftSeparator = "---"

type draftStep struct{ model ports.Model }

// NewDraftStep produces the documents a user needs in order to apply. The
// platform drafts; the user submits.
func NewDraftStep(m ports.Model) ports.Step { return draftStep{model: m} }

func (s draftStep) Name() string { return "draft" }

func (s draftStep) Run(ctx context.Context, st domain.State) (domain.State, error) {
	if st.Posting == nil {
		return domain.State{}, fmt.Errorf("draft: no posting in state")
	}
	if st.Verdict == nil {
		return domain.State{}, fmt.Errorf("draft: no verdict in state")
	}

	prompt := fmt.Sprintf(
		"Candidate: %s\nSkills: %s\n\nPosting: %s at %s\n\n%s",
		st.Profile.Summary,
		strings.Join(st.Profile.Skills, ", "),
		st.Posting.Title, st.Posting.Company,
		st.Posting.Body,
	)

	raw, err := s.model.Complete(ctx, draftSystem, prompt)
	if err != nil {
		return domain.State{}, fmt.Errorf("draft: %w", err)
	}

	cv, letter, found := strings.Cut(unfence(raw), draftSeparator)
	if !found {
		return domain.State{}, fmt.Errorf("draft: model did not separate the two documents with %q", draftSeparator)
	}

	return st.WithPackage(domain.Package{
		CV:          strings.TrimSpace(cv),
		CoverLetter: strings.TrimSpace(letter),
	}), nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS, including the arch test.

- [ ] **Step 7: Commit**

```bash
git add internal/core/app/
git commit -m "Add the fetch, score and draft steps

Three unlike operations behind one interface: one reads a board, two call a
model, and the blueprint cannot tell them apart. That is the claim this
increment exists to test.

The score step rejects a decision outside apply/stretch/skip rather than
storing whatever the model said, and tolerates a markdown fence around the
JSON because small local models emit one routinely. Failing on formatting
rather than substance would be the wrong reason to fail."
```

---

### Task 7: Telemetry

**Files:**
- Create: `internal/telemetry/telemetry.go`
- Modify: none

**Interfaces:**
- Consumes: `config.Config`
- Produces: `telemetry.Init(ctx, cfg config.Config) (func(context.Context) error, error)`

- [ ] **Step 1: Write telemetry.go**

Create `internal/telemetry/telemetry.go`:

```go
// Package telemetry wires OpenTelemetry's trace pipeline to an OTLP gRPC
// endpoint and installs the tracer provider as the process-wide global. It is
// called once, from cmd/atlas, before any blueprint is constructed: the
// blueprint runner reads otel.Tracer at package init, so Init must run first.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"forge/atlas/internal/config"
)

// Init builds the trace provider and returns a shutdown func that must run on
// every exit path, including error paths, or a short run's spans are dropped
// with it.
//
// When cfg.OTel.Enabled is false, Init installs nothing and returns a no-op
// shutdown. The global provider stays the SDK's default no-op, so every span
// in the blueprint runner costs nothing and the binary runs with no collector.
//
// Resource attributes come from OTEL_RESOURCE_ATTRIBUTES in the environment.
// Nothing here hardcodes service.name, so the same binary is correct wherever
// it runs.
func Init(ctx context.Context, cfg config.Config) (func(context.Context) error, error) {
	if !cfg.OTel.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx, resource.WithTelemetrySDK(), resource.WithFromEnv())
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTel.Endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(cfg.OTel.ExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build exporter: %w", err)
	}

	// AlwaysSample: the SDK would have to sample before a run's outcome is
	// known, and the collector decides after, which is the only order that can
	// implement "keep every error".
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}
```

- [ ] **Step 2: Add the SDK dependencies**

```bash
go get go.opentelemetry.io/otel/sdk@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@latest
go mod tidy
```

- [ ] **Step 3: Verify the arch test still passes**

Run: `go test ./internal/... -v`
Expected: PASS. `internal/telemetry` imports an exporter, which is forbidden inward — the arch test must confirm `internal/core` does not reach it.

- [ ] **Step 4: Commit**

```bash
git add internal/telemetry/ go.mod go.sum
git commit -m "Add the OTLP trace pipeline

Disabled by default and a no-op when off, so the binary runs with no
collector and every span in the blueprint runner costs nothing.

Sampling is AlwaysOn where it is enabled: the SDK samples before a run's
outcome is known and the collector samples after, which is the only order
that can keep every error."
```

---

### Task 8: Composition root, the real run, and the increment note

**Files:**
- Create: `cmd/atlas/main.go`, `docs/notes/2026-08-24-increment-1a.md`

**Interfaces:**
- Consumes: everything above
- Produces: the `atlas` binary

- [ ] **Step 1: Write main.go**

Create `cmd/atlas/main.go`:

```go
// Command atlas runs the increment 1a blueprint: fetch one posting, score it,
// draft a package. This is the only file that knows every concrete type.
package main

import (
	"context"
	"fmt"
	"os"

	"forge/atlas/internal/adapters/outbound/greenhouse"
	"forge/atlas/internal/adapters/outbound/llm"
	"forge/atlas/internal/config"
	"forge/atlas/internal/core/app"
	"forge/atlas/internal/core/domain"
	"forge/atlas/internal/core/ports"
	"forge/atlas/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}

	shutdown, err := telemetry.Init(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	source := greenhouse.New(cfg.Greenhouse.BaseURL, cfg.Greenhouse.Timeout)
	model := llm.New(cfg.Model.BaseURL, cfg.Model.Name, cfg.Model.Timeout)

	blueprint := app.Blueprint{
		Name: "job-hunt",
		Steps: []ports.Step{
			app.NewFetchStep(source, cfg.Greenhouse.Board),
			app.NewScoreStep(model),
			app.NewDraftStep(model),
		},
	}

	// The profile is hardcoded here. Building it from a CV, a preferences
	// conversation and an evidence corpus is a later epic; increment 1 proves
	// the path, not the inputs.
	start := domain.State{
		Profile: domain.Profile{
			Summary: "Backend engineer, Go, distributed systems, Kubernetes",
			Skills:  []string{"go", "kubernetes", "postgres", "rabbitmq", "opentelemetry"},
		},
	}

	final, err := blueprint.Run(ctx, start)
	if err != nil {
		return err
	}

	return report(final)
}

func report(s domain.State) error {
	if s.Posting == nil || s.Verdict == nil || s.Package == nil {
		return fmt.Errorf("blueprint finished with an incomplete state")
	}
	fmt.Printf("POSTING  %s at %s (%s)\n", s.Posting.Title, s.Posting.Company, s.Posting.Location)
	fmt.Printf("URL      %s\n\n", s.Posting.URL)
	fmt.Printf("VERDICT  %s\n", s.Verdict.Decision)
	for _, r := range s.Verdict.Reasons {
		fmt.Printf("         - %s\n", r)
	}
	fmt.Printf("\nCV\n%s\n\nCOVER LETTER\n%s\n", s.Package.CV, s.Package.CoverLetter)
	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Run it against the real world**

Run:
```bash
go run ./cmd/atlas -model-name qwen3.5:9b
```
Expected: a real GitLab posting, a verdict with reasons, and two drafted documents. A model call on a laptop takes tens of seconds; that is why the model timeout default is minutes rather than seconds.

If the score step fails parsing, that is a finding, not a defect to paper over. Record which model and what it returned in the increment note.

- [ ] **Step 4: Run it with tracing**

In one terminal:
```bash
docker run --rm -p 4317:4317 -p 55679:55679 otel/opentelemetry-collector:latest
```

In another:
```bash
OTEL_RESOURCE_ATTRIBUTES=service.name=atlas,deployment.environment.name=local \
  go run ./cmd/atlas -otel
```
Expected: the collector logs a trace containing `blueprint.job-hunt` with child spans `step.fetch`, `step.score`, `step.draft`.

- [ ] **Step 5: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS, every package.

- [ ] **Step 6: Write the increment note**

Create `docs/notes/2026-08-24-increment-1a.md`. Answer all four, honestly:

```markdown
# Increment 1a — step model tracer

## What the pattern was

## What surprised me

Specifically: did the Step interface earn its place across three steps, or
did it only look like it did? Three is the smallest number that can show the
difference, and this is the cheapest moment to find out it is wrong.

Also: which model, and did it hold the JSON contract?

## What I would do differently

## What I still do not understand
```

- [ ] **Step 7: Commit**

```bash
git add cmd/ docs/notes/
git commit -m "Wire the increment 1a blueprint and record what it taught

One binary fetches a real GitLab posting, scores it against a hardcoded
profile with a local model, and drafts a CV and cover letter, with a trace
covering every step.

Deliberately useless as a product. It is the multimeter for the substrate,
the same way the hello workload is the multimeter for the platform, and it
runs offline and free because the board API is public and the model is
local."
```

---

## What increment 1b inherits

The three steps, the blueprint, and both adapters stay. 1b replaces how the
blueprint executes, not what it does, and answers one question: can ADK Go
reach an OpenAI-compatible endpoint, or does that mean implementing its model
interface?

`ports.Model` is the seam. If ADK cannot be pointed at a base URL, the adapter
behind that port is where it gets absorbed, and nothing in `internal/core`
changes.
