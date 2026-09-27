# Epic 12 — Surfaces — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** prove, mechanically, that the next driving surface needs a new adapter package and a
branch in `run()`, never a change to `internal/core`. The proof comes in five parts:
- an architecture test that holds inbound adapters apart;
- a progress callback the core exposes without knowing who listens;
- a guard that `run()` builds the registry once;
- a pid lock so two processes never share one store;
- a proof that an unattended run denies rather than hangs.

**Architecture:** One new test file in `internal/arch` pins the seam. The core gains one domain
type (`domain.StepEvent`) and one `Runner` option (`WithProgress`), the same caller-supplied
callback shape as `Runner.WithTracer` and `ports.Agent.Do`'s `onEvent`. A new `internal/pidlock`
package holds the store lock. It sits beside `internal/config` and `internal/telemetry` as
composition-root infrastructure, with its liveness check split per OS exactly as
`acpagent/proc_unix.go` and `proc_windows.go` already split process handling. `cmd/atlas` wires
all of it. No port changes, and no new surface ships.

**Tech Stack:** Go 1.27, the standard library (`go/ast`, `os/exec`, `syscall`),
`golang.org/x/sys/windows` (already in go.mod as indirect; it becomes direct).

**Spec:** `docs/specs/2026-09-27-epic-surfaces.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md`, Epic 12, stories 12.1-12.7

**Tenet:** "The narrow waist, and who owns a change", in `../CLAUDE.md` (the Forge root). Adding
the sixth surface must not change the core at all.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **No surface the spec rejected is built:** no HTTP API, chat channel, cron runner, or Atlas as
  an inbound ACP agent. No progressive-disclosure tool loading and no self-written skills.
- **The core does not learn about any surface.** No `if` in `internal/core` depends on who called.
  What differs per surface is a callback the adapter supplies.
- **No port changes.** `ports.*` keeps every signature. The only core additions are
  `domain.StepStatus`, its constants, `domain.StepEvent`, and `(*app.Runner).WithProgress`.
- **No core package imports an adapter**, and none imports `net/http` or `crypto/tls`.
  `internal/arch/arch_test.go` already enforces both.
- The `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no literal in
  `buildRegistry` or `startAgent`. `cmd/atlas/main_test.go` enforces them.
- `ctx context.Context` comes first on every blocking call and is never stored in a struct.
- No use-case vocabulary in any `.go` file (`internal/arch/vocabulary_test.go`), tests included.
- Every wrapped error carries its component prefix: `pidlock: ` and `atlas: `.
- No emojis. Comments describe current behaviour only. README and docs stay succinct.
- Tests assert behaviour. Prove each guard by mutation, and **confirm the mutation applied**
  before reading the result.
- Every test runs offline. The Windows liveness check must compile: `GOOS=windows go vet` on
  the packages it touches passes.
- Commit messages: subject line, blank line, then
  `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## What "done" means

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
GOOS=windows go vet ./internal/pidlock/ ./cmd/atlas/
```

Then the epic's claim, with pasted output:

> A new inbound package that reaches a sibling surface or an outbound adapter fails the build's
> tests. A sketch of the next surface compiles against the core with no core change. The CLI
> shows each step as it runs. A second process against a held store is refused, naming the pid,
> and a dead holder's lock is reclaimed.

## Rulings on what the spec leaves open

| # | Subject | Ruling |
|---|---|---|
| R1 | Where the lock lives | A new package, `internal/pidlock`, beside `internal/config` and `internal/telemetry`. It is composition-root infrastructure. It is not a port, because no second implementation exists or is plausible, and it is not an adapter, because nothing in the core calls it. Only `run()` does. |
| R2 | Stale-pid detection, per OS | Split as `acpagent` already is, with the same build tags. **Unix** (`proc_unix.go`, `//go:build unix`): `syscall.Kill(pid, 0)`. Nil means alive; `EPERM` means alive under another user; anything else, such as `ESRCH`, means dead. **Windows** (`proc_windows.go`, `//go:build windows`): `windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid)`. `ERROR_ACCESS_DENIED` means alive; any other error means dead. On success, `GetExitCodeProcess` returns 259 (`STILL_ACTIVE`, defined locally because `x/sys/windows` has no such constant) while the process is alive. |
| R3 | What counts as stale | A holder pid that is not alive, **or** a lock whose content is not a positive integer, as a crash mid-write would leave. Either is taken over. |
| R4 | The same process locking twice | Refused, like any live holder. The pid named is our own. A process never needs two locks on one store. |
| R5 | Two processes reclaiming the same stale lock at once | Not solved. Creation is `O_EXCL`, so only one wins a create, but a reclaimer can remove a lock another reclaimer has just written. This is one user and one machine, and the window is a crash followed by two starts in the same instant. Recorded under Deliberately not built. |
| R6 | Lock path | `<Store.Root>/.atlas.lock`, as the spec says. It sits untracked in the record's working tree. `gitdocs` stages paths explicitly, never with an add-all, so it is never committed, and `Docs.List` reads HEAD, so it never appears as a document. |
| R7 | When the lock is taken | In `run()`, immediately after `config.Load` resolves `Store.Root` and before anything opens the store. The root is created first (`os.MkdirAll`) if missing, as `gitdocs.Open` would create it anyway. It is released by a deferred call in `run()`. An AST guard pins "acquired before `gitdocs.Open`". |
| R8 | A failed step's progress line | `atlas: step <id> (<tool>) failed`, without the error. `run()` already prints the full error when `Run` returns, so printing it twice would be noise. |
| R9 | A `Runner` with no progress callback | `NewRunner` installs a no-op function, so `runStep` calls it unconditionally with no nil check. This is the same "pays nothing" shape as the no-op tracer. |
| R10 | Which steps report | Every step `Run` attempts reports `StepStarted`, then exactly one of `StepDone` or `StepFailed`. That includes a step whose tool is missing or whose config fails to render. A step after a failure is never attempted and reports nothing. |
| R11 | The isolation rule's edges | An inbound package may depend on its own subpackages (`inbound/web` on `inbound/web/tmpl`), but not on a sibling that merely shares a name prefix (`inbound/web` on `inbound/webhook`). The package set is enumerated with `go list .../inbound/...`, so a future surface is picked up without editing the test. The test also fails if the enumeration does not include `packfile` and `mcpserve`, so it cannot pass vacuously. |
| R12 | Story 12.6's evidence | Beyond the design review the spec asks for, a throwaway sketch of the next surface is compiled. It lives in `internal/adapters/inbound/zzsketch`: decode a blueprint, call `Run` with `WithProgress`, encode the state. The isolation test passes with it present, and fails when the sketch imports an outbound adapter. `git diff --stat` shows it needed no core file. The sketch is deleted before commit, and the output goes in the note. |

## Review Focus

1. **The isolation test must fail for a real future surface, not only for today's two
   packages.** A reasonable person expects a new `inbound/web` that imports `outbound/gitdocs` to
   break the build. Covered by Task 1: `TestIsolationRuleSeparatesSurfaces` plus the mutation
   step that adds a throwaway inbound package.
2. **Someone else's live process must never be treated as dead.** A lock held by a running atlas
   under another user must refuse, not be reclaimed. Covered by Task 3: the unix
   `EPERM`-is-alive branch, pinned by `TestAliveTreatsPermissionDeniedAsAlive` (a unix-only test
   against pid 1).
3. **A crash must never leave the store locked forever.** Covered by Task 3:
   `TestADeadHoldersLockIsReclaimed` and `TestAGarbledLockIsReclaimed`.
4. **An unattended run must deny, not hang.** Covered by Task 5:
   `TestAnUnattendedAskDeniesInsteadOfHanging` with stdin closed and with stdin at the null
   device.
5. **A step that fails before its tool runs must still report.** Covered by Task 2:
   `TestAMissingToolReportsStartedThenFailed`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/arch/surfaces_test.go` | `TestInboundAdaptersStayIsolated`, the rule as a pure function, and its unit test |
| `internal/core/domain/progress.go` | `StepStatus`, `StepEvent` |
| `internal/core/app/runner.go` | `WithProgress`; `runStep` reports around `execStep` |
| `internal/pidlock/lock.go` | `Acquire`, `(*Lock).Release`, stale detection |
| `internal/pidlock/proc_unix.go`, `proc_windows.go` | `alive(pid int) bool` per OS |
| `cmd/atlas/main.go` | Lock in `run()`, `progressPrinter`, `WithProgress` wiring |
| `cmd/atlas/main_test.go` | `TestBuildRegistryIsCalledOnce`, `TestRunLocksTheStoreBeforeOpeningIt`, `TestCompositionRootReportsProgress`, `TestProgressPrinterWritesOneLinePerEvent` |
| `internal/adapters/outbound/termprompt/prompt_test.go` | The unattended-denies proof |
| `docs/design/surfaces.md` | The seam, the rules, how to add a surface, running unattended |
| `docs/notes/2026-09-27-epic-12.md` | Increment note with the evidence |

## Deliberately not built

Everything in the spec's "Deliberately not in this epic" table, plus:

| Out | Why |
|---|---|
| Advisory OS file locks (`flock`, `LockFileEx`) | A pid file with a liveness check meets "check if alive, else reclaim" portably. OS locks would close R5's race at the cost of two more per-OS code paths. Revisit if two starts in one instant is ever observed. |
| A `gitdocs.Store` that locks itself | A store opened twice in one process, as tests do, would then deadlock or refuse. The lock is a process concern, taken once in `run()`. |
| Progress output formatting beyond one line per event | A surface's rendering is its own. The CLI's is the minimum that tells running from hung. |

---

## Task 1: Inbound adapters cannot see each other

**Stories:** 12.3, the epic's main deliverable.

**Files:**
- Create: `internal/arch/surfaces_test.go`

**Interfaces:**
- Consumes: `runGoListDeps(t *testing.T, pkg string) []byte` (exists in `arch_test.go`, same package `arch_test`).
- Produces: `TestInboundAdaptersStayIsolated`, `TestIsolationRuleSeparatesSurfaces`.

- [ ] **Step 1: Write the test file** (`internal/arch/surfaces_test.go`)

```go
package arch_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	inboundRoot  = "github.com/tunedev/atlas/internal/adapters/inbound/"
	outboundRoot = "github.com/tunedev/atlas/internal/adapters/outbound/"
)

// TestInboundAdaptersStayIsolated holds the seam every surface plugs into:
// an inbound adapter reaches the core, the standard library and third-party
// packages, never another inbound adapter and never an outbound one.
// Outbound adapters are wired once, at the composition root, so a surface
// that imported one would stand up its own copy of something every surface
// shares. Every package under inbound/ is checked, including ones added
// after this test was written.
func TestInboundAdaptersStayIsolated(t *testing.T) {
	pkgs := inboundPackages(t)
	for _, want := range []string{inboundRoot + "packfile", inboundRoot + "mcpserve"} {
		if !slices.Contains(pkgs, want) {
			t.Fatalf("inbound packages %v do not include %s; the enumeration is broken, not the seam", pkgs, want)
		}
	}
	for _, pkg := range pkgs {
		deps := strings.Fields(string(runGoListDeps(t, pkg)))
		for _, dep := range isolationViolations(pkg, deps) {
			t.Errorf("%s depends on %s; an inbound adapter may reach the core, never another surface or an outbound adapter", pkg, dep)
		}
	}
}

// TestIsolationRuleSeparatesSurfaces checks the rule itself against
// dependency lists no real package has yet, so a future surface's likely
// mistakes are pinned before anyone makes them.
func TestIsolationRuleSeparatesSurfaces(t *testing.T) {
	web := inboundRoot + "web"
	cases := []struct {
		dep       string
		violation bool
	}{
		{inboundRoot + "packfile", true},                    // a sibling surface
		{inboundRoot + "webhook", true},                     // a sibling sharing a name prefix
		{outboundRoot + "gitdocs", true},                    // an outbound adapter
		{outboundRoot + "openaiprov/wire", true},            // an outbound subpackage
		{web, false},                                        // itself
		{web + "/tmpl", false},                              // its own subpackage
		{"github.com/tunedev/atlas/internal/core/app", false},
		{"github.com/tunedev/atlas/internal/core/ports", false},
		{"net/http", false},
	}
	for _, c := range cases {
		got := isolationViolations(web, []string{c.dep})
		if (len(got) == 1) != c.violation {
			t.Errorf("dependency %s: violations %v, want violation=%v", c.dep, got, c.violation)
		}
	}
}

// isolationViolations returns the dependencies of pkg that break the seam:
// any inbound package that is not pkg or below it, and any outbound package.
func isolationViolations(pkg string, deps []string) []string {
	var bad []string
	for _, dep := range deps {
		own := dep == pkg || strings.HasPrefix(dep, pkg+"/")
		switch {
		case own:
		case strings.HasPrefix(dep, inboundRoot), strings.HasPrefix(dep, outboundRoot):
			bad = append(bad, dep)
		}
	}
	return bad
}

// inboundPackages lists every package under internal/adapters/inbound.
func inboundPackages(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", inboundRoot+"...").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v: %s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	return strings.Fields(string(out))
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/arch/ -run 'Isolat' -v`
Expected: both PASS. This test pins a property that is already true, so there is no RED on the
real tree; Step 3 is its RED.

- [ ] **Step 3: Prove it fails for each way a surface can break the seam**

Run each mutation, confirm it applied (`cat` the file), run
`go test ./internal/arch/ -run TestInboundAdaptersStayIsolated -v`, record the FAIL line, then
remove the mutation:

1. **Sibling:** create `internal/adapters/inbound/packfile/zz_mutation.go` containing
   `package packfile` and `import _ "github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"`.
   Expected: FAIL naming `packfile` and `mcpserve`.
2. **Outbound:** the same file importing `_ "github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"`.
   Expected: FAIL naming `gitdocs`.
3. **A future surface:** create a new package `internal/adapters/inbound/zzsurface/surface.go`
   (`package zzsurface`) importing `_ "github.com/tunedev/atlas/internal/adapters/outbound/tools"`.
   Expected: FAIL naming `zzsurface` and `tools`. This proves new packages are enumerated.
4. **Vacuity:** temporarily change the enumeration pattern in `inboundPackages` to
   `inboundRoot + "nothing/..."`. Expected: FAIL at the "enumeration is broken" check.

Remove every mutation and confirm `git status --short` shows only `surfaces_test.go`.

- [ ] **Step 4: Commit**

```bash
gofmt -l . ; go vet ./... && go test -race ./...
git add internal/arch/surfaces_test.go
git commit -F - <<'EOF'
Hold inbound adapters apart: no sibling surface, no outbound adapter

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Task 2: Progress leaves the core through a callback

**Stories:** 12.1.

**Files:**
- Create: `internal/core/domain/progress.go`
- Modify: `internal/core/app/runner.go`
- Test: `internal/core/app/runner_test.go`

**Interfaces:**
- Consumes: `fakeTool{name, result, err, seen}` and `fakeRegistry` (exist in `runner_test.go`).
- Produces: `domain.StepStatus`, `domain.StepStarted/StepDone/StepFailed`,
  `domain.StepEvent{StepID, Tool string; Status StepStatus; Err error}`,
  `func (r *Runner) WithProgress(f func(domain.StepEvent)) *Runner`.

- [ ] **Step 1: Write the failing tests** (append to `internal/core/app/runner_test.go`; add
  imports as needed)

```go
func recordProgress(events *[]domain.StepEvent) func(domain.StepEvent) {
	return func(e domain.StepEvent) { *events = append(*events, e) }
}

func TestProgressReportsEachStepStartAndDoneInOrder(t *testing.T) {
	reg := fakeRegistry{
		"a": &fakeTool{name: "a", result: map[string]any{"v": "1"}},
		"b": &fakeTool{name: "b", result: map[string]any{"v": "2"}},
	}
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "a"}, {ID: "two", Tool: "b"}}}
	if _, err := app.NewRunner(reg).WithProgress(recordProgress(&events)).Run(context.Background(), bp); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []domain.StepEvent{
		{StepID: "one", Tool: "a", Status: domain.StepStarted},
		{StepID: "one", Tool: "a", Status: domain.StepDone},
		{StepID: "two", Tool: "b", Status: domain.StepStarted},
		{StepID: "two", Tool: "b", Status: domain.StepDone},
	}
	if !slices.Equal(events, want) {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

func TestProgressReportsAFailedStepWithItsErrorAndStops(t *testing.T) {
	boom := errors.New("boom")
	reg := fakeRegistry{"a": &fakeTool{name: "a", err: boom}, "b": &fakeTool{name: "b", result: map[string]any{}}}
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "a"}, {ID: "two", Tool: "b"}}}
	if _, err := app.NewRunner(reg).WithProgress(recordProgress(&events)).Run(context.Background(), bp); err == nil {
		t.Fatal("Run succeeded with a failing step")
	}
	if len(events) != 2 || events[0].Status != domain.StepStarted || events[1].Status != domain.StepFailed || events[1].StepID != "one" {
		t.Fatalf("events = %+v, want one started and one failed, and nothing for the step after", events)
	}
	if !errors.Is(events[1].Err, boom) {
		t.Errorf("failed event's Err = %v, want it to wrap the tool's error", events[1].Err)
	}
}

func TestAMissingToolReportsStartedThenFailed(t *testing.T) {
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "absent"}}}
	_, _ = app.NewRunner(fakeRegistry{}).WithProgress(recordProgress(&events)).Run(context.Background(), bp)
	if len(events) != 2 || events[0].Status != domain.StepStarted || events[1].Status != domain.StepFailed || events[1].Err == nil {
		t.Errorf("events = %+v, want started then failed with an error", events)
	}
}
```

Every existing `Runner` test, none of which calls `WithProgress`, must keep passing unchanged.
That is the "behaves exactly as before" half of 12.1.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/core/app/ -run 'Progress|MissingTool' -v`
Expected: FAIL (undefined: `domain.StepEvent`).

- [ ] **Step 3: Implement**

`internal/core/domain/progress.go`:

```go
package domain

// StepStatus is where a step's execution stands.
type StepStatus string

const (
	StepStarted StepStatus = "started"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

// StepEvent is one change in a step's status, reported as it happens. Err
// is set only when Status is StepFailed.
type StepEvent struct {
	StepID string
	Tool   string
	Status StepStatus
	Err    error
}
```

In `internal/core/app/runner.go`, add `progress func(domain.StepEvent)` to `Runner` and set it in
`NewRunner` to `func(domain.StepEvent) {}`. Extend `NewRunner`'s comment with one clause: "and
reports progress to no one unless WithProgress sets a callback". Then add:

```go
// WithProgress replaces the Runner's progress callback, returning the same
// Runner for chaining at construction time. The callback runs synchronously,
// in the order steps execute, on Run's own goroutine: each step Run attempts
// reports StepStarted, then StepDone or StepFailed.
func (r *Runner) WithProgress(f func(domain.StepEvent)) *Runner {
	r.progress = f
	return r
}
```

Rename the current `runStep` to `execStep`, keeping its body and its comment about spans. Add a
new `runStep` that reports around it:

```go
// runStep executes one step, reporting its start and its outcome through
// the progress callback.
func (r *Runner) runStep(ctx context.Context, blueprint string, s domain.Step, state *domain.State) error {
	r.progress(domain.StepEvent{StepID: s.ID, Tool: s.Tool, Status: domain.StepStarted})
	if err := r.execStep(ctx, blueprint, s, state); err != nil {
		r.progress(domain.StepEvent{StepID: s.ID, Tool: s.Tool, Status: domain.StepFailed, Err: err})
		return err
	}
	r.progress(domain.StepEvent{StepID: s.ID, Tool: s.Tool, Status: domain.StepDone})
	return nil
}
```

- [ ] **Step 4: Run to verify they pass, then the whole suite and the architecture tests**

Run: `go test ./internal/core/... ./internal/arch/ -v 2>&1 | grep -E '^(--- |ok|FAIL)' && go test -race ./...`
Expected: PASS everywhere.

- [ ] **Step 5: Prove the terminal event is required**

Temporarily delete the `StepDone` call. Confirm it applied, run
`go test ./internal/core/app/ -run TestProgressReportsEachStepStartAndDoneInOrder`, and expect
FAIL. Restore.

- [ ] **Step 6: Commit**

```bash
gofmt -l . ; go vet ./...
git add internal/core/domain/progress.go internal/core/app/runner.go internal/core/app/runner_test.go
git commit -F - <<'EOF'
Report each step's progress through a callback the caller supplies

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Task 3: The store lock

**Stories:** 12.4.

**Files:**
- Create: `internal/pidlock/lock.go`, `internal/pidlock/proc_unix.go`, `internal/pidlock/proc_windows.go`
- Test: `internal/pidlock/lock_test.go`, `internal/pidlock/proc_unix_test.go`

**Interfaces:**
- Produces: `func Acquire(path string) (*Lock, error)` and `func (l *Lock) Release() error` (package `github.com/tunedev/atlas/internal/pidlock`).

- [ ] **Step 1: Write the failing tests**

`internal/pidlock/lock_test.go`:

```go
package pidlock_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/pidlock"
)

func lockPath(t *testing.T) string { return filepath.Join(t.TempDir(), ".atlas.lock") }

func holder(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestAcquireWritesThePidAndReleaseRemovesIt(t *testing.T) {
	path := lockPath(t)
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := holder(t, path); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock holds %q, want this process's pid", got)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock still present after Release: %v", err)
	}
}

func TestALiveHolderIsRefusedByPid(t *testing.T) {
	path := lockPath(t)
	live := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pidlock.Acquire(path)
	if err == nil || !strings.Contains(err.Error(), live) {
		t.Fatalf("err = %v, want a refusal naming pid %s", err, live)
	}
	if got := holder(t, path); got != live {
		t.Errorf("a refused Acquire changed the lock to %q", got)
	}
}

func TestADeadHoldersLockIsReclaimed(t *testing.T) {
	// A process that has already exited: this test binary, run with no tests.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper: %v", err)
	}
	dead := strconv.Itoa(cmd.ProcessState.Pid())
	path := lockPath(t)
	if err := os.WriteFile(path, []byte(dead+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over a dead holder (pid %s): %v", dead, err)
	}
	defer func() { _ = l.Release() }()
	if got := holder(t, path); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("reclaimed lock holds %q, want this process's pid", got)
	}
}

func TestAGarbledLockIsReclaimed(t *testing.T) {
	path := lockPath(t)
	if err := os.WriteFile(path, []byte("not a pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over a garbled lock: %v", err)
	}
	_ = l.Release()
}

func TestReleaseLeavesALockThatIsNoLongerOurs(t *testing.T) {
	path := lockPath(t)
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := holder(t, path); got != "1" {
		t.Errorf("Release removed a lock another process holds; it now reads %q", got)
	}
}
```

`internal/pidlock/proc_unix_test.go` (white-box, unix only, pinning Review Focus 2):

```go
//go:build unix

package pidlock

import (
	"os"
	"testing"
)

// Pid 1 is always running on unix and, for an ordinary user, cannot be
// signalled: kill(1, 0) returns EPERM, which must read as alive.
func TestAliveTreatsPermissionDeniedAsAlive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: kill(1, 0) succeeds, so EPERM is not exercised")
	}
	if !alive(1) {
		t.Error("alive(1) = false; a process owned by another user must read as alive")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/pidlock/ -v`
Expected: FAIL (the package has no non-test files).

- [ ] **Step 3: Implement**

`internal/pidlock/lock.go`:

```go
// Package pidlock keeps one process at a time working in a directory. A lock
// is a file holding its owner's pid. A lock whose owner is no longer running,
// or whose content is not a pid, is stale and is taken over rather than left
// for a person to delete.
package pidlock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Lock is a lock this process holds.
type Lock struct {
	path string
	pid  int
}

// Acquire takes the lock at path for this process. It fails, naming the pid,
// when a running process holds it, including this one. A stale lock is
// removed and taken.
func Acquire(path string) (*Lock, error) {
	pid := os.Getpid()
	for range 2 {
		err := create(path, pid)
		if err == nil {
			return &Lock{path: path, pid: pid}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("pidlock: %w", err)
		}
		if holder, ok := readPid(path); ok && alive(holder) {
			return nil, fmt.Errorf("pidlock: %s is held by running process %d", path, holder)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("pidlock: remove stale lock %s: %w", path, err)
		}
	}
	return nil, fmt.Errorf("pidlock: %s was taken while it was being reclaimed", path)
}

// Release removes the lock if it still holds this process's pid. A lock
// another process has since taken is left alone.
func (l *Lock) Release() error {
	if holder, ok := readPid(l.path); !ok || holder != l.pid {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("pidlock: release %s: %w", l.path, err)
	}
	return nil
}

// create writes pid to path, failing if path already exists.
func create(path string, pid int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// readPid reads the pid a lock holds. It reports false when the file is
// missing or holds anything but a positive integer.
func readPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}
```

`internal/pidlock/proc_unix.go`:

```go
//go:build unix

package pidlock

import (
	"errors"
	"syscall"
)

// alive reports whether pid is a running process. Signal 0 checks for the
// process without delivering anything; EPERM means it exists under another
// user.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
```

`internal/pidlock/proc_windows.go`:

```go
//go:build windows

package pidlock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited.
const stillActive = 259

// alive reports whether pid is a running process. Access denied means it
// exists under another user.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
```

Then run `go mod tidy`, which moves `golang.org/x/sys` from indirect to direct.

- [ ] **Step 4: Run to verify they pass, and that Windows compiles**

```bash
go test -race ./internal/pidlock/ -v
GOOS=windows go vet ./internal/pidlock/
GOOS=darwin go vet ./internal/pidlock/
```

Expected: every test PASSES; both cross-OS vets are clean.

- [ ] **Step 5: Prove the guards can fail**

1. Make `alive` in `proc_unix.go` return `err == nil` only. Confirm it applied, run
   `go test ./internal/pidlock/ -run TestAliveTreatsPermissionDeniedAsAlive`, and expect FAIL.
   Restore.
2. Make `Acquire` return the refusal whenever `readPid` is ok, skipping `alive`. Confirm it
   applied, run `go test ./internal/pidlock/ -run TestADeadHoldersLockIsReclaimed`, and expect
   FAIL. Restore.

- [ ] **Step 6: Commit**

```bash
gofmt -l . ; go vet ./... && go test -race ./...
git add internal/pidlock go.mod go.sum
git commit -F - <<'EOF'
Lock a directory to one process by pid, reclaiming a dead holder's lock

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Task 4: Wire the CLI: the lock, progress lines, and one registry

**Stories:** 12.2, 12.4 (wiring), 12.7.

**Files:**
- Modify: `cmd/atlas/main.go`
- Test: `cmd/atlas/main_test.go`

**Interfaces:**
- Consumes: `pidlock.Acquire`, `(*pidlock.Lock).Release`, `domain.StepEvent` and its status
  constants, `(*app.Runner).WithProgress`.
- Produces: `func progressPrinter(w io.Writer) func(domain.StepEvent)` and the constant
  `storeLockName = ".atlas.lock"` in `cmd/atlas`.

- [ ] **Step 1: Write the failing tests** (append to `cmd/atlas/main_test.go`; add imports as
  needed)

```go
// runDecl returns run()'s declaration in main.go.
func runDecl(t *testing.T) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			return fset, fn
		}
	}
	t.Fatal("run() not found in main.go")
	return nil, nil
}

// callPositions returns where run() calls pkg.name, or name when pkg is
// empty.
func callPositions(fn *ast.FuncDecl, pkg, name string) []token.Pos {
	var at []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if pkg == "" && f.Name == name {
				at = append(at, call.Pos())
			}
		case *ast.SelectorExpr:
			if id, ok := f.X.(*ast.Ident); ok && id.Name == pkg && f.Sel.Name == name {
				at = append(at, call.Pos())
			}
		}
		return true
	})
	return at
}

// Every surface shares the registry run() builds; a surface branch that
// built its own would stand up a second provider and a second tool set.
func TestBuildRegistryIsCalledOnce(t *testing.T) {
	_, run := runDecl(t)
	if n := len(callPositions(run, "", "buildRegistry")); n != 1 {
		t.Errorf("run() calls buildRegistry %d times; want exactly once, shared by every surface", n)
	}
}

// The lock is held before anything opens the store, so a second process is
// refused before it touches git.
func TestRunLocksTheStoreBeforeOpeningIt(t *testing.T) {
	_, run := runDecl(t)
	lock, open := callPositions(run, "pidlock", "Acquire"), callPositions(run, "gitdocs", "Open")
	if len(lock) != 1 || len(open) != 1 {
		t.Fatalf("run() calls pidlock.Acquire %d times and gitdocs.Open %d times; want one each", len(lock), len(open))
	}
	if lock[0] > open[0] {
		t.Error("run() opens the store before locking it")
	}
}

func TestCompositionRootReportsProgress(t *testing.T) {
	_, run := runDecl(t)
	var found bool
	ast.Inspect(run, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithProgress" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("run() never calls WithProgress; the CLI would show no step progress")
	}
}

func TestProgressPrinterWritesOneLinePerEvent(t *testing.T) {
	var b bytes.Buffer
	p := progressPrinter(&b)
	p(domain.StepEvent{StepID: "fetch", Tool: "http.request", Status: domain.StepStarted})
	p(domain.StepEvent{StepID: "fetch", Tool: "http.request", Status: domain.StepDone})
	p(domain.StepEvent{StepID: "judge", Tool: "judge.ask", Status: domain.StepFailed, Err: errors.New("boom")})
	want := "atlas: step fetch (http.request) started\n" +
		"atlas: step fetch (http.request) done\n" +
		"atlas: step judge (judge.ask) failed\n"
	if b.String() != want {
		t.Errorf("progress output =\n%q\nwant\n%q", b.String(), want)
	}
}
```

`TestBuildRegistryIsCalledOnce` passes on today's `main.go`; that is the property being pinned,
and Step 5 is its RED. The other three fail until Step 3.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/atlas/ -run 'CalledOnce|LocksTheStore|ReportsProgress|ProgressPrinter' -v`
Expected: the lock, progress and printer tests FAIL (undefined `progressPrinter`, or no
`pidlock.Acquire` call). `TestBuildRegistryIsCalledOnce` PASSES.

- [ ] **Step 3: Implement**

In `cmd/atlas/main.go`:

```go
// storeLockName is the lock file, inside Store.Root, that keeps one atlas
// process at a time working in a store.
const storeLockName = ".atlas.lock"

// progressPrinter writes one line to w as each step starts and finishes, so
// a long step reads as running rather than hung. A failure's cause is left
// to the error run() returns.
func progressPrinter(w io.Writer) func(domain.StepEvent) {
	return func(e domain.StepEvent) {
		fmt.Fprintf(w, "atlas: step %s (%s) %s\n", e.StepID, e.Tool, e.Status)
	}
}
```

In `run()`, directly after `cfg, err := config.Load(...)` and its error check:

```go
	// One process at a time works in a store; the lock is taken before
	// anything opens it, and a lock left by a process that has exited is
	// reclaimed.
	if err := os.MkdirAll(cfg.Store.Root, 0o755); err != nil {
		return fmt.Errorf("store root: %w", err)
	}
	lock, err := pidlock.Acquire(filepath.Join(cfg.Store.Root, storeLockName))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		}
	}()
```

Change the runner line to:

```go
	runner := app.NewRunner(registry).WithTracer(tracer).WithProgress(progressPrinter(os.Stderr))
```

Add imports: `io`, `path/filepath` (if absent), `github.com/tunedev/atlas/internal/core/domain`,
`github.com/tunedev/atlas/internal/pidlock`.

- [ ] **Step 4: Run to verify they pass, then the whole suite and the Windows build**

```bash
go test ./cmd/atlas/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
GOOS=windows go vet ./cmd/atlas/
```

Expected: PASS everywhere, including `TestCompositionPassesNoLiterals`,
`TestRunIsSeparateFromMainSoDefersExecute` and `TestCompositionRootInjectsATracer`.

- [ ] **Step 5: Prove the registry and lock guards can fail**

1. Add a second call, `_ = buildRegistry(cfg, docs, index, source, crawler)`, after the first.
   Confirm it applied, run `go test ./cmd/atlas/ -run TestBuildRegistryIsCalledOnce`, and expect
   FAIL. Remove it.
2. Move the lock block below `gitdocs.Open`. Confirm it applied, run
   `go test ./cmd/atlas/ -run TestRunLocksTheStoreBeforeOpeningIt`, and expect FAIL. Restore.

- [ ] **Step 6: Commit**

```bash
git add cmd/atlas go.mod go.sum
git commit -F - <<'EOF'
Lock the store, print step progress, and build the registry once

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Task 5: Unattended invocation is proven safe

**Stories:** 12.5.

**Files:**
- Test: `internal/adapters/outbound/termprompt/prompt_test.go`
- Create: `docs/design/surfaces.md` (the operational note lives here; Task 6 completes the doc)

**Interfaces:**
- Consumes: `termprompt.New(in io.Reader, out io.Writer) *Prompt`,
  `app.NewPermissionPolicy(rules []app.PermissionRule, human ports.Permission)`, and `req`
  (a `ports.PermissionRequest` already declared in `prompt_test.go`).

- [ ] **Step 1: Write the test** (append to `prompt_test.go`; add imports for `os` and
  `github.com/tunedev/atlas/internal/core/app`)

```go
// Under a scheduler, stdin is closed or the null device. A policy with no
// rule for a call falls through to the terminal prompt, which must deny at
// once rather than wait for a person who is not there.
func TestAnUnattendedAskDeniesInsteadOfHanging(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	closed, w := io.Pipe()
	_ = w.Close()

	for name, in := range map[string]io.Reader{"closed pipe": closed, "null device": devNull} {
		policy := app.NewPermissionPolicy(nil, termprompt.New(in, io.Discard))
		done := make(chan ports.PermissionDecision, 1)
		go func() {
			d, _ := policy.Decide(context.Background(), req)
			done <- d
		}()
		select {
		case d := <-done:
			if d != ports.PermissionDeny {
				t.Errorf("%s: decision = %s, want deny", name, d)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: an unconfigured ask with no one at the terminal hung instead of denying", name)
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/adapters/outbound/termprompt/ -run Unattended -v -race`
Expected: PASS. This pins behaviour the spec proved by reading the code. Step 3 is its RED.

- [ ] **Step 3: Prove it can fail**

In `termprompt/prompt.go`'s `read`, temporarily replace `close(lines)` with `select {}`, so the
reader never signals end of input. Confirm it applied, run the Step 2 command, and expect FAIL
after 5s ("hung instead of denying"). Restore.

- [ ] **Step 4: Write the operational note** (`docs/design/surfaces.md`; create the file with
  this section, and Task 6 adds the rest above it)

````markdown
## Running a pack unattended

A pack run by a scheduler is the CLI surface with no one at the terminal: stdin is closed or the
null device. Any tool call that no `-permission-rules` entry decides falls through to the
terminal prompt, which then denies at once. The step fails rather than hangs
(`TestAnUnattendedAskDeniesInsteadOfHanging`).

So a scheduled pack must be invoked with `-permission-rules` covering every tool call its agent
may make. The last rule should be an explicit catch-all, allow or deny, so nothing reaches the
prompt:

```
atlas -pack packs/<pack>.yaml -permission-rules 'notes.write:edit:allow,*:*:deny'
```

A second scheduled run while the first is still going is refused at startup, naming the first
run's pid (`.atlas.lock`). A scheduler should treat that as "already running", not as a failure
to retry in a loop.
````

- [ ] **Step 5: Commit**

```bash
gofmt -l . ; go test -race ./internal/adapters/outbound/termprompt/
git add internal/adapters/outbound/termprompt/prompt_test.go docs/design/surfaces.md
git commit -F - <<'EOF'
Prove an unattended ask denies instead of hanging, and say how to run unattended

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Task 6: The seam is sufficient: sketch proof, design doc, live evidence, PR

**Stories:** 12.6, plus evidence for 12.2 and 12.4.

**Files:**
- Create then delete: `internal/adapters/inbound/zzsketch/sketch.go` (untracked; never committed)
- Modify: `docs/design/surfaces.md`
- Create: `docs/notes/2026-09-27-epic-12.md`

- [ ] **Step 1: Sketch the next surface against the core, then delete it**

Write `internal/adapters/inbound/zzsketch/sketch.go`, the shape the web UI epic's handler will
take. It imports only `encoding/json`, `net/http`, `internal/core/app` and
`internal/core/domain`:

```go
// Package zzsketch is a throwaway proof that a second surface needs no core
// change. It is deleted before commit.
package zzsketch

import (
	"encoding/json"
	"net/http"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
)

// Handler decodes a blueprint, runs it with a progress callback of its own,
// and encodes every step's output and the progress it saw.
func Handler(runner *app.Runner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bp domain.Blueprint
		if err := json.NewDecoder(r.Body).Decode(&bp); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var events []domain.StepEvent
		state, err := runner.WithProgress(func(e domain.StepEvent) { events = append(events, e) }).Run(r.Context(), bp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outputs": state.Outputs(), "progress": events})
	})
}
```

Record each of these outputs:

```bash
go build ./internal/adapters/inbound/zzsketch/
go test ./internal/arch/ -run TestInboundAdaptersStayIsolated -v    # PASS with the sketch present
git status --short                                                   # only zzsketch/ untracked
git diff --stat HEAD -- internal/core                                # empty: the sketch needed no core change
```

Then add `_ "github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"` to the sketch's
imports and rerun the isolation test. Expected: FAIL naming `zzsketch` and `gitdocs`.

Also note, in the report, the limit of this design review. The sketch shares one `*app.Runner`
across requests and sets its progress callback per request, which is a data race. The web UI
epic's handler must build a Runner per request (`app.NewRunner(registry).WithTracer(...).WithProgress(...)`),
reusing the one registry `run()` built. `NewRunner` and the options are cheap, and nothing in the
core has to change for that. Put this under the design doc's "How to add a surface".

Delete the sketch: `rm -rf internal/adapters/inbound/zzsketch`. Confirm `git status --short` is
clean.

- [ ] **Step 2: Complete `docs/design/surfaces.md`**

Add these sections above "Running a pack unattended". The whole doc stays under about 90 lines.

- `# Surfaces`: one core, thin inbound adapters, citing the Forge tenet ("The narrow waist").
- `## The seam`: the spec's may / may-not table, condensed, naming the three tests that enforce
  it: `TestCoreImportsNoAdapters`, `TestInboundAdaptersStayIsolated`,
  `TestBuildRegistryIsCalledOnce`.
- `## Progress`: `WithProgress` and `StepEvent`. Say that the callback runs on `Run`'s goroutine,
  and that the CLI prints one line per event to stderr.
- `## One process per store`: `.atlas.lock`, refusal naming the pid, and reclaiming a dead
  holder's lock. How liveness is checked per OS, in one line each. The known race (R5), in one
  line.
- `## How to add a surface`: a numbered checklist.
  1. A new package under `internal/adapters/inbound/<name>`, importing only core, the standard
     library and third-party packages.
  2. A new optional branch in `run()`, gated on config and reusing `registry` and `cfg`.
  3. A Runner per request, with that surface's own `WithProgress`.
  4. Bind loopback only, with a per-run token compared in constant time, as `mcpserve` does.
  5. Evidence that `git diff --stat -- internal/core` is empty.

- [ ] **Step 3: Live evidence**

Build once: `go build -o "$tmp/atlas" ./cmd/atlas`, with `tmp=$(mktemp -d)`. Use a store under
`$tmp` for every run: `-store-root "$tmp/ws" -store-index-path "$tmp/index.db" -store-history-path "$tmp/h.duckdb"`.

1. **Progress (12.2).** Write a two-step offline pack in `$tmp/p.yaml`. Both steps use
   `file.read` on a file you write into `$tmp`; read `packs/*.yaml` and the `file.read` tool for
   its keys first. Run `"$tmp/atlas" -pack "$tmp/p.yaml" <store flags> 2>"$tmp/err.txt"` and
   paste `err.txt`. Expected: four lines, started and done for each step.
2. **A live holder is refused (12.4).** Start `sleep 300 & holder=$!`, then
   `mkdir -p "$tmp/ws" && echo $holder > "$tmp/ws/.atlas.lock"`. Run atlas and paste its stderr
   and exit code. Expected: non-zero exit, and the message names `$holder`. Then
   `kill $holder; wait $holder 2>/dev/null`.
3. **A dead holder is reclaimed (12.4).** With the lock still holding the now-dead pid, run
   atlas again. Paste the progress lines and exit 0, and show `.atlas.lock` absent afterwards.

- [ ] **Step 4: The increment note** (`docs/notes/2026-09-27-epic-12.md`)

Use the four standard headings (`## What the pattern was`, `## What surprised me`,
`## What I would do differently`, `## What I still do not understand`). Fill them from this
execution's real evidence:
- the isolation test's mutation proofs from Task 1 (sibling, outbound, a new future package,
  vacuity);
- the sketch proof with its `git diff --stat`, and the Runner-per-request finding;
- the live evidence from Step 3.

No placeholders.

- [ ] **Step 5: Verify, commit, push, open the PR**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
GOOS=windows go vet ./internal/pidlock/ ./cmd/atlas/
git add docs
git commit -F - <<'EOF'
Document the surfaces seam, and record the epic's evidence

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
git push -u origin epic-12
gh pr create --base main --head epic-12 --title "Epic 12: surfaces" --body-file - <<'EOF'
Implements Epic 12 per docs/plans/2026-09-27-epic-12-surfaces.md (spec: docs/specs/2026-09-27-epic-surfaces.md). Ships no new surface; proves the next one needs no core change.

- `TestInboundAdaptersStayIsolated`: an inbound package may not reach a sibling surface or an outbound adapter; every future inbound package is checked automatically
- `Runner.WithProgress` + `domain.StepEvent`: progress leaves the core through a callback; the CLI prints one line per step
- `internal/pidlock`: one process per store; refusal names the pid; a dead holder's lock is reclaimed (unix kill-0, windows OpenProcess/GetExitCodeProcess)
- `TestBuildRegistryIsCalledOnce`, `TestRunLocksTheStoreBeforeOpeningIt`
- An unattended ask denies instead of hanging; docs/design/surfaces.md says how to run unattended and how to add a surface

## Evidence
<paste: the suite summary; the Windows vet; the isolation test's four mutation FAIL lines; the sketch's build, the isolation pass, the empty core diff and the injected FAIL; the three live runs>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
```
