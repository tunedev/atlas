# Atlas

A harness for domain-specific AI agents.

An agent needs a provider, a set of tools, a permission model, somewhere to keep what it
learns, and a place to run code that cannot reach the rest of your machine. Atlas is that
runtime. A use case is a *pack* of configuration pointed at it.

## The bar

> **A new pack needs no Go.**

Everything a use case knows — its prompts, its endpoints, its field mappings, its order of
operations, its rules — lives in configuration. Nothing in the Go tree knows what a job
posting is.

This is testable, and it is the binding test: a second, unrelated pack must run with zero
Go changes. If it cannot, this is an application with a config file.

The reference is Claude Code. Claude Code is a harness; coding is what people point it at.
Its tools, permission prompts, sandbox and session loop know nothing about any particular
codebase.

## Where the boundary falls

| Harness (Go) | Pack (config) |
|---|---|
| Tool registry: name to implementation | Which tools a blueprint calls, and with what |
| Generic tools | Prompts, endpoints, field mappings |
| Blueprint runner | The blueprints themselves |
| Permission engine | The allow / ask / deny / suspend rules |
| Provider routing, memory, sandbox | Which model, which memory scope |
| Observability | Nothing |

Adding a use case, a data source, or a document type must not mean editing Go.

## The claim, and what backs it

An agent may be non-deterministic because the platform around it is not. That trade is
only honest if the deterministic half is real, so three things are built rather than
asserted:

**Isolation.** A container per run closes the process boundary; a scoped filesystem closes
the filesystem boundary. They are named separately because they fail separately. The mount
runs on the host and is bind-mounted in, so the container needs no `CAP_SYS_ADMIN` — which
would hand back most of what the container was there to take away.

**Authorization.** The sandbox holds no credentials. Calls to external systems leave
through a broker that resolves what this workspace is entitled to and injects identity on
the way out. Identity is bound to the execution environment, so a blueprint cannot name
one and a model cannot ask for one. Anything arriving through the prompt is input, never
authorization.

**Resumable execution.** The permission engine has a fourth outcome besides allow, ask and
deny: **suspend**. A pack that needs an integration the user has not authorized stops, the
run persists, the user completes the authorization, and the run continues from the step
that stopped. No step runs twice.

## Status

**Design, no code.** Four specs, one implementation plan, nothing built.

`internal/` does not exist yet. This section will say something different when that changes,
and it will not overstate what is there — see `docs/specs/` for what is actually decided.

| Document | What it settles |
|---|---|
| [`agent-substrate-design`](docs/specs/2026-08-24-agent-substrate-design.md) | The harness, packs, tools, blueprints, the boundary |
| [`increment-1-boundaries-design`](docs/specs/2026-09-09-increment-1-boundaries-design.md) | The contract, the ports, the sandbox, the broker, suspend |
| [`job-hunt-product-design`](docs/specs/2026-08-24-job-hunt-product-design.md) | Pack one: what it does for whom, and what it refuses |
| [`increment-1-harness`](docs/plans/2026-08-25-increment-1-harness.md) | The task-by-task build |

## Pack one

A job hunt. It finds roles, judges whether they are worth your time, drafts the
application, and tracks what happens next.

It **drafts and does not submit.** Submitting is per-portal browser automation with a much
larger trust cost, and drafting is where the value is. The condition for revisiting that is
written down rather than left implicit: when the drafting loop is trusted and the manual
submit step is the remaining friction.

Pack one exists to demonstrate the harness. A tool that knows what a job posting is is not
a tool, it is pack logic in the wrong place.

## Transport

Service-to-service RPC is gRPC, and must stay trivially callable over HTTP/JSON. One
`connect-go` handler generated from one `.proto` serves both, so a browser sending JSON
and a runner sending gRPC reach the same method. The `.proto` is the versioned contract.

## Context

Atlas is one repo of the Forge, a workshop for learning
infrastructure and agentic engineering by building it. The engineering tenets that govern
this repo live at the Forge level: hexagonal architecture, the five cloud native
attributes, stability patterns, and total configurability.

Everything runs on a laptop, for free. If it does not work on a plane, it is a bug.
