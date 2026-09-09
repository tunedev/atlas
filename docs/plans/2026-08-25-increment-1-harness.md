# Increment 1 — Harness Tracer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run a job-hunt pack — fetch a real posting, score it, draft a package — entirely from a YAML file, on a Go harness that knows nothing about jobs; then prove the harness exists by running a second, unrelated pack with no Go changes.

**Architecture:** A tool registry maps names to typed Go implementations. The harness ships two generic tools, `http.request` and `model.complete`. A blueprint is an ordered list of steps, each naming a tool and supplying config. The runner resolves the tool, renders that config as a Go template against accumulated state, executes, narrows the result by an optional path, and stores it under the step id. Every invocation gets a span.

**Tech Stack:** Go 1.27, `gopkg.in/yaml.v3`, `text/template`, OpenTelemetry OTLP gRPC, Greenhouse's public board API, Ollama's OpenAI-compatible endpoint.

**Spec:** `docs/specs/2026-08-24-agent-substrate-design.md`

## Global Constraints

- Go 1.27. Module path `github.com/tunedev/atlas`, matching `github.com/tunedev/nerve`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL, or string constant naming a use-case concept outside `packs/`. This is the plan's binding constraint; a task that violates it has failed regardless of its tests.
- Hexagonal layout per `../../../CLAUDE.md` Tenet 1: `internal/core/{domain,ports,app}`, `internal/adapters/`, `cmd/atlas/main.go` as the only file knowing every concrete type.
- Ports are written in the core's vocabulary. No core package imports an adapter or a driver, enforced by `internal/arch`.
- `ctx context.Context` is the first parameter of every blocking or remote function. No `Context` stored in a struct.
- Every remote call has a timeout. The default is not "no timeout".
- No conditionals, loops, or expression language in blueprints. Templates and a path selector only.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis in code, log lines, or output.
- Comments describe current behaviour only. No history, no bug narratives, no dates.

## What "done" means

Two commands must both work at the end:

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml     # a real posting, scored and drafted
go run ./cmd/atlas -pack packs/hn-summary.yaml   # something unrelated, zero Go changes
```

The second is the increment. The first is only its excuse.

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module `github.com/tunedev/atlas` |
| `internal/config/config.go` | Typed config, validated at startup |
| `internal/config/layers.go` | defaults, env, flags — later wins |
| `internal/arch/arch_test.go` | Fails if core imports an adapter or driver |
| `internal/arch/vocabulary_test.go` | Fails if Go outside `packs/` names a use-case concept |
| `internal/core/domain/blueprint.go` | `Blueprint`, `Step` — the parsed pack |
| `internal/core/domain/state.go` | `State` — vars plus per-step output |
| `internal/core/ports/ports.go` | `Tool`, `Registry` |
| `internal/core/app/render.go` | Template rendering and `select:` path narrowing |
| `internal/core/app/runner.go` | Resolve, render, execute, narrow, store, span |
| `internal/adapters/inbound/packfile/load.go` | YAML to `domain.Blueprint` |
| `internal/adapters/outbound/tools/http.go` | `http.request` |
| `internal/adapters/outbound/tools/model.go` | `model.complete` |
| `internal/adapters/outbound/tools/registry.go` | Name to implementation |
| `internal/telemetry/telemetry.go` | OTLP trace pipeline |
| `packs/job-hunt.yaml` | Pack one |
| `packs/hn-summary.yaml` | Pack two, the proof |
| `cmd/atlas/main.go` | Composition root |

---

### Task 1: Module scaffold, config, and both architecture guards

Two guards, not one. The second is this plan's binding constraint made mechanical: a test that fails if any Go file outside `packs/` mentions a use-case concept.

**Files:**
- Create: `go.mod`, `internal/config/config.go`, `internal/config/layers.go`
- Test: `internal/arch/arch_test.go`, `internal/arch/vocabulary_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `config.Config` with `Pack PackConfig`, `Model ModelConfig`, `OTel OTelConfig`; `config.Load(args []string) (Config, error)`

- [ ] **Step 1: Initialise the module**

```bash
cd /home/tunedev/forge/atlas
go mod init github.com/tunedev/atlas
```

- [ ] **Step 2: Write the dependency-direction guard**

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
	out, err := exec.Command("go", "list", "-deps", "github.com/tunedev/atlas/internal/core/...").Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}
	// Adapter tree and third-party drivers only. Never list stdlib packages:
	// go list -deps is transitive, so net/http would fail this for the wrong reason.
	forbidden := []string{
		"github.com/tunedev/atlas/internal/adapters",
		"go.opentelemetry.io/otel/exporters",
		"gopkg.in/yaml.v3",
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

- [ ] **Step 3: Write the vocabulary guard**

Create `internal/arch/vocabulary_test.go`:

```go
package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness knows nothing about any use case. A pack supplies every word
// specific to what it does; the Go tree supplies none of them.
//
// This is the difference between a harness and an application with a config
// file, and it is the one property of this increment worth enforcing
// mechanically, because it erodes one convenience at a time.
func TestGoTreeIsFreeOfUseCaseVocabulary(t *testing.T) {
	// Words that would only appear in Go if pack logic had leaked into it.
	// Deliberately includes the second pack's vocabulary too: a harness that
	// is generic for one use case and not the other is not generic.
	forbidden := []string{
		"posting", "greenhouse", "cover letter", "coverletter",
		"job-hunt", "jobhunt", "recruiter", "hackernews", "hacker news",
	}

	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// packs/ is where use-case vocabulary belongs. Skip docs and VCS.
			switch d.Name() {
			case "packs", ".git", "docs", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file names the forbidden words in order to forbid them.
		if strings.HasSuffix(path, "vocabulary_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(b))
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("%s contains use-case vocabulary %q; that belongs in a pack", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
```

- [ ] **Step 4: Write the failing config test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/config"
)

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.BaseURL == "" {
		t.Error("Model.BaseURL has no default")
	}
	if cfg.Model.Timeout == 0 {
		t.Error("Model.Timeout defaults to zero; every remote call needs a timeout")
	}
	if cfg.Pack.HTTPTimeout == 0 {
		t.Error("Pack.HTTPTimeout defaults to zero")
	}
}

func TestEnvOverridesDefault(t *testing.T) {
	t.Setenv("ATLAS_MODEL_NAME", "from-env")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Name != "from-env" {
		t.Errorf("Model.Name = %q, want from-env", cfg.Model.Name)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_MODEL_NAME", "from-env")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-model-name", "from-flag"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Name != "from-flag" {
		t.Errorf("Model.Name = %q, want from-flag; flags are the last layer", cfg.Model.Name)
	}
}

func TestPackPathIsRequired(t *testing.T) {
	if _, err := config.Load([]string{}); err == nil {
		t.Error("Load succeeded with no pack path; there is nothing to run without one")
	}
}

func TestZeroTimeoutIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-model-timeout", "0s"}); err == nil {
		t.Error("Load accepted a zero model timeout; invalid config must fail at boot")
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./internal/...`
Expected: FAIL — `internal/config` does not exist.

- [ ] **Step 6: Write config.go**

Create `internal/config/config.go`:

```go
// Package config loads atlas's configuration in layers: defaults, then
// environment, then flags — later layers win. The result is validated once at
// startup; nothing here is read in a run path.
package config

import (
	"fmt"
	"time"
)

// Config is the fully resolved, validated configuration for the atlas binary.
// Nothing here describes any particular use case: what to run comes from a
// pack file, named by Pack.Path.
type Config struct {
	Pack  PackConfig
	Model ModelConfig
	OTel  OTelConfig
}

type PackConfig struct {
	Path        string
	HTTPTimeout time.Duration
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
	if c.Pack.Path == "" {
		return fmt.Errorf("config: no pack path; pass -pack")
	}
	if c.Pack.HTTPTimeout <= 0 {
		return fmt.Errorf("config: http timeout must be positive, got %s", c.Pack.HTTPTimeout)
	}
	if c.Model.Timeout <= 0 {
		return fmt.Errorf("config: model timeout must be positive, got %s", c.Model.Timeout)
	}
	if c.Model.Name == "" {
		return fmt.Errorf("config: model name is empty")
	}
	if c.Model.BaseURL == "" {
		return fmt.Errorf("config: model base URL is empty")
	}
	return nil
}
```

- [ ] **Step 7: Write layers.go**

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
// A model call on a laptop is slow, so its timeout default is generous where
// the HTTP one is not.
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
		Pack: PackConfig{
			HTTPTimeout: 20 * time.Second,
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
	if v := os.Getenv("ATLAS_PACK"); v != "" {
		c.Pack.Path = v
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
	fs := flag.NewFlagSet("atlas", flag.ContinueOnError)
	fs.StringVar(&c.Pack.Path, "pack", c.Pack.Path, "path to a pack file")
	fs.DurationVar(&c.Pack.HTTPTimeout, "http-timeout", c.Pack.HTTPTimeout, "timeout for http.request")
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

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS. Both arch tests pass — one vacuously, since `internal/core` does not exist yet; the other because no Go file yet names a use-case word.

- [ ] **Step 9: Commit**

```bash
git add go.mod internal/
git commit -m "Add module scaffold, layered config, and both architecture guards

Two guards. The first is the usual dependencies-point-inward check. The
second fails if any Go file outside packs/ contains use-case vocabulary,
which is this increment's binding constraint made mechanical.

That property is what separates a harness from an application with a config
file, and it erodes one convenience at a time, so it gets a test rather than
a paragraph. The word list deliberately includes the second pack's
vocabulary too: a harness generic for one use case and not the other is not
generic.

Config names no use case. What to run comes from a pack file."
```

---

### Task 2: The blueprint and state types

**Files:**
- Create: `internal/core/domain/blueprint.go`, `internal/core/domain/state.go`
- Test: `internal/core/domain/state_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `domain.Blueprint{Name string, Vars map[string]string, Steps []Step}`
  - `domain.Step{ID string, Tool string, With map[string]string}`
  - `domain.State` with `NewState(vars map[string]string) *State`, `(*State).Vars() map[string]string`, `(*State).Put(stepID string, out any)`, `(*State).Outputs() map[string]any`

- [ ] **Step 1: Write the failing test**

Create `internal/core/domain/state_test.go`:

```go
package domain_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

func TestStateKeepsVarsAndStepOutputsApart(t *testing.T) {
	s := domain.NewState(map[string]string{"board": "acme"})
	s.Put("first", map[string]any{"title": "a title"})

	if got := s.Vars()["board"]; got != "acme" {
		t.Errorf("Vars()[board] = %q", got)
	}
	out, ok := s.Outputs()["first"]
	if !ok {
		t.Fatal("Outputs() has no entry for step first")
	}
	m, ok := out.(map[string]any)
	if !ok || m["title"] != "a title" {
		t.Errorf("Outputs()[first] = %#v", out)
	}
}

func TestOutputsIsNotNilBeforeAnyStepRuns(t *testing.T) {
	// A template referencing .steps before any step has run must render an
	// empty value, not panic on a nil map.
	if domain.NewState(nil).Outputs() == nil {
		t.Error("Outputs() is nil on a fresh State")
	}
	if domain.NewState(nil).Vars() == nil {
		t.Error("Vars() is nil on a fresh State")
	}
}

func TestPutOverwritesTheSameStepID(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("x", "first")
	s.Put("x", "second")
	if s.Outputs()["x"] != "second" {
		t.Errorf("Outputs()[x] = %v, want second", s.Outputs()["x"])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/domain/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the types**

Create `internal/core/domain/blueprint.go`:

```go
package domain

// Blueprint is a parsed pack: an ordered list of steps plus the variables its
// templates may reference.
//
// Nothing here describes a use case. What a blueprint does lives entirely in
// the tool names its steps call and the config they carry.
type Blueprint struct {
	Name  string
	Vars  map[string]string
	Steps []Step
}

// Step names a tool and supplies its configuration. With values are Go
// template source, rendered against State immediately before the tool runs,
// so a step can reference what earlier steps produced.
type Step struct {
	ID   string
	Tool string
	With map[string]string
}
```

Create `internal/core/domain/state.go`:

```go
package domain

// State is what a blueprint accumulates as it runs: the variables it started
// with, and one output per completed step, keyed by step id.
//
// A pointer with unexported maps rather than a value type: the runner appends
// to it step by step, and templates read it by path. Both maps are non-nil
// from construction so a template referencing a step that has not run yet
// renders empty rather than panicking.
type State struct {
	vars    map[string]string
	outputs map[string]any
}

func NewState(vars map[string]string) *State {
	if vars == nil {
		vars = map[string]string{}
	}
	return &State{vars: vars, outputs: map[string]any{}}
}

func (s *State) Vars() map[string]string { return s.vars }

func (s *State) Outputs() map[string]any { return s.outputs }

// Put records a step's output under its id, replacing any previous value.
func (s *State) Put(stepID string, out any) { s.outputs[stepID] = out }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS, including both arch tests.

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain/
git commit -m "Add the blueprint and state types

A blueprint is an ordered list of steps, each naming a tool and carrying
config. Nothing in these types describes a use case: what a blueprint does
lives in the tool names and the config, both of which come from a file.

State keeps starting variables and per-step output apart, and both maps are
non-nil from construction so a template referencing a step that has not run
renders empty rather than panicking."
```

---

### Task 3: Template rendering and the path selector

The one piece of real logic in the harness. Everything a pack can express passes through here, and the limits chosen here are the limits packs will hit.

**Files:**
- Create: `internal/core/app/render.go`
- Test: `internal/core/app/render_test.go`

**Interfaces:**
- Consumes: `domain.State`
- Produces: `app.Render(tmpl string, s *domain.State) (string, error)`, `app.Select(value any, path string) (any, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/core/app/render_test.go`:

```go
package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
)

func TestRenderSubstitutesVars(t *testing.T) {
	s := domain.NewState(map[string]string{"host": "example.com"})
	got, err := app.Render("https://{{ .vars.host }}/x", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "https://example.com/x" {
		t.Errorf("got %q", got)
	}
}

func TestRenderReadsEarlierStepOutputByPath(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("first", map[string]any{"title": "Widget", "inner": map[string]any{"deep": "value"}})

	got, err := app.Render("{{ .steps.first.title }} / {{ .steps.first.inner.deep }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "Widget / value" {
		t.Errorf("got %q", got)
	}
}

func TestRenderFailsOnAMissingKeyRatherThanEmittingNoValue(t *testing.T) {
	// A silently empty prompt is far worse than a failed run: the model would
	// be asked to work from nothing and would answer anyway.
	if _, err := app.Render("{{ .steps.absent.title }}", domain.NewState(nil)); err == nil {
		t.Error("Render succeeded on a missing key")
	}
}

func TestRenderLeavesPlainTextAlone(t *testing.T) {
	got, err := app.Render("no templates here", domain.NewState(nil))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "no templates here" {
		t.Errorf("got %q", got)
	}
}

func TestSelectNarrowsByDottedPath(t *testing.T) {
	value := map[string]any{
		"items": []any{
			map[string]any{"name": "first"},
			map[string]any{"name": "second"},
		},
	}
	got, err := app.Select(value, "items.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["name"] != "first" {
		t.Errorf("got %#v", got)
	}
}

func TestSelectWithAnEmptyPathReturnsTheWholeValue(t *testing.T) {
	got, err := app.Select(map[string]any{"a": 1}, "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got == nil {
		t.Error("Select with an empty path dropped the value")
	}
}

func TestSelectFailsOnAMissingKey(t *testing.T) {
	if _, err := app.Select(map[string]any{"a": 1}, "b"); err == nil {
		t.Error("Select succeeded on a missing key")
	}
}

func TestSelectFailsOnAnOutOfRangeIndex(t *testing.T) {
	if _, err := app.Select(map[string]any{"items": []any{}}, "items.0"); err == nil {
		t.Error("Select succeeded on an out-of-range index")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/app/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write render.go**

Create `internal/core/app/render.go`:

```go
// Package app runs blueprints: it resolves each step's tool, renders that
// step's config against accumulated state, executes, and records the result.
package app

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/tunedev/atlas/internal/core/domain"
)

// Render evaluates one config value as a Go template against state. Packs
// reach earlier output through .steps.<id>.<path> and their own variables
// through .vars.<name>.
//
// Option "missingkey=error" is the whole reason this is not a one-liner. The
// default emits "<no value>" into the string, which for a prompt means the
// model is asked to work from a gap and answers anyway — a silently wrong
// result rather than a failed run.
//
// Deliberately no conditionals, loops, or expression language: a blueprint is
// a sequence, not a program. The cheapest way to learn what expressiveness a
// pack actually needs is to run out of it with a real pack in hand.
func Render(tmpl string, s *domain.State) (string, error) {
	t, err := template.New("with").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("render: parse %q: %w", tmpl, err)
	}

	data := map[string]any{
		"vars":  s.Vars(),
		"steps": s.Outputs(),
	}

	var out strings.Builder
	if err := t.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render: execute %q: %w", tmpl, err)
	}
	return out.String(), nil
}

// Select narrows a tool's result by a dotted path before it is stored, so a
// pack can keep the one item it cares about rather than a whole response.
// Numeric segments index a slice; everything else is a map key. An empty path
// returns the value unchanged.
func Select(value any, path string) (any, error) {
	if path == "" {
		return value, nil
	}
	current := value
	for _, segment := range strings.Split(path, ".") {
		next, err := descend(current, segment)
		if err != nil {
			return nil, fmt.Errorf("select %q: %w", path, err)
		}
		current = next
	}
	return current, nil
}

func descend(value any, segment string) (any, error) {
	if i, err := strconv.Atoi(segment); err == nil {
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("segment %q indexes a %T, not a list", segment, value)
		}
		if i < 0 || i >= len(items) {
			return nil, fmt.Errorf("index %d is out of range, length %d", i, len(items))
		}
		return items[i], nil
	}

	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("segment %q reads a %T, not an object", segment, value)
	}
	found, ok := fields[segment]
	if !ok {
		return nil, fmt.Errorf("no key %q", segment)
	}
	return found, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS, all eight render tests plus both arch guards.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/
git commit -m "Add template rendering and the path selector

Everything a pack can express passes through here, so the limits chosen here
are the limits packs will hit: templates over vars and earlier step output,
a dotted path selector, and nothing else. No conditionals, no loops, no
expression language.

missingkey=error is load-bearing rather than fussy. The default renders a
missing key as <no value> inside the string, which in a prompt means the
model is asked to work from a gap and answers anyway. A failed run is much
better than a confidently wrong one."
```

---

### Task 4: The Tool port and the runner

**Files:**
- Create: `internal/core/ports/ports.go`, `internal/core/app/runner.go`
- Test: `internal/core/app/runner_test.go`

**Interfaces:**
- Consumes: `domain.Blueprint`, `domain.State`, `app.Render`, `app.Select`
- Produces:
  - `ports.Tool` with `Name() string` and `Invoke(ctx context.Context, with map[string]string) (any, error)`
  - `ports.Registry` with `Lookup(name string) (Tool, bool)`
  - `app.NewRunner(r ports.Registry) *app.Runner`, method `Run(ctx context.Context, b domain.Blueprint) (*domain.State, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/core/app/runner_test.go`:

```go
package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

type fakeTool struct {
	name   string
	result any
	err    error
	seen   []map[string]string
}

func (f *fakeTool) Name() string { return f.name }

func (f *fakeTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	f.seen = append(f.seen, with)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeRegistry map[string]ports.Tool

func (r fakeRegistry) Lookup(name string) (ports.Tool, bool) {
	t, ok := r[name]
	return t, ok
}

func TestRunExecutesStepsInOrderAndRecordsOutput(t *testing.T) {
	first := &fakeTool{name: "a", result: map[string]any{"value": "one"}}
	second := &fakeTool{name: "b", result: map[string]any{"value": "two"}}

	r := app.NewRunner(fakeRegistry{"a": first, "b": second})
	state, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Outputs()["s1"] == nil || state.Outputs()["s2"] == nil {
		t.Errorf("outputs = %#v", state.Outputs())
	}
}

func TestRunRendersConfigAgainstEarlierOutput(t *testing.T) {
	first := &fakeTool{name: "a", result: map[string]any{"title": "Widget"}}
	second := &fakeTool{name: "b", result: "ok"}

	r := app.NewRunner(fakeRegistry{"a": first, "b": second})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Vars: map[string]string{"who": "someone"},
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{
				"prompt": "{{ .vars.who }} wants {{ .steps.s1.title }}",
			}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(second.seen) != 1 {
		t.Fatalf("second tool invoked %d times", len(second.seen))
	}
	if got := second.seen[0]["prompt"]; got != "someone wants Widget" {
		t.Errorf("prompt = %q", got)
	}
}

func TestRunAppliesSelectBeforeStoring(t *testing.T) {
	only := &fakeTool{name: "a", result: map[string]any{
		"items": []any{map[string]any{"name": "first"}},
	}}

	r := app.NewRunner(fakeRegistry{"a": only})
	state, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "a", With: map[string]string{"select": "items.0"}}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored, ok := state.Outputs()["s1"].(map[string]any)
	if !ok || stored["name"] != "first" {
		t.Errorf("stored = %#v; select was not applied", state.Outputs()["s1"])
	}
}

func TestRunDoesNotPassSelectToTheTool(t *testing.T) {
	// select is the runner's instruction, not the tool's business.
	only := &fakeTool{name: "a", result: map[string]any{"items": []any{"x"}}}
	r := app.NewRunner(fakeRegistry{"a": only})
	if _, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "a", With: map[string]string{"select": "items.0"}}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, leaked := only.seen[0]["select"]; leaked {
		t.Error("select reached the tool")
	}
}

func TestRunFailsOnAnUnknownTool(t *testing.T) {
	r := app.NewRunner(fakeRegistry{})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "nope", With: map[string]string{}}},
	})
	if err == nil {
		t.Error("Run succeeded with an unregistered tool")
	}
}

func TestRunStopsAtTheFailingStep(t *testing.T) {
	boom := errors.New("boom")
	bad := &fakeTool{name: "a", err: boom}
	after := &fakeTool{name: "b", result: "unused"}

	r := app.NewRunner(fakeRegistry{"a": bad, "b": after})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{}},
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom wrapped", err)
	}
	if len(after.seen) != 0 {
		t.Error("a later step ran after a failure")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/app/ -v`
Expected: FAIL — `ports` and `app.NewRunner` undefined.

- [ ] **Step 3: Write the ports**

Create `internal/core/ports/ports.go`:

```go
package ports

import "context"

// Tool is one named unit of capability. The harness ships generic tools; a
// pack calls them by name and supplies their configuration.
//
// with arrives fully rendered: the runner has already evaluated every
// template against accumulated state, so a tool never sees template source
// and never reads state. That is what keeps tools generic.
//
// The return is any because a tool's result shape is its own business, and
// packs narrow it by path rather than the harness knowing its type.
type Tool interface {
	Name() string
	Invoke(ctx context.Context, with map[string]string) (any, error)
}

// Registry resolves a tool name to its implementation. Adding a tool is
// adding a registry entry; nothing dispatches on a name anywhere else.
type Registry interface {
	Lookup(name string) (Tool, bool)
}
```

- [ ] **Step 4: Write the runner**

Create `internal/core/app/runner.go`:

```go
package app

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// selectKey is the runner's own instruction inside a step's config: it names
// the path to narrow the tool's result to before storing it. It is stripped
// before the tool is invoked, because narrowing a result is the runner's
// business and a tool that knew about it would be less generic.
const selectKey = "select"

// tracer comes from the global provider, which telemetry.Init installs before
// any runner is constructed. With tracing disabled the global is a no-op and
// every span below costs nothing.
var tracer = otel.Tracer("github.com/tunedev/atlas/runner")

// Runner executes a blueprint: resolve each step's tool, render its config
// against accumulated state, invoke, narrow, store.
//
// Nothing here knows what any tool does or what any pack is for.
type Runner struct {
	registry ports.Registry
}

func NewRunner(r ports.Registry) *Runner { return &Runner{registry: r} }

// Run executes every step in order and stops at the first failure. The
// returned State carries each completed step's output keyed by step id.
func (r *Runner) Run(ctx context.Context, b domain.Blueprint) (*domain.State, error) {
	ctx, span := tracer.Start(ctx, "blueprint."+b.Name)
	defer span.End()
	span.SetAttributes(attribute.Int("blueprint.steps", len(b.Steps)))

	state := domain.NewState(b.Vars)
	for _, s := range b.Steps {
		if err := r.runStep(ctx, b.Name, s, state); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}
	return state, nil
}

// runStep is where a span per tool invocation is opened. Instrumenting here
// rather than inside each tool is what stops a newly added tool arriving
// untraced, and traces are how a pack author — who cannot read Go — finds out
// which step was slow or wrong.
func (r *Runner) runStep(ctx context.Context, blueprint string, s domain.Step, state *domain.State) error {
	ctx, span := tracer.Start(ctx, "tool."+s.Tool)
	defer span.End()
	span.SetAttributes(
		attribute.String("blueprint.name", blueprint),
		attribute.String("step.id", s.ID),
		attribute.String("tool.name", s.Tool),
	)

	tool, ok := r.registry.Lookup(s.Tool)
	if !ok {
		err := fmt.Errorf("blueprint %s: step %s: no tool named %q", blueprint, s.ID, s.Tool)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	with, path, err := renderConfig(s, state)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	result, err := tool.Invoke(ctx, with)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	narrowed, err := Select(result, path)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	state.Put(s.ID, narrowed)
	return nil
}

// renderConfig evaluates every config value as a template and lifts out the
// runner's own select key.
func renderConfig(s domain.Step, state *domain.State) (map[string]string, string, error) {
	with := make(map[string]string, len(s.With))
	var path string

	for key, raw := range s.With {
		rendered, err := Render(raw, state)
		if err != nil {
			return nil, "", err
		}
		if key == selectKey {
			path = rendered
			continue
		}
		with[key] = rendered
	}
	return with, path, nil
}
```

- [ ] **Step 5: Add the OTel API dependency**

```bash
go get go.opentelemetry.io/otel@latest
go mod tidy
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS. The arch test must still pass: `go.opentelemetry.io/otel` is the API, not an exporter.

- [ ] **Step 7: Commit**

```bash
git add internal/core/ go.mod go.sum
git commit -m "Add the Tool port and the blueprint runner

A tool receives fully rendered config and never reads state, which is what
keeps tools generic: the runner evaluates every template before invoking,
so a tool never sees template source.

select is the runner's instruction and is stripped before invocation.
Narrowing a result is the runner's business, and a tool that knew about it
would be less generic for it.

A span per tool invocation is opened here rather than inside each tool, so a
newly added tool cannot arrive untraced. Traces are the pack author's
debugger: they cannot read Go, and this is how they see which step was slow."
```

---

### Task 5: The pack loader

**Files:**
- Create: `internal/adapters/inbound/packfile/load.go`
- Test: `internal/adapters/inbound/packfile/load_test.go`

**Interfaces:**
- Consumes: `domain.Blueprint`
- Produces: `packfile.Load(path string) (domain.Blueprint, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/inbound/packfile/load_test.go`:

```go
package packfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadParsesAPack(t *testing.T) {
	path := write(t, `
name: example
vars:
  host: example.com
steps:
  - id: one
    tool: http.request
    with:
      url: "https://{{ .vars.host }}/a"
      select: items.0
  - id: two
    tool: model.complete
    with:
      system: be terse
      user: "{{ .steps.one.title }}"
`)

	b, err := packfile.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Name != "example" {
		t.Errorf("Name = %q", b.Name)
	}
	if b.Vars["host"] != "example.com" {
		t.Errorf("Vars = %#v", b.Vars)
	}
	if len(b.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(b.Steps))
	}
	if b.Steps[0].ID != "one" || b.Steps[0].Tool != "http.request" {
		t.Errorf("step 0 = %#v", b.Steps[0])
	}
	if b.Steps[0].With["select"] != "items.0" {
		t.Errorf("step 0 with = %#v", b.Steps[0].With)
	}
	if b.Steps[1].With["system"] != "be terse" {
		t.Errorf("step 1 with = %#v", b.Steps[1].With)
	}
}

func TestLoadRejectsAStepWithNoID(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - tool: http.request\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a step with no id; later steps reference output by id")
	}
}

func TestLoadRejectsADuplicateStepID(t *testing.T) {
	path := write(t, `
name: x
steps:
  - id: same
    tool: a
  - id: same
    tool: b
`)
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a duplicate step id; the second would overwrite the first's output")
	}
}

func TestLoadRejectsAStepWithNoTool(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - id: one\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a step with no tool")
	}
}

func TestLoadRejectsAPackWithNoSteps(t *testing.T) {
	path := write(t, "name: x\nsteps: []\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a pack with no steps")
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	if _, err := packfile.Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("Load succeeded on a missing file")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/inbound/packfile/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Add the YAML dependency**

```bash
go get gopkg.in/yaml.v3
go mod tidy
```

- [ ] **Step 4: Write the loader**

Create `internal/adapters/inbound/packfile/load.go`:

```go
// Package packfile reads a pack from a YAML file into a blueprint. This is an
// inbound adapter: a file is one way a blueprint reaches the core, and packs
// held somewhere else would be another.
package packfile

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/domain"
)

type pack struct {
	Name  string            `yaml:"name"`
	Vars  map[string]string `yaml:"vars"`
	Steps []step            `yaml:"steps"`
}

type step struct {
	ID   string            `yaml:"id"`
	Tool string            `yaml:"tool"`
	With map[string]string `yaml:"with"`
}

// Load reads and validates a pack file.
//
// Validation happens here rather than at run time because a pack author's
// feedback loop is the whole point: a duplicate step id should be an error
// naming the file, not an output that silently went missing after a model
// call has already been paid for.
func Load(path string) (domain.Blueprint, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("packfile: read %s: %w", path, err)
	}

	var p pack
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return domain.Blueprint{}, fmt.Errorf("packfile: parse %s: %w", path, err)
	}
	if p.Name == "" {
		return domain.Blueprint{}, fmt.Errorf("packfile: %s has no name", path)
	}
	if len(p.Steps) == 0 {
		return domain.Blueprint{}, fmt.Errorf("packfile: %s has no steps", path)
	}

	seen := make(map[string]bool, len(p.Steps))
	steps := make([]domain.Step, 0, len(p.Steps))
	for i, s := range p.Steps {
		if s.ID == "" {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s step %d has no id", path, i)
		}
		if seen[s.ID] {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s has two steps with id %q", path, s.ID)
		}
		if s.Tool == "" {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s step %q has no tool", path, s.ID)
		}
		seen[s.ID] = true

		with := s.With
		if with == nil {
			with = map[string]string{}
		}
		steps = append(steps, domain.Step{ID: s.ID, Tool: s.Tool, With: with})
	}

	vars := p.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	return domain.Blueprint{Name: p.Name, Vars: vars, Steps: steps}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS, including both arch guards — `yaml.v3` is forbidden inward and lives only in this adapter.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/inbound/packfile/ go.mod go.sum
git commit -m "Add the pack loader

An inbound adapter: a YAML file is one way a blueprint reaches the core, and
packs held somewhere else would be another.

Validation is at load rather than at run because a pack author's feedback
loop is the point. A duplicate step id should be an error naming the file,
not an output that silently went missing after a model call has already
been paid for."
```

---

### Task 6: The two generic tools

**Files:**
- Create: `internal/adapters/outbound/tools/http.go`, `internal/adapters/outbound/tools/model.go`, `internal/adapters/outbound/tools/registry.go`
- Test: `internal/adapters/outbound/tools/http_test.go`, `internal/adapters/outbound/tools/model_test.go`, `internal/adapters/outbound/tools/registry_test.go`

**Interfaces:**
- Consumes: `ports.Tool`, `ports.Registry`
- Produces:
  - `tools.NewHTTP(timeout time.Duration) *tools.HTTP` — `Name() == "http.request"`, config keys `url`, `method` (default GET)
  - `tools.NewModel(baseURL, model string, timeout time.Duration) *tools.Model` — `Name() == "model.complete"`, config keys `system`, `user`, `expect` (`json` parses the reply)
  - `tools.NewRegistry(list ...ports.Tool) tools.Registry`

- [ ] **Step 1: Write the HTTP test**

Create `internal/adapters/outbound/tools/http_test.go`:

```go
package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestHTTPFetchesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"first"}]}`))
	}))
	defer srv.Close()

	out, err := tools.NewHTTP(5*time.Second).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("out is %T, want map", out)
	}
	if _, ok := m["items"]; !ok {
		t.Errorf("out = %#v", m)
	}
}

func TestHTTPReturnsNonJSONAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain body"))
	}))
	defer srv.Close()

	out, err := tools.NewHTTP(5*time.Second).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("out is %T, want map", out)
	}
	if m["body"] != "plain body" {
		t.Errorf("out = %#v; a non-JSON body should be reachable as .body", m)
	}
}

func TestHTTPFailsWithoutAURL(t *testing.T) {
	if _, err := tools.NewHTTP(time.Second).Invoke(context.Background(), map[string]string{}); err == nil {
		t.Error("Invoke succeeded with no url")
	}
}

func TestHTTPFailsOnUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := tools.NewHTTP(5*time.Second).Invoke(context.Background(),
		map[string]string{"url": srv.URL}); err == nil {
		t.Error("Invoke succeeded on a 503")
	}
}

func TestHTTPName(t *testing.T) {
	if got := tools.NewHTTP(time.Second).Name(); got != "http.request" {
		t.Errorf("Name = %q", got)
	}
}
```

- [ ] **Step 2: Write the model test**

Create `internal/adapters/outbound/tools/model_test.go`:

```go
package tools_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func modelServer(t *testing.T, reply string, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if captured != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, captured)
		}
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
		})
		_, _ = w.Write(out)
	}))
}

func TestModelReturnsTextByDefault(t *testing.T) {
	var sent map[string]any
	srv := modelServer(t, "an answer", &sent)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second).Invoke(context.Background(),
		map[string]string{"system": "be terse", "user": "a question"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["text"] != "an answer" {
		t.Errorf("out = %#v; text should be reachable as .text", out)
	}
	msgs, ok := sent["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("sent messages = %#v", sent["messages"])
	}
}

func TestModelParsesJSONWhenAsked(t *testing.T) {
	srv := modelServer(t, `{"decision":"yes","reasons":["a","b"]}`, nil)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["decision"] != "yes" {
		t.Errorf("out = %#v; expect json should parse the reply into fields", out)
	}
}

func TestModelStripsAFenceBeforeParsingJSON(t *testing.T) {
	// Small local models wrap JSON in a markdown fence routinely. Failing on
	// formatting rather than on substance would be the wrong reason to fail.
	srv := modelServer(t, "```json\n{\"ok\":true}\n```", nil)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["ok"] != true {
		t.Errorf("out = %#v", out)
	}
}

func TestModelFailsWhenJSONIsExpectedAndNotReturned(t *testing.T) {
	srv := modelServer(t, "not json at all", nil)
	defer srv.Close()

	if _, err := tools.NewModel(srv.URL, "m", 5*time.Second).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"}); err == nil {
		t.Error("Invoke succeeded with expect=json and a non-JSON reply")
	}
}

func TestModelFailsWithoutAUserMessage(t *testing.T) {
	srv := modelServer(t, "x", nil)
	defer srv.Close()

	if _, err := tools.NewModel(srv.URL, "m", time.Second).Invoke(context.Background(),
		map[string]string{"system": "s"}); err == nil {
		t.Error("Invoke succeeded with no user message")
	}
}

func TestModelName(t *testing.T) {
	if got := tools.NewModel("http://x", "m", time.Second).Name(); got != "model.complete" {
		t.Errorf("Name = %q", got)
	}
}
```

- [ ] **Step 3: Write the registry test**

Create `internal/adapters/outbound/tools/registry_test.go`:

```go
package tools_test

import (
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestRegistryLooksUpByToolName(t *testing.T) {
	r := tools.NewRegistry(tools.NewHTTP(time.Second), tools.NewModel("http://x", "m", time.Second))

	if _, ok := r.Lookup("http.request"); !ok {
		t.Error("http.request not registered")
	}
	if _, ok := r.Lookup("model.complete"); !ok {
		t.Error("model.complete not registered")
	}
	if _, ok := r.Lookup("absent"); ok {
		t.Error("Lookup found a tool that was never registered")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/tools/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 5: Write the HTTP tool**

Create `internal/adapters/outbound/tools/http.go`:

```go
// Package tools holds the harness's generic tools and the registry that
// resolves their names. A tool here knows nothing about any use case: what it
// fetches, and what is done with the result, come from a pack.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTP fetches a URL. A JSON object body is returned parsed so packs can
// select into it by path; anything else is returned as text under "body".
type HTTP struct {
	client *http.Client
}

func NewHTTP(timeout time.Duration) *HTTP {
	return &HTTP{client: &http.Client{Timeout: timeout}}
}

func (h *HTTP) Name() string { return "http.request" }

func (h *HTTP) Invoke(ctx context.Context, with map[string]string) (any, error) {
	url := with["url"]
	if url == "" {
		return nil, fmt.Errorf("http.request: no url")
	}
	method := with["method"]
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http.request: build request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http.request: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http.request: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("http.request: %s returned %d", url, resp.StatusCode)
	}

	var parsed map[string]any
	if json.Unmarshal(body, &parsed) == nil {
		return parsed, nil
	}
	// Not JSON, or JSON that is not an object. Either way a pack can still
	// reach it, as text, rather than the run failing over a content type.
	return map[string]any{"body": string(body)}, nil
}
```

- [ ] **Step 6: Write the model tool**

Create `internal/adapters/outbound/tools/model.go`:

```go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Model calls an OpenAI-compatible chat completions endpoint. Ollama serves
// this locally, so a pack runs offline and free; synapse-gateway speaks the
// same protocol, so routing through it is a base URL change.
//
// With expect=json the reply is parsed into fields a pack can select by path.
// Without it the reply is returned as text under "text".
type Model struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewModel(baseURL, model string, timeout time.Duration) *Model {
	return &Model{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: timeout},
	}
}

func (m *Model) Name() string { return "model.complete" }

func (m *Model) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if with["user"] == "" {
		return nil, fmt.Errorf("model.complete: no user message")
	}

	messages := []map[string]string{}
	if s := with["system"]; s != "" {
		messages = append(messages, map[string]string{"role": "system", "content": s})
	}
	messages = append(messages, map[string]string{"role": "user", "content": with["user"]})

	payload, err := json.Marshal(map[string]any{
		"model":    m.model,
		"messages": messages,
		"stream":   false,
	})
	if err != nil {
		return nil, fmt.Errorf("model.complete: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("model.complete: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model.complete: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model.complete: model %s returned %d", m.model, resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("model.complete: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("model.complete: model %s returned no choices", m.model)
	}

	text := out.Choices[0].Message.Content
	if with["expect"] != "json" {
		return map[string]any{"text": text}, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(unfence(text)), &fields); err != nil {
		return nil, fmt.Errorf("model.complete: expected json, got %q: %w", text, err)
	}
	return fields, nil
}

// unfence strips a markdown code fence. Small local models wrap JSON in one
// routinely, and failing on formatting rather than on substance would be the
// wrong reason to fail.
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

- [ ] **Step 7: Write the registry**

Create `internal/adapters/outbound/tools/registry.go`:

```go
package tools

import "github.com/tunedev/atlas/internal/core/ports"

// Registry resolves a tool name to its implementation. Adding a tool is
// adding it to this map at the composition root; nothing switches on a tool
// name anywhere else.
type Registry map[string]ports.Tool

func NewRegistry(list ...ports.Tool) Registry {
	r := make(Registry, len(list))
	for _, t := range list {
		r[t.Name()] = t
	}
	return r
}

func (r Registry) Lookup(name string) (ports.Tool, bool) {
	t, ok := r[name]
	return t, ok
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/... -v`
Expected: PASS, all twelve tool tests plus everything earlier.

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/outbound/tools/
git commit -m "Add the two generic tools and the registry

http.request fetches a URL and returns a parsed JSON object where it can, or
the body as text where it cannot, so a content type never fails a run.
model.complete calls an OpenAI-compatible endpoint and optionally parses the
reply into fields a pack can select by path.

Neither knows what it is fetching or what the answer means. A tool that knew
about a use case would be pack logic in the wrong place.

The model tool strips a markdown fence before parsing JSON: small local
models emit one routinely, and failing on formatting rather than substance
would be the wrong reason to fail."
```

---

### Task 7: Telemetry

**Files:**
- Create: `internal/telemetry/telemetry.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `telemetry.Init(ctx context.Context, cfg config.Config) (func(context.Context) error, error)`

- [ ] **Step 1: Write telemetry.go**

Create `internal/telemetry/telemetry.go`:

```go
// Package telemetry wires OpenTelemetry's trace pipeline to an OTLP gRPC
// endpoint and installs the tracer provider as the process-wide global. It is
// called once, from cmd/atlas, before the runner is constructed: the runner
// reads otel.Tracer at package init, so Init must run first.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/tunedev/atlas/internal/config"
)

// Init builds the trace provider and returns a shutdown func that must run on
// every exit path, including error paths, or a short run's spans are dropped
// with it.
//
// When cfg.OTel.Enabled is false, Init installs nothing and returns a no-op
// shutdown. The global provider stays the SDK's default no-op, so every span
// in the runner costs nothing and the binary runs with no collector.
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

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/... -v`
Expected: PASS. The arch test confirms `internal/core` does not reach the exporter this package imports.

- [ ] **Step 4: Commit**

```bash
git add internal/telemetry/ go.mod go.sum
git commit -m "Add the OTLP trace pipeline

Disabled by default and a no-op when off, so the binary runs with no
collector and every span in the runner costs nothing.

Sampling is AlwaysOn where enabled: the SDK samples before a run's outcome
is known and the collector samples after, which is the only order that can
keep every error."
```

---

### Task 8: Two packs, the composition root, and the increment note

The second pack is the increment. Write it after the first one runs, without touching Go — if that turns out to be impossible, that is the finding.

**Files:**
- Create: `packs/job-hunt.yaml`, `packs/hn-summary.yaml`, `cmd/atlas/main.go`, `docs/notes/2026-08-25-increment-1.md`

**Interfaces:**
- Consumes: everything above
- Produces: the `atlas` binary

- [ ] **Step 1: Write the composition root**

Create `cmd/atlas/main.go`:

```go
// Command atlas runs a pack. This is the only file that knows every concrete
// type, and it knows nothing about what any pack does.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/telemetry"
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

	blueprint, err := packfile.Load(cfg.Pack.Path)
	if err != nil {
		return err
	}

	registry := tools.NewRegistry(
		tools.NewHTTP(cfg.Pack.HTTPTimeout),
		tools.NewModel(cfg.Model.BaseURL, cfg.Model.Name, cfg.Model.Timeout),
	)

	state, err := app.NewRunner(registry).Run(ctx, blueprint)
	if err != nil {
		return err
	}

	// The harness cannot format a result it does not understand, so it prints
	// each step's output as JSON and lets the reader make sense of it.
	out, err := json.MarshalIndent(state.Outputs(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Println(string(out))
	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Write the first pack**

Create `packs/job-hunt.yaml`:

```yaml
name: job-hunt
vars:
  board_api: https://boards-api.greenhouse.io
  board: gitlab
  profile: "Backend engineer. Go, distributed systems, Kubernetes, Postgres, OpenTelemetry."

steps:
  - id: role
    tool: http.request
    with:
      url: "{{ .vars.board_api }}/v1/boards/{{ .vars.board }}/jobs?content=true"
      select: jobs.0

  - id: verdict
    tool: model.complete
    with:
      expect: json
      system: |
        You judge whether a job advertisement suits a candidate.
        Reply with JSON only: {"decision":"apply|stretch|skip","reasons":["...","..."]}
        decision must be exactly one of apply, stretch, or skip.
        reasons must be short and specific to this role and this candidate.
      user: |
        Candidate: {{ .vars.profile }}

        Role: {{ .steps.role.title }} at {{ .steps.role.company_name }}
        Location: {{ .steps.role.location.name }}

        {{ .steps.role.content }}

  - id: documents
    tool: model.complete
    with:
      system: |
        You draft application documents.
        Output exactly two sections separated by a line containing only ---
        Section one is a tailored CV. Section two is a letter of application.
        Use only facts given about the candidate. State a gap plainly rather
        than inventing experience.
      user: |
        Candidate: {{ .vars.profile }}
        Verdict was {{ .steps.verdict.decision }}.

        Role: {{ .steps.role.title }} at {{ .steps.role.company_name }}

        {{ .steps.role.content }}
```

- [ ] **Step 4: Run the first pack against the real world**

Run:
```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml
```
Expected: JSON with three keys — `role`, `verdict`, `documents`. A model call on a laptop takes tens of seconds per step, which is why the model timeout default is minutes.

Note: the board API returns `content` as HTML-entity-escaped HTML. The model reads it anyway. If output quality suffers for it, record that in the note rather than adding an HTML-stripping tool now — wanting one is exactly the kind of evidence this increment exists to produce.

- [ ] **Step 5: Write the second pack, changing no Go**

Create `packs/hn-summary.yaml`:

```yaml
name: hn-summary
vars:
  item: "8863"

steps:
  - id: item
    tool: http.request
    with:
      url: "https://hacker-news.firebaseio.com/v0/item/{{ .vars.item }}.json"

  - id: summary
    tool: model.complete
    with:
      system: "You summarise a discussion in one sentence. Be plain and specific."
      user: "Title: {{ .steps.item.title }}\nBy: {{ .steps.item.by }}\nScore: {{ .steps.item.score }}"
```

- [ ] **Step 6: Run the second pack — this is the increment**

Run:
```bash
git status --porcelain -- '*.go'   # must print nothing
go run ./cmd/atlas -pack packs/hn-summary.yaml
```
Expected: JSON with `item` and `summary`, and **no Go file modified since the first pack ran.**

If any Go change was needed, stop and record exactly what and why. That is the increment's actual result, and a negative one is worth more than a passing test — it names the first place the harness abstraction leaks.

- [ ] **Step 7: Run with tracing**

In one terminal:
```bash
docker run --rm -p 4317:4317 otel/opentelemetry-collector:latest
```

In another:
```bash
OTEL_RESOURCE_ATTRIBUTES=service.name=atlas,deployment.environment.name=local \
  go run ./cmd/atlas -pack packs/job-hunt.yaml -otel
```
Expected: a trace with `blueprint.job-hunt` over child spans `tool.http.request`, `tool.model.complete`, `tool.model.complete`, each carrying `step.id`.

- [ ] **Step 8: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS, every package, both arch guards included.

- [ ] **Step 9: Write the increment note**

Create `docs/notes/2026-08-25-increment-1.md`:

```markdown
# Increment 1 — the harness tracer

## What the pattern was

## What surprised me

The question this increment existed to answer: did the second pack run with
no Go changes? If not, what did it need, and is that a missing generic tool
or a leak of use-case knowledge into the harness?

Also: did templates plus a path selector reach far enough, or was the first
thing wanted a conditional? Which model, and did it hold the JSON contract?

## What I would do differently

## What I still do not understand
```

- [ ] **Step 10: Commit**

```bash
git add cmd/ packs/ docs/notes/
git commit -m "Run two packs on one harness

The first pack fetches a real advertisement from a public board API, judges
it against a profile, and drafts application documents. Every word specific
to that use case lives in packs/job-hunt.yaml; the Go tree contains none of
it, enforced by the vocabulary guard.

The second pack is the increment. An unrelated summarisation use case runs
on the same binary with no Go change, which is the only evidence that this
is a harness rather than an application with a config file.

The composition root prints each step's output as JSON: the harness cannot
format a result it does not understand."
```

---

## What increment 2 inherits

The registry, the runner, rendering, and both tools. Increment 2 adds the
permission model — allow, ask, deny over tool invocations — which pack one
already needs and which the harness owns.

`ports.Tool` is the seam everything later hangs from: permissions wrap
invocation, the sandbox wraps execution, and memory becomes another tool.
