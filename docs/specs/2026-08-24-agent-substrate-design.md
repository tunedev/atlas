# The harness, and the first pack

Atlas is a harness: a runtime that gives an agent a provider, a tool registry, a
permission model, memory, and a sandbox. A use case is a *pack* of configuration pointed
at that runtime. The job hunt is pack one, and it exists to demonstrate the harness.

Read alongside `2026-08-24-job-hunt-product-design.md`, which describes what pack one
does. This spec covers the runtime underneath it.

## The category

The reference is Claude Code. Claude Code is a harness; coding is what people point it at.
Its tools, skills, permission prompts, sandbox and session loop know nothing about any
particular codebase.

`github.com/MadsLorentzen/ai-job-search` — the project pack one is modelled on — has no
runtime at all. It is slash commands, skills and a CSV. **Claude Code was its harness.**
Atlas replaces that harness and the job logic is ported onto it as configuration.

## The bar

> **A new pack needs no Go.**

Everything a use case knows — its prompts, its endpoints, its field mappings, its order
of operations, its rules — lives in configuration. Nothing in the Go tree knows what a
job posting is.

This is testable, and increment 1 tests it: a second, trivial, unrelated pack must run
with zero Go changes. If it cannot, this is an application with a config file.

## The boundary

| Harness (Go) | Pack (config) |
|---|---|
| Tool registry: name to implementation | Which tools a blueprint calls, and with what |
| Generic tools | Prompts, endpoints, field mappings |
| Blueprint runner | The blueprints themselves |
| Template and path resolution | The paths and templates used |
| Permission engine | The allow / ask / deny rules |
| Provider routing, memory, sandbox | Which model, which memory scope |
| Observability | Nothing |

**Violated when:** adding a use case, a job board, or a document type means editing Go.

## Tools

A tool is a named, typed unit of capability. The harness ships generic ones; a pack calls
them by name.

| Tool | Does |
|---|---|
| `http.request` | Fetch JSON or text from a URL |
| `model.complete` | System plus user message to text, optionally JSON-shaped |

Later, on evidence: `convert.document`, `sandbox.exec`, `browser.fetch`, `memory.recall`.

A tool that knows about job postings is not a tool, it is pack logic in the wrong place.

## Blueprints

A blueprint is an ordered list of steps. Each step names a tool and supplies its config.

```yaml
name: job-hunt
steps:
  - id: fetch
    tool: http.request
    with:
      url: "{{ .vars.greenhouse }}/v1/boards/{{ .vars.board }}/jobs?content=true"
      select: jobs.0
  - id: score
    tool: model.complete
    with:
      system: "You judge whether a posting suits a candidate. Reply JSON only."
      user: "Candidate: {{ .vars.profile }}\n\nPosting: {{ .steps.fetch.title }}"
      expect: json
```

Adding a step is a list entry. Adding a use case is a file.

## Data flow

Tools return structured data. Steps reference earlier output by path, through Go
templates rendered against accumulated state. A `select:` narrows a response before it is
stored.

**Deliberately absent: conditionals, loops, and an expression language.**

A blueprint is a sequence, not a program. Adding CEL or similar would mean designing for
needs no pack has demonstrated, and the cheapest way to learn what expressiveness is
actually required is to run out of it with a real pack in hand. When this runs out, the
gap will name itself.

## Permissions

The harness owns an allow / ask / deny model over tool invocations. This is Claude Code's
permission model, and it belongs to the runtime, not to any pack.

| Outcome | Behaviour |
|---|---|
| allow | The tool runs |
| ask | The user is shown the call and decides |
| deny | The tool never runs |

Default for an unmatched invocation is ask. Deny is evaluated before allow.

Pack one's auto-apply rules are this, configured. That they looked like a job-hunt feature
was the harness showing through a use case.

## Surfaces

Runner first, chat later.

The blueprint runner is the engine: packs run on a schedule or on demand, and the web UI
shows results and collects decisions. A conversational loop is a later surface over the
same tools and the same permission model — chat becomes one more way to invoke what packs
already expose, not a second implementation of them.

## Adopt or rebuild

| Component | Status | Decision |
|---|---|---|
| ADK Go (`google.golang.org/adk/v2`) | Public | Adopt in 1b, for agent execution when chat arrives |
| `agent-sandbox.sigs.k8s.io` | Public, Kubernetes SIG | Adopt. Sandbox runtime, proven on kind |
| `synapse-gateway` | Public, AGPL | Adopt. Provider routing behind `model.complete` |
| `<private employer memory system>` | Private, employer | Pattern only. Memory and retrieval is rebuilt |
| `<private employer sandbox>` | Private, employer | Pattern only. Informs how the public sandbox is used |

Nothing private is vendored, forked, or called.

## Observability

Every tool invocation emits a span carrying the tool name, the step id, and the blueprint.
`synapse-gateway` contributes `gen_ai.*` spans, so token cost and model latency land in
the same trace as the step that caused them.

A pack author cannot read Go. Traces are how they find out which step was slow, which was
wrong, and what it cost — so this is the pack author's debugger, not operator polish.

## Increment 1: the tracer

| In | Out |
|---|---|
| Tool registry with two generic tools | Any further tool |
| Blueprint format, loader, runner | Runtime-authored packs, a UI |
| Templates and `select:` | Conditionals, loops, expressions |
| Two packs: job hunt, and one trivial unrelated one | Permissions, sandbox, memory |
| A span per tool invocation | Dashboards, alerting |
| Whatever storage works | Persistence |

**Done when:** the job pack fetches a real posting, scores it and drafts a package, fully
traced — **and a second unrelated pack runs with no Go changes.**

The second pack is the whole point. Without it the increment proves a Go abstraction and
says nothing about whether the harness exists.

## Order after the tracer

1. Permissions over tool invocations, which pack one already needs.
2. More tools, each earning its place from a pack that wanted it.
3. Sandbox, when packs stop being first-party.
4. Memory and retrieval, shaped by what earlier packs turned out to need.
5. Chat as a second surface over the same tools.

## Open questions

- Where packs live. Files on disk are enough for increment 1; runtime-addable packs
  eventually need storage and an authoring surface.
- Whether `expect: json` belongs on `model.complete` or is a separate parsing step. The
  first is fewer moving parts; the second keeps the tool generic.
- What memory actually needs, which earlier increments are meant to reveal.
