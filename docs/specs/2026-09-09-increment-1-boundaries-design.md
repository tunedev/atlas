# Increment 1 — the harness, with its boundaries

Amends `2026-08-24-agent-substrate-design.md`. That spec defers permissions, the sandbox
and memory until after the tracer. This increment pulls three of them forward: the
sandbox, the credential broker, and one permission outcome. Memory stays deferred.

## Why they move

The harness makes one claim: an agent may be non-deterministic because the platform
around it is not. Isolation, authorization and resumable execution are the platform's
side of that bargain.

A tracer that loads a blueprint, calls two tools and prints the result demonstrates the
tool registry. It does not demonstrate the claim, because nothing in it can fail in the
way the claim is about. The three additions are the smallest set that makes the claim
testable rather than asserted.

Memory does not move. `2026-08-24-agent-substrate-design.md` is right that memory should
be shaped by what earlier packs turn out to need, and no pack has run yet.

## What "done" means

Four things, each with pasted output:

1. The job pack runs end to end from YAML, fully traced.
2. A second, unrelated pack runs with no Go changes. This remains the binding test.
3. A pack that needs an unauthorized integration suspends, is authorized out of band, and
   resumes from the step that stopped — completing the run.
4. A process inside the sandbox cannot address a path outside its workspace, asserted per
   attempt in an escape suite.

## The contract

`proto/atlas/v1/atlas.proto`, served by one `connect-go` handler. The web UI will speak
HTTP/JSON to it; a runner speaks gRPC. One handler, per Tenet 1.

| RPC | Purpose |
|---|---|
| `ListPacks` | What this deployment can run |
| `RunBlueprint` | Start a run; returns a run id |
| `GetRun` | Run state, step outputs, current step |
| `ResumeRun` | Continue a suspended run |

`ResumeRun` is in v1 deliberately. Suspend and resume are not a feature layered onto a
runner; they are a property of the run's state machine. A contract without them forces
every client to change when they arrive.

Ports never speak protobuf. Generated types stop at the adapter boundary.

## Ports

Five, each with a nameable second implementation.

| Port | First implementation | Second |
|---|---|---|
| `Tool` | the shipped generic tools | every later tool |
| `Provider` | Ollama | Claude, Gemini, OpenAI |
| `Broker` | Pipedream Connect | direct per-user API keys |
| `Store` | files on disk | nerve |
| `Runs` | Postgres | nerve, once the seam exists |

`Store` starting on disk is what lets this increment proceed independently of nerve. The
nerve seam becomes a config change, not a rewrite.

`Runs` is Postgres from the start rather than in-memory. A run that suspends must survive
the process that was running it, or A2H is a demo rather than a mechanism.

## The run state machine

```
pending -> running -> completed
                   -> failed
                   -> suspended -> running   (via ResumeRun)
```

A suspended run persists: the blueprint, accumulated step state, the step index it
stopped at, and what it is waiting for. Resuming re-enters at that index with that state.
No step is re-executed.

## The sandbox

A container per run closes the process boundary. The workspace's scoped VFS closes the
filesystem boundary. These are separate boundaries and are named separately, because they
fail separately.

**The mount runs on the host, not in the container.** FUSE inside a container needs
`/dev/fuse` plus `CAP_SYS_ADMIN`, and `CAP_SYS_ADMIN` gives back much of what the
container was there to take away. So the host mounts the workspace's scoped VFS and
bind-mounts the resulting directory into the container. The container receives no extra
capability, and the agent sees an ordinary directory tree.

What this closes:

| Boundary | Closed by | Asserted by |
|---|---|---|
| Filesystem | scoped VFS, path traversal and symlink resolution inside the scope | escape suite: traversal, symlink, handle reuse |
| Process | container: no host PID, no host network, dropped capabilities, read-only root outside the mount | a process inside cannot see or signal a host process |
| Credentials | the container is never given any | no token reachable from inside |

What this does not close, recorded rather than omitted: kernel-surface escapes from a
shared-kernel container. The refusal of gVisor in nerve's E11.6 stands, and this is the
cost of that refusal.

## The broker

The sandbox holds no provider credentials, no OAuth tokens, no integration secrets.

A tool call needing an external system goes out through the `Broker` port. The Pipedream
Connect adapter resolves which integrations this workspace is entitled to and injects
identity as the request leaves. The calling code never holds a token.

Identity is bound to the execution environment: the container is created for one
workspace, and the broker resolves entitlements from that binding. A blueprint cannot name
an identity, and a model cannot ask for one. Anything arriving through the prompt is
input, never authorization.

## A2H

The permission engine gains a fourth outcome.

| Outcome | Behaviour |
|---|---|
| allow | the tool runs |
| ask | the user is shown the call and decides |
| deny | the tool never runs |
| **suspend** | the run persists and stops; the user is given something to complete |

`suspend` is what an unauthorized integration produces. The run stops, the user completes
the Pipedream Connect authorization, `ResumeRun` continues from the step that stopped with
the capability now available.

Deny is still evaluated before allow. Default for an unmatched invocation is still ask.

## Tools

| Tool | Does |
|---|---|
| `http.request` | fetch JSON or text |
| `model.complete` | system plus user message to text, optionally JSON-shaped |
| `browser.fetch` | render a page and return its text |
| `sandbox.exec` | run a command inside the run's container |

`browser.fetch` is read-only. It renders and reads. It does not fill a field, click a
control, or submit a form. The v1 refusal of submitting applications in
`2026-08-24-job-hunt-product-design.md` stands unchanged; this tool exists so discovery
can reach sources that publish no API.

`sandbox.exec` is what makes the sandbox load-bearing rather than decorative. It is also
the tool the permission engine most needs, so its rules are part of this increment.

A tool that knows what a job posting is is not a tool. That constraint is unchanged and
still binding.

## Observability

A span per tool invocation, carrying tool name, step id, blueprint, run id. Suspension and
resumption are span events on the run, so the gap between them is visible as a gap rather
than as a missing trace.

## Testing

The escape suite and the suspend/resume path are the two places where a passing test means
something a reader would not otherwise believe. Both assert behaviour end to end against
the real container and the real store.

The second-pack test is unchanged and remains the increment's binding constraint: if a
second unrelated pack needs Go, the harness does not exist.

Tests that assert a mock was called are not written.

## Deliberately out

| Out | Why | What changes the answer |
|---|---|---|
| Form filling and submission | the v1 refusal, and its stated reversal condition is unmet | the drafting loop is trusted and manual submit is the remaining friction |
| Memory, and nerve as `Store` | no pack has yet shown what memory it needs | a pack runs out of what disk gives it |
| Web UI | runner first | there is something worth looking at |
| Chat | a later surface over the same tools and permissions | the runner is not the shape a user wants |
| Conditionals and loops in blueprints | no pack has run out of a sequence yet | one does, and the gap names itself |

## Open questions

- The nerve seam. Nerve serves a read-only operator surface today. Whether atlas reaches
  it over Connect or embeds it as a library is undecided and does not block this
  increment, because `Store` starts on disk.
- Whether `expect: json` belongs on `model.complete` or is a separate parsing step.
  Carried forward from the substrate spec, still unresolved.
- Container lifecycle on a suspended run. A run may be suspended for days. Whether the
  container is destroyed and recreated on resume, or held, is a cost and a correctness
  question, and the answer depends on whether any step leaves state outside the mount.
