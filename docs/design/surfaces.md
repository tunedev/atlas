# Surfaces

One core, thin inbound adapters. Per the Forge tenet "the narrow waist": every addition to
`internal/core` is paid for by every caller, on every surface, forever, so a new surface must
be config plus a thin adapter, never a core change.

## The seam

An inbound adapter (`internal/adapters/inbound/<name>`) may import the core, the standard
library, and third-party packages. It may not import a sibling surface, an outbound adapter,
or composition-root infrastructure (`internal/config`, `internal/telemetry`) — those are wired
once, at `cmd/atlas`. Three tests enforce this on every build:

- `TestCoreImportsNoAdapters` — the core never imports an adapter.
- `TestInboundAdaptersStayIsolated` — no inbound package reaches a sibling, an outbound
  adapter, or config/telemetry; checked automatically for every package under `inbound/`,
  present or future.
- `TestBuildRegistryIsCalledOnce` — `buildRegistry` runs once per process, so a surface cannot
  stand up a second copy of the tool registry.

## Progress

`Runner.WithProgress(func(domain.StepEvent))` sets a callback invoked as each step starts and
finishes. It runs synchronously on `Run`'s own goroutine, in step order: `StepStarted`, then
`StepDone` or `StepFailed`. The CLI's callback prints one line per event to stderr
(`progressPrinter`); a different surface supplies its own callback and gets its own notion of
progress with no core change.

## One process per store

`pidlock.Acquire` takes `<store-root>/.atlas.lock` before anything opens the store. A live
holder is refused by name: the error names the holder's pid. A holder that has exited is
reclaimed: its stale lock is removed and retaken. Liveness per OS:

- unix: `kill(pid, 0)` — `EPERM` still means alive (owned by another user).
- windows: `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` then `GetExitCodeProcess`; code
  259 (`STILL_ACTIVE`) means alive, and access denied also means alive.

A lock file whose content is not a positive pid is stale and taken over, same as a dead
holder's. The reclaim race (R5) — two processes both find the lock stale and both try to
retake it — is documented, not solved: `Acquire` retries creation twice before giving up.

## How to add a surface

1. A new package under `internal/adapters/inbound/<name>`, importing only core, the standard
   library and third-party packages.
2. A new optional branch in `run()`, gated on config and reusing `registry` and `cfg`.
3. A Runner per request, with that surface's own `WithProgress` — never one `*app.Runner`
   shared across requests, since setting its progress callback per request is a data race.
   Build with `app.NewRunner(registry).WithTracer(...).WithProgress(...)` per request; both are
   cheap, and nothing in the core changes for this.
4. Bind loopback only, with a per-run token compared in constant time, as `mcpserve` does.
5. Evidence that `git diff --stat -- internal/core` is empty.

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
