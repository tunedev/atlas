# Surfaces

One core, thin inbound adapters. Per the Forge tenet "the narrow waist": every addition to
`internal/core` is paid for by every caller, on every surface, forever, so a new surface must
be config plus a thin adapter, never a core change.

## The seam

Inside the module, an inbound adapter (`internal/adapters/inbound/<name>`) may import only
`internal/core/...` and its own subtree. The standard library and third-party packages are
allowed. Everything else in the module, present or future, is forbidden: a sibling surface, an
outbound adapter, and composition-root infrastructure (`internal/config`, `internal/telemetry`,
`internal/pidlock`), all wired once at `cmd/atlas`. Four tests enforce this on every build:

- `TestCoreImportsNoAdapters` — the core never imports an adapter.
- `TestInboundAdaptersStayIsolated` — the allowlist above, checked for every package under
  `inbound/`, present or future.
- `TestEveryPackageHasAKnownHome` — every package in the module sits in a known home:
  `internal/core/...`, `internal/adapters/inbound/<x>/...`, `internal/adapters/outbound/<x>/...`,
  or exactly one of `internal/arch`, `internal/config`, `internal/telemetry`,
  `internal/pidlock`, `cmd/atlas`. A surface placed anywhere else fails until a home is added
  deliberately.
- `TestBuildRegistryIsCalledOnce` — `buildRegistry` is called once across all of package
  `main`, so a surface cannot stand up a second copy of the tool registry.

## Progress

`Runner.WithProgress(func(domain.StepEvent))` returns a Runner whose callback is invoked as each
step starts and finishes. It runs synchronously on `Run`'s own goroutine, in step order: `StepStarted`, then
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
holder's. A lock holding the process's own pid is stale too: a container restarted as pid 1
finds its own old lock. The CLI prints `atlas: reclaimed stale lock left by pid N` when it
takes one over. The refusal reads `pidlock: <path> is held by running process N; if N is not
atlas, delete <path>`.

`Acquire` writes the pid to a temp file in the same directory and hard-links it into place, so
the lock never exists without its pid and, when no lock exists, exactly one racing creator
wins. An empty or garbled lock can therefore only come from a crash. Reclaiming a stale lock
removes it and tries creation again, two attempts in all. Two processes reclaiming the same
stale lock at once can both hold it: one removes the lock the other just created. This race
is documented, not solved.

The lock covers `Store.Root` only. `Store.IndexPath` defaults outside it
(`~/.atlas/index.db`), so two processes on different roots sharing the default index are not
excluded.

## How to add a surface

1. A new package under `internal/adapters/inbound/<name>`, importing only core, the standard
   library and third-party packages.
2. A new optional branch in `run()`, gated on config and reusing `registry` and `cfg`.
3. A Runner may be shared: `WithTracer` and `WithProgress` return a copy and leave the
   receiver unchanged. Each request derives its own with `runner.WithProgress(...)`.
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
