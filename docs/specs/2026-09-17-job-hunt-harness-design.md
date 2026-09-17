# The job-hunt harness

Supersedes the scope of `2026-08-24-job-hunt-product-design.md` where the two disagree.
The harness design in `2026-08-24-agent-substrate-design.md` stands unchanged: this spec
is what gets pointed at it, and which ports that requires.

## What it is

Claude Code for job applicants.

A job hunt is a numbers game whose losing move is to play it as one. Volume without
tailoring gets filtered; tailoring by hand caps you at three applications a day. The
product is the thing that removes that trade: every application is specific, and there are
many of them, because the specificity is produced rather than typed.

The user brings a CV, an evidence corpus and a set of deal-breakers. The system finds
roles, judges them, drafts a package per role that traces every claim back to evidence,
and hands it over. The user sends it.

## What already exists

This is not a new project. `atlas` is 17 commits of harness:

| Built | Where |
|---|---|
| Tool registry, `Tool` and `Registry` ports | `internal/core/ports` |
| Blueprint runner: resolve, render, execute, narrow, store, span | `internal/core/app` |
| Template rendering and a dotted-path selector | `internal/core/app` |
| Layered config: defaults, file, env, flags; validated at startup | `internal/config` |
| YAML pack loader | `internal/adapters/inbound/packfile` |
| `http.request` and `model.complete`, both bounded and timed | `internal/adapters/outbound/tools` |
| OTLP trace pipeline, a span per tool invocation | `internal/telemetry` |
| Architecture guards: dependency direction, vocabulary, no net/http in core | `internal/arch` |

The mapping to the reference is close enough to be the point: a **blueprint** is a slash
command, a **pack** is a skill, the **tool registry** is the tool list, and the
allow/ask/deny rule engine is the permission prompt. What the reference does for code, this
does for a job hunt.

`cmd/atlas/main.go` and two packs exist but have never been run. That is increment 0.

## The spine

Every capability enters through a port the core owns. A port exists here only where a
second implementation is nameable, and each row below names one.

| Port | First | Second |
|---|---|---|
| `Tool` | the shipped generic tools | every later tool |
| `Provider` | Ollama, local | Gemini free tier, Groq, a user's own key |
| `Judge` | local constrained decoding | TypeSafe |
| `Source` | structured job APIs | a Colly crawler |
| `Store` | files on disk | nerve |
| `Runs` | Postgres | nerve |

`Store` arrives with increment 4, which is the first thing needing durable bytes. `Runs`
arrives with increment 8, which is the first thing needing a run to outlive its process.

Ports never speak an adapter's vocabulary. Generated types, `*sql.Tx`, `*colly.Collector`
and a TypeSafe response all stop at the adapter boundary.

## Judgement is typed, not prose

The system asks a model questions whose answers are values, never paragraphs:

- **noul** — probability of yes.
- **choice** — one option from a set, with the distribution over all of them.
- **score** — a probability-weighted position on ordered levels.

All questions for a decision go in **one call**, including speculative ones, and code
decides afterwards which answers were relevant. Asking "is this a stretch role" and "which
deal-breaker tripped" and "how senior is this posting" together costs one round trip;
asking them in sequence costs three and invites the model to contradict itself.

This is the pattern TypeSafe sells and it is worth having. It is **not** worth being
unable to work offline for, and it is **not** worth shipping a user's salary expectations
to a third party by default.

So: `Judge` is a core port. The default adapter runs locally against the `Provider`,
getting typed answers by constrained decoding rather than by asking a model to promise it
will return JSON. The TypeSafe adapter is opt-in per workspace, and the moment a workspace
enables it the UI states plainly that content leaves the machine.

**Violated when:** a core package imports a TypeSafe type, or the plane test fails because
judgement needs a network.

## Inference

Ollama first, because it is installed, working, and already serving a model on this
machine. `Provider` exists so that is a configuration line rather than a commitment.

vLLM is deferred, not refused. Its advantage is continuous batching, which matters exactly
when the workload becomes "score two hundred postings, six questions each". That is a real
future workload, so the decision is: **adopt vLLM when batch scoring is measurably the
bottleneck, and not before.** The GPU here is 8 GB, which bounds it to a 7B at four bits
or a 3-4B at full precision; that ceiling is a fact to design within, not a surprise to
discover.

Hosted providers are the user's own account and the user's own bill. A subscription is not
an API: ChatGPT Plus and its equivalents are chat entitlements and cannot lawfully be
driven from code. What a user can bring is an API key, or a free tier.

## Discovery, and the data work

Discovery has two halves and one port.

**Structured sources** are job APIs that return JSON: Greenhouse, Lever, Ashby, Workable,
RemoteOK. These are cheap, stable, and where the volume starts.

**Crawled sources** are everything that publishes no API: company career pages, aggregator
listings, a watchlist. This is Colly, and it is the first genuinely data-engineering-shaped
part of the system: rate limiting per host, `robots.txt`, retry and backoff, incremental
recrawl, and extraction rules that go stale silently when a page changes.

Both are `Source` implementations. A failing source degrades discovery to the remaining
sources and never fails the run — the ordered-provider-chain-with-breakers pattern the
Forge tenets already require for outbound calls.

Deduplication is across sources, not within one. The same role appears on a board, an
aggregator and the company's own page, and counting it three times corrupts every number
downstream.

**Scraping conduct is a correctness requirement, not etiquette.** Identify the crawler,
honour `robots.txt`, rate limit per host, cache so a rerun is cheap, and never authenticate
to a site in order to scrape it. A crawler that gets the user's account banned has
subtracted value from the product it exists to serve.

## The corpus is the long game

Every application produces data: what was applied to, what was said, what evidence was
cited, what came back, and how long it took. That corpus is what later turns into

- which requirements the user repeatedly fails to meet, which is upskilling guidance
  grounded in their own rejections rather than in generic advice;
- which claims correlate with replies, which is how tailoring improves;
- what a role is actually worth, from postings rather than from surveys.

None of it is built in v1. All of it is why the schema is designed as an append-only record
of decisions rather than as a table of current state. **What is recorded now determines
what can be learned later**, and that is the one thing that cannot be retrofitted.

## Increments

One unknown each, as the tenets require.

| # | Increment | The unknown it adds | Done when |
|---|---|---|---|
| **0** | Finish the tracer | none - it is written | Both packs run from YAML; the second needed no Go |
| **1** | `Provider` port | provider routing | A pack runs on Ollama and on a hosted key with only config changed |
| **2** | `Judge` port, local | constrained decoding | Six typed questions return calibrated values in one call, offline |
| **3** | `Source` port, structured | outbound resilience | Roles arrive from three APIs, deduped, one source down does not fail the run |
| **4** | Profile | document ingest | A CV becomes structured history the user can correct |
| **5** | Fit judgement | nothing new - 2 plus 4 | Apply / stretch / skip with the deal-breaker named |
| **6** | Crawled sources | Colly, politeness, staleness | A career page with no API yields roles, politely, repeatably |
| **7** | Tailoring | evidence tracing | Every claim traces to the corpus; an unsupported claim shows as a gap |
| **8** | Apply loop | the rule engine, `Runs` persistence | Package drafted; allow/ask/deny; drafted and sent are distinct states, and a run survives the process |
| **9** | Tracking | none - `Store` and `Runs` already exist | Pipeline state, what went quiet, outcomes |
| **10** | Web UI | Svelte | The product is usable by someone who will not touch a terminal |
| **11** | Sandbox and broker | containers, Pipedream | A pack needing an unauthorised integration suspends and resumes |

Increment 10 is where Svelte enters. Deliberately late: the repo's own rule is that clients
come once there is an API worth consuming, and increments 0-9 are what make one.

Increment 11 is the A2H story, and it keeps its earlier design.

## Cost

The constraint is that a month bills nothing.

| Thing | Cost |
|---|---|
| Ollama, Postgres, RabbitMQ, the crawler, Colly | free, local |
| Gemini and Groq free tiers | free, rate limited |
| A user's own API key | the user's bill, by design |
| TypeSafe | unknown - no published price. Opt-in, never a default |
| Hosted prod | inside a free tier, reduced stack |

**Violated when:** a month bills anything, or a feature only works on a paid tier.

## Deliberately out

| Out | Why | What changes it |
|---|---|---|
| Submitting applications | Per-portal automation, much larger trust cost, and the drafting loop is the value | The drafting loop is trusted and manual submit is the remaining friction |
| Driving a browser to fill forms | Same, plus it is what gets accounts banned | As above |
| Scraping behind a login | Contractual and account risk taken on the user's behalf | Nothing currently foreseen |
| Multi-tenancy | One tenant; a workspace is a subtree | Not expected |
| vLLM | Deferred on evidence, not refused | Batch scoring measurably bottlenecks |
| Nerve as `Store` | No pack has yet shown what memory it needs | A pack runs out of what disk gives it |

## Open questions

- **Calibration, and what it may cost.** Two distinct problems hide here.
  *Shape* is easy: Ollama can constrain output to a JSON schema, so a `choice` comes back
  as one of the allowed options.
  *Probability* is not. A `noul` is only meaningful if it is a real likelihood, which needs
  token logprobs. vLLM exposes those; Ollama's support is thin. So the honest risk is that
  increment 2 pulls vLLM forward from "deferred on evidence" to "required", and the
  evidence will be this, not batch throughput. Increment 2 must answer it before increment
  5 depends on it.
  Separately, nothing yet checks a probability against an outcome. Until applications have
  results to compare against, a calibrated-looking number is still an assertion.
- Tailoring output format. A careers page will not accept markdown, and a PDF toolchain is
  heavy. Undecided, and increment 7 forces it.
- Where packs live once users can author them. Files on disk are enough until they are not.
- Whether `Judge` and `Provider` stay separate ports. They might collapse once the local
  adapter exists, and that will be obvious then rather than now.
