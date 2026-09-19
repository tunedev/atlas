# Increment 0 — Finish the tracer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the harness exists by running two unrelated packs from YAML with no Go change between them.

**Architecture:** Everything is already written. `cmd/atlas/main.go` loads config, starts telemetry, loads a pack, builds the tool registry from config, injects a tracer into the runner, runs the blueprint and prints each step's output as JSON. Two packs exist. None of it has ever been executed.

**Tech Stack:** Go 1.27, Ollama on `http://localhost:11434/v1` serving `qwen3.5:9b`, Greenhouse's public board API, Hacker News' public item API, OpenTelemetry OTLP gRPC.

**Spec:** `docs/specs/2026-09-17-job-hunt-harness-design.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md` (Epic 0)

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL or string constant naming a use-case concept outside `packs/`. `internal/arch/vocabulary_test.go` enforces it.
- No core package imports an adapter or a driver; `internal/core/...` compiles in neither `net/http` nor `crypto/tls`. `internal/arch/arch_test.go` enforces both.
- `ctx context.Context` first parameter of every blocking or remote call. Never stored in a struct.
- Every remote call has a timeout. Every response body is bounded.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis in code, logs or output.
- Comments describe current behaviour only. No history, no dates, no ticket references.
- Tests assert behaviour. A test that cannot fail is a defect.

## What "done" means

Three commands, with pasted output:

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml
go run ./cmd/atlas -pack packs/hn-summary.yaml
go test ./... -race
```

The second command is the increment. The first is only its excuse. **If `hn-summary.yaml` requires any change to a `.go` file, the harness does not exist and this increment has failed — report that rather than editing Go to accommodate it.**

## The starting state

`6c8af76` is on `increment-1` and is honest about itself: *"composition root and two packs, unverified"*. The code was written by a task that was cut off before it could run anything. Treat every line of `cmd/atlas/main.go` as unproven.

Two things in it were deliberately added against its original brief and must survive:

1. The tracer is obtained **after** `telemetry.Init` and injected via `.WithTracer(...)`. The runner defaults to a no-op tracer and no longer reads the global registry, so without this line no blueprint span exists.
2. Every timeout and byte limit is read from `config`, never written as a literal.

## File Structure

| File | Responsibility | State |
|---|---|---|
| `cmd/atlas/main.go` | Composition root; the only file knowing every concrete type | Written, unrun |
| `packs/job-hunt.yaml` | Pack one: fetch a posting, judge it, draft documents | Written, unrun |
| `packs/hn-summary.yaml` | Pack two, unrelated. The proof | Written, unrun |
| `cmd/atlas/main_test.go` | Asserts the composition root wires config through | To create |
| `docs/notes/2026-09-17-increment-0.md` | The increment note | To create |

---

### Task 1: Run the tracer and fix whatever is actually broken

The code is unproven, so this task is empirical. Do not assume it works; do not assume it does not.

**Files:**
- Modify: `cmd/atlas/main.go` (only if running it proves something wrong)
- Modify: `packs/*.yaml` (only if a template or selector is wrong)

**Interfaces:**
- Consumes: `config.Load(args []string) (config.Config, error)`; `telemetry.Init(ctx context.Context, cfg config.Config) (func(context.Context) error, error)`; `packfile.Load(path string) (domain.Blueprint, error)`; `tools.NewRegistry(list ...ports.Tool) tools.Registry`; `tools.NewHTTP(timeout time.Duration, maxBytes int64) *tools.HTTP`; `tools.NewModel(baseURL, model string, timeout time.Duration, maxBytes int64) *tools.Model`; `app.NewRunner(reg ports.Registry) *app.Runner` with `.WithTracer(t trace.Tracer) *app.Runner` and `.Run(ctx context.Context, b domain.Blueprint) (*domain.State, error)`
- Produces: a binary that runs a pack

- [ ] **Step 1: Confirm the environment before blaming the code**

```bash
curl -s -m 5 http://localhost:11434/api/tags | head -c 200
```

Expected: a JSON list of models including `qwen3.5:9b`.

If Ollama is not running, STOP and report it. Do not start services, install anything, or change the model default to work around it.

- [ ] **Step 2: Build**

```bash
go build ./... && go vet ./...
```

Expected: no output. If it fails, the composition root does not compile against the packages it was written for — record the exact error in your report before fixing it.

- [ ] **Step 3: Run the second pack first**

Run the unrelated one first, deliberately: it is the binding test, it touches only a public JSON API and a model, and it has fewer ways to fail for uninteresting reasons.

```bash
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

Expected: JSON on stdout with two keys, `item` and `summary`. `summary` is one sentence about the discussion.

- [ ] **Step 4: Fix what that revealed, if anything**

Likely failure modes, in the order they are worth checking:

- A template references a field the API did not return. `Render` uses `missingkey=error`, so this fails loudly rather than emitting an empty string. Fix the pack's template, not the renderer.
- A step's config key is misspelled. `http.request` reads `url` and `method`; `model.complete` reads `system`, `user` and `expect`.
- `model.complete` returns a non-JSON body when `expect: json` is set. That is the model misbehaving, and it is the tool's job to error. Do not weaken the check.

Change the minimum. If a fix requires touching a `.go` file, say so explicitly in your report and name which file and why — that is a finding about the harness, not routine work.

- [ ] **Step 5: Run the job pack**

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Expected: JSON with three keys, `role`, `verdict` and `documents`. `verdict` is an object with `decision` in `apply|stretch|skip` and a `reasons` array. `documents` is text with two sections separated by a line containing only `---`.

- [ ] **Step 6: Prove the binding test**

```bash
git status --short
```

Expected: **no modified `.go` files** since Step 2. If any Go file changed in order to make the second pack run, the increment has failed its own test. Report it plainly; do not proceed to Step 7 as though it passed.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "Run both packs, and fix what running them revealed"
```

If nothing needed fixing, commit nothing and say so — an empty fix is the best possible outcome here.

---

### Task 2: Prove the composition root reads config rather than literals

Running the packs proves they work. It does not prove the timeouts and limits came from config — a hardcoded literal would behave identically on a fast network.

**Files:**
- Create: `cmd/atlas/main_test.go`
- Modify: `cmd/atlas/main.go` (extract a constructor if the test cannot reach the wiring)

**Interfaces:**
- Consumes: everything Task 1 consumes
- Produces: `main.build(cfg config.Config) (*app.Runner, error)` if extraction is needed

- [ ] **Step 1: Write the failing test**

Two separate properties, because one test cannot honestly cover both. The tools do not
expose their timeout, so no test can observe the value; what a test *can* pin is that the
constructor takes only config and contains no literal to pass instead.

Create `cmd/atlas/main_test.go`:

```go
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/config"
)

func TestBuildRegistryRegistersBothShippedTools(t *testing.T) {
	cfg := config.Config{}
	cfg.Pack.HTTPTimeout = 1234 * time.Millisecond
	cfg.Pack.HTTPMaxBytes = 4321
	cfg.Model.BaseURL = "http://example.invalid/v1"
	cfg.Model.Name = "a-model"
	cfg.Model.Timeout = 5678 * time.Millisecond
	cfg.Model.MaxBytes = 8765

	reg := buildRegistry(cfg)

	if _, ok := reg.Lookup("http.request"); !ok {
		t.Error("http.request is not registered")
	}
	if _, ok := reg.Lookup("model.complete"); !ok {
		t.Error("model.complete is not registered")
	}
	if _, ok := reg.Lookup("nonexistent.tool"); ok {
		t.Error("Lookup reported a tool that was never registered")
	}
}

// A hardcoded timeout and a configured one behave identically against a fast
// endpoint, so the property is checked structurally: buildRegistry must pass
// configured values through and never a literal of its own.
func TestBuildRegistryPassesNoLiterals(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "buildRegistry" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok {
				return true
			}
			if lit.Kind == token.INT || lit.Kind == token.FLOAT {
				t.Errorf("buildRegistry contains the literal %s; every value must come from config", lit.Value)
			}
			return true
		})
		return
	}
	t.Fatal("buildRegistry not found in main.go")
}
```

- [ ] **Step 5: Assert the shutdown path cannot be skipped**

`os.Exit` does not run deferred functions, so a `defer shutdown(...)` in the same function as an `os.Exit` would silently lose every span. Add to `cmd/atlas/main_test.go`:

```go
// main() calls os.Exit; run() must therefore own every defer, or the
// telemetry shutdown never executes and spans are lost on the error path.
func TestRunIsSeparateFromMainSoDefersExecute(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if _, ok := n.(*ast.DeferStmt); ok {
				t.Error("main() contains a defer; os.Exit will skip it")
			}
			return true
		})
	}
}
```

The imports this needs are already present from Step 1.

- [ ] **Step 6: Run the whole suite**

Run: `go test ./... -race`
Expected: PASS, every package.

- [ ] **Step 7: Commit**

```bash
git add cmd/atlas
git commit -m "Assert the composition root wires config and cannot skip its shutdown"
```

---

### Task 3: Prove the blueprint is traced

The runner's spans are the increment's observability claim, and they exist only because `main.go` injects a tracer. That line is one deletion away from silently producing no blueprint spans ever again.

**Files:**
- Modify: `cmd/atlas/main_test.go`
- Create: `docs/notes/2026-09-17-increment-0.md`

- [ ] **Step 1: Write the failing test**

A real collector is not available in tests. Assert instead that the composition root injects a tracer at all — which is the thing that can regress.

Add to `cmd/atlas/main_test.go`:

```go
// The runner defaults to a no-op tracer and no longer reads the global
// registry, so a composition root that does not call WithTracer produces no
// blueprint spans at all.
func TestCompositionRootInjectsATracer(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !bytes.Contains(src, []byte("WithTracer(")) {
		t.Error("main.go never calls WithTracer; the runner will keep its no-op tracer")
	}
	initAt := bytes.Index(src, []byte("telemetry.Init("))
	tracerAt := bytes.Index(src, []byte("otel.Tracer("))
	if initAt < 0 {
		t.Fatal("main.go never calls telemetry.Init")
	}
	if tracerAt < 0 {
		t.Fatal("main.go never obtains a tracer")
	}
	if tracerAt < initAt {
		t.Error("the tracer is obtained before telemetry.Init installs a provider; it will be the no-op one")
	}
}
```

Add imports `"bytes"` and `"os"`. Both are new to this file: Task 2's tests parse `main.go` through `parser.ParseFile` and never read it as bytes.

- [ ] **Step 2: Run it**

Run: `go test ./cmd/atlas/ -run TestCompositionRootInjectsATracer -v`
Expected: PASS, because `6c8af76` already does this correctly. If it FAILS, the ordering regressed and that is a real finding.

- [ ] **Step 3: Observe a real trace**

Start any OTLP collector on `localhost:4317`, then:

```bash
go run ./cmd/atlas -pack packs/hn-summary.yaml -otel
```

Expected: a span named for the blueprint with a child span per step, each carrying the tool name and step id.

If no collector is available in this environment, STOP and report that — do not fake the evidence, and do not skip to Step 4 claiming the trace was seen.

- [ ] **Step 4: Write the increment note**

Create `docs/notes/2026-09-17-increment-0.md` with four sections: what the pattern was, what surprised you, what you would do differently, what you still do not understand.

It must record honestly:

- Whether either pack needed a `.go` change, and if so which and why.
- Whether the trace was actually observed or only asserted structurally.
- What the packs revealed about the blueprint format that the format's designer did not anticipate — the first real use of a format always finds something.

A note saying everything went fine is worthless. If everything did go fine, say what that suggests is under-tested.

- [ ] **Step 5: Run everything and commit**

```bash
go build ./... && go vet ./... && go test ./... -race
go run ./cmd/atlas -pack packs/job-hunt.yaml
go run ./cmd/atlas -pack packs/hn-summary.yaml
git add -A
git commit -m "Prove the blueprint is traced, and record what the first real packs taught"
```

Paste all output into the report.

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| vLLM | Epic 2. Ollama is serving `qwen3.5:9b` today and the base URL is already OpenAI-compatible, so the swap is a config line later |
| Git, SQLite, DuckDB | Epic 1. This increment persists nothing |
| Any job-hunt logic in Go | Forbidden permanently. It lives in `packs/` |
| A second model, retries, breakers | Epic 2 |
| Making the output pretty | The harness cannot format a result it does not understand. JSON is correct here |
