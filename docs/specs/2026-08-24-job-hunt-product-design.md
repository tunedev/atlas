# Job hunt slice - product design

Product expectations for the Atlas job hunt slice. Scope is what the product does and
who it does it for. Architecture is decided per epic, at the point each is built.

## What it is

A multi-user web platform that runs a job hunt on your behalf. It finds roles, judges
whether they are worth your time, drafts the application, applies under rules you set,
and tracks what happens next. Users bring their own LLM account; the platform supplies
the workflow, not the inference.

Modelled on `github.com/MadsLorentzen/ai-job-search`, which is a single-user set of
Claude Code slash commands over local files. That repo is a source of product
requirements, not of architecture.

## Two kinds of authentication

These are unrelated and must not be collapsed.

| Concern | Mechanism | What it gates |
|---|---|---|
| Who you are | Google sign-in, only method | Access, workspace, data isolation |
| What thinks for you | Claude, Gemini, or OpenAI account | Every agent action the platform takes |

A user without a connected provider can sign in and see their workspace. They cannot
run discovery, scoring, or tailoring. Provider connection is a prerequisite surfaced
during onboarding, not a preference.

## Surfaces

- **Web UI** - the product. Google sign-in, all user-facing work.
- **HTTP API** - what the web UI consumes.
- **Scheduled worker** - per user, runs discovery and staleness detection unattended.
- **CLI** - localhost admin and debugging only, unauthenticated, not a user surface.

## Multi-user and the workspace tenet

`../../../CLAUDE.md` refuses multi-tenant SaaS isolation and prescribes path-prefix
namespacing, where a workspace is a subtree. A Google-authenticated user is a workspace
subtree. Multi-user here means per-user scoping over a shared deployment, not tenant
isolation machinery. The refusal stands.

## The three registries

The same shape appears at three altitudes. Each is data added at runtime, never a code
edit, and this is why the epics below must be built as composable pieces.

| Registry | Entries | Added by |
|---|---|---|
| Providers | Claude, Gemini, OpenAI | User connecting an account |
| Portals | Greenhouse, Lever, Ashby, RemoteOK, HN, LinkedIn, freehire, career pages | Configuration, later by user |
| Apply rules | allow / ask / deny patterns over postings | User, promoted from repeated decisions |

## Epics

Ordered by dependency. The order is the build order; the whole list is the product.

| # | Epic | What the user gets | Depends on |
|---|---|---|---|
| 1 | Identity and workspace | Google sign-in; your data is yours | - |
| 2 | Agent connections | Connect Claude, Gemini or OpenAI; switch without loss | 1 |
| 3 | Profile | CV import, preferences and deal-breakers, evidence corpus, learning from your decisions | 1, 2 |
| 4 | Discovery | Roles arrive without asking, deduped across sources | 1 |
| 5 | Fit judgement | Apply / stretch / skip with reasons, including which deal-breaker tripped | 3, 4 |
| 6 | Tailoring | CV variant, cover letter, application answers, drawn from evidence | 3, 5 |
| 7 | Apply loop | Preview and one-click apply; auto-apply under rules you set | 6 |
| 8 | Tracking | Pipeline state, what has gone quiet, follow-ups, outcomes | 7 |
| 9 | Interview prep | Company research, your STAR examples mapped to the role, mock questions | 3, 8 |
| 10 | Gap and upskilling | What you are consistently missing across the roles you want | 3, 5 |
| 11 | Salary benchmarking | What a role is worth before you negotiate | 4 |
| 12 | Sandbox and blueprints | Add a portal, build a blueprint, write a skill, without waiting for a code change | 4-8 |

Epics 1 and 2 gate everything: no second person can test the product until identity and
provider connection work.

## Epic detail

### 1. Identity and workspace
Google sign-in is the only method. Every record belongs to exactly one user. A user sees
their own postings, scores, documents, and applications, and nothing else.

### 2. Agent connections
The user connects a Claude, Gemini, or OpenAI account. Their usage, their bill. Switching
provider does not lose profile, postings, scores, or applications. The platform states
which provider produced any given output.

### 3. Profile
Four inputs, all optional individually, useless in aggregate if all absent:
- CV upload, parsed into structured history the user corrects.
- A preferences conversation covering goals, deal-breakers, salary floor, location.
  A CV contains none of this and fit judgement needs all of it.
- An evidence corpus - past cover letters, project write-ups, reviews - that tailoring
  quotes from.
- A decision log. Every apply and skip is a labelled example that tunes later scoring.
  Stated preferences drift; revealed preferences do not.

### 4. Discovery
Runs on a schedule per user. Sources:
- Structured job APIs: Greenhouse, Lever, Ashby, Workable, RemoteOK, HN "Who is hiring".
- A company watchlist, polled directly.
- LinkedIn public `jobs-guest` endpoints and the freehire.me REST API.
Results are deduplicated across sources. A failing portal degrades discovery to the
remaining portals; it never fails the run.

### 5. Fit judgement
Each posting gets a verdict of apply, stretch, or skip, with reasons the user can read.
Deal-breaker hits are named explicitly. The user can disagree, and disagreement is
recorded as a decision that feeds epic 3.

### 6. Tailoring
Per role: a CV variant, a cover letter, and answers to the posting's questions. Every
claim traces to evidence in the user's corpus. A claim with no supporting evidence is
shown as a visible gap rather than written as fact.

### 7. Apply loop
Applying means the platform drafts every artifact the user needs in order to apply. The
platform does not submit and does not drive a browser.

One click on a posting produces the package: CV variant, cover letter, answers, and the
posting's submission URL and instructions. The user reviews it, edits it, and sends it.

On top of that sits a rule engine with the semantics of Claude Code's permission
allowlist:

| Rule outcome | Behaviour |
|---|---|
| allow | Drafts the full package unattended, ready and waiting |
| ask | Shows the posting and waits for the user |
| deny | Never drafts; a deal-breaker match is always deny |

Default for an unmatched posting is ask. Deny is evaluated before allow. A repeated
decision can be promoted into a standing rule.

An allow rule firing wrongly is not free: drafting runs on the user's own provider
account, so a rule that is too broad spends their tokens on packages they discard.

### 8. Tracking
Every application's state, how long it has been there, and what has gone quiet. Follow-up
prompts. Outcomes recorded through to offer or rejection.

Drafted and submitted are distinct states, and the platform cannot see the boundary
between them. A package stays drafted until the user says they sent it. That single
action is what starts the staleness clock, what lets fit judgement be evaluated against
outcomes, and what distinguishes a package that was used from one that was ignored -
which is also the signal that an allow rule is firing too broadly.

### 9. Interview prep
Company research for a scheduled interview, the user's own STAR examples mapped to the
role's requirements, and mock questions.

### 10. Gap and upskilling
Across the roles the user targets, the requirements they repeatedly do not meet.

### 11. Salary benchmarking
What a role is worth, so the user does not negotiate blind.

### 12. Sandbox and blueprints
Add a job portal without a code change. Build a blueprint for a step the product did not
anticipate. Write a custom skill. Extracted from epics 4-8 once those exist, shaped by
the duplication they actually exhibit rather than by a guess made in advance.

## Transport

Service-to-service RPC is gRPC. The same handler serves HTTP/JSON, so the web UI and an
agent runner call the same method over different protocols. See the transport section of
`../../../CLAUDE.md` Tenet 1 for the rule and the reasoning; it governs every repo, not
just this one.

What it means here: the web UI talks JSON to the same `connect-go` handler an agent
runner talks gRPC to. There is no separate REST layer, no gateway process, and no second
adapter to keep in sync.

## Not in v1

Deferred, with the reason, so the option stays visible rather than reading as closed:

| Deferred | Why now | What would change the answer |
|---|---|---|
| Submitting applications | Drafting is the whole value; submitting is per-portal browser automation with a much larger trust cost | The drafting loop is trusted and the manual submit step is the remaining friction |
| Referral and warm-introduction pathfinding | Needs a graph of who the user knows, which nothing here has | A real source of that graph appears |
| Inbound recruiter triage | The product chases roles; inbound is a different flow | Enough inbound to be a burden |
| Notification channels | The user pulls; nothing pushes yet | The user misses something that mattered |

## Permanently out of scope

- Any sign-in method other than Google.
- Platform-funded inference. Users bring their own provider.

## Open questions

- Tailoring output format. Upstream compiles LaTeX or Typst to PDF and validates the ATS
  text layer with `pdftotext`. That is a heavy toolchain dependency. Whether Atlas
  generates PDFs or stops at structured text is undecided, and the decision above makes
  it weightier: the package is the final deliverable, and the user uploads it to a
  careers page that will not accept markdown.
- Whether provider connection uses OAuth sign-in or API keys per provider, which differs
  by provider and changes onboarding.
