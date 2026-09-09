# Increment 1, Part 2 — Boundaries — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the harness's determinism claim testable — a versioned RPC contract, a container sandbox, a credential broker the sandbox never holds tokens for, and a run that suspends for a human and resumes from the step that stopped.

**Architecture:** Part 1 (`2026-08-25-increment-1-harness.md`, Tasks 1-8) builds the tracer: a tool registry, a blueprint runner, two generic tools, and two packs. This plan continues at Task 9. It puts a `connect-go` handler in front of the runner, persists runs so one can outlive its process, wraps tool execution in a container whose workspace is a host directory bind-mounted in, adds a fourth permission outcome, and routes external calls through a broker that injects identity on egress.

**Tech Stack:** Go 1.27, `connectrpc.com/connect`, `buf` for codegen, `github.com/jackc/pgx/v5`, `github.com/docker/docker` client, `github.com/chromedp/chromedp`, Pipedream Connect REST API, OpenTelemetry OTLP gRPC, Postgres 16.

**Spec:** `docs/specs/2026-09-09-increment-1-boundaries-design.md`

**Part 1:** `docs/plans/2026-08-25-increment-1-harness.md` — Tasks 1-8, unchanged and still authoritative. Two globals in it were corrected: the module path is `github.com/tunedev/atlas` (not `forge/atlas`) and the toolchain is Go 1.27 (not 1.24.6).

## Global Constraints

Every task's requirements implicitly include this section.

- Go 1.27. Module path `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL, or string constant naming a use-case concept outside `packs/`. Enforced by `internal/arch/vocabulary_test.go` from Part 1 Task 1. A task that violates it has failed regardless of its tests.
- Hexagonal layout: `internal/core/{domain,ports,app}`, `internal/adapters/{inbound,outbound}`, `cmd/atlas/main.go` as the only file knowing every concrete type. Enforced by `internal/arch/arch_test.go`.
- **Ports never speak protobuf.** Generated types stop at the adapter boundary, exactly as `*sql.Tx` does. A port signature carrying a generated message is a failed task.
- `ctx context.Context` is the first parameter of every blocking or remote function. Never stored in a struct.
- Every remote call has a timeout. The default is not "no timeout".
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis in code, log lines, or output.
- Comments describe current behaviour only. No history, no bug narratives, no dates, no ticket references.
- Tests that assert a mock was called are not written. Assert behaviour.

## What "done" means

Four commands, each with pasted output:

```bash
# Part 1's proof, unchanged and still binding
go run ./cmd/atlas -pack packs/job-hunt.yaml
go run ./cmd/atlas -pack packs/hn-summary.yaml     # zero Go changes

# Part 2's proof
go test ./internal/adapters/outbound/sandbox/... -run TestEscape -v
go test ./internal/core/app -run TestSuspendResume -v
```

Plus one manual demonstration: start the server, run a pack that needs an unauthorized integration, watch it suspend, authorize out of band, call `ResumeRun`, watch it finish.

## Demoable cuts

The sequence is deliberate. Two points produce something worth showing:

| After | What runs | What it proves |
|---|---|---|
| **Task 8** (Part 1) | Two packs from YAML, traced | The harness exists — a second pack needed no Go |
| **Task 11** | The above, every tool inside a container, escape suite green | Isolation is real, not asserted |
| **Task 14** | The above, plus suspend and resume across a human authorization | The whole claim |

If time runs out, it runs out after a cut, not in the middle of one.

## File Structure

| File | Responsibility |
|---|---|
| `proto/atlas/v1/atlas.proto` | The versioned contract |
| `buf.yaml`, `buf.gen.yaml` | Codegen configuration |
| `internal/gen/atlas/v1/...` | Generated types and `atlasv1connect` handler interface |
| `internal/core/domain/run.go` | `Run`, `RunState`, the state machine |
| `internal/core/domain/permission.go` | `Outcome`, `Rule`, `RuleSet` |
| `internal/core/ports/runs.go` | `Runs` — persist and load a run |
| `internal/core/ports/broker.go` | `Broker` — resolve entitlements, sign an outbound request |
| `internal/core/ports/sandbox.go` | `Sandbox` — a workspace an execution cannot leave |
| `internal/core/ports/store.go` | `Store` — a workspace's durable bytes |
| `internal/core/app/permission.go` | Evaluate a tool invocation against a `RuleSet` |
| `internal/core/app/resume.go` | Re-enter a suspended run at its stopped step |
| `internal/adapters/inbound/rpc/server.go` | Connect handler over the runner |
| `internal/adapters/outbound/provider/ollama.go` | `Provider` over Ollama |
| `internal/adapters/outbound/runstore/postgres.go` | `Runs` over Postgres |
| `internal/adapters/outbound/filestore/disk.go` | `Store` over a host directory |
| `internal/adapters/outbound/sandbox/docker.go` | `Sandbox` over the Docker API |
| `internal/adapters/outbound/sandbox/escape_test.go` | The escape suite |
| `internal/adapters/outbound/broker/pipedream.go` | `Broker` over Pipedream Connect |
| `internal/adapters/outbound/tools/exec.go` | `sandbox.exec` |
| `internal/adapters/outbound/tools/browser.go` | `browser.fetch`, read-only |
| `migrations/0001_runs.sql` | The `runs` table |
| `docs/notes/2026-09-09-increment-1-boundaries.md` | The increment note |

---

## A correction this plan makes to the spec

The spec says the host mounts *the workspace's scoped VFS* via `fusefs` and bind-mounts the result. `fusefs` lives in nerve, and the spec's own `Store` port starts on disk, so there is no scoped VFS in this increment.

**What is built instead:** the `Store` disk adapter creates one directory per run on the host; the sandbox bind-mounts that directory and nothing else. The container code, the bind-mount, and the escape suite are identical either way. Swapping the disk directory for a `fusefs` mount later changes one adapter and no container code — which is the point of the port.

The escape suite still means something: it asserts a process in the container cannot address a path outside its bind-mount. It does not yet assert anything about symlink resolution *inside* a scoped VFS, because there is no scoped VFS. Task 11 records that distinction in the note rather than overstating what passed.

---

## Amendment to Part 1: the Provider port

The spec names five ports. Part 1 creates `internal/core/ports/ports.go` with `Tool` and `Registry` only, and its `model.complete` tool talks to Ollama directly. That is one port short, and `Provider` earns its place under the calibration rule: Claude, Gemini and OpenAI are nameable second implementations, and the product design already makes provider choice a user-facing registry.

Do this before Task 9. It is small, and every later task assumes the split.

- [ ] **Step 1: Write the port**

Append to `internal/core/ports/ports.go`:

```go
// Completion is what a provider returned for one prompt.
type Completion struct {
	Text  string
	Model string
}

// Provider turns a prompt into text. The tool that calls it does not know
// which vendor answered.
type Provider interface {
	Complete(ctx context.Context, system, user string) (Completion, error)
}
```

- [ ] **Step 2: Move the Ollama call behind it**

Create `internal/adapters/outbound/provider/ollama.go` holding the HTTP call `model.complete` currently makes. `tools.NewModel` takes a `ports.Provider` instead of a base URL, and `model.complete` becomes provider-agnostic: it renders the prompt, calls `Complete`, and applies `expect: json`.

- [ ] **Step 3: Assert the tool does not name a vendor**

Add to `internal/adapters/outbound/tools/model_test.go` a test that drives `model.complete` with a stub `ports.Provider` returning fixed text, asserting the tool returns it. If the test needs an Ollama URL to compile, the split is incomplete.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/adapters/outbound/... ./internal/core/... -race
git add internal/core/ports/ports.go internal/adapters/outbound/provider internal/adapters/outbound/tools cmd/atlas
git commit -m "Put a port between the tool that asks and the vendor that answers"
```

`Completion.Model` exists so the product design's requirement — "the platform states which provider produced any given output" — has somewhere to read from. It is recorded on the step output, not logged.

---

### Task 9: The contract and the Connect handler

The runner exists and is called from `main`. This puts a versioned RPC surface in front of it. `ResumeRun` is defined now and returns `unimplemented` until Task 14; the contract does not change when A2H lands.

**Files:**
- Create: `proto/atlas/v1/atlas.proto`, `buf.yaml`, `buf.gen.yaml`, `internal/adapters/inbound/rpc/server.go`
- Test: `internal/adapters/inbound/rpc/server_test.go`
- Modify: `cmd/atlas/main.go`, `internal/config/config.go`

**Interfaces:**
- Consumes: `app.Runner.Run(ctx context.Context, bp domain.Blueprint, vars map[string]string) (domain.State, error)` and `packfile.Load(path string) (domain.Blueprint, error)` from Part 1.
- Produces: `rpc.NewServer(runner *app.Runner, packs PackLister) *rpc.Server` implementing `atlasv1connect.AtlasServiceHandler`; `rpc.PackLister` with `List(ctx context.Context) ([]domain.PackSummary, error)`.

- [ ] **Step 1: Write the contract**

Create `proto/atlas/v1/atlas.proto`:

```proto
syntax = "proto3";

package atlas.v1;

option go_package = "github.com/tunedev/atlas/internal/gen/atlas/v1;atlasv1";

service AtlasService {
  rpc ListPacks(ListPacksRequest) returns (ListPacksResponse) {}
  rpc RunBlueprint(RunBlueprintRequest) returns (RunBlueprintResponse) {}
  rpc GetRun(GetRunRequest) returns (GetRunResponse) {}
  rpc ResumeRun(ResumeRunRequest) returns (ResumeRunResponse) {}
}

enum RunState {
  RUN_STATE_UNSPECIFIED = 0;
  RUN_STATE_PENDING = 1;
  RUN_STATE_RUNNING = 2;
  RUN_STATE_COMPLETED = 3;
  RUN_STATE_FAILED = 4;
  RUN_STATE_SUSPENDED = 5;
}

message PackSummary {
  string name = 1;
  string description = 2;
}

message StepOutput {
  string step_id = 1;
  string json = 2;
}

message ListPacksRequest {}

message ListPacksResponse {
  repeated PackSummary packs = 1;
}

message RunBlueprintRequest {
  string pack = 1;
  map<string, string> vars = 2;
}

message RunBlueprintResponse {
  string run_id = 1;
  RunState state = 2;
}

message GetRunRequest {
  string run_id = 1;
}

message GetRunResponse {
  string run_id = 1;
  RunState state = 2;
  int32 current_step = 3;
  repeated StepOutput outputs = 4;
  string waiting_for = 5;
  string failure = 6;
}

message ResumeRunRequest {
  string run_id = 1;
}

message ResumeRunResponse {
  string run_id = 1;
  RunState state = 2;
}
```

`waiting_for` carries the integration slug a suspended run needs. It is empty in every other state.

- [ ] **Step 2: Configure codegen**

Create `buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

Create `buf.gen.yaml`:

```yaml
version: v2
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go
    out: internal/gen
    opt: paths=source_relative
  - remote: buf.build/connectrpc/go
    out: internal/gen
    opt: paths=source_relative
```

- [ ] **Step 3: Generate**

```bash
cd /home/tunedev/forge/atlas
go get connectrpc.com/connect@latest
go get google.golang.org/protobuf@latest
buf generate
```

Expected: `internal/gen/atlas/v1/atlas.pb.go` and `internal/gen/atlas/v1/atlasv1connect/atlas.connect.go` exist.

If `buf` is not installed, stop and report it. Do not hand-write generated code.

- [ ] **Step 4: Write the failing test**

Create `internal/adapters/inbound/rpc/server_test.go`:

```go
package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/inbound/rpc"
	atlasv1 "github.com/tunedev/atlas/internal/gen/atlas/v1"
	"github.com/tunedev/atlas/internal/gen/atlas/v1/atlasv1connect"
)

func TestListPacksOverHTTPJSON(t *testing.T) {
	srv := rpc.NewServer(nil, stubLister{names: []string{"alpha", "beta"}})

	mux := http.NewServeMux()
	mux.Handle(atlasv1connect.NewAtlasServiceHandler(srv))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := atlasv1connect.NewAtlasServiceClient(ts.Client(), ts.URL)
	res, err := client.ListPacks(context.Background(), connect.NewRequest(&atlasv1.ListPacksRequest{}))
	if err != nil {
		t.Fatalf("ListPacks: %v", err)
	}
	if got := len(res.Msg.Packs); got != 2 {
		t.Fatalf("packs = %d, want 2", got)
	}
	if res.Msg.Packs[0].Name != "alpha" {
		t.Fatalf("first pack = %q, want alpha", res.Msg.Packs[0].Name)
	}
}

func TestResumeRunIsUnimplementedUntilA2H(t *testing.T) {
	srv := rpc.NewServer(nil, stubLister{})
	_, err := srv.ResumeRun(context.Background(), connect.NewRequest(&atlasv1.ResumeRunRequest{RunId: "r1"}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("code = %v, want unimplemented", connect.CodeOf(err))
	}
}
```

Add the missing import `"connectrpc.com/connect"` and a `stubLister` in the same file:

```go
type stubLister struct{ names []string }

func (s stubLister) List(context.Context) ([]domain.PackSummary, error) {
	out := make([]domain.PackSummary, 0, len(s.names))
	for _, n := range s.names {
		out = append(out, domain.PackSummary{Name: n})
	}
	return out, nil
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/adapters/inbound/rpc/... -v`
Expected: FAIL — package `rpc` does not exist.

- [ ] **Step 6: Add the domain type**

Append to `internal/core/domain/blueprint.go`:

```go
// PackSummary names a pack this deployment can run.
type PackSummary struct {
	Name        string
	Description string
}
```

- [ ] **Step 7: Implement the handler**

Create `internal/adapters/inbound/rpc/server.go`:

```go
// Package rpc serves the atlas.v1 contract over Connect, gRPC and gRPC-Web.
package rpc

import (
	"context"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	atlasv1 "github.com/tunedev/atlas/internal/gen/atlas/v1"
)

// PackLister reports the packs this deployment can run.
type PackLister interface {
	List(ctx context.Context) ([]domain.PackSummary, error)
}

// Server adapts the core runner to the atlas.v1 contract.
type Server struct {
	runner *app.Runner
	packs  PackLister
}

func NewServer(runner *app.Runner, packs PackLister) *Server {
	return &Server{runner: runner, packs: packs}
}

func (s *Server) ListPacks(ctx context.Context, req *connect.Request[atlasv1.ListPacksRequest]) (*connect.Response[atlasv1.ListPacksResponse], error) {
	packs, err := s.packs.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*atlasv1.PackSummary, 0, len(packs))
	for _, p := range packs {
		out = append(out, &atlasv1.PackSummary{Name: p.Name, Description: p.Description})
	}
	return connect.NewResponse(&atlasv1.ListPacksResponse{Packs: out}), nil
}

func (s *Server) RunBlueprint(ctx context.Context, req *connect.Request[atlasv1.RunBlueprintRequest]) (*connect.Response[atlasv1.RunBlueprintResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errRunsNotPersisted)
}

func (s *Server) GetRun(ctx context.Context, req *connect.Request[atlasv1.GetRunRequest]) (*connect.Response[atlasv1.GetRunResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errRunsNotPersisted)
}

func (s *Server) ResumeRun(ctx context.Context, req *connect.Request[atlasv1.ResumeRunRequest]) (*connect.Response[atlasv1.ResumeRunResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errRunsNotPersisted)
}
```

Add at the bottom of the file:

```go
var errRunsNotPersisted = errors.New("runs are not persisted yet")
```

with `"errors"` imported. Task 10 replaces all three bodies.

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/adapters/inbound/rpc/... -v`
Expected: PASS, both tests.

- [ ] **Step 9: Serve it from the composition root**

In `cmd/atlas/main.go`, alongside the existing `-pack` path, add a `-serve` mode:

```go
mux := http.NewServeMux()
mux.Handle(atlasv1connect.NewAtlasServiceHandler(rpc.NewServer(runner, lister)))

protocols := new(http.Protocols)
protocols.SetHTTP1(true)
protocols.SetUnencryptedHTTP2(true)

srv := &http.Server{
	Addr:              cfg.Server.Addr,
	Handler:           mux,
	Protocols:         protocols,
	ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
}
```

`SetUnencryptedHTTP2` is what lets a gRPC client reach the same handler a browser reaches with JSON. Do not add `golang.org/x/net/http2/h2c`; it is the older way and is not needed on Go 1.27.

Add to `internal/config/config.go`:

```go
// ServerConfig configures the inbound RPC surface.
type ServerConfig struct {
	Addr              string        `env:"ATLAS_SERVER_ADDR"`
	ReadHeaderTimeout time.Duration `env:"ATLAS_SERVER_READ_HEADER_TIMEOUT"`
}
```

with defaults `"127.0.0.1:8080"` and `5 * time.Second`, wired into `defaults()` as `Server`.

- [ ] **Step 10: Verify both protocols reach the same method**

```bash
go run ./cmd/atlas -serve &
curl -sS -X POST http://127.0.0.1:8080/atlas.v1.AtlasService/ListPacks \
  -H 'Content-Type: application/json' -d '{}'
```

Expected: a JSON body listing both packs. Paste the output into the commit message.

- [ ] **Step 11: Commit**

```bash
git add proto buf.yaml buf.gen.yaml internal/gen internal/adapters/inbound/rpc internal/core/domain/blueprint.go internal/config cmd/atlas
git commit -m "Serve the versioned contract that a browser and a runner both reach"
```

---

### Task 10: Runs — the port, Postgres, and the state machine

A run that suspends must outlive the process that was running it. That is why this is Postgres from the start and not an in-memory map.

**Files:**
- Create: `internal/core/domain/run.go`, `internal/core/ports/runs.go`, `internal/adapters/outbound/runstore/postgres.go`, `migrations/0001_runs.sql`
- Test: `internal/core/domain/run_test.go`, `internal/adapters/outbound/runstore/postgres_test.go`
- Modify: `internal/adapters/inbound/rpc/server.go`, `cmd/atlas/main.go`, `internal/config/config.go`, `docker-compose.yml`

**Interfaces:**
- Consumes: `domain.State` and `domain.Blueprint` from Part 1.
- Produces: `domain.Run` with fields `ID string`, `Pack string`, `State RunState`, `CurrentStep int`, `Vars map[string]string`, `Outputs map[string]json.RawMessage`, `WaitingFor string`, `Failure string`; `domain.RunState` constants `RunPending`, `RunRunning`, `RunCompleted`, `RunFailed`, `RunSuspended`; `(*domain.Run).Advance(step int, out json.RawMessage)`, `(*domain.Run).Suspend(waitingFor string)`, `(*domain.Run).Complete()`, `(*domain.Run).Fail(err error)`; `ports.Runs` with `Save(ctx context.Context, r domain.Run) error` and `Load(ctx context.Context, id string) (domain.Run, error)`.

- [ ] **Step 1: Write the failing state-machine test**

Create `internal/core/domain/run_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

func TestASuspendedRunResumesAtTheStepThatStopped(t *testing.T) {
	r := domain.NewRun("r1", "alpha", map[string]string{})
	r.Advance(0, json.RawMessage(`{"a":1}`))
	r.Suspend("gmail")

	if r.State != domain.RunSuspended {
		t.Fatalf("state = %v, want suspended", r.State)
	}
	if r.CurrentStep != 1 {
		t.Fatalf("current step = %d, want 1", r.CurrentStep)
	}
	if r.WaitingFor != "gmail" {
		t.Fatalf("waiting for = %q, want gmail", r.WaitingFor)
	}

	r.Resume()
	if r.State != domain.RunRunning {
		t.Fatalf("state after resume = %v, want running", r.State)
	}
	if r.CurrentStep != 1 {
		t.Fatalf("resume moved the step to %d; it must re-enter at 1", r.CurrentStep)
	}
	if r.WaitingFor != "" {
		t.Fatalf("waiting for = %q after resume, want empty", r.WaitingFor)
	}
}

func TestACompletedRunRefusesToResume(t *testing.T) {
	r := domain.NewRun("r2", "alpha", nil)
	r.Complete()
	if err := r.Resume(); err == nil {
		t.Fatal("resuming a completed run must fail")
	}
}

func TestAFailedRunKeepsItsFailure(t *testing.T) {
	r := domain.NewRun("r3", "alpha", nil)
	r.Fail(errors.New("provider refused"))
	if r.State != domain.RunFailed {
		t.Fatalf("state = %v, want failed", r.State)
	}
	if r.Failure != "provider refused" {
		t.Fatalf("failure = %q", r.Failure)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/domain/ -run TestASuspended -v`
Expected: FAIL — `undefined: domain.NewRun`.

- [ ] **Step 3: Implement the state machine**

Create `internal/core/domain/run.go`:

```go
package domain

import (
	"encoding/json"
	"errors"
)

// RunState is where a run is in its lifecycle.
type RunState int

const (
	RunPending RunState = iota
	RunRunning
	RunCompleted
	RunFailed
	RunSuspended
)

func (s RunState) String() string {
	switch s {
	case RunPending:
		return "pending"
	case RunRunning:
		return "running"
	case RunCompleted:
		return "completed"
	case RunFailed:
		return "failed"
	case RunSuspended:
		return "suspended"
	}
	return "unknown"
}

// Run is one execution of a blueprint, durable across the process that started it.
type Run struct {
	ID          string
	Pack        string
	State       RunState
	CurrentStep int
	Vars        map[string]string
	Outputs     map[string]json.RawMessage
	WaitingFor  string
	Failure     string
}

func NewRun(id, pack string, vars map[string]string) *Run {
	return &Run{
		ID:      id,
		Pack:    pack,
		State:   RunPending,
		Vars:    vars,
		Outputs: map[string]json.RawMessage{},
	}
}

// Advance records a step's output and moves to the next step.
func (r *Run) Advance(step int, out json.RawMessage) {
	r.State = RunRunning
	if r.Outputs == nil {
		r.Outputs = map[string]json.RawMessage{}
	}
	r.Outputs[stepKey(step)] = out
	r.CurrentStep = step + 1
}

// Suspend stops the run at the current step, naming what it waits for.
// The step index is not moved: resuming re-enters the step that stopped.
func (r *Run) Suspend(waitingFor string) {
	r.State = RunSuspended
	r.WaitingFor = waitingFor
}

// Resume re-enters a suspended run at the step it stopped on.
func (r *Run) Resume() error {
	if r.State != RunSuspended {
		return errors.New("only a suspended run can resume")
	}
	r.State = RunRunning
	r.WaitingFor = ""
	return nil
}

func (r *Run) Complete() { r.State = RunCompleted }

func (r *Run) Fail(err error) {
	r.State = RunFailed
	if err != nil {
		r.Failure = err.Error()
	}
}

func stepKey(step int) string {
	return "step-" + strconv.Itoa(step)
}
```

Imports are `"encoding/json"`, `"errors"` and `"strconv"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/domain/ -v`
Expected: PASS, all three tests.

- [ ] **Step 5: Write the port**

Create `internal/core/ports/runs.go`:

```go
package ports

import (
	"context"

	"github.com/tunedev/atlas/internal/core/domain"
)

// Runs persists runs so one can outlive the process that started it.
type Runs interface {
	Save(ctx context.Context, r domain.Run) error
	Load(ctx context.Context, id string) (domain.Run, error)
}
```

- [ ] **Step 6: Write the migration**

Create `migrations/0001_runs.sql`:

```sql
CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    pack         TEXT        NOT NULL,
    state        SMALLINT    NOT NULL,
    current_step INTEGER     NOT NULL,
    vars         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    outputs      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    waiting_for  TEXT        NOT NULL DEFAULT '',
    failure      TEXT        NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS runs_state_idx ON runs (state) WHERE state = 5;
```

The partial index covers the only query that scans by state: finding suspended runs.

- [ ] **Step 7: Write the failing adapter test**

Create `internal/adapters/outbound/runstore/postgres_test.go`:

```go
package runstore_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/runstore"
	"github.com/tunedev/atlas/internal/core/domain"
)

func TestASuspendedRunSurvivesTheProcess(t *testing.T) {
	dsn := os.Getenv("ATLAS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ATLAS_POSTGRES_DSN not set; run make up")
	}
	ctx := context.Background()

	store, err := runstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	r := domain.NewRun("run-survives", "alpha", map[string]string{"k": "v"})
	r.Advance(0, json.RawMessage(`{"a":1}`))
	r.Suspend("gmail")

	if err := store.Save(ctx, *r); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A second store stands in for a second process.
	reopened, err := runstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Load(ctx, "run-survives")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.State != domain.RunSuspended {
		t.Fatalf("state = %v, want suspended", got.State)
	}
	if got.WaitingFor != "gmail" {
		t.Fatalf("waiting for = %q, want gmail", got.WaitingFor)
	}
	if got.CurrentStep != 1 {
		t.Fatalf("current step = %d, want 1", got.CurrentStep)
	}
	if string(got.Outputs["step-0"]) != `{"a":1}` {
		t.Fatalf("output not restored: %s", got.Outputs["step-0"])
	}
}
```

- [ ] **Step 8: Run test to verify it fails**

Run: `go test ./internal/adapters/outbound/runstore/... -v`
Expected: FAIL — package does not exist.

- [ ] **Step 9: Implement the adapter**

Create `internal/adapters/outbound/runstore/postgres.go`:

```go
// Package runstore persists runs in Postgres.
package runstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunedev/atlas/internal/core/domain"
)

// Store is a Runs implementation over Postgres.
type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

const upsert = `
INSERT INTO runs (id, pack, state, current_step, vars, outputs, waiting_for, failure, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (id) DO UPDATE SET
  state = EXCLUDED.state,
  current_step = EXCLUDED.current_step,
  vars = EXCLUDED.vars,
  outputs = EXCLUDED.outputs,
  waiting_for = EXCLUDED.waiting_for,
  failure = EXCLUDED.failure,
  updated_at = now()`

func (s *Store) Save(ctx context.Context, r domain.Run) error {
	vars, err := json.Marshal(r.Vars)
	if err != nil {
		return fmt.Errorf("marshal vars: %w", err)
	}
	outputs, err := json.Marshal(r.Outputs)
	if err != nil {
		return fmt.Errorf("marshal outputs: %w", err)
	}
	_, err = s.pool.Exec(ctx, upsert,
		r.ID, r.Pack, int16(r.State), r.CurrentStep, vars, outputs, r.WaitingFor, r.Failure)
	if err != nil {
		return fmt.Errorf("save run %s: %w", r.ID, err)
	}
	return nil
}

const selectOne = `
SELECT pack, state, current_step, vars, outputs, waiting_for, failure
FROM runs WHERE id = $1`

func (s *Store) Load(ctx context.Context, id string) (domain.Run, error) {
	var (
		r       domain.Run
		state   int16
		vars    []byte
		outputs []byte
	)
	row := s.pool.QueryRow(ctx, selectOne, id)
	if err := row.Scan(&r.Pack, &state, &r.CurrentStep, &vars, &outputs, &r.WaitingFor, &r.Failure); err != nil {
		return domain.Run{}, fmt.Errorf("load run %s: %w", id, err)
	}
	r.ID = id
	r.State = domain.RunState(state)
	if err := json.Unmarshal(vars, &r.Vars); err != nil {
		return domain.Run{}, fmt.Errorf("unmarshal vars: %w", err)
	}
	if err := json.Unmarshal(outputs, &r.Outputs); err != nil {
		return domain.Run{}, fmt.Errorf("unmarshal outputs: %w", err)
	}
	return r, nil
}
```

- [ ] **Step 10: Bring up Postgres and apply the migration**

Add a `postgres` service to `docker-compose.yml` on `ATLAS_POSTGRES_PORT` (default 5433, to avoid colliding with nerve's), then:

```bash
make up
psql "$ATLAS_POSTGRES_DSN" -f migrations/0001_runs.sql
```

If `make up` does not exist yet in this repo, create it wrapping `docker compose up -d --wait`, matching nerve's Makefile shape.

- [ ] **Step 11: Run test to verify it passes**

Run: `ATLAS_POSTGRES_DSN=... go test ./internal/adapters/outbound/runstore/... -v`
Expected: PASS. Paste the output.

- [ ] **Step 12: Replace the three unimplemented handler bodies**

In `internal/adapters/inbound/rpc/server.go`, `NewServer` gains a `runs ports.Runs` parameter. `RunBlueprint` creates a run, saves it as pending, executes, saves the terminal state, and returns the id. `GetRun` loads and maps to the response, including `waiting_for` and `failure`. `ResumeRun` stays `unimplemented` until Task 14 — the contract is stable, the behaviour is not there yet.

Map `domain.RunState` to `atlasv1.RunState` with an explicit switch. Do not cast the integer: the proto reserves 0 for `UNSPECIFIED`, so the numbers do not line up, and a cast would silently mislabel every state.

```go
func protoState(s domain.RunState) atlasv1.RunState {
	switch s {
	case domain.RunPending:
		return atlasv1.RunState_RUN_STATE_PENDING
	case domain.RunRunning:
		return atlasv1.RunState_RUN_STATE_RUNNING
	case domain.RunCompleted:
		return atlasv1.RunState_RUN_STATE_COMPLETED
	case domain.RunFailed:
		return atlasv1.RunState_RUN_STATE_FAILED
	case domain.RunSuspended:
		return atlasv1.RunState_RUN_STATE_SUSPENDED
	}
	return atlasv1.RunState_RUN_STATE_UNSPECIFIED
}
```

Add a table test asserting every `domain.RunState` maps to a distinct non-unspecified value. A state added later without a case here would otherwise report as unspecified over the wire.

- [ ] **Step 13: Run the full suite**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add internal/core/domain/run.go internal/core/ports/runs.go internal/adapters/outbound/runstore migrations internal/adapters/inbound/rpc docker-compose.yml Makefile
git commit -m "Give a run somewhere to wait that outlives the process running it"
```

---

### Task 11: The sandbox — container per run, workspace bind-mount, sandbox.exec, escape suite

**Demoable cut.** After this task the tracer runs with every tool inside a container and the escape suite is green.

**Files:**
- Create: `internal/core/ports/sandbox.go`, `internal/core/ports/store.go`, `internal/adapters/outbound/filestore/disk.go`, `internal/adapters/outbound/sandbox/docker.go`, `internal/adapters/outbound/tools/exec.go`
- Test: `internal/adapters/outbound/sandbox/escape_test.go`, `internal/adapters/outbound/filestore/disk_test.go`
- Modify: `cmd/atlas/main.go`, `internal/config/config.go`

**Interfaces:**
- Consumes: `ports.Tool` from Part 1 Task 4, with `Invoke(ctx context.Context, cfg map[string]any, st domain.State) (json.RawMessage, error)`.
- Produces: `ports.Store` with `Workspace(ctx context.Context, runID string) (string, error)` returning a host path; `ports.Sandbox` with `Exec(ctx context.Context, runID string, cmd []string) (ports.ExecResult, error)` and `Destroy(ctx context.Context, runID string) error`; `ports.ExecResult` with `Stdout string`, `Stderr string`, `ExitCode int`; `sandbox.NewDocker(cli *client.Client, store ports.Store, cfg sandbox.Config) *sandbox.Docker`.

- [ ] **Step 1: Write the ports**

Create `internal/core/ports/store.go`:

```go
package ports

import "context"

// Store gives a run a durable place for its bytes.
// The path it returns is what a sandbox mounts and nothing else.
type Store interface {
	Workspace(ctx context.Context, runID string) (string, error)
}
```

Create `internal/core/ports/sandbox.go`:

```go
package ports

import "context"

// ExecResult is what running a command inside a sandbox produced.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Sandbox runs a command in an environment it cannot leave.
type Sandbox interface {
	Exec(ctx context.Context, runID string, cmd []string) (ExecResult, error)
	Destroy(ctx context.Context, runID string) error
}
```

- [ ] **Step 2: Write the failing store test**

Create `internal/adapters/outbound/filestore/disk_test.go`:

```go
package filestore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/filestore"
)

func TestAWorkspaceIsItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	s := filestore.NewDisk(root)

	a, err := s.Workspace(context.Background(), "run-a")
	if err != nil {
		t.Fatalf("workspace a: %v", err)
	}
	b, err := s.Workspace(context.Background(), "run-b")
	if err != nil {
		t.Fatalf("workspace b: %v", err)
	}
	if a == b {
		t.Fatal("two runs share a workspace")
	}
	if !strings.HasPrefix(a, root) {
		t.Fatalf("workspace %q escaped the root %q", a, root)
	}
}

func TestARunIDCannotSpellItsWayOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	s := filestore.NewDisk(root)

	for _, id := range []string{"../escape", "a/../../escape", "/etc", "a/b"} {
		got, err := s.Workspace(context.Background(), id)
		if err == nil {
			t.Fatalf("run id %q was accepted, resolving to %q", id, got)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/outbound/filestore/... -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement the disk store**

Create `internal/adapters/outbound/filestore/disk.go`:

```go
// Package filestore gives each run a directory under one root.
package filestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Disk is a Store over a host directory.
type Disk struct {
	root string
}

func NewDisk(root string) *Disk { return &Disk{root: root} }

// Workspace returns the host path for a run, creating it if absent.
// A run id is one path segment. Anything else is refused rather than cleaned,
// because a cleaned id and a refused id are indistinguishable to a caller
// that then writes to the wrong place.
func (d *Disk) Workspace(ctx context.Context, runID string) (string, error) {
	if runID == "" || runID == "." || runID == ".." ||
		strings.ContainsAny(runID, `/\`) || strings.Contains(runID, "..") {
		return "", fmt.Errorf("run id %q is not a single path segment", runID)
	}
	path := filepath.Join(d.root, runID)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return path, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapters/outbound/filestore/... -v`
Expected: PASS, both tests.

- [ ] **Step 6: Write the failing escape suite**

Create `internal/adapters/outbound/sandbox/escape_test.go`:

```go
package sandbox_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"

	"github.com/tunedev/atlas/internal/adapters/outbound/filestore"
	"github.com/tunedev/atlas/internal/adapters/outbound/sandbox"
)

func newSandbox(t *testing.T) (*sandbox.Docker, func()) {
	t.Helper()
	if os.Getenv("ATLAS_SANDBOX_TESTS") == "" {
		t.Skip("ATLAS_SANDBOX_TESTS not set; needs a Docker daemon")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	store := filestore.NewDisk(t.TempDir())
	sb := sandbox.NewDocker(cli, store, sandbox.Config{
		Image:   "alpine:3.20",
		Timeout: 30 * time.Second,
		Mount:   "/workspace",
	})
	return sb, func() { _ = sb.Destroy(context.Background(), "escape") }
}

func TestTheWorkspaceIsWritableFromInside(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape",
		[]string{"sh", "-c", "echo hello > /workspace/f && cat /workspace/f"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Fatalf("stdout = %q, want hello", res.Stdout)
	}
}

func TestEscapeByPathTraversalFails(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape",
		[]string{"sh", "-c", "cat /workspace/../../etc/shadow"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("read the host shadow file: %s", res.Stdout)
	}
}

func TestEscapeBySymlinkFails(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape",
		[]string{"sh", "-c", "ln -s / /workspace/root && cat /workspace/root/etc/shadow"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("a symlink reached the host: %s", res.Stdout)
	}
}

func TestTheRootFilesystemIsReadOnly(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape",
		[]string{"sh", "-c", "touch /planted"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("wrote outside the workspace; root is not read-only")
	}
}

func TestNoHostNetworkIsReachable(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape",
		[]string{"sh", "-c", "wget -q -T 2 -O - http://169.254.169.254/ || echo BLOCKED"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "BLOCKED") {
		t.Fatalf("reached the network: %s", res.Stdout)
	}
}

func TestNoHostProcessIsVisible(t *testing.T) {
	sb, cleanup := newSandbox(t)
	defer cleanup()

	res, err := sb.Exec(context.Background(), "escape", []string{"sh", "-c", "ps -o pid= | wc -l"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("ps returned %q: %v", res.Stdout, err)
	}
	// A private PID namespace shows a handful of processes. The host has hundreds.
	if count > 20 {
		t.Fatalf("saw %d processes; the PID namespace is shared with the host", count)
	}
}
```

- [ ] **Step 7: Run the suite to verify it fails**

Run: `ATLAS_SANDBOX_TESTS=1 go test ./internal/adapters/outbound/sandbox/... -run TestEscape -v`
Expected: FAIL — package does not exist.

- [ ] **Step 8: Implement the Docker sandbox**

Create `internal/adapters/outbound/sandbox/docker.go`:

```go
// Package sandbox runs a command in a container that can reach one directory.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config is what a sandbox needs that is not a dependency.
type Config struct {
	Image   string
	Mount   string
	Timeout time.Duration
	Memory  int64
	NanoCPU int64
}

// Docker runs each command in its own container.
type Docker struct {
	cli   *client.Client
	store ports.Store
	cfg   Config
}

func NewDocker(cli *client.Client, store ports.Store, cfg Config) *Docker {
	return &Docker{cli: cli, store: store, cfg: cfg}
}

// Exec runs cmd with the run's workspace bind-mounted and nothing else reachable.
func (d *Docker) Exec(ctx context.Context, runID string, cmd []string) (ports.ExecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	workspace, err := d.store.Workspace(ctx, runID)
	if err != nil {
		return ports.ExecResult{}, fmt.Errorf("workspace: %w", err)
	}

	created, err := d.cli.ContainerCreate(ctx,
		&container.Config{
			Image:           d.cfg.Image,
			Cmd:             cmd,
			WorkingDir:      d.cfg.Mount,
			NetworkDisabled: true,
			User:            "65534:65534",
		},
		&container.HostConfig{
			Mounts: []mount.Mount{{
				Type:   mount.TypeBind,
				Source: workspace,
				Target: d.cfg.Mount,
			}},
			ReadonlyRootfs: true,
			NetworkMode:    "none",
			PidMode:        "",
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			Resources: container.Resources{
				Memory:   d.cfg.Memory,
				NanoCPUs: d.cfg.NanoCPU,
			},
			AutoRemove: false,
		},
		nil, nil, containerName(runID),
	)
	if err != nil {
		return ports.ExecResult{}, fmt.Errorf("create container: %w", err)
	}
	defer func() {
		_ = d.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID,
			container.RemoveOptions{Force: true})
	}()

	if err := d.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return ports.ExecResult{}, fmt.Errorf("start container: %w", err)
	}

	statusCh, errCh := d.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var code int
	select {
	case err := <-errCh:
		return ports.ExecResult{}, fmt.Errorf("wait: %w", err)
	case status := <-statusCh:
		code = int(status.StatusCode)
	case <-ctx.Done():
		return ports.ExecResult{}, fmt.Errorf("exec timed out after %s: %w", d.cfg.Timeout, ctx.Err())
	}

	logs, err := d.cli.ContainerLogs(ctx, created.ID,
		container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ports.ExecResult{}, fmt.Errorf("logs: %w", err)
	}
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil && err != io.EOF {
		return ports.ExecResult{}, fmt.Errorf("demux logs: %w", err)
	}

	return ports.ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
	}, nil
}

// Destroy removes anything left behind for a run.
func (d *Docker) Destroy(ctx context.Context, runID string) error {
	err := d.cli.ContainerRemove(ctx, containerName(runID), container.RemoveOptions{Force: true})
	if client.IsErrNotFound(err) {
		return nil
	}
	return err
}

func containerName(runID string) string { return "atlas-" + runID }
```

Notes an implementer needs:

- `NetworkDisabled` plus `NetworkMode: "none"` are both set. The first is the container config, the second the host config; setting only one leaves a path open depending on daemon version.
- `CapDrop: ALL` and `no-new-privileges` are what make the earlier FUSE decision matter: **no `CAP_SYS_ADMIN` is granted anywhere.** The mount happens on the host, before the container exists.
- `User: 65534:65534` is `nobody`. The workspace is created 0750 by the store, so the bind-mount needs group access or the store's mode widened to 0770 — resolve this when the writable test runs, and record which you chose.
- `AutoRemove: false` with an explicit remove in `defer`: auto-remove races the log read, and the logs are the test's evidence.

- [ ] **Step 9: Run the escape suite to verify it passes**

Run: `ATLAS_SANDBOX_TESTS=1 go test ./internal/adapters/outbound/sandbox/... -run 'TestEscape|TestThe|TestNo' -v`
Expected: PASS, all six. Paste the output; this is the increment's headline evidence.

- [ ] **Step 10: Add the sandbox.exec tool**

Create `internal/adapters/outbound/tools/exec.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Exec is the sandbox.exec tool: run a command inside the run's sandbox.
type Exec struct {
	sandbox ports.Sandbox
}

func NewExec(sb ports.Sandbox) *Exec { return &Exec{sandbox: sb} }

func (e *Exec) Name() string { return "sandbox.exec" }

func (e *Exec) Invoke(ctx context.Context, cfg map[string]any, st domain.State) (json.RawMessage, error) {
	raw, ok := cfg["cmd"].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("sandbox.exec needs a non-empty cmd list")
	}
	cmd := make([]string, 0, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("cmd[%d] is not a string", i)
		}
		cmd = append(cmd, s)
	}

	res, err := e.sandbox.Exec(ctx, st.RunID, cmd)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"stdout":    res.Stdout,
		"stderr":    res.Stderr,
		"exit_code": res.ExitCode,
	})
}
```

`domain.State` gains a `RunID string` field; add it in `internal/core/domain/state.go` and set it where the runner constructs state.

- [ ] **Step 11: Wire it and run both packs through the container**

Register `sandbox.exec` in the registry at `cmd/atlas/main.go`. Add config: `ATLAS_SANDBOX_IMAGE` (default `alpine:3.20`), `ATLAS_SANDBOX_TIMEOUT` (default `30s`), `ATLAS_SANDBOX_MOUNT` (default `/workspace`), `ATLAS_SANDBOX_MEMORY` (default 512 MiB), `ATLAS_STORE_ROOT` (default `./.atlas/workspaces`).

Run both packs:

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

Expected: both still pass, unchanged. The second pack still needed no Go.

- [ ] **Step 12: Write the increment-cut note**

Create `docs/notes/2026-09-09-sandbox-cut.md` covering: what the pattern was, what surprised you, what you would do differently, what you still do not understand. State plainly that the escape suite asserts containment of a **plain host directory**, not of a scoped VFS, and that symlink resolution inside a scoped VFS is untested because no scoped VFS exists yet.

- [ ] **Step 13: Commit**

```bash
git add internal/core/ports internal/adapters/outbound/filestore internal/adapters/outbound/sandbox internal/adapters/outbound/tools/exec.go internal/core/domain/state.go cmd/atlas internal/config docs/notes
git commit -m "Run every tool in a container that can reach one directory"
```

---

### Task 12: The permission engine — allow, ask, deny, suspend

**Files:**
- Create: `internal/core/domain/permission.go`, `internal/core/app/permission.go`
- Test: `internal/core/domain/permission_test.go`, `internal/core/app/permission_test.go`
- Modify: `internal/core/app/runner.go`, `internal/config/config.go`, `packs/job-hunt.yaml`

**Interfaces:**
- Consumes: `domain.Run`, `ports.Tool`.
- Produces: `domain.Outcome` constants `OutcomeAllow`, `OutcomeAsk`, `OutcomeDeny`, `OutcomeSuspend`; `domain.Rule` with `Tool string`, `Match string`, `Outcome Outcome`, `Integration string`; `domain.RuleSet` with `Evaluate(tool string, cfg map[string]any) domain.Decision`; `domain.Decision` with `Outcome Outcome` and `Integration string`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/domain/permission_test.go`:

```go
package domain_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

func ruleset() domain.RuleSet {
	return domain.RuleSet{Rules: []domain.Rule{
		{Tool: "sandbox.exec", Match: "rm *", Outcome: domain.OutcomeDeny},
		{Tool: "sandbox.exec", Match: "*", Outcome: domain.OutcomeAllow},
		{Tool: "http.request", Match: "https://api.example.com/*", Outcome: domain.OutcomeAllow},
		{Tool: "mail.send", Match: "*", Outcome: domain.OutcomeSuspend, Integration: "gmail"},
	}}
}

func TestDenyIsEvaluatedBeforeAllow(t *testing.T) {
	d := ruleset().Evaluate("sandbox.exec", map[string]any{"cmd": []any{"rm", "-rf", "/"}})
	if d.Outcome != domain.OutcomeDeny {
		t.Fatalf("outcome = %v, want deny", d.Outcome)
	}
}

func TestAnUnmatchedInvocationDefaultsToAsk(t *testing.T) {
	d := ruleset().Evaluate("http.request", map[string]any{"url": "https://elsewhere.test/x"})
	if d.Outcome != domain.OutcomeAsk {
		t.Fatalf("outcome = %v, want ask", d.Outcome)
	}
}

func TestSuspendCarriesTheIntegrationItWaitsFor(t *testing.T) {
	d := ruleset().Evaluate("mail.send", map[string]any{})
	if d.Outcome != domain.OutcomeSuspend {
		t.Fatalf("outcome = %v, want suspend", d.Outcome)
	}
	if d.Integration != "gmail" {
		t.Fatalf("integration = %q, want gmail", d.Integration)
	}
}

func TestAnEmptyRuleSetAsks(t *testing.T) {
	var rs domain.RuleSet
	if got := rs.Evaluate("anything", nil).Outcome; got != domain.OutcomeAsk {
		t.Fatalf("outcome = %v, want ask", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/domain/ -run TestDeny -v`
Expected: FAIL — `undefined: domain.RuleSet`.

- [ ] **Step 3: Implement**

Create `internal/core/domain/permission.go`:

```go
package domain

import (
	"fmt"
	"path"
)

// Outcome is what the permission engine decided about one tool invocation.
type Outcome int

const (
	OutcomeAsk Outcome = iota
	OutcomeAllow
	OutcomeDeny
	OutcomeSuspend
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAsk:
		return "ask"
	case OutcomeAllow:
		return "allow"
	case OutcomeDeny:
		return "deny"
	case OutcomeSuspend:
		return "suspend"
	}
	return "unknown"
}

// Rule matches a tool invocation and names an outcome.
// Integration is set only on a suspend rule, naming what the run waits for.
type Rule struct {
	Tool        string
	Match       string
	Outcome     Outcome
	Integration string
}

// Decision is the evaluated outcome for one invocation.
type Decision struct {
	Outcome     Outcome
	Integration string
}

// RuleSet evaluates an invocation. Deny wins over everything; an unmatched
// invocation asks.
type RuleSet struct {
	Rules []Rule
}

func (rs RuleSet) Evaluate(tool string, cfg map[string]any) Decision {
	subject := subjectOf(tool, cfg)

	var matched *Rule
	for i := range rs.Rules {
		r := rs.Rules[i]
		if r.Tool != tool {
			continue
		}
		ok, err := path.Match(r.Match, subject)
		if err != nil || !ok {
			continue
		}
		if r.Outcome == OutcomeDeny {
			return Decision{Outcome: OutcomeDeny}
		}
		if matched == nil {
			matched = &rs.Rules[i]
		}
	}
	if matched == nil {
		return Decision{Outcome: OutcomeAsk}
	}
	return Decision{Outcome: matched.Outcome, Integration: matched.Integration}
}

// subjectOf is the string a rule's Match is tested against. It is the tool's
// most identifying argument, and is empty when the tool has none.
func subjectOf(tool string, cfg map[string]any) string {
	switch tool {
	case "http.request", "browser.fetch":
		if u, ok := cfg["url"].(string); ok {
			return u
		}
	case "sandbox.exec":
		if raw, ok := cfg["cmd"].([]any); ok && len(raw) > 0 {
			return joinArgs(raw)
		}
	}
	return ""
}

func joinArgs(raw []any) string {
	out := ""
	for i, v := range raw {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprint(v)
	}
	return out
}
```

`subjectOf` names tool names, not use-case concepts. `internal/arch/vocabulary_test.go` will not object; confirm this by running it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/domain/ -v`
Expected: PASS, all permission tests plus the run tests from Task 10.

- [ ] **Step 5: Run the vocabulary guard**

Run: `go test ./internal/arch/ -v`
Expected: PASS. If it fails, `subjectOf` has leaked a use-case concept — fix it there, not in the guard.

- [ ] **Step 6: Change the Runner's surface, and wire the engine in**

Part 1 produces `Runner.Run(ctx context.Context, bp domain.Blueprint, vars map[string]string) (domain.State, error)`. That signature cannot suspend: it has no run to record a suspension on and no step index to resume from. This step replaces it. Every later task in this plan assumes the surface below.

Rewrite `internal/core/app/runner.go` around this shape:

```go
// Registry maps a tool name to its implementation.
type Registry map[string]ports.Tool

// Runner executes a blueprint's steps in order against a run.
type Runner struct {
	registry Registry
	rules    domain.RuleSet
	packs    map[string]domain.Blueprint
}

// Option configures a Runner at construction.
type Option func(*Runner)

// WithRules sets the permission rules the runner evaluates against.
func WithRules(rs domain.RuleSet) Option {
	return func(r *Runner) { r.rules = rs }
}

// WithPacks gives the runner the blueprints it can resume by name.
func WithPacks(packs map[string]domain.Blueprint) Option {
	return func(r *Runner) { r.packs = packs }
}

func NewRunner(reg Registry, opts ...Option) *Runner {
	r := &Runner{registry: reg, packs: map[string]domain.Blueprint{}}
	for _, o := range opts {
		o(r)
	}
	return r
}

// SetRules replaces the rule set. Used when an authorization changes what a
// suspended run is permitted to do.
func (r *Runner) SetRules(rs domain.RuleSet) { r.rules = rs }

// Blueprint returns a loaded pack by name.
func (r *Runner) Blueprint(_ context.Context, pack string) (domain.Blueprint, error) {
	bp, ok := r.packs[pack]
	if !ok {
		return domain.Blueprint{}, fmt.Errorf("no pack named %q", pack)
	}
	return bp, nil
}

// Execute runs the blueprint from run.CurrentStep to the end, or until a step
// suspends. Steps already recorded on the run are not run again.
func (r *Runner) Execute(ctx context.Context, run *domain.Run, bp domain.Blueprint) error {
	for i := run.CurrentStep; i < len(bp.Steps); i++ {
		step := bp.Steps[i]

		tool, ok := r.registry[step.Tool]
		if !ok {
			return fmt.Errorf("step %s: no tool named %q", step.ID, step.Tool)
		}

		cfg, err := render(step.With, stateOf(run))
		if err != nil {
			return fmt.Errorf("step %s: render: %w", step.ID, err)
		}

		switch d := r.rules.Evaluate(step.Tool, cfg); d.Outcome {
		case domain.OutcomeDeny:
			return fmt.Errorf("step %s: %s is denied", step.ID, step.Tool)
		case domain.OutcomeSuspend:
			run.Suspend(d.Integration)
			return nil
		case domain.OutcomeAsk:
			if !r.assumeYes {
				return fmt.Errorf("step %s: %s needs approval and there is no surface to ask on", step.ID, step.Tool)
			}
		case domain.OutcomeAllow:
		}

		out, err := tool.Invoke(ctx, cfg, stateOf(run))
		if err != nil {
			return fmt.Errorf("step %s: %w", step.ID, err)
		}
		if sel, ok := step.With["select"].(string); ok && sel != "" {
			if out, err = narrow(out, sel); err != nil {
				return fmt.Errorf("step %s: select: %w", step.ID, err)
			}
		}
		run.Advance(i, out)
	}
	run.Complete()
	return nil
}
```

`render`, `narrow` and `stateOf` are Part 1's template rendering and path selector, unchanged except that `stateOf(run)` builds a `domain.State` from `run.Vars` and `run.Outputs` rather than from a standalone accumulator. Add `assumeYes bool` to `Runner` with a `WithAssumeYes()` option; the `-yes` flag sets it.

A suspended run returns `nil`, not an error. A suspension is not a failure, and a caller that treats it as one will mark the run failed and lose it.

Update `cmd/atlas/main.go`'s `-pack` path to construct a `domain.Run`, call `Execute`, and print the outputs — the printing behaviour Part 1 Task 8 established does not change.

- [ ] **Step 7: Add rules to config and pack one**

Rules are configuration. Load them from a `rules:` block in the pack file, parsed by `packfile`. Add `rules:` to `packs/job-hunt.yaml` with at minimum a deny on destructive `sandbox.exec` commands.

- [ ] **Step 8: Run the full suite**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/core/domain/permission.go internal/core/app internal/adapters/inbound/packfile packs internal/config
git commit -m "Give the permission engine a fourth answer: wait for a human"
```

---

### Task 13: The broker, and A2H end to end

**Files:**
- Create: `internal/core/ports/broker.go`, `internal/adapters/outbound/broker/pipedream.go`, `internal/core/app/resume.go`
- Test: `internal/adapters/outbound/broker/pipedream_test.go`, `internal/core/app/resume_test.go`
- Modify: `internal/adapters/outbound/tools/http.go`, `internal/adapters/inbound/rpc/server.go`, `cmd/atlas/main.go`, `internal/config/config.go`

**Interfaces:**
- Consumes: `domain.Run`, `ports.Runs`, `app.Runner`.
- Produces: `ports.Broker` with `Entitled(ctx context.Context, workspace, integration string) (bool, error)` and `Send(ctx context.Context, workspace, integration string, req *http.Request) (*http.Response, error)`; `broker.NewPipedream(httpClient *http.Client, cfg broker.Config) *broker.Pipedream`; `app.Resume(ctx context.Context, runs ports.Runs, runner *Runner, id string) (domain.Run, error)`.

- [ ] **Step 1: Write the port**

Create `internal/core/ports/broker.go`:

```go
package ports

import (
	"context"
	"net/http"
)

// Broker resolves which integrations a workspace may use and sends a request
// under that workspace's identity. The caller never holds a credential.
type Broker interface {
	Entitled(ctx context.Context, workspace, integration string) (bool, error)
	Send(ctx context.Context, workspace, integration string, req *http.Request) (*http.Response, error)
}
```

The `*http.Request` here is not a leak of an adapter type: it is the standard library, available to core, and the alternative is inventing a request struct that exists only to be converted back. Record that reasoning in the increment note.

- [ ] **Step 2: Write the failing broker test**

Create `internal/adapters/outbound/broker/pipedream_test.go`:

```go
package broker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/broker"
)

func TestTheCallerNeverSeesACredential(t *testing.T) {
	var sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	b := broker.NewPipedream(upstream.Client(), broker.Config{
		BaseURL:     upstream.URL,
		ProjectID:   "proj_test",
		Token:       "broker-side-secret",
		Timeout:     5 * time.Second,
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/me", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("caller's request already carries %q", got)
	}

	res, err := b.Send(context.Background(), "ws-1", "gmail", req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer res.Body.Close()

	if !strings.Contains(sawAuth, "broker-side-secret") {
		t.Fatalf("broker did not inject identity; upstream saw %q", sawAuth)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("the caller's request was mutated to carry %q", got)
	}
}

func TestAnUnentitledIntegrationIsNotSent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	b := broker.NewPipedream(upstream.Client(), broker.Config{
		BaseURL: upstream.URL, ProjectID: "proj_test", Token: "t", Timeout: 5 * time.Second,
	})

	ok, err := b.Entitled(context.Background(), "ws-1", "gmail")
	if err != nil {
		t.Fatalf("entitled: %v", err)
	}
	if ok {
		t.Fatal("an absent connection reported as entitled")
	}
}
```

The second assertion in the first test is the important one: the broker must not mutate the caller's request, or a credential ends up in a struct the sandbox could observe.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/outbound/broker/... -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement the broker**

Create `internal/adapters/outbound/broker/pipedream.go`:

```go
// Package broker sends requests under a workspace's identity without the
// caller ever holding a credential.
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Config is what the broker needs. Token is a secret and is never logged.
type Config struct {
	BaseURL   string
	ProjectID string
	Token     string
	Timeout   time.Duration
}

// Pipedream sends through Pipedream Connect's proxy.
type Pipedream struct {
	client *http.Client
	cfg    Config
}

func NewPipedream(client *http.Client, cfg Config) *Pipedream {
	return &Pipedream{client: client, cfg: cfg}
}

// Entitled reports whether the workspace has connected this integration.
func (p *Pipedream) Entitled(ctx context.Context, workspace, integration string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s/v1/connect/%s/accounts?external_user_id=%s&app=%s",
		p.cfg.BaseURL, url.PathEscape(p.cfg.ProjectID),
		url.QueryEscape(workspace), url.QueryEscape(integration))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("build entitlement request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)

	res, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("entitlement lookup: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("entitlement lookup: status %d", res.StatusCode)
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode entitlements: %w", err)
	}
	return len(body.Data) > 0, nil
}

// Send proxies req under the workspace's connected identity.
// The caller's request is cloned; it is never mutated, so no credential is
// observable from the calling side.
func (p *Pipedream) Send(ctx context.Context, workspace, integration string, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	outbound := req.Clone(ctx)
	outbound.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	outbound.Header.Set("X-PD-External-User-ID", workspace)
	outbound.Header.Set("X-PD-App-Slug", integration)

	proxied, err := url.Parse(fmt.Sprintf("%s/v1/connect/%s/proxy",
		p.cfg.BaseURL, url.PathEscape(p.cfg.ProjectID)))
	if err != nil {
		return nil, fmt.Errorf("build proxy url: %w", err)
	}
	outbound.Header.Set("X-PD-Target-URL", req.URL.String())
	outbound.URL = proxied
	outbound.Host = proxied.Host
	outbound.RequestURI = ""

	res, err := p.client.Do(outbound)
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", integration, err)
	}
	return res, nil
}
```

`req.Clone(ctx)` is what makes the second assertion in the test pass. Verify against Pipedream Connect's current proxy documentation before running against the real service; the header names above are the shape, and the exact spellings must be confirmed. If they differ, change them here and note it — do not work around a mismatch in the caller.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapters/outbound/broker/... -v`
Expected: PASS, both tests.

- [ ] **Step 6: Write the failing resume test**

Create `internal/core/app/resume_test.go`:

```go
package app_test

import (
	"context"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
)

// memRuns is a Runs double used only to observe what the app persisted.
type memRuns struct{ runs map[string]domain.Run }

func (m *memRuns) Save(_ context.Context, r domain.Run) error {
	m.runs[r.ID] = r
	return nil
}

func (m *memRuns) Load(_ context.Context, id string) (domain.Run, error) {
	return m.runs[id], nil
}

func TestSuspendResumeRunsEachStepExactlyOnce(t *testing.T) {
	var executed []string
	runner := app.NewRunner(app.Registry{
		"tool.a": recordingTool(&executed, "a"),
		"tool.b": recordingTool(&executed, "b"),
		"tool.c": recordingTool(&executed, "c"),
	}, app.WithRules(domain.RuleSet{Rules: []domain.Rule{
		{Tool: "tool.a", Match: "*", Outcome: domain.OutcomeAllow},
		{Tool: "tool.b", Match: "*", Outcome: domain.OutcomeSuspend, Integration: "gmail"},
		{Tool: "tool.c", Match: "*", Outcome: domain.OutcomeAllow},
	}}))

	bp := domain.Blueprint{Name: "three", Steps: []domain.Step{
		{ID: "s0", Tool: "tool.a"},
		{ID: "s1", Tool: "tool.b"},
		{ID: "s2", Tool: "tool.c"},
	}}

	runs := &memRuns{runs: map[string]domain.Run{}}
	ctx := context.Background()

	r := domain.NewRun("r1", "three", nil)
	if err := runner.Execute(ctx, r, bp); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := runs.Save(ctx, *r); err != nil {
		t.Fatalf("save: %v", err)
	}

	if r.State != domain.RunSuspended {
		t.Fatalf("state = %v, want suspended", r.State)
	}
	if r.WaitingFor != "gmail" {
		t.Fatalf("waiting for = %q, want gmail", r.WaitingFor)
	}
	if got := len(executed); got != 1 {
		t.Fatalf("executed %v before suspending; want only [a]", executed)
	}

	// The integration is now authorized. Resume.
	runner.SetRules(domain.RuleSet{Rules: []domain.Rule{
		{Tool: "tool.a", Match: "*", Outcome: domain.OutcomeAllow},
		{Tool: "tool.b", Match: "*", Outcome: domain.OutcomeAllow},
		{Tool: "tool.c", Match: "*", Outcome: domain.OutcomeAllow},
	}})

	resumed, err := app.Resume(ctx, runs, runner, "r1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != domain.RunCompleted {
		t.Fatalf("state = %v, want completed", resumed.State)
	}

	want := []string{"a", "b", "c"}
	if len(executed) != len(want) {
		t.Fatalf("executed %v, want %v", executed, want)
	}
	for i := range want {
		if executed[i] != want[i] {
			t.Fatalf("executed %v, want %v", executed, want)
		}
	}
	if _, ok := resumed.Outputs["step-0"]; !ok {
		t.Fatal("the output from before the suspension was lost")
	}
}
```

Add `recordingTool` in the same file: a `ports.Tool` whose `Invoke` appends its label to the slice and returns `json.RawMessage("{}")`.

The assertion that matters is `executed` equalling `[a b c]` — one `a`, not two. A resume that re-runs the steps before the suspension is the failure mode this test exists to catch.

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./internal/core/app/ -run TestSuspendResume -v`
Expected: FAIL — `undefined: app.Resume`.

- [ ] **Step 8: Implement resume**

Create `internal/core/app/resume.go`:

```go
package app

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Resume continues a suspended run from the step it stopped on.
// Steps already recorded are not run again.
func Resume(ctx context.Context, runs ports.Runs, runner *Runner, id string) (domain.Run, error) {
	r, err := runs.Load(ctx, id)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load run %s: %w", id, err)
	}
	if err := r.Resume(); err != nil {
		return domain.Run{}, fmt.Errorf("run %s: %w", id, err)
	}

	bp, err := runner.Blueprint(ctx, r.Pack)
	if err != nil {
		return domain.Run{}, fmt.Errorf("blueprint for %s: %w", r.Pack, err)
	}

	if err := runner.Execute(ctx, &r, bp); err != nil {
		r.Fail(err)
	}
	if err := runs.Save(ctx, r); err != nil {
		return domain.Run{}, fmt.Errorf("save run %s: %w", id, err)
	}
	return r, nil
}
```

`Runner.Execute` must start at `r.CurrentStep`, not at zero. If it currently starts at zero, that is the change this task makes to `runner.go`.

- [ ] **Step 9: Run test to verify it passes**

Run: `go test ./internal/core/app/ -run TestSuspendResume -v`
Expected: PASS.

- [ ] **Step 10: Emit suspension and resumption as span events**

The spec requires that the gap between a suspension and its resumption be visible as a gap rather than as a missing trace. In `Runner.Execute`, on the suspend branch, add an event to the current span before returning:

```go
trace.SpanFromContext(ctx).AddEvent("run.suspended", trace.WithAttributes(
	attribute.String("run.id", run.ID),
	attribute.String("run.waiting_for", d.Integration),
	attribute.Int("run.step", i),
))
```

In `app.Resume`, after `r.Resume()` succeeds:

```go
trace.SpanFromContext(ctx).AddEvent("run.resumed", trace.WithAttributes(
	attribute.String("run.id", r.ID),
	attribute.Int("run.step", r.CurrentStep),
))
```

Imports are `"go.opentelemetry.io/otel/attribute"` and `"go.opentelemetry.io/otel/trace"`. Part 1 Task 7 already established the trace pipeline; this adds events to it and starts no new spans.

Verify by running a pack that suspends and confirming both events appear on the trace, then paste the collector output.

- [ ] **Step 11: Implement ResumeRun on the handler**

Replace the `unimplemented` body in `internal/adapters/inbound/rpc/server.go` with a call to `app.Resume`. Map a "not suspended" error to `connect.CodeFailedPrecondition`, and a missing run to `connect.CodeNotFound`.

- [ ] **Step 12: Route http.request through the broker when a step names an integration**

In `internal/adapters/outbound/tools/http.go`, when the step config carries `integration:`, send via `Broker.Send` rather than the bare client. Without `integration:`, behaviour is unchanged.

- [ ] **Step 13: Demonstrate A2H end to end**

```bash
go run ./cmd/atlas -serve &
# start a run whose step needs an unauthorized integration
curl -sS -X POST http://127.0.0.1:8080/atlas.v1.AtlasService/RunBlueprint \
  -H 'Content-Type: application/json' -d '{"pack":"job-hunt"}'
# observe suspended, and what it waits for
curl -sS -X POST http://127.0.0.1:8080/atlas.v1.AtlasService/GetRun \
  -H 'Content-Type: application/json' -d '{"runId":"<id>"}'
# authorize the integration in Pipedream Connect, then
curl -sS -X POST http://127.0.0.1:8080/atlas.v1.AtlasService/ResumeRun \
  -H 'Content-Type: application/json' -d '{"runId":"<id>"}'
```

Expected: `RUN_STATE_SUSPENDED` with `waitingFor` naming the integration, then `RUN_STATE_COMPLETED`. Paste all three responses.

- [ ] **Step 14: Run the full suite**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 15: Commit**

```bash
git add internal/core/ports/broker.go internal/adapters/outbound/broker internal/core/app/resume.go internal/core/app/runner.go internal/adapters/inbound/rpc internal/adapters/outbound/tools/http.go cmd/atlas internal/config
git commit -m "Hand a run back to the person who can authorize it, and take it back afterwards"
```

---

### Task 14: browser.fetch, and the increment note

**Files:**
- Create: `internal/adapters/outbound/tools/browser.go`, `docs/notes/2026-09-09-increment-1-boundaries.md`
- Test: `internal/adapters/outbound/tools/browser_test.go`
- Modify: `cmd/atlas/main.go`, `internal/config/config.go`, `README.md`

**Interfaces:**
- Consumes: `ports.Tool`.
- Produces: `tools.NewBrowser(cfg tools.BrowserConfig) *tools.Browser` with `Name() string` returning `"browser.fetch"`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/tools/browser_test.go`:

```go
package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/domain"
)

func TestBrowserFetchReturnsRenderedText(t *testing.T) {
	if os.Getenv("ATLAS_BROWSER_TESTS") == "" {
		t.Skip("ATLAS_BROWSER_TESTS not set; needs a Chrome binary")
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p id="p">before</p>
<script>document.getElementById("p").textContent = "after";</script></body></html>`))
	}))
	defer page.Close()

	b := tools.NewBrowser(tools.BrowserConfig{Timeout: 20 * time.Second})
	out, err := b.Invoke(context.Background(), map[string]any{"url": page.URL}, domain.State{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// "after" proves the page was rendered, not merely downloaded.
	if !strings.Contains(body.Text, "after") {
		t.Fatalf("text = %q; the page was not rendered", body.Text)
	}
}

func TestBrowserFetchRefusesANonHTTPScheme(t *testing.T) {
	b := tools.NewBrowser(tools.BrowserConfig{Timeout: 5 * time.Second})
	if _, err := b.Invoke(context.Background(), map[string]any{"url": "file:///etc/passwd"}, domain.State{}); err == nil {
		t.Fatal("a file:// url was accepted")
	}
}
```

The second test runs without Chrome and is the one that must never be skipped: `file://` reaching a renderer is a read of the host filesystem.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/outbound/tools/ -run TestBrowserFetch -v`
Expected: FAIL — `undefined: tools.NewBrowser`.

- [ ] **Step 3: Implement**

Create `internal/adapters/outbound/tools/browser.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/tunedev/atlas/internal/core/domain"
)

// BrowserConfig is what the browser tool needs.
type BrowserConfig struct {
	Timeout time.Duration
}

// Browser is the browser.fetch tool. It renders a page and returns its text.
// It is read-only: it does not fill a field, click a control, or submit a form.
type Browser struct {
	cfg BrowserConfig
}

func NewBrowser(cfg BrowserConfig) *Browser { return &Browser{cfg: cfg} }

func (b *Browser) Name() string { return "browser.fetch" }

func (b *Browser) Invoke(ctx context.Context, cfg map[string]any, _ domain.State) (json.RawMessage, error) {
	raw, ok := cfg["url"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("browser.fetch needs a url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("browser.fetch refuses scheme %q", u.Scheme)
	}

	ctx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.DisableGPU,
			chromedp.NoSandbox,
		)...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var text string
	err = chromedp.Run(browserCtx,
		chromedp.Navigate(u.String()),
		chromedp.Text("body", &text, chromedp.ByQuery, chromedp.NodeVisible),
	)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", u.Redacted(), err)
	}
	return json.Marshal(map[string]any{"url": u.String(), "text": text})
}
```

`chromedp.NoSandbox` is set because the renderer runs inside the run's container, which is already the process boundary; Chrome's own sandbox needs user namespaces the container does not grant. Record that in the note — it is a real trade and it should not be discovered later by someone reading the flag.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/outbound/tools/ -run TestBrowserFetch -v`
Expected: PASS for the scheme test; the render test passes with `ATLAS_BROWSER_TESTS=1` and a Chrome binary present.

- [ ] **Step 5: Register it and add config**

Register `browser.fetch` at `cmd/atlas/main.go`. Add `ATLAS_BROWSER_TIMEOUT`, default `20s`.

- [ ] **Step 6: Prove the binding constraint still holds**

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml
go run ./cmd/atlas -pack packs/hn-summary.yaml
go test ./internal/arch/ -v
```

Expected: both packs run; the architecture and vocabulary guards pass. The second pack has still never needed Go.

- [ ] **Step 7: Write the increment note**

Create `docs/notes/2026-09-09-increment-1-boundaries.md` with four sections: what the pattern was, what surprised you, what you would do differently, what you still do not understand.

It must record, plainly:

- The escape suite asserts containment of a plain host directory. There is no scoped VFS yet, so symlink resolution *inside* a scope is untested.
- `chromedp.NoSandbox` is set, and why.
- `ports.Broker` takes `*http.Request`, and why that is judged not to be a leak.
- Whether the workspace directory mode ended at 0750 or 0770, and what forced it.
- Whether a suspended run's container is destroyed or held, which the spec left open.

- [ ] **Step 8: Update the README status**

`README.md` currently says "Design, no code." Replace that section with what the binary does, in the present tense, naming what is not there. Do not describe the change; describe the state. The README is the thing a reader sees first, and a stale one is the failure this repo was set up to avoid.

- [ ] **Step 9: Run everything**

```bash
go build ./...
go vet ./...
go test ./... -race
ATLAS_SANDBOX_TESTS=1 go test ./internal/adapters/outbound/sandbox/... -v
```

Expected: all green. Paste the output.

- [ ] **Step 10: Commit**

```bash
git add internal/adapters/outbound/tools/browser.go cmd/atlas internal/config docs/notes README.md
git commit -m "Read a page that publishes no API, and say what this increment left open"
```

---

## Deliberately not in this plan

| Out | Why |
|---|---|
| Form filling and submission | The v1 refusal in the product design; its reversal condition is unmet |
| Nerve as `Store` | Increment 2, shaped by what packs turn out to need |
| An interactive prompt for `ask` | A surface concern; there is no surface yet |
| Web UI | Runner first |
| Conditionals and loops in blueprints | No pack has run out of a sequence |
| Multiple concurrent runs per workspace | No pack has asked for it; the container name would need to change first |
