# Agent substrate and the first increment

The job hunt slice is the first consumer of an agent substrate, not a standalone app. The
substrate is the finish line; the slice is how it gets built with a live consumer at every
step.

Read alongside `2026-08-24-job-hunt-product-design.md`, which defines what the product does.
This spec covers what runs underneath it.

## The finish line

- An agentic path from discovery to a drafted application, running unattended.
- Composable steps, so a path is configuration rather than code.
- A sandbox, so user-authored steps cannot reach anything they should not.
- Memory and retrieval, so the system improves rather than repeating itself.
- Observable throughout, because iterative refinement needs evidence and not intuition.

The job view is a presentation over that substrate, and its components are meant to be
reusable for a different idea.

## The thing that sentence must not become

"The job view is just a presentation of the plumbing" is one step from building plumbing
with no consumer, which Tenet 7 refuses and the calibration rule exists to prevent.

The resolution is order, not scope. Build a tracer through both at once: one real posting,
end to end, on the real substrate. Every substrate piece has a live consumer from the day
it exists. Then thicken.

## Adopt or rebuild

| Component | Status | Decision |
|---|---|---|
| ADK Go (`google.golang.org/adk/v2`) | Public | Adopt. Agent execution, tools, multi-agent |
| `agent-sandbox.sigs.k8s.io` | Public, Kubernetes SIG | Adopt. Sandbox runtime, proven on kind |
| `synapse-gateway` | Public, AGPL | Adopt. Model routing (AT-2, AT-88) |
| `<private employer memory system>` | Private, employer | Pattern only. The memory and retrieval layer is rebuilt |
| `<private employer sandbox>` | Private, employer | Pattern only. Its ideas inform how the public sandbox is used |

ADK ships an official Go SDK, so it does not trip the refusal of polyglot two-tier
services. The Forge stays Go end to end.

Nothing private is vendored, forked, or called.

## Blueprints: composable steps, no canvas

A blueprint is a declarative description of a path through typed steps. Adding one is
configuration. This is the architectural thesis at the capability altitude, and the third
registry after providers and portals.

What is deliberately not built: a visual canvas.

n8n's flexibility is worth learning from, but n8n's actual value is its integration
library, not its canvas. A canvas without integrations is a more expensive way to call a
function, and it competes with the web UI for the same evenings. The step model comes
first; a canvas can be added over it later without redesign, and the reverse is not true.

## Sandboxing

User-authored steps execute under a boundary. The `<private employer sandbox>` pattern is the one to
learn from: untrusted code reaches the outside world only through a local broker, and
never holds a credential, a provider key, or a tenant identity in its own process.

That pattern matters more here than it does in a single-user tool. Atlas has real users,
and `../../../CLAUDE.md` now makes sandboxing of user-authored code a correctness
requirement rather than later hardening.

## Observability is the point, not the polish

Iterative refinement requires knowing which step was slow, which was wrong, and what it
cost. Every step emits a span. `synapse-gateway` contributes `gen_ai.*` spans for the
model calls, so token cost and model latency land in the same trace as the request that
caused them.

A step whose behaviour cannot be seen cannot be improved, only guessed at, and the whole
premise of the substrate is that it gets refined.

## Increment 1: the tracer

One posting, all the way through, on the real substrate.

| In | Out |
|---|---|
| One portal, one query | Every portal, the portal registry |
| One blueprint, hardcoded path | User-authored blueprints |
| Score and draft for one posting | Batch scheduling, apply rules |
| Sandbox running one trusted step | Untrusted user code |
| Traces for every step | Dashboards, alerting |
| Whatever storage works | The memory and retrieval layer |

Done when: a real posting is fetched, scored with reasons, and a package drafted, by a
blueprint running on ADK, with a trace covering every step.

Deliberately useless as a product. It is the multimeter for the substrate, in the same way
the `hello` workload is the multimeter for the platform, and for the same reason: it makes
every later step a drop-in rather than a first attempt.

## Order after the tracer

1. Thicken discovery: more portals, the portal registry as a real registry.
2. Thicken the path: scoring, tailoring, the apply loop as steps in a blueprint.
3. User-authored blueprints and the sandbox boundary that makes them safe.
4. Memory and retrieval over the substrate, which is where the pattern-derived ideas land.

## Open questions

- Where blueprints are declared. Proto, YAML, or database rows. Proto keeps one contract
  and versioning; database rows are what runtime-addable eventually requires.
- Whether the sandbox is needed for increment 1. Trusted first-party steps may not need
  it, and adding it later is a boundary change rather than a rewrite.
- What the memory layer actually needs, which the earlier increments are supposed to
  reveal rather than assume.
