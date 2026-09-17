# The job-hunt harness

Supersedes `2026-08-24-job-hunt-product-design.md` wherever the two disagree. The harness
design in `2026-08-24-agent-substrate-design.md` stands: this spec is what gets pointed at
it, and which ports that requires.

## What it is

Claude Code for job applicants.

A job hunt is a numbers game whose losing move is to play it as one. Volume without
tailoring gets filtered; tailoring by hand caps you at three applications a day. The
product removes that trade: every application is specific, and there are many of them,
because the specificity is produced rather than typed.

**It runs on the user's machine.** Their CV, their salary expectations, their rejections
and their API keys stay there. The only thing that is shared is the one thing that was
already public: job postings.

## What already exists

`atlas` is 17 commits of harness: tool registry and `Tool`/`Registry` ports, a blueprint
runner with template rendering and a path selector, layered validated config, a YAML pack
loader, two bounded and timed generic tools, an OTLP trace pipeline, and architecture
guards for dependency direction, vocabulary, and keeping the network stack out of the core.

The mapping to the reference is the point: a **blueprint** is a slash command, a **pack** is
a skill, the **tool registry** is the tool list, and the allow/ask/deny engine is the
permission prompt. What the reference does for code, this does for a job hunt.

`cmd/atlas/main.go` and two packs exist but have never been run. That is increment 0.

## Topology

Two pieces, and only one of them is ours to operate.

**The app** runs on the user's machine: a single Go binary, a local web UI, a git
repository of their own work, and a SQLite index. No account, no sign-in, no server, no
container. Deleting the directory deletes the product.

**The postings feed** is a public git repository of normalised job postings, updated on a
schedule by a GitHub Action. GitHub Actions is free for public repositories on standard
runners, so the feed costs nothing to run and has no database, no per-user state, and no
personal data in it — postings are public information and nothing about a user ever
reaches it. The app pulls it like any other git remote.

This is what buys back unattended discovery without buying a backend. The crawl runs
whether the laptop is open or not; the app collects when it opens. A user who distrusts the
feed can fork it, run their own, or turn it off and crawl locally.

**Violated when:** the product requires a service we operate, or anything about a user
leaves their machine without them choosing it.

### This amends a Forge constraint

`../../CLAUDE.md` currently reads: *"Atlas has real users, so prod is always-on and
reachable when the laptop is shut, inside a cloud free tier."* That was written when Atlas
was going to be a hosted multi-user web platform. It no longer holds, and the replacement
is stricter rather than looser: **there is no prod.** The always-on requirement it was
protecting — discovery that happens while the laptop is shut — is met by the postings feed,
which we do not operate and which holds no user data. The Forge file should be amended to
say so, the same way it was amended once before when "it all runs on the laptop" turned out
to cover two different things.

## The spine

Every capability enters through a port the core owns. A port exists only where a second
implementation is nameable, and each row names one.

| Port | First | Second |
|---|---|---|
| `Tool` | the shipped generic tools | every later tool |
| `Agent` | Gemini CLI over ACP | Claude Code via its ACP adapter |
| `Provider` | Ollama, local | a user's own key; a free tier |
| `Judge` | local constrained decoding | TypeSafe |
| `Source` | the postings feed, pulled as a git remote | a local Colly crawler |
| `Docs` | git | a plain directory |
| `Index` | SQLite | DuckDB, if the corpus work outgrows it |

Ports never speak an adapter's vocabulary. `*colly.Collector`, a git object, an ACP
JSON-RPC message and a TypeSafe response all stop at the adapter boundary.

## Two kinds of thinking, two kinds of port

Conflating these would be the central design mistake.

| | The **doer** | The **scorer** |
|---|---|---|
| Work | Research a company, draft a letter, tailor a CV | Judge two hundred postings, six questions each |
| Shape | Conversational, multi-step, tool-using | Batch, typed, calibrated |
| Needs | A full agent the user already pays for | A raw model call, ideally with logprobs |
| Port | `Agent`, over ACP | `Provider` and `Judge` |

An ACP agent returns prose and tool calls, never probabilities, so it cannot be the scorer.
Driving a conversational agent two hundred times to score a board is slow and expensive.
They are different ports because they are different jobs.

### The `Agent` port and ACP

The Agent Client Protocol is JSON-RPC over stdio, with the agent running as a subprocess of
the client. It already specifies tool calls, permission requests and session resume — three
things this project would otherwise design by hand, badly.

Atlas is the **client**. The user brings the agent they already pay for, used the way its
vendor sanctions. There is no Go SDK, official or community, so we implement the subset we
need: initialise, session lifecycle, prompt, streamed updates, permission requests. The
transport is small; the schema is the work. Implementing a published protocol from its
schema is a deliverable in its own right under Tenet 8.

**A subscription is not an API.** Chat entitlements cannot lawfully be driven from code.
What ACP gives us is different and legitimate: the user runs their own agent, on their own
machine, under their own terms.

## Judgement is typed, not prose

The system asks questions whose answers are values, never paragraphs: **noul** (probability
of yes), **choice** (one option, with the distribution over all), **score** (a
probability-weighted position on ordered levels).

All questions for a decision go in **one call**, including speculative ones, and code
decides afterwards which mattered. "Is this a stretch", "which deal-breaker tripped" and
"how senior is this" together cost one round trip; asked in sequence they cost three and
invite the model to contradict itself.

`Judge` is a core port. The default adapter runs locally against `Provider`. TypeSafe is
one implementation, opt-in per workspace, and the UI states plainly when a workspace
enables it — TypeSafe is hosted only and its request body carries the content being judged.

**Violated when:** a core package imports a TypeSafe type, or judgement needs a network.

## Storage

**Git is the system of record.** Every application, CV variant, cover letter and piece of
evidence is a file in the user's own repository. Versioning, diffs, provenance and
portability come free, and the history *is* the corpus. The user can push it to any host,
or nowhere.

**SQLite is a derived index**, never authoritative: postings, scores, pipeline state, run
state. Its second implementation is nameable and likely: the corpus work described below is
analytical, and DuckDB is what that becomes if SQLite stops being enough. It exists because
git is not a query engine — "which postings mention Kubernetes"
across five hundred files is grep, not SQL. It must be rebuildable from git at any time, so
losing it is a rebuild rather than a loss.

This is the nerve tenet one level up: the record is the record, and everything else is
transport or projection.

No Postgres, no Docker, no server in the local product.

## Discovery, and the data work

**The feed** is normalised postings from structured APIs — Greenhouse, Lever, Ashby,
Workable, RemoteOK — crawled by the scheduled Action and committed as data.

**Local crawling** covers what the feed cannot: a user's own company watchlist, a careers
page nobody else follows. This is Colly, and it is the first genuinely
data-engineering-shaped part of the system: per-host rate limiting, `robots.txt`, retry
with backoff, incremental recrawl, and extraction rules that go stale silently when a page
changes.

Both are `Source` implementations. A failing source degrades discovery to the remaining
sources and never fails the run. Deduplication is across sources, because the same role
appears on a board, an aggregator and the company's own page, and counting it three times
corrupts every number downstream.

**Scraping conduct is a correctness requirement, not etiquette.** Identify the crawler,
honour `robots.txt`, rate limit per host, cache so a rerun is cheap, and never authenticate
to a site in order to scrape it. A crawler that gets the user's account banned has
subtracted value from the product it exists to serve.

## The corpus is the long game

Every application produces data: what was applied to, what was said, what evidence was
cited, what came back, how long it took. That corpus later becomes

- which requirements the user repeatedly fails to meet — upskilling guidance grounded in
  their own rejections rather than in generic advice;
- which claims correlate with replies, which is how tailoring improves;
- what a role is actually worth, from postings rather than surveys.

None of it is built in v1. All of it is why the git history is an append-only record of
decisions rather than a snapshot of current state. **What is recorded now determines what
can be learned later**, and that is the one thing that cannot be retrofitted.

## Increments

One unknown each, as the tenets require.

| # | Increment | The unknown it adds | Done when |
|---|---|---|---|
| **0** | Finish the tracer | none — it is written | Both packs run from YAML; the second needed no Go |
| **1** | `Docs` and `Index` | git as a store; derived SQLite | A document is committed and found again by query; deleting the index and rebuilding it loses nothing |
| **2** | `Provider` port | provider routing | A pack runs on Ollama and on a hosted key with only config changed |
| **3** | `Judge` port, local | constrained decoding, calibration | Six typed questions return values in one call, offline |
| **4** | `Agent` port | ACP over JSON-RPC/stdio | A real agent completes a multi-step pack, with permission requests surfaced |
| **5** | The postings feed | a scheduled Action, normalisation | Postings land in a public repo on a schedule; the app pulls them |
| **6** | Profile | document ingest | A CV becomes structured history the user can correct |
| **7** | Fit judgement | nothing new — 3 plus 6 | Apply / stretch / skip with the deal-breaker named |
| **8** | Local crawling | Colly, politeness, staleness | A careers page with no API yields roles, politely, repeatably |
| **9** | Tailoring | evidence tracing | Every claim traces to the corpus; an unsupported claim shows as a gap |
| **10** | Apply loop | the rule engine | Package drafted; allow/ask/deny; drafted and sent are distinct states |
| **11** | Tracking | none | Pipeline state, what went quiet, outcomes |
| **12** | Web UI | Svelte | Usable by someone who will not open a terminal |

Increment 12 is where Svelte enters, deliberately late: clients come once there is an API
worth consuming, and 0-11 are what make one.

## Cost

The constraint is that a month bills nothing.

| Thing | Cost |
|---|---|
| The app, Ollama, SQLite, git, the local crawler | free, local |
| The postings feed | free — public repo, standard runners |
| A user's own agent or API key | their bill, by design |
| Gemini and Groq free tiers | free, rate limited |
| TypeSafe | unknown; no published price. Opt-in, never a default |

**Violated when:** a month bills anything, or a feature only works on a paid tier.

## Deliberately out

| Out | Why | What changes it |
|---|---|---|
| Submitting applications | Per-portal automation, much larger trust cost, and drafting is the value | The drafting loop is trusted and manual submit is the remaining friction |
| Driving a browser to fill forms | As above, plus it is what gets accounts banned | As above |
| Scraping behind a login | Contractual and account risk taken on the user's behalf | Nothing foreseen |
| Accounts, sign-in, multi-tenancy | There is no server to have accounts on | Nothing foreseen |
| Postgres | The local product has no server to run one | Nothing foreseen |
| vLLM | Deferred on evidence, not refused | Increment 3 needs logprobs, or batch scoring bottlenecks |
| Nerve as a store | Git covers the record; no pack has shown a need git cannot meet | A pack runs out of what git and SQLite give it |

## Open questions

- **Calibration, and what it may cost.** Two problems hide here. *Shape* is easy: Ollama
  constrains output to a JSON schema, so a `choice` comes back as one of the allowed
  options. *Probability* is not. A `noul` only means something if it is a real likelihood,
  which needs token logprobs — vLLM exposes them, Ollama's support is thin. So increment 3
  may pull vLLM from "deferred" to "required", and the evidence will be logprobs rather
  than throughput. Increment 3 must settle it before increment 7 depends on it.
  Separately, nothing yet checks a probability against an outcome; until applications have
  results, a calibrated-looking number is still an assertion.
- **Scheduled workflows on an idle repository.** GitHub disables scheduled workflows on
  repositories after a period of inactivity. Whether commits made by the Action itself
  count as activity determines whether the feed quietly stops. Verify before relying on it,
  and have the app notice a stale feed rather than trusting it.
- **Distribution.** A local-first product has to be installed. A single static Go binary is
  the cheapest answer; whether that is enough for a non-technical user is unresolved.
- **Binary artifacts in git.** Generated PDFs bloat history. Whether to commit them, keep
  them untracked, or stop at structured text is undecided, and increment 9 forces it.
- **Whether `Judge` and `Provider` stay separate.** They may collapse once the local
  adapter exists. That will be obvious then, not now.
