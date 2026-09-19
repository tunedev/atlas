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
| `Provider` | vLLM, local | Ollama; a user's own key; a free tier |
| `Judge` | local, constrained decoding plus logprobs | TypeSafe |
| `Source` | the postings feed, pulled as a git remote | a local Colly crawler; a rendered fetch |
| `Docs` | git | a plain directory |
| `Index` | SQLite | DuckDB, built alongside it |

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

`Judge` is a core port. The default adapter runs locally against `Provider`, using
constrained decoding for the shape of an answer and token logprobs for its probability. TypeSafe is
one implementation, opt-in per workspace, and the UI states plainly when a workspace
enables it — TypeSafe is hosted only and its request body carries the content being judged.

**Violated when:** a core package imports a TypeSafe type, or judgement needs a network.

## Storage

**Git is the system of record.** Every application, CV variant, cover letter and piece of
evidence is a file in the user's own repository. Versioning, diffs, provenance and
portability come free, and the history *is* the corpus. The user can push it to any host,
or nowhere.

**SQLite is a derived index**, never authoritative: postings, scores, pipeline state, run
state. **DuckDB is built alongside it**, not held in reserve: the corpus work described
below is analytical, and an analytical engine is the right tool for it. SQLite serves the
transactional path — what is in the pipeline, what state a run is in — and DuckDB serves
the questions that scan history. Both are derived, so the port is not a choice the user
makes but a routing decision the app makes per query.

Both statically link, so the app stays one binary. The cost is cgo, which means building
per platform in CI rather than cross-compiling from one machine. It exists because git is
not a query engine — "which postings mention Kubernetes"
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

**Rendered sources** exist because Colly does not execute JavaScript, and a large share of
careers pages are single-page applications that return an empty shell to an HTTP fetch. The
renderer is Rod: it waits for elements by default and its `Race` handles the "cookie banner
or content or interstitial" problem that is the actual shape of scraping, where a driver
requiring an explicit wait before every interaction produces flakes that reproduce only on
a slow day.

Where it runs is the design decision, not which library. **Rendering runs in the feed.** A
GitHub runner already has a browser and the compute is free; a friend installing a single
binary should not be handed a 150 MB download. Locally it is off by default, and a user who
opts in gets `launcher.LookPath()` against a browser they already have — Rod can download a
pinned revision and must not be allowed to.

A page that needs rendering and is fetched without it must report **needs rendering**, never
zero results. Silent zero is the failure nobody notices.

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
| **1** | `Docs` and `Index` | git as a store; derived SQLite and DuckDB | A document is committed and found again by query; deleting both indices and rebuilding them loses nothing |
| **2** | `Provider` port | vLLM, provider routing | A pack runs on local vLLM and on a hosted key with only config changed |
| **3** | `Judge` port, local | constrained decoding, logprobs | Six typed questions return values with probabilities in one call, offline, and each is recorded so it can be checked against an outcome later |
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

## Inference

vLLM is the local provider. It was going to be deferred until batch scoring hurt; it is
adopted now because the `Judge` port needs token logprobs and vLLM exposes them where
Ollama's support is thin. Continuous batching — the reason to want it for scoring two
hundred postings — arrives as a second benefit rather than the justification.

It serves an OpenAI-compatible API, so the `Provider` adapter is one shape pointed at
different base URLs: vLLM locally, a hosted key remotely, Ollama for anyone who prefers it.

Two facts to design within rather than discover:

- **8 GB of VRAM** on the development machine bounds the model to roughly a 7B at four bits
  or a 3-4B at full precision, plus KV cache. That is a ceiling on the scorer's quality,
  and the answer when it binds is a hosted `Provider`, not a bigger local model.
- **vLLM is a Python service, not a Go library.** The app stays a single binary; vLLM is a
  process it talks to. For a product meant to be installed by a friend, that is a real
  burden, which is precisely why `Provider` is a port: vLLM is the reference implementation
  and the one the scorer is calibrated against, while Ollama and a hosted key remain
  first-class for people who will not run a Python server.

## Cost

The constraint is that a month bills nothing.

| Thing | Cost |
|---|---|
| The app, vLLM, Ollama, SQLite, DuckDB, git, the local crawler | free, local |
| Rendering in the feed | free — the runner already has a browser |
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
| Downloading a browser onto a user's machine | It breaks the one-binary promise for a minority of sources | Nothing foreseen; the feed renders instead |
| Accounts, sign-in, multi-tenancy | There is no server to have accounts on | Nothing foreseen |
| Postgres | The local product has no server to run one | Nothing foreseen |
| Nerve as a store | Git covers the record; no pack has shown a need git cannot meet | A pack runs out of what git and SQLite give it |

## Open questions

- **Calibration.** Adopting vLLM settles how a probability is obtained — logprobs — but
  not whether it is any good. Nothing yet checks a stated likelihood against an outcome, and
  until applications have results to compare against, a calibrated-looking number is still
  an assertion. Increment 3 must at minimum record the number and the eventual outcome in a
  way that makes the check possible later, or increment 7 inherits confident nonsense.
- **Scheduled workflows on an idle repository.** GitHub disables scheduled workflows on
  repositories after a period of inactivity. Whether commits made by the Action itself
  count as activity determines whether the feed quietly stops. Verify before relying on it,
  and have the app notice a stale feed rather than trusting it.
- **Distribution.** A local-first product has to be installed. The app is one static
  binary, built per platform in CI because DuckDB needs cgo. vLLM is a separate Python
  service, so the easy path for a non-technical user is Ollama or a hosted key, and whether
  that is enough is unresolved.
- **Binary artifacts in git.** Generated PDFs bloat history. Whether to commit them, keep
  them untracked, or stop at structured text is undecided, and increment 9 forces it.
- **Whether `Judge` and `Provider` stay separate.** They may collapse once the local
  adapter exists. That will be obvious then, not now.
