# Surfaces — one agent core, many thin inbound adapters

**Supersedes nothing.** This design proposes a new epic for
`docs/plans/2026-09-17-roadmap.md` — not yet added there; see Sequencing below — and sits
under `docs/specs/2026-09-17-job-hunt-harness-design.md`, which it does not contradict.
**Amends nothing** in `docs/specs/2026-09-24-epic-4-agent.md`; it depends on that epic's
result and is precise below about how the two relate.

**Goal:** the harness already has one core and one driving adapter (the CLI, over a pack
file). Before a second driving adapter exists — the web UI epic already on the roadmap —
prove that adding one needs configuration and a new adapter package, never a change to
`internal/core`. This epic ships that proof: a progress callback the core exposes without
knowing who is listening, an architecture test that makes the seam mechanical rather than
a convention, and mutual-exclusion at the store so two adapters never corrupt one
another's work. It ships no new human-facing surface of its own.

## Why now

A study of `NousResearch/hermes-agent` (MIT, Python), a large production agent, states the
rule directly: *"Platform-agnostic core — one `AIAgent` class serves CLI, gateway, ACP,
batch, and API server. Platform differences live in the entry point, not the agent."* And:
*"When two subsystems are involved, the narrower one owns the change. Prefer a fix in an
adapter or plugin over a branch in the agent core; the core is a narrow waist, and every
addition there is paid for on every API call."* Its surfaces share one session store, one
tool registry, one provider resolver and one slash-command registry; what differs per
surface is callbacks, not logic.

`docs/specs/2026-08-24-agent-substrate-design.md` already names the same shape for Atlas,
independently: Claude Code as the reference, a harness the use case is configuration on top
of. That spec is about what the harness runs (packs); this one is about how a run is
reached from outside. The principle is the same one applied one layer further out: **one
core, many thin entry points**, and the corollary from the Forge's architectural thesis —
new kinds need config, not code — applies to *surfaces* exactly as it already applies to
tools, providers and judges.

## What already exists

Read from `internal/adapters/inbound/`, `internal/core/app/runner.go`, and
`cmd/atlas/main.go` on this branch.

| Piece | Direction | What it is |
|---|---|---|
| `packfile.Load` + CLI flags | inbound, driving | The only way a human reaches the core today: a pack path and `-var` overrides become a `domain.Blueprint`, handed to `app.Runner.Run` |
| `app.Runner` | core | Executes a blueprint against `ports.Registry`, one span per step, no use-case knowledge |
| `mcpserve` | inbound, callback | Offers a *subset* of the same `ports.Registry` to the ACP agent subprocess *we spawned*, over MCP-over-HTTP on `127.0.0.1`, so it can call our tools mid-turn |
| `ports.Agent` / `acpagent` | **outbound** | Atlas as the ACP *client*, driving an external coding agent as the "doer" (epic 4) |

Only one of these is a driving surface in this epic's sense. The other two are already
correct and are not touched here; the next two sections say precisely why.

### `mcpserve` is a callback channel, not a surface

`mcpserve`'s own doc comment calls it an inbound adapter, and by the dependency-direction
test it is one: the agent subprocess calls it, so the arrow points into the core the same
way a human's CLI invocation does. But nobody outside the process this same user started
ever reaches it: it binds an ephemeral `127.0.0.1` port, mints a fresh bearer token per run
(`mcpserve.NewToken`), and exists only while `cfg.Agent.Command != ""`. It is scoped to
exactly one peer — the agent subprocess `cmd/atlas` itself launched — never a second human
or a second process. Calling it a "surface" in the sense this epic means (a way a *person*,
or something acting for one on a schedule, reaches the core) would be wrong; it is
plumbing internal to the doer, not an entry point. Nothing about it changes here.

### Relation to epic 4's ACP work

Epic 4 makes Atlas an ACP **client**: it drives someone else's agent as a subprocess. The
question this epic's brief raised — an "ACP session" as a candidate surface — is the
mirror image: Atlas *as* an ACP **agent**, so an ACP client (Zed, or any other) could drive
Atlas's core the way Atlas now drives Claude Code. That is a real, cheap-to-build option —
the wire-level schema knowledge already lives in `internal/adapters/outbound/acpagent`
(`wire.go`, `rpc.go`), so an agent-role adapter would reuse hard-won understanding of the
same JSON-RPC shapes, not duplicate transport code. It is rejected for this epic anyway: see
Deliberately not in this epic. The short version is that Atlas's "doer" already *is* an
external agent under epic 4, so Atlas serving as a second, inbound agent role would mean
running its own agentic loop to decide what to do with a freeform ACP prompt — a
capability nothing in this codebase has or needs, since a pack already says what to do.
Revisit only when a named ACP client is something the user actually wants to point at
Atlas.

## What the core actually is

Not one struct. The thing every surface drives is the trio composed once, in
`cmd/atlas/main.go`, and only there:

```go
registry := buildRegistry(cfg, docs, index, source)     // one ports.Registry
runner   := app.NewRunner(registry).WithTracer(tracer)  // the one entry point
// cfg is the one resolved config.Config; docs, index and source are its one
// set of derived stores.
```

`app.Runner.Run(ctx, blueprint) (*domain.State, error)` is the seam. An inbound adapter's
entire job is: obtain a `domain.Blueprint` from wherever it lives for that surface, and
call `Run`. Nothing about that call changes with this epic except that it can now also
report progress (see Callbacks, below).

**"Session" is not a new concept, and this epic does not add one.** Atlas already has
exactly the resumability primitive hermes-agent's session store gives it, at a narrower
scope: `app.AgentSession`, recorded through `ports.Docs` at
`agent-sessions/<subject-id>.json`, keyed by the subject id a pack author chooses. It
survives a process restart because it is a document, not memory. It resumes exactly one
`agent.do` tool call, not a whole pack run, which is the right scope — a pack run is a
sequence of tool calls, of which `agent.do` is one, and only that one call talks to a
process (the agent subprocess) capable of holding a multi-turn conversation worth
resuming. Nothing above it needs a second, bigger "session": a driving surface that wants
to reconnect to a pack run in progress does so by asking the same `ports.Docs`/`ports.Index`
the pack itself is writing to, the same way a second CLI invocation would look at the same
git repository. Inventing an additional in-memory "run" identity for a surface to poll is
deliberately left to whichever epic first needs one to exist (see Sessions and
resumability).

## The seam: what an inbound adapter may and may not do

| May | May not |
|---|---|
| Obtain a `domain.Blueprint` from its own transport (a file, stdin, an HTTP body) | Import another package under `internal/adapters/inbound/` |
| Call `app.Runner.Run`, optionally with `WithProgress` | Import anything under `internal/adapters/outbound/` directly — those are wired once, at the composition root, exactly as `mcpserve` already only takes `ports.Registry` and `ports.Permission`, never a concrete provider or store |
| Translate `domain.State`, `domain.StepEvent` and errors into its own wire format | Be imported by any package under `internal/core/` |
| Hold its own transport bookkeeping (routing, auth, an in-flight-request map) entirely inside its own package | Define a second `ports.Registry`, a second provider, or a second `config.Config` |

The first three rows of "may not" are already mechanically enforced:

- `TestCoreImportsNoAdapters` (`internal/arch/arch_test.go`) already forbids any
  `github.com/tunedev/atlas/internal/adapters` prefix from `internal/core`'s dependency
  graph — inbound included. Verified by reading the test; nothing here changes it.
- **New:** `TestInboundAdaptersStayIsolated` (`internal/arch/surfaces_test.go`, added by
  this epic) walks every package directory under `internal/adapters/inbound/` and runs
  `go list -deps` on each, failing if any dependency has the prefix
  `internal/adapters/inbound/` (and is not itself) or `internal/adapters/outbound`. This
  already passes against today's two inbound packages — `packfile` imports only `yaml.v3`
  and `internal/core/domain`; `mcpserve` imports the MCP SDK plus `internal/core/app` and
  `internal/core/ports` — so the test pins down a property that is already true rather
  than inventing a new one.

The fourth row — "adding a surface touches no core file" — is not asserted by a test; it
is a story acceptance criterion checked by evidence, the same way epic 0.2 proved harness
genericity by running a second pack rather than by writing a test that could know what
"needs Go" means in general.

## Callbacks, not flags

The core must never carry an `if httpSurface` branch. The existing precedent is
`ports.Agent.Do`'s `onEvent` and `Runner.WithTracer`: a function the caller supplies,
called synchronously and in order, on the caller's own goroutine. This epic extends the
same shape to blueprint execution:

```go
// In internal/core/domain, beside Blueprint and Step.

// StepStatus is where a step's execution currently stands.
type StepStatus string

const (
	StepStarted StepStatus = "started"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

// StepEvent is one change in a step's status, reported as it happens. Err is
// set only when Status is StepFailed.
type StepEvent struct {
	StepID string
	Tool   string
	Status StepStatus
	Err    error
}
```

```go
// In internal/core/app, on Runner.

// WithProgress replaces the Runner's progress callback, returning the same
// Runner for chaining at construction time. The callback runs synchronously,
// in the order steps execute, on Run's own goroutine -- the same discipline
// ports.Agent.Do already holds for onEvent. A Runner with no callback set
// calls nothing and pays nothing extra.
func (r *Runner) WithProgress(f func(domain.StepEvent)) *Runner
```

`runStep` calls it with `StepStarted` before invoking the tool, and with `StepDone` or
`StepFailed` after. The core never asks "who is listening" — a surface that wants a
progress bar, a log line, or nothing at all is a decision made once, at the composition
root, by whether `WithProgress` is called.

## Surfaces: what this epic builds, and what it deliberately leaves for the next one

| Surface | Status | Why |
|---|---|---|
| CLI, over a pack file | Kept, hardened | Already proven by the tracer (epic 0). This epic wires it to `WithProgress` so a long, agent-backed pack shows which step is running without needing `-otel` |
| Web UI (today's roadmap epic 12) | Deferred, unaffected | This epic's entire purpose is to make that epic's local server a drop-in second surface. Its content — the review surface, the decision surface, the local-only bind — is untouched here |

No third surface is built. Considered and rejected:

- **An HTTP API on localhost, ahead of the web UI.** This is exactly what the roadmap's
  epic 12.1 already is ("the local server: serves the UI and the API on localhost only").
  Building it here would be the duplication the brief warned against, not a second
  well-chosen surface — it is the *same* surface, built twice.
- **A chat channel for driving Atlas from a phone.** No spec in this repository names this
  need; the harness spec's product design stops at a local web UI. Building it would mean
  a public-facing bot process and a second, ongoing auth surface for the CV and salary
  data this product exists to protect, with no named consumer. This is the shape of the
  reference's 25+ messaging adapters the Forge already refuses. Revisit only against a
  stated need, not a hypothetical one.
- **A scheduled/cron runner, as new code.** Reading `internal/adapters/outbound/termprompt`
  settles this: `termprompt.New` starts a reader goroutine over `os.Stdin`; under cron,
  stdin is closed or `/dev/null`, so that goroutine's `Scanner.Scan()` returns `false`
  immediately and every later `Decide` call reads from an already-closed channel, returning
  `PermissionDeny`. An unattended run that hits an unconfigured `ask` already fails the
  step rather than hanging forever — proven by the existing code, not assumed. A scheduled
  pack is therefore not a new surface; it is the CLI surface, invoked by the OS timer, with
  its permission rules fully specified so it never actually needs to fall through to `ask`.
  That is a configuration discipline (see story 12.5), not an adapter.
- **Atlas as an inbound ACP agent.** Covered above, under Relation to epic 4's ACP work.

## Patterns held back, and what would earn them

Two more mechanisms from the reference implementation are worth naming and setting aside
deliberately, so a later reader finds a recorded decision rather than an oversight.

**Progressive-disclosure tool loading.** The reference replaces raw tool schemas in the
model-visible list with three bridge tools — search, describe, call — so a large tool
surface costs a small, fixed amount of context regardless of how many tools exist behind
it. It solves schema bloat. Measured against `origin/main` today: `cmd/atlas` registers
nine tools (`http.request`, `model.complete`, `judge.ask`, file read, file text,
`docs.put`, quote ground, extract, decision), every one taking `map[string]string`, and
there is no MCP client anywhere in the tree — `mcpserve` is a server the agent calls, not
a client atlas uses to reach tools of its own. Adopting the bridge now, for nine
trivial-schema tools and no MCP client, would be exactly the interface tax the calibration
rule warns against: there is no second case this makes cheap yet. **Trigger:** atlas gains
an MCP client of its own, or the model-visible tool schemas are measured to crowd the
context window — for example, token-counting the rendered tool list against the model's
context budget and finding it a non-trivial fraction of it.

**Skills as markdown, with staged disclosure.** The reference lets its agent write its own
procedures as `SKILL.md` files, disclosed to itself in stages — a list, then a view, then a
view of one path — which is procedural memory the agent accumulates across runs. In Atlas,
procedures are packs, authored by the user in YAML, and nothing in Atlas writes a procedure
for its own later use; the harness's whole premise (`docs/specs/2026-08-24-agent-substrate-design.md`)
is that use-case knowledge lives in configuration a person wrote, not in something the
system generated for itself. **Trigger:** something in Atlas starts writing a procedure it
intends to reuse. Note also that the reference maintains a bespoke, content-addressed
ledger with rollback to manage these files; Atlas would not need one; git already gives
versioning, diffing and rollback for anything committed through `ports.Docs`, which is the
recorded refusal already in `../../CLAUDE.md`'s "Nerve as a store" row.

## One tool registry, one provider resolution, one config

`buildRegistry` and `startAgent` are already the only functions in `cmd/atlas/main.go`
allowed to construct a concrete adapter, and `TestCompositionPassesNoLiterals`
(`cmd/atlas/main_test.go`) already asserts, by walking the AST, that neither contains a
literal — every value they use must come from `cfg`. Wiring a second surface into `run()`
means adding a third optional branch after the existing agent branch
(`if cfg.Agent.Command != ""`), reusing the single `registry` and `cfg` values `run()`
already built — never calling `buildRegistry` a second time.

**New guard, extending the existing three** (`TestCompositionPassesNoLiterals`,
`TestRunIsSeparateFromMainSoDefersExecute`, `TestCompositionRootInjectsATracer`):
`TestBuildRegistryIsCalledOnce` walks `run()`'s AST and fails if `buildRegistry` appears
in more than one call expression. This is what stops a future surface from silently
standing up its own provider and registry instead of sharing the one `run()` already
built — the literal mechanism by which "one tool registry, one provider resolution, one
config" stays true as a fourth branch is added, rather than a convention nobody checks.

## Sessions and resumability

Two surfaces cannot touch the same `Store.Root` concurrently, by design, not by
coordination. `gitdocs.Store` writes to a working tree with no OS-level locking of its own,
and two processes interleaving commits against one git repository is exactly the kind of
corruption a "declare state, don't script steps" system must refuse to risk. The answer is
deliberately the simplest one the tenets allow: a single lock file at
`<Store.Root>/.atlas.lock`, holding the current process's pid, acquired in `run()`
immediately after `Store.Root` is resolved and released on exit. A second `atlas` process
against the same store finds the lock, checks whether that pid is still alive, and:

- if it is, fails at startup naming the pid — loudly, before touching git, matching the
  "invalid config fails at boot" shape `config.Config.validate()` already uses;
- if it is not (the previous process crashed), reclaims the lock rather than requiring a
  human to delete a file by hand, which is what "operable by one tired person" demands.

This is the entire resumability story this epic needs. It does not require a new in-memory
"run" identity, a run registry, or any coordination protocol: at most one `atlas` process
ever holds a given store, so "two surfaces touching the same work" resolves to "one
process, whichever surface it is currently serving." A surface that wants to let several
of its own callers watch one in-progress run at once — several open browser tabs against
one running blueprint, say — is a within-process concern for that surface's own design
(epic 12/13), using `WithProgress` to fan out to however many listeners it has open; it is
not a core concept, because nothing about which run is currently live is a business fact
worth remembering past that process's own life. Compare a `Judgement` or a `Decision`,
each of which is recorded because a later epic needs to check it against an outcome — an
in-flight run has no such future, and inventing a store for it would be recording state
nothing later reads.

## Security

No new externally reachable listener is added by this epic. `mcpserve`'s existing pattern —
bind `127.0.0.1` only, mint a fresh bearer token per run (`mcpserve.NewToken`,
`crypto/rand`), compare it in constant time (`crypto/subtle.ConstantTimeCompare`) — is the
template the next surface (the web UI) should reuse rather than re-invent, and this spec
states it as the standing rule the security section of that epic's own spec must not
relax: **default bind is loopback only**, no surface listens on `0.0.0.0` without an
explicit, separately-considered opt-in, and nothing an authenticated request cannot show is
ever computed just to serve it. `ModelConfig.APIKey` already never appears in logs or
usage output; any future surface that renders effective config must redact it the same way.
The lock file introduced above holds a pid, nothing else, and is not a secret.

## Testing

- `Runner.WithProgress` is tested with a fake `[]domain.StepEvent` collector against the
  existing `fakeTool`/`fakeRegistry` pair in `internal/core/app/runner_test.go`: order,
  one `StepStarted` and one terminal event per step, and a `StepFailed` event carrying the
  tool's own error — no I/O, matching every other `Runner` test already in that file.
- `TestInboundAdaptersStayIsolated` proves the seam mechanically, as described above; it
  requires no fixture beyond the two packages already on this branch, and will fail the
  moment a future surface reaches into another one's package.
- The lock file's two outcomes — a live pid refuses, a dead pid is reclaimed — are tested
  directly against a real file and a real (possibly already-exited) process, no store or
  git repository required.
- Story 12.6 ("a second surface adds no core file") is evidence, pasted into that epic's
  own `docs/notes/` entry as a `git diff --stat` against `internal/core/`, the same way
  epic 0.2's evidence was "the second pack ran," not a test asserting genericity in the
  abstract.

## Stories

**Why:** every future surface — the web UI, and whatever comes after it — is a drop-in
only if the seam is proven once, the same argument epic 0 made for packs.

| Story | Acceptance |
|---|---|
| 12.1 Progress leaves the core through a callback | `Runner.WithProgress` exists; a step's start and its terminal outcome each produce one ordered `domain.StepEvent`; a `Runner` that never calls it behaves exactly as before |
| 12.2 The CLI shows a run's progress | `atlas -pack` prints one line per step, to stderr, as it starts and finishes — a long agent-backed pack is now distinguishable from a hang without `-otel` |
| 12.3 Inbound adapters cannot see each other | `TestInboundAdaptersStayIsolated` passes today and fails the moment a future inbound package imports a sibling or an outbound adapter directly |
| 12.4 One process, one store, at a time | A second `atlas` invocation against a locked `Store.Root` fails at startup naming the pid; a lock left by a process that is no longer running is reclaimed automatically |
| 12.5 Unattended invocation is proven safe | A test drives `PermissionPolicy` behind `termprompt` with stdin closed and confirms an unconfigured `ask` denies rather than hangs; the operational note — a scheduled pack's `-permission-rules` must cover every tool it calls — is written down where epic 12/13's deployment doc will read it |
| 12.6 The seam is sufficient for the web UI, by design review | This spec is checked against the web UI epic's planned handler shape (decode a request, call `runner.Run` with `WithProgress`, encode a response) and confirmed to need no new core method or type beyond what this epic ships |
| 12.7 The registry is built once | `TestBuildRegistryIsCalledOnce` passes; `run()` still constructs `buildRegistry`'s result exactly once regardless of how many surface branches follow it |

## Sequencing

This epic needs only Epic 4 (the ACP work already establishes the callback vocabulary this
design reuses, and `mcpserve` is the existing inbound-adapter precedent this design
extends). It needs nothing from epics 5–11 and touches no file any of them own.

**Proposed placement:** insert as **Epic 12**, renumbering the roadmap's current Epic 12
(the web UI) to **Epic 13**. Its content is unchanged; only its number moves, to make room
for the epic it depends on and that depends on it. This spec does not edit
`docs/plans/2026-09-17-roadmap.md` — that edit is left to whoever folds this proposal in.
Structurally this epic could land any time after Epic 4, since the DAG in the roadmap's own
Sequencing section does not otherwise require it; it is placed at 12 because Epic 13 (the
web UI) is its only consumer, and nothing is gained by proving the seam earlier than the
epic that needs it.

## Deliberately not in this epic

| Out | Why |
|---|---|
| The web UI itself | Roadmap epic 12, renumbered 13 here. This epic exists to make that epic's server a drop-in second surface, not to build it |
| An HTTP API on localhost, built separately from the web UI | It is the same surface as 13.1; building it now would be the duplication the brief warned against |
| A chat channel (phone-driven) | No named consumer in any spec in this repo; a public-facing bot process and a second auth surface for CV and salary data, unjustified against "someone will use it" |
| A scheduled/cron runner, as new code | Not a new surface — `termprompt`'s existing behaviour against closed stdin already makes the CLI surface safe unattended; the gap is a configuration discipline (story 12.5), not an adapter |
| Atlas as an inbound ACP agent | Duplicates the outbound doer's role (epic 4); no named ACP client wants to drive Atlas today. Revisit on a concrete client |
| Progressive-disclosure tool loading (search/describe/call bridge tools) | Nine tools, trivial schemas, no MCP client anywhere in the tree. Interface tax with no second case yet. Trigger: an MCP client, or measured schema-bloat against the context budget |
| Skills as self-written markdown procedures | Atlas's procedures are user-authored packs; nothing writes its own. Trigger: something in Atlas starts writing a procedure it intends to reuse |
| A bespoke ledger for self-written procedures | Git already gives versioning and rollback through `ports.Docs`; a second ledger would duplicate the record, refused the same way `../../CLAUDE.md`'s "Nerve as a store" row already refuses a second store for the corpus |
| A persisted, in-memory "run" identity for cross-connection polling | Nothing about an in-flight run is a business fact worth an eventual outcome check, unlike a `Judgement` or a `Decision`; left to whichever surface first needs to fan a running blueprint out to more than one listener |
| Distributed or multi-host coordination beyond a single lock file | One user, one machine, no multi-tenancy — the Forge's own refusal list already rules this out |
