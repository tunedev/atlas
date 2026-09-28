# Epic 14 — Spend and exposure — design

**Supersedes nothing.** This design proposes Epic 14 of `docs/plans/2026-09-17-roadmap.md`,
which the roadmap does not yet name. It sits under `docs/specs/2026-09-17-job-hunt-harness-design.md`
and rests on Epic 2's `Provider` (`docs/design/the-provider.md`'s stories, delivered as
`internal/core/ports/provider.go`), Epic 1's record (`Docs`/`Index`), and Epic 12's `Runner`
seam. It is written against code that exists today, not against a future increment.

**Goal, restated.** The first draft of this design scoped itself to what a run cost in
dollars and what left the machine. The human's actual ask is wider and more precise: "I want
all the turns and all the calls to any external dependencies especially LLMs to have a ledger
from day one so we can easily look back at areas we can optimize for a more efficient call, or
preempted [sic] exhaustive options." That is not a spend report that happens to count tokens.
It is an **optimisation record** — every call this process makes to something outside itself,
kept from the day the calling code is written, so the questions in §"Six questions this
schema answers" can be asked of real traffic instead of retrofitted onto it later. Spend in
USD is one column of that record, not its purpose; §1 and §2.1 below are the model-pricing
slice of a design that now covers every outbound dependency.

## What exists today

Checked by reading, not assumed:

- `grep -rn "pricing\|cost\|price" internal/` returns nothing. No price is computed anywhere.
- `ports.Usage{PromptTokens, CompletionTokens}` and `Completion.Latency` are recorded on every
  `Provider.Complete` call (`internal/core/ports/provider.go`), but only one caller reads them:
  `tools.Model.Invoke` (`internal/adapters/outbound/tools/model.go:46-51`) puts
  `gen_ai.response.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` and
  `atlas.model.latency_ms` (the non-standard name, chosen because `gen_ai.*` has no field for
  it) on the current span.
- **`judge.ask`, `judge.each`, `extract.run` and `citations.judge` set no usage attributes at
  all.** `app.Judge.Ask` and `app.Extractor.Extract` (`internal/core/app/judge.go`,
  `internal/core/app/extract.go`) call `Provider.Complete` directly and read `Usage` only to
  check for truncation (`judge.go:57`); nothing downstream of that check touches a span. Per
  Epic 7's own measurement (`docs/specs/2026-09-27-epic-7-fit.md`), `judge.each` is the
  dominant model-calling path in the system — one call per role, hundreds of roles per board —
  and today it is invisible to telemetry. This is the concrete evidence that instrumenting one
  tool (`model.complete`) rather than the port left most of the traffic unmeasured.
- `internal/telemetry/telemetry.go` wires an OTLP trace pipeline that is **off by default**
  (`cfg.OTel.Enabled` defaults to `false`; `Init`'s own doc comment: "the binary runs with no
  collector"). Any design that puts spend only on spans is invisible on the common path: a
  laptop run with no collector up.
- No log call anywhere in `internal/` carries prompt or completion text (checked: no
  `slog.Info/Debug/Warn/Error` call exists in `internal/`; the one `.WarnContext` call,
  `crawl.go:55`, logs a crawl target URL, not a prompt). Spans carry only the four attributes
  above, plus `blueprint.name`, `blueprint.steps`, `step.id`, `tool.name`
  (`internal/core/app/runner.go:64,95-99`). **Today's baseline already keeps prompt and
  completion text off telemetry entirely** — this design's redaction rule is to keep that true
  under load, not to invent it.
- The record pattern is fixed by `internal/core/app/record.go` and
  `internal/core/app/judgementrecord.go`: `Docs` (git) holds the document, `Index` (SQLite)
  holds a derived, rebuildable row (`RecordDocument`). Every "record" this design adds follows
  that shape exactly.
- `Runner` (`internal/core/app/runner.go`) already has the exact seam this design needs:
  `WithTracer` and `WithProgress` are copy-on-write additions the composition root wires in,
  and the runner "knows nothing about what any tool does" (its own doc comment). `Run` opens
  one span per blueprint; `execStep` opens one child span per step. A step can call `Provider`
  more than once (`judge.each` loops over every selected item, one call per item, one after
  another — `internal/adapters/outbound/tools/each.go`), so one step's span can cover many
  provider calls.
- `cmd/atlas/main.go`'s `buildRegistry` (lines 72-122) is the one place a `ports.Provider` is
  constructed (`openaiprov.New`, line 73) and the one place it is handed to every consumer:
  `app.NewJudge` (81), `app.NewExtractor` (88), `tools.NewModel` (94). `judge` then feeds
  `tools.NewJudge`, `tools.NewCitationsJudge` and `tools.NewJudgeEach`. **Every model call in
  the system passes through this one constructed value.**
- `ModelConfig` has no separate provider/vendor field. `openaiprov.Config.Name` and `.Model`
  are both filled from `cfg.Model.Name` (`main.go:73-80`), so `Provider.Name()` and
  `Completion.Model` coincide today. Epic 2.4's ordered provider chain is exactly where they
  would diverge (several named engines, one chain), so this design keys pricing by both axes
  now rather than forcing a breaking change to the table's shape when that chain exists.

**What else reaches outside the process.** `Provider` is one of five paths, read one by one
rather than assumed:

- **`crawlsource.Source.Pull`** (`internal/adapters/outbound/crawlsource/source.go:147-175`)
  loops over every configured target and, per target, either fetches it with Colly through a
  shared `Conduct` (robots check, per-host pacing, `Conduct.send`'s own retry-with-backoff loop
  — `retry.go:20-37` — and conditional-GET revalidation against a local ETag/Last-Modified
  cache — `revalidate.go`) or renders it in a headless Chrome `chrome.render` navigates
  (`render.go:49-119`). One `Pull` call therefore makes as many outbound requests as it has
  targets, each with its own retry count and its own cache outcome — a single number on `Pull`
  itself cannot represent that. `Source.LastReport().Revalidated` already exists as a
  Pull-wide revalidation count (`source.go:66-71`, read by `tools.CrawlPull` into its own
  `_meta.revalidated` — `tools/crawl.go:65`); the failure taxonomy `crawlsource` already
  computes per target — `KindDisallowed`, `KindFetch`, `KindStale`, `KindNeedsRendering`,
  `KindRenderingUnavailable` (`source.go:kindOf`, lines 276-290) — is a ready-made error
  classification, independent of and older than PR #52's `ports.ErrRejected`. None of this
  reaches a span, a log, or a record today.
- **`feedsource.Source.Pull`** (`internal/adapters/outbound/feedsource/source.go:45-68`) makes
  exactly one outbound call per `Pull`: a depth-one `git.FetchContext` of one pinned ref from
  one remote. When the tip has not moved, `go-git` returns `git.NoErrAlreadyUpToDate` and no
  new objects are transferred — the git-native equivalent of an HTTP 304. Nothing records
  whether a Pull actually moved bytes or found nothing new.
- **`acpagent.Client.Do`** (`internal/adapters/outbound/acpagent/client.go`, `turn.go`) speaks
  ACP to a subprocess over stdio. The design already found (below, §2.4) that atlas cannot see
  what that subprocess itself sends over the network; what it can see — the turn's wall time and
  whether it errored — is recorded nowhere but the step span the runner already opens.
- **`tools.HTTP`** (`internal/adapters/outbound/tools/http.go`, tool name `http.request`) is a
  raw `net/http` `GET`/`HEAD` a pack calls directly. It implements `ports.Tool`, not
  `ports.Provider` or `ports.Source` — there is no port to decorate here, because there is no
  port at all between this tool and the network. It has no cache and no retry.
- **`app.Extractor.Extract`** (`internal/core/app/extract.go:54`) — the "extractor" a first
  read of the brief might expect to be a sixth path — calls `provider.Complete` directly, the
  same call `app.Judge.Ask` makes. It is not a separate outbound dependency; it is already
  covered the moment `Provider` is decorated once (§2.1), which is exactly what line 24 above
  already found missing usage attributes for.

**Recording is not free.** `ports.Docs.Put`'s own contract — "a write that changes a path's
body produces a new revision" (`internal/core/ports/docs.go`) — is, on `gitdocs.Store`, one git
commit. Every `RecordDocument` call is a commit. This bounds how granular a *recorded* ledger
entry can be without the record itself becoming the thing that needs optimising (§3's volume
analysis).

**Coverage already exists and is already flagged as under-used.** `ports.Answer.Coverage`
(`internal/core/ports/judge.go:59-69`) carries `Represented`/`Declared` on every answer a
`Judgement` holds; `docs/notes/2026-09-24-epic-3-closing.md`, quoted in full by
`docs/specs/2026-09-27-epic-11-tracking.md`, already names the gap this design closes for the
ledger: "a coverage-0 judgement should probably be excluded from any calibration sample built
later... but nothing marks it as such today; `Coverage` sits on the record, unused by anything
that reads it." §2.6 below is that marking, at the call level rather than only the judgement
level.

## Approaches considered

**Where to meter a call.**

| | Approach | Verdict |
|---|---|---|
| A | Teach every tool that calls `Provider` (`model.go`, `judge.go`, `extract.go`, `citejudge.go`, and whatever Epic 9/10 add) to record its own usage and cost | Rejected. This is the exact incident `../CLAUDE.md`'s "Fix placement" section warns about: it fixes today's four call sites and leaves every future one a landmine, and it is precisely how `judge.each` went unmeasured — `model.complete` was taught, nothing else was. |
| B | Decorate `ports.Provider` once, at `buildRegistry`'s one construction site, the same shape `Chain` already uses (a `Provider` implemented over another `Provider`) | **Chosen.** Every current and future consumer of `Provider` is metered without a line changing in `model.go`, `judge.go`, `extract.go`, or whichever tool Epic 9 or 10 adds. |
| C | Have `Runner.execStep` inspect a tool's returned `map[string]any` for a conventional `usage` key | Rejected. `execStep`'s own doc comment is "nothing here knows what any tool does"; reading a tool's result shape by convention breaks that, and it still only sees one number per step, not one per call inside `judge.each`'s loop. |

**The same choice, widened to `Source` and `Agent`, with one caveat `Provider` does not have.**
`Provider.Complete` is one call in, one `Completion` out — a decorator over the port sees
exactly what happened, every time. `Agent.Do` is the same shape: one turn in, one `AgentResult`
out, so a `MeteredAgent` decorating `ports.Agent` (§2.4) works exactly like `Metered` does, with
the same zero-cost guarantee for whichever agent adapter runs next. `Source.Pull`, on
`feedsource`, is also 1:1 with its one outbound git fetch, so a port-level decorator would work
there too — but on `crawlsource`, `Pull` fans out internally over every target (above), and a
decorator sitting outside `Pull` cannot see a single target's URL, retry count, or cache
outcome; `Pull`'s own return shape (`[]ports.Item`, one `Failures` error) does not carry them
out. Recording `crawlsource`'s per-target detail can therefore only happen inside
`crawlsource` itself, at `Source.fetch`/`s.fetched`/`s.rendered`, exactly where those facts are
already computed and nowhere else. §2.2 records both adapters the same way — inside the
adapter, at its real call site — rather than splitting `Source` into "the port gets a decorator"
for one implementation and "the adapter records itself" for the other; a reader should not have
to learn two mechanisms for one interface. This is not Approach A's rejected pattern: Approach A
was teaching every *consumer* of a port (`model.go`, `judge.go`, `extract.go`, and whichever
tool comes next) to record itself, which grows one entry at a time forever. `crawlsource` and
`feedsource` are the *adapters*, not consumers — there are exactly two `Source` implementations
today, recording lives in both of them once, and a third `Source` a future epic adds either
follows the same one-line pattern or, if it is 1:1 like `feedsource`, could be covered by a
generic wrapper with no code of its own. `tools.HTTP` has no port beneath it at all (above), so
recording is a small addition to that one file — the narrowest case, not a precedent for
teaching tools in general to record themselves.

**Where to record the roll-up.**

| | Approach | Verdict |
|---|---|---|
| A | A span attribute or event only, read back from the OTel backend | Rejected as the only channel: OTel is off by default, so this is invisible on the common path (see above). Used as the *secondary*, richer channel when `-otel` is on. |
| B | A git document per run, through `Docs`/`Index`, exactly like a judgement | **Chosen** as the primary channel. Available with no collector, consistent with "git is the record" everywhere else in this codebase, and queryable by `ports.Index.Find` since Epic 1. |
| C | An in-memory total printed once and discarded | Rejected. Answers "what did this run cost" and nothing else; there is no way to ask "what have I spent this month," which is the actual question behind the user's repeated request. |

## What a "run" is

Read from `Runner.Run` (`internal/core/app/runner.go:61-74`): one call to `Run` executes a
whole blueprint — every step, in order — under one span named `"blueprint."+b.Name`, and it is
already the unit `docs/specs/2026-09-27-epic-13-web-ui.md` calls "a run" ("Two runs never share
a `Runner`", "a run submitted while another is running queues behind it"). It is what
`atlas -pack packs/x.yaml` executes once per process invocation (Epic 0.1), and what a future
`-serve` executes once per click (Epic 13.1). A judgement is not a run — `judge.each` alone can
make hundreds of calls inside one run's single step. An agent session is not a separate unit
either: `agent.do` is one step's tool inside a run, the same as `model.complete` is.

**A run is one `Runner.Run` call.** The roll-up this design adds is scoped to it.

## 1. Pricing

**Scope: the model provider only.** A dollar price table means something for `Provider` —
vendors sell tokens. It means nothing for a crawl fetch, a git fetch, an HTTP fetch or an agent
turn: nobody bills atlas per request for those, on the free-laptop-or-free-tier stack the Forge
constrains this project to. §2.2–§2.4 record their cost in time and bytes instead, the same
non-dollar column §"Local models are not free" below already establishes for local compute.

```go
// PriceMToken is USD per one million tokens, kept at that scale to avoid
// float underflow at the token counts involved.
type PriceMToken struct {
    Input  float64
    Output float64
}

// Price is a provider:model's price, or the fact that none is known. Known
// is false exactly when no table entry matched; the zero PriceMToken it
// carries then is a placeholder, never a quote.
type Price struct {
    PriceMToken
    Known bool
}

// PricingTable prices a provider:model pair, keyed "<provider>:<model>",
// with a "<provider>:*" fallback for an engine priced the same regardless
// of which model name it reports (every local engine, today).
type PricingTable map[string]PriceMToken

func (t PricingTable) Lookup(provider, model string) Price {
    if p, ok := t[provider+":"+model]; ok {
        return Price{p, true}
    }
    if p, ok := t[provider+":*"]; ok {
        return Price{p, true}
    }
    return Price{}
}

// Cost prices u against price. An unknown price costs zero and never
// guesses; price.Known is the caller's signal that the zero is real.
func Cost(u ports.Usage, price Price) float64 {
    if !price.Known {
        return 0
    }
    return float64(u.PromptTokens)/1e6*price.Input + float64(u.CompletionTokens)/1e6*price.Output
}
```

**Where it lives.** `internal/config` gains:

```go
// SpendConfig locates the pricing table layered over the embedded
// defaults. PricingPath, when set, is a file of provider:model price
// entries, expanded from a leading "~" like Store.Root and Feed.CachePath.
type SpendConfig struct {
    PricingPath string
}
```

Loaded once at startup (`config.Load`), like every other path-shaped setting: default empty
(embedded defaults only), overridable by `ATLAS_SPEND_PRICING_PATH` and `-spend-pricing-path`.
A malformed file fails loudly at boot — Tenet 6's rule, and the same rule `AgentConfig.validate`
already enforces for its own settings.

**Two rules, per the brief:**

1. **An unknown provider:model prices at zero, never an error.** `Cost` above returns 0 rather
   than failing `Provider.Complete`'s caller; pricing a call is never allowed to be the reason a
   pack run fails, which would make an operational nicety into a reliability dependency — the
   opposite of what this epic is for.
2. **A zero price must be visible, not silent.** `Price.Known` travels alongside every zero.
   The roll-up (below) counts unpriced calls separately from priced ones, at zero USD each —
   the same discipline `docs/specs/2026-09-27-epic-11-tracking.md`'s `ExclusionCounts` already
   uses for outcomes that would otherwise be silently merged into a misleading total.

**Embedding is deliberately narrow.** Only the two local engines Epic 2.2/2.3 names get an
embedded default: `"ollama:*"` and `"vllm:*"`, both `{0, 0}` with `Known: true`. No hosted
provider's price is embedded. A hosted vendor's price changes on their own schedule, not
atlas's; embedding one would be a fact that goes stale the moment it is written, and a wrong
non-zero number is worse than a visible unpriced zero — it looks trustworthy and is not. A
hosted provider:model stays unpriced until `Spend.PricingPath` supplies it. **Updating the
table is editing that file**, no code change, no rebuild.

**Local models are not "free" the way a skipped call is free.** A local engine's price is a
deliberate `$0, Known: true` — it costs nothing in dollars, and that is real. But it costs GPU
time and electricity, which dollars do not capture. The roll-up (below) always carries total
model time (`Completion.Latency`, already recorded per Epic 2.6) as a co-equal column next to
USD, specifically so "$0.00" for a local run reads as "zero dollars, N minutes of local
compute" rather than "free." Epic 7's own measurement — 200 roles in about 9 minutes on a
laptop GPU — is exactly the number this column would have shown.

**A roll-up document freezes the price it computed.** Editing `Spend.PricingPath` changes only
future runs. A recorded run's USD figures are not re-derived from the table on read; re-pricing
history is not supported, the same immutability the rest of the record already has.

## 2. Metering every call, once — every dependency, at its own boundary

One shape, one `LedgerEntry`, used by all five paths so a reader learns one schema, not five:

```go
// internal/core/app/ledger.go

// LedgerEntry is one call to something outside this process. Dependency and
// Operation say what kind of call it was ("provider"/"complete",
// "crawl"/"fetch", "crawl"/"render", "feed"/"pull", "agent"/"do",
// "http"/"request"); every other field is zero-valued when it does not
// apply to that kind, which is a fact about the call, not a gap in the
// record — InputTokens/OutputTokens/USD/Priced mean nothing for a git
// fetch, RetryCount and Cached mean nothing for a model call today because
// no retrying decorator exists yet (RetryCount stays real, and stays 0,
// until PR #52's classification gives Metered something to retry on).
type LedgerEntry struct {
    Dependency, Operation     string
    StepID, Tool              string
    Endpoint                  string // host only, never a full URL
    Hosted                    bool
    RequestHash                string // sha256; see per-dependency notes below
    Bytes                      int    // response/rendered-page size; 0 for a model call, priced in tokens instead
    InputTokens, OutputTokens int
    USD                        float64
    Priced                     bool
    LatencyMS                  int64
    RetryCount                 int
    Cached                     bool   // served from a local cache, or answered "not modified" / "already up to date"
    ErrorClass                 string // "" on success
    ZeroCoverageAnswers, TotalAnswers int // set only by Judge.Ask; see §2.6
}
```

### 2.1 The model provider — `Metered` over `ports.Provider`

Unchanged in shape from the first draft of this design; `CallSpend` above is renamed
`LedgerEntry` and gains the fields the other four paths need, all zero here except:

```go
func (m *Metered) Complete(ctx context.Context, p ports.Prompt) (ports.Completion, error) {
    start := time.Now()
    c, err := m.next.Complete(ctx, p)
    e := LedgerEntry{
        Dependency: "provider", Operation: "complete",
        StepID: stepIDFrom(ctx), Tool: toolFrom(ctx),
        Endpoint: m.endpoint, Hosted: m.hosted,
        RequestHash: promptHash(p),
        LatencyMS: time.Since(start).Milliseconds(),
    }
    if err != nil {
        e.ErrorClass = classify(err) // ports.ErrRejected's classification, PR #52 — referenced, not redesigned
        ledgerFrom(ctx).Add(e)
        return c, err // still no USD on a failed call: Completion is zero-valued, there is no Usage to price
    }
    price := m.prices.Lookup(m.next.Name(), c.Model)
    e.InputTokens, e.OutputTokens = c.Usage.PromptTokens, c.Usage.CompletionTokens
    e.USD, e.Priced = Cost(c.Usage, price), price.Known
    ledgerFrom(ctx).Add(e)
    trace.SpanFromContext(ctx).AddEvent("atlas.model.call", /* same four attributes as before */)
    return c, nil
}
```

**What changes from the first draft: a failed call is now recorded.** The original design
recorded nothing on error ("no Usage on a failed call"). That was correct about pricing — it
still is, `Completion` is zero-valued on error, there is no `Usage` to charge for — but it
meant a retried or refused call left no trace at all, which is exactly the data
"which calls were retried, and why" (§"Six questions") needs. The entry's cost fields stay
zero and unpriced on error; its `ErrorClass` and `LatencyMS` do not.

**`RequestHash` covers `System`, `User` and `Schema` — never `Temperature`, `Seed`,
`MaxTokens`, `TopLogProbs`, `Provider` or `Model`.** This resolves the open question the first
draft of this design left for the human ("whether `PromptHash` should also cover the schema").
It does, and here is why the boundary sits exactly there:

- **`Schema` is in, because it changes what is being asked.** The same `System`+`User` text
  sent once unconstrained and once against a JSON Schema are different requests — a reader
  who saw them hash the same would wrongly read two answers to one question as duplicate work.
  `openaiprov` never re-marshals `p.Schema` (`client.go`'s own comment: it would reorder
  `properties`), so hashing it as the exact bytes sent, not a decoded-and-re-encoded form,
  matches what actually left the process.
- **Sampling params are out, because they don't change the question.** Two calls with
  identical `System`/`User`/`Schema` under different `Temperature` or `Seed` are still the
  identical question asked twice — collapsing them under one hash is the point: it is exactly
  the duplicate-work signal §"Six questions" question 2 asks for, whether the duplication was
  a bug or a deliberate resample.
- **`Provider` and `Model` are out, because hiding them would hide the case that matters
  most.** Epic 2.4's ordered provider chain will send the identical prompt first to a primary
  engine and, on failure, to a fallback. A hash that folded the model in would make those two
  attempts look unrelated; a hash that ignores it lets a reader filter by `RequestHash` first
  and read `Provider`/`Model` per matching row — seeing the fallback happen, not hiding it.

Two entries with the same `RequestHash` are, by this definition, the same question asked more
than once — a cache or dedupe candidate regardless of which engine answered or how it was
told to sample.

### 2.2 The crawler — recorded inside `crawlsource`, not through a port decorator

Per §"Approaches considered," `Pull`'s fan-out means the entry has to be built where the
per-target facts already live: `Source.fetch` (`crawlsource/source.go:208-216`), around the
call to `s.fetched` or `s.rendered`.

```go
func (s *Source) fetch(ctx context.Context, t Target, revalidated *revalidationCounter) (*goquery.Selection, *url.URL, error) {
    start := time.Now()
    page, u, err := /* existing fetched/rendered dispatch, unchanged */
    e := LedgerEntry{
        Dependency: "crawl", Operation: crawlOperation(t), // "fetch" or "render"
        StepID: stepIDFrom(ctx), Tool: toolFrom(ctx),
        Endpoint: hostOf(t.URL), Hosted: app.IsHostedEndpoint(t.URL),
        RequestHash: sha256Hex(t.URL),
        LatencyMS: time.Since(start).Milliseconds(),
        RetryCount: retriesFor(t), // Conduct.send's own attempt count, already computed; render never retries
        Cached: revalidated.sawNotModified(t.URL), // false for a render, which never consults the revalidation cache
        Bytes: bytesOf(page),
    }
    if err != nil {
        e.ErrorClass = kindOf(err) // crawlsource's own taxonomy (source.go), unchanged by this epic
    }
    ledgerFrom(ctx).Add(e)
    return page, u, err
}
```

`RequestHash` is `sha256(t.URL)`: two targets that resolve to the identical URL in one run —
whether because a pack lists it twice or because two boards happen to share a posting — made
the identical outbound request, cache or no cache. `feedsource.Source.Pull` follows the same
shape at its one call site (`repo.FetchContext`, `source.go:53-59`):
`Dependency: "feed", Operation: "pull"`, `RequestHash: sha256(RemoteURL+Ref)`,
`Cached: errors.Is(err, git.NoErrAlreadyUpToDate)`, `RetryCount: 0` (`go-git`'s `FetchContext`
is not retried). One `Pull` is one entry, because one `Pull` is one outbound call — the same
1:1 shape `Metered` already has for `Provider`, just recorded inside the adapter instead of
through a wrapper, since nothing outside `feedsource` needs to compose a second `Source`
implementation underneath it the way `Chain` composes several `Provider`s.

### 2.3 `tools.HTTP` — recorded in the one file that makes the call

`http.request` has no port beneath it (§"What exists today"), so the entry is added directly
in `Invoke`, around `h.client.Do(req)`: `Dependency: "http", Operation: "request"`,
`RequestHash: sha256(method+" "+url)`, `Bytes` from the read body, `Cached: false`,
`RetryCount: 0` — both real today, since this tool has neither mechanism, and both ready to
become real the day it grows one.

### 2.4 The ACP agent subprocess — `MeteredAgent` over `ports.Agent`

`Agent.Do` is 1:1 with one turn, the same shape as `Provider.Complete`, so it gets the same
kind of decorator, wired at `startAgent`'s one construction of `acpagent.New`
(`cmd/atlas/main.go:169-178`):

```go
type MeteredAgent struct{ next ports.Agent }

func (m *MeteredAgent) Do(ctx context.Context, task ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error) {
    start := time.Now()
    res, sid, err := m.next.Do(ctx, task, sessionID, onEvent)
    e := LedgerEntry{
        Dependency: "agent", Operation: "do",
        StepID: stepIDFrom(ctx), Tool: toolFrom(ctx),
        RequestHash: sha256Hex(task.Prompt),
        LatencyMS: time.Since(start).Milliseconds(),
    }
    if err != nil {
        e.ErrorClass = classify(err)
    }
    ledgerFrom(ctx).Add(e)
    return res, sid, err
}
```

**What this does and does not see, unchanged from the first draft.** `agent.do` runs a
subprocess over ACP; it never calls `ports.Provider`, so `InputTokens`/`OutputTokens`/`USD`
stay zero here always — the same finding `docs/specs/2026-09-27-epic-13-web-ui.md` §13.5
already states for its egress table ("atlas cannot see where an agent sends what it is
given"). What `MeteredAgent` adds beyond what the step's own span already shows is the
`RequestHash` — proof of whether the identical instruction was handed to the agent twice —
and a record that survives with no collector running, the same reason §3 puts the whole
roll-up in `Docs`/`Index` rather than only on a span. `Endpoint`/`Hosted`/`Bytes` stay empty:
there is no single endpoint to name for a subprocess, and it is genuinely not this epic's to
know.

### 2.5 What is not a sixth path

`app.Extractor.Extract` calls `provider.Complete` (`extract.go:54`), so it is metered the
moment `Provider` is decorated (§2.1) — zero additional code, the same way `judge.go` needed
none. `internal/adapters/outbound/crawlsource/extract.go`'s own `extract` function (confusingly
same name, different layer) reads an already-fetched `goquery.Selection` with no network call
at all; it is not a dependency, it is parsing.

### 2.6 Coverage-0: which model calls measured nothing

`Coverage` is computed in exactly one place, `app.Judge.Ask`
(`internal/core/app/judge.go`, feeding on `internal/core/app/mass.go`'s option matching), from
the `Question`s the call answered — data `Metered` never sees, since `Metered` sits below
`Provider` and knows nothing about questions. `Metered` therefore cannot compute `Coverage`
itself, and should not be taught to: that would mean importing judgement vocabulary into a
decorator whose whole value is not knowing what any caller does with a `Completion`. Instead,
`Judge.Ask` — the one place `Coverage` already exists — annotates the entry `Metered` already
wrote, through the same `Ledger` both reach via `ctx`:

```go
// Ledger.AnnotateLastCoverage records, on the most recently added entry,
// how many of a judgement's answers had zero coverage against how many
// there were. Safe because one context makes one Provider call at a time
// before reading its own ledger back — the same sequential-context
// constraint §"How the ledger reaches every recorder" (below) names for
// every recorder, not a new one this adds.
func (l *Ledger) AnnotateLastCoverage(zero, total int) { /* ... */ }
```

```go
// internal/core/app/judge.go, at the end of Ask, after answers is built
zero := 0
for _, a := range answers {
    if a.Coverage.Declared > 0 && a.Coverage.Represented == 0 {
        zero++
    }
}
if l := ledgerFrom(ctx); l != nil {
    l.AnnotateLastCoverage(zero, len(answers))
}
```

This is one call site, `Judge.Ask`, the same one that already computes `Coverage` and nothing
else — not Approach A's pattern of teaching every consumer, since there is exactly one
consumer that could ever know this. A `LedgerEntry` with `TotalAnswers > 0` and
`ZeroCoverageAnswers == TotalAnswers` is a call that came back with a number and measured
nothing with it: the model named none of the declared options among its alternatives for any
question asked, the "single-option fallback made visible" `ports.Coverage`'s own doc comment
already describes, now visible at the call that produced it rather than only inside the
judgement document it produced. The entry never duplicates `Coverage.Represented`/`Declared`
per answer — that stays exactly where `judgementDoc` already keeps it (§"Relation to a
judgement document," below); the ledger holds only the two counts a reader needs to filter for
waste without opening every judgement in a run.

### How the ledger reaches every recorder

`Runner.Run` attaches one fresh `Ledger` to the context it passes down, `execStep` attaches
the step's id and tool alongside it — unchanged from the first draft:

```go
func (r *Runner) Run(ctx context.Context, b domain.Blueprint) (*domain.State, error) {
    ledger := &Ledger{}
    ctx = withLedger(ctx, ledger)
    ctx, span := r.tracer.Start(ctx, "blueprint."+b.Name)
    defer span.End()
    ...
}
```

`withLedger`/`ledgerFrom` are the same unexported context key and accessor pair `otel` itself
already models in this codebase's `ctx` (`trace.SpanFromContext`, `model.go:45`). Every one of
§2.1–§2.4's recorders reaches the run's one `Ledger` this same way, so `crawlsource`,
`feedsource`, `acpagent` and `tools.HTTP` need no new plumbing beyond importing
`internal/core/app` for the accessor — already a permitted, inward-pointing import for an
adapter, the same direction `crawlsource` and `feedsource` already import `internal/core/ports`.
The constraint this names plainly, once for every path rather than once for `Provider` alone:
a call made from a `context.Context` not derived from the one `Runner`/`Invoke` handed down goes
unmetered. Nothing today calls outside that context.

`app.IsHostedEndpoint` (a host/loopback check) is new in this epic, used identically for a
model endpoint (§2.1), a crawl target (§2.2), and, once Epic 13 imports it, its own egress
table (`docs/specs/2026-09-27-epic-13-web-ui.md` §13.5, which currently describes this rule but
does not implement it) — one function, three callers, no second implementation to keep in sync.

## 3. The per-run roll-up

```go
// internal/core/app/ledger.go

// Ledger accumulates one run's LedgerEntrys as they happen. It is created
// fresh per Run call, never reused across runs and never shared as a
// Runner field, so two runs sharing a registry (Epic 13.1's server) never
// interleave.
type Ledger struct {
    mu      sync.Mutex
    entries []LedgerEntry
}

func (l *Ledger) Add(e LedgerEntry) { l.mu.Lock(); defer l.mu.Unlock(); l.entries = append(l.entries, e) }
func (l *Ledger) Snapshot() []LedgerEntry { l.mu.Lock(); defer l.mu.Unlock(); return append([]LedgerEntry{}, l.entries...) }

// RunLedger is every call every dependency made over one Runner.Run.
type RunLedger struct {
    Blueprint string
    Entries   []LedgerEntry
}
```

**How the ledger reaches every recorder without changing any tool's signature** is described
in full in §2's closing subsection; `Run` attaches one fresh `Ledger` to the context it passes
down, `execStep` attaches the step's id and tool name alongside it, and every one of §2.1–§2.4's
recorders reads it back from `ctx`.

**`Runner` gains a ledger sink, exactly as it gained progress in Epic 12.1:**

```go
// WithLedger returns a copy of the Runner that reports each run's entries to
// f once Run finishes, success or failure. The receiver is unchanged. f
// runs synchronously after every step has been attempted, so a run that
// fails partway is still reported with whatever it actually called.
func (r *Runner) WithLedger(f func(RunLedger)) *Runner
```

Default: a no-op, so a caller that does not wire one pays nothing, the same guarantee
`WithProgress` already makes.

**Where it is recorded — once per run, regardless of how many dependencies it called.** The
composition root wires `WithLedger` to a function that writes one document through
`RecordDocument`, the same call `RecordJudgement` already makes:

```go
Document{
    Path:    fmt.Sprintf("ledger/%s/%s.json", b.Blueprint, time.Now().UTC().Format(recordTimeFormat)),
    Body:    /* RunLedger, plus one Totals row per (dependency, provider, model) computed from Entries */,
    Message: "Record ledger for " + b.Blueprint,
    Kind:    "ledger_run",
    Fields:  map[string]string{
        "blueprint":            b.Blueprint,
        "total_usd":            fmt.Sprintf("%.4f", totalUSD),
        "unpriced_calls":       strconv.Itoa(unpricedCount),
        "hosted":               strconv.FormatBool(anyHosted),
        "zero_coverage_calls":  strconv.Itoa(zeroCoverageCount),
    },
}
```

Git holds every entry's full detail (dependency, operation, provider, model, tokens, USD,
priced, hosted, endpoint, latency, retry count, cached, error class, request hash, coverage
counts); the index row is the five flat fields above, queryable without reading the
repository — the same split `docs/design/the-record.md` already describes for a judgement. A
per-(dependency, provider, model) breakdown, and the local-engine time column, live in the
document body; "per what dimension" in the brief's own words is answered there, not by adding
more index fields no query needs. One document per run, one commit per run, regardless of
whether it made three model calls or three hundred model calls plus forty crawl fetches and
one agent turn — §"Volume" below is why that stays true.

### Six questions this schema answers

The brief asks that the schema be designed around what it must answer, not around the fields
it happens to have. Each of the following reads `Docs.Get`/`Index.Find` over `ledger_run`
documents already recorded; none needs a new reader tool, the same as `spend_run` needed none.

1. **Which step costs the most, within a run and across runs.** Within one run: group
   `RunLedger.Entries` by `StepID`, sum `USD` (model calls) or `LatencyMS` (every dependency) —
   the local-compute-time column (§1, "Local models are not free") makes this answerable even
   when every call is `$0`. Across runs: `Index.Find(Kind: "ledger_run", Match:
   {"blueprint": "..."})` returns every run's `total_usd`/index row without reading a single
   document body, then a reader opens the specific runs worth reading in full.
2. **Which calls repeat an identical prompt.** `RequestHash` (§2.1) groups exact-duplicate
   requests within or across a run's entries; a `Dependency`/`Operation` filter narrows to one
   kind (identical prompts, identical crawl targets, identical agent instructions) before
   grouping.
3. **Which calls were retried, and why.** `RetryCount > 0` finds them; `ErrorClass` on a
   failed entry says why, sourced from `ports.ErrRejected`'s classification for a model call
   (PR #52, referenced not redesigned here) and from `crawlsource`'s own existing `kindOf`
   taxonomy for a crawl. A model call's `RetryCount` reads 0 across the board until a retrying
   decorator exists to increment it — a true zero, not a missing one, and this schema does not
   invent that decorator to answer the question early.
4. **Which prompts are large relative to the model's context window.** `InputTokens` against
   `app.JudgeConfig.ContextTokens` (already configured, `cmd/atlas/main.go:86`) for a model
   call; `Bytes` against the adapter's own configured `MaxBytes` (`crawlsource.Config.MaxBytes`,
   `openaiprov.Config.MaxBytes`, `tools.HTTP`'s own bound) for everything else — the same
   "oversized relative to a known bound" shape, answered per dependency with the bound each
   dependency already carries rather than one new global constant.
5. **Which LLM calls measured nothing.** `ZeroCoverageAnswers == TotalAnswers && TotalAnswers >
   0` (§2.6) finds a `provider`/`complete` entry whose resulting judgement never saw a
   declared option among its alternatives for any question it answered — detectable waste
   `docs/notes/2026-09-24-epic-3-closing.md` already named and nothing before this design
   marked.
6. **Anything the record already answers.** `Priced`/`Hosted`/`Cached` are booleans a reader
   filters on directly (unpriced calls, hosted egress, cache misses) the same way
   `docs/specs/2026-09-27-epic-11-tracking.md`'s `ExclusionCounts` are already filtered rather
   than silently folded into a total.

### Relation to a judgement document: a pointer, never a copy

A `provider`/`complete` `LedgerEntry` that fed a `Judge.Ask` call shares its run's `StepID` and
its call's timestamp with the judgement `RecordAssessedJudgement` writes moments later
(`judgementrecord.go:139-141`); it does not carry `Chosen`, `Distribution`, `Alternatives`, or
per-question `Coverage` — those stay exactly where `judgementDoc` already keeps them
(`judgementrecord.go:31-46`). The two counts §2.6 adds (`ZeroCoverageAnswers`/`TotalAnswers`)
are a derived summary, the same discipline `total_usd`/`unpriced_calls` already use for the
roll-up itself: a number computed from the record, not a second copy of it. A reader who wants
the full per-answer picture behind a `zero-coverage` entry follows `StepID`+time to the
judgement `Index.Find(Kind: "judgement", ...)` already finds; the ledger's job stops at making
that judgement worth looking at.

**Where a user sees it.** Two places, neither of them a new UI:

- The CLI already prints one line per step as it runs (Epic 0.1's own acceptance). It gains one
  line after a run finishes: total USD, unpriced call count if any, zero-coverage call count if
  any, and total dependency time, written from the same `RunLedger` `WithLedger` receives — no
  new print path.
- The record itself, queryable today: `ports.Index.Find(ctx, ports.Query{Kind: "ledger_run",
  Match: map[string]string{"blueprint": "job-hunt"}})` answers "what has this pack cost me, and
  where," available since Epic 1, no new tool required. Once Epic 13's `index.find`/`docs.get`
  generic tools land, the same rows surface through them for free, the same way a judgement
  does — this design adds no reader of its own to duplicate that.

### Volume: what a busy run produces, and what bounds it

Epic 7's own measurement — 200 roles judged in about 9 minutes (`docs/specs/2026-09-27-epic-7-fit.md`)
— is the real number to size against, now with crawl fetches added to the same run rather than
guessed at. A `judge.each` step over 200 roles produces 200 `provider`/`complete` entries; a
`crawl.pull`/`source.pull` step that gathered those roles' postings first might add tens to a
few hundred `crawl` entries, one per target; one `feed`/`pull` entry per feed source pulled;
zero or a handful of `agent`/`do` entries, since an agent turn is comparatively rare and
expensive by design. A large run's `RunLedger.Entries` therefore sits in the low thousands at
the extreme, not the millions — nothing in this system calls an outbound dependency in a hot
per-request loop the way a served web handler would.

**One document per run is what bounds it, and this does not change with the widening.** Every
entry's fields are scalars and one hash — no prompt text, no page HTML, no agent transcript
(§6) — so even a few thousand entries stay a JSON document in the hundreds of kilobytes, not
megabytes: comparable to, and smaller than, many blueprint or crawl-target files already
committed to the same store. `Docs.Put` is one git commit per call (§"What exists today"); this
design commits once per run by construction (`Ledger` accumulates in memory across the whole
`Run`, `WithLedger` fires once at the end), the same way the first draft's `spend_run` already
did for model calls alone — widening to five dependencies grows the document's array, not the
number of commits a run produces. A design that instead wrote one document per call — the
naturally tempting shape for "per-call detail" — would turn a 200-role run into 200 commits
plus however many crawl and feed commits, which is the volume problem the brief warned against,
not a variant of the intended design; §"Deliberately not in this increment" names it explicitly
so a future increment does not reach for it by habit.

**What is not built to bound it further, and what is lost by not building it.** No retention
policy and no cross-run aggregation ship in this increment — a `ledger_run` document is never
deleted, compacted, or rolled up into a weekly total. What is lost by not building that: nothing
today, since git history growth from one document per run is the same growth rate every other
record kind in this store already has (judgements, decisions, agent sessions), and this design
adds no new *rate* of documents, only a wider one each. What would be lost by building
aggregation now: the ability to open one run and see exactly which call happened when, replaced
by a lossy summary — the same trade-off Budgets (§7) is deliberately deferred past, for the
same reason: a real need for it has not yet appeared, and building it before it does means
guessing at the aggregation a real question will actually want.

## 4. Reliability posture: the ledger is never load-bearing

The reference gap analysis this epic translates from (`qonstrue-guardrail-gaps.scratchpad.md`
gap 27: agent traffic partly unobserved because the recording path was allowed to matter) states
the rule this design follows: a ledger is best-effort and never blocks the call it observes.
Concretely, for all five dependencies, not the model provider alone:

- No recorder in §2.1–§2.4 can fail the call it wraps. `Metered.Complete`,
  `crawlsource`'s per-target recording, `feedsource.Source.Pull`'s, `tools.HTTP.Invoke`'s and
  `MeteredAgent.Do`'s all call `Ledger.Add` (or `AnnotateLastCoverage`) after the real call has
  already returned its result, and none of those methods can error — pricing is a pure function
  that cannot error (§1), and appending to a mutex-guarded slice cannot fail. A future
  dependency's recorder inherits the same shape by construction, since there is nothing to wire
  it to but `Ledger.Add`.
- The one place recording *can* fail is `WithLedger`'s function writing the roll-up document
  (`RecordDocument`'s `docs.Put`/`index.Upsert`, over a git repository and SQLite file that can
  themselves be full, locked, or briefly unavailable). That function logs the failure at `warn`
  and returns nothing; `Run`'s own return value — the state or the error the pack's steps
  actually produced — is entirely unaffected. A run that judged 190 of 200 postings, crawled
  forty pages and ran one agent turn, then failed to write its ledger, still returns its real
  result to the caller.
- Recording happens **after** every step has been attempted (success or failure), not only on a
  clean run, so a run that fails partway is not reported as free — it wasn't, and neither is a
  crawl that only reached some of its targets or an agent turn that timed out.

## 5. Exposure: what leaves the machine

**What Go can know without knowing what a CV is.** `internal/arch/vocabulary_test.go` forbids
use-case vocabulary in `internal/`; Go cannot classify a prompt's, a page's, or an agent turn's
content. What it *can* state, mechanically, on every call to every dependency: which endpoint
it went to, whether that endpoint is hosted (§2's `IsHostedEndpoint`, now shared by the model
provider, the crawler, and Epic 13's own egress table), the provider and model when there is
one, and — via `RequestHash` (§2.1–§2.4) — a SHA-256 over the exact request content sent, the
same technique `internal/core/app/fingerprint.go` already uses to prove "was this exact text
sent" without storing the text. That hash is what this epic can offer as evidence for a model
prompt, a crawl target, or an agent instruction alike; it cannot say "this contained a CV."

**What Epic 13.5 already owns, and this epic does not rebuild.** §13.5 answers "does the user
consent" with a pack-declared, per-endpoint `discloses:` list ("profile summary and CV text,
posting text, drafted cover letter and CV") and a one-time acknowledgement gate, entirely in the
web surface. This design does not touch that gate, that badge, or that acknowledgement record —
"do not duplicate the UI" is exactly this. What this epic supplies is the missing half §13.5
itself names as not built: **"Per-run disclosure, computed from the actual prompt"** is listed
under §13.5's own "Not built," because inspecting a prompt was not built anywhere. It still
is not — Go still does not read prompt content — but this epic gives §13.5 the mechanical
per-call facts (endpoint, hosted, byte-length-free hash, provider, model) it did not have
before, recorded per run, so a future increment can cross-reference "this call's hash" against
"this pack's declared `discloses:` list" instead of trusting the declaration alone.

**A CLI gap this epic surfaces but does not close.** `discloses:` lives in the **view file**
(`packs/job-hunt.ui.yaml`), which only exists for the web surface. A pack run from the CLI has
no disclosure statement at all today — §13.5 says as much ("The CLI is unchanged... Whether the
CLI should gate as well is recorded under Not built"). This epic's roll-up document is exactly
where a CLI run's real, mechanical exposure facts (hosted or not, which endpoint, how many
calls) now live, git-recorded, even though no CLI-side consent gate reads them yet. Building
that gate is explicitly out of scope here (§7 and "Deliberately not in this increment").

## 6. Redaction

**The rule, unchanged in substance and now stated over five dependencies.** A span, a span
event, a log line, or a recorded document produced by this epic may carry: counts, durations,
byte lengths, hashes, model/provider/endpoint identifiers, and booleans. **Never prompt text,
never completion text, never a crawled page's body, never an agent's turn text, never an HTTP
response body, never a field's raw value.** This is not a new restriction — §"What exists
today" found that nothing in `internal/` puts text on telemetry today — it is the rule this
design must not be the first thing to break, since `LedgerEntry` is the first place a call's
*content* (via `RequestHash`) is referenced by this epic's own code across every dependency,
and a hash is the boundary that must hold for all five, not only the model provider.

**How it is enforced, not promised.** A test in `internal/core/app`, in the shape
`docs/specs/2026-09-27-epic-13-web-ui.md`'s own `TestNoResponseCarriesTheAPIKey` already uses
for a different secret: run a blueprint whose rendered prompt, crawl target page, and agent
task each contain a sentinel string (`"CANARY-38f2..."`, unique per test run) through a real
`Runner` with a `tracetest.NewInMemoryExporter`
(`go.opentelemetry.io/otel/sdk/trace/tracetest`, already available — `otel/sdk` is already a
module dependency, no new one needed) in place of the OTLP exporter, then scan every recorded
span's attributes and every event's attributes, across every span, for the sentinel substring.
The test fails the moment any future change — a debug `AddEvent`, a verbose error wrap, a
well-meaning "let's log the crawled page for once" — puts content back on a span, for any of
the five dependencies alike. The same sentinel is checked against the `ledger_run` document
`RecordDocument` writes, so a future field added to `LedgerEntry` that captures more than a
hash — by any of §2.1–§2.4's recorders — is caught before it reaches git.

**A residual risk, named rather than solved, and now shared by two adapters.** `openaiprov`'s
own `errorSnippetMaxBytes` (`client.go`) already truncates a non-2xx response body into an
error message; if a vendor's own error response happens to echo request content, that
snippet — bounded, but not redacted — reaches `err.Error()`, and from there a step's
`span.SetStatus(codes.Error, err.Error())` (`runner.go:69,104,110,116,122`). `crawlsource`'s
own error paths (`fmt.Errorf("%s: status %d: %w", ...)` in `source.go`, and the HTML-fetch
errors `chrome.render` produces) carry a target URL into an error message the same way, though
never a page's body. Both predate this design and are not solved by it; recorded here because
§"Redaction" asked what is enforced versus promised, and both are cases that are neither yet.

## 7. Budgets are explicitly not this epic

Measuring what was called and refusing to call more are different jobs with different failure
postures. This epic's posture is §4: ledger recording must never block a run. A budget's entire
point is the opposite — it must be allowed to block a run, deliberately, the way Epic 10's
allow/ask/deny already blocks a submission. Building both in one epic would mean one code path
serving two contradictory reliability rules. If a budget is ever wanted — over dollars, over
crawl request counts, over agent turns, or over calls to any dependency this design now
records — it is a policy decision over the roll-up this epic already records (`ledger_run`
rows queried by `Index.Find`, the same way Epic 10's rules read Epic 6's deal-breakers) —
sequenced after this epic, and after Epic 10 for the allow/ask/deny vocabulary it would reuse.
Nothing in this design forecloses it; nothing in this design builds it.

## Sequencing (proposed; the roadmap itself is not edited here)

Epic 14 needs `Provider` (Epic 2), `feedsource` (Epic 5) and `crawlsource` (Epic 8) to exist,
since §2.1–§2.2 attach to each of them by reading their real code — all three already built,
per §"What exists today." It does not need Epic 3, 7, 9, 10, 11, 12 or 13: nothing in §2–§3
reads a judgement, a decision, or a UI. Unlike Epic 12 ("needs only 4, and nothing from 5-11;
it can land any time afterward"), Epic 14 can land any time after 2, 5 and 8, including
*before* 3.

**"From day one" means before Epic 13 and before Epic 11's remaining stories, not just "soon."**
The human's own framing — a ledger from day one so later work is measured as it is built rather
than retrofitted — is a sequencing constraint, not only a design preference:

- **Before Epic 13.** §13.5 already names "per-run disclosure, computed from the actual prompt"
  and the egress table's `hosted` rule as things it wants but has not built
  (`docs/specs/2026-09-27-epic-13-web-ui.md`). This epic builds `IsHostedEndpoint` once (§2) and
  the per-call facts §5 hands to §13.5; building Epic 13's own version first would mean this
  epic either duplicating that check or refactoring Epic 13 to import it after the fact — the
  same "fix it once at the source" argument `../CLAUDE.md`'s Fix Placement section makes about
  restoring an invariant at its origin rather than teaching a later consumer to route around a
  gap in an earlier one.
- **Before Epic 11's remaining stories (11.2–11.4).** Epic 11's own design already found the
  coverage-0 gap this epic closes (§2.6, §"What exists today": `docs/notes/2026-09-24-epic-3-closing.md`,
  quoted by `docs/specs/2026-09-27-epic-11-tracking.md`) and left it unmarked. 11.4, the first
  calibration check, scores predicted probability against observed outcome — exactly the
  computation a coverage-0 judgement should not be allowed to silently enter. Landing this
  epic's `ZeroCoverageAnswers`/`TotalAnswers` marking before 11.4 is built means 11.4 can exclude
  those judgements from its very first calibration sample rather than being retrofitted to filter
  them out once a wrong number has already shipped.
- **Before Epic 9 and Epic 10 generate the traffic that most needs optimising.** Epic 9's
  tailoring (one CV variant and one cited letter per posting) and Epic 10's apply loop are, by
  the roadmap's own description, where per-posting model and crawl traffic multiplies past
  Epic 7's already-measured 200-role baseline. A ledger recording that traffic from its first
  call, rather than from whenever this epic happens to land, is the concrete meaning of "so we
  can look back at areas we can optimize" — there is nothing to look back at for calls made
  before the ledger existed.

Recommended slot: directly after Epic 8, in parallel with Epic 3 the way Epic 4 already runs in
parallel with it — and, on the current state of this worktree, immediately implementable, since
2, 5 and 8 are the only prerequisites and all three already exist. Epic 13 §13.5 and Epic 11's
remaining stories should each be re-read once this epic lands, for the two reasons above.

## Testing

No network, no engine, per the discipline every other epic's design already follows.

| Test | Catches |
|---|---|
| `Cost` table-driven over `(Usage, Price)` pairs, including `Known: false` | A priced call that should read 0 does not, or an unpriced call silently costs something |
| `PricingTable.Lookup` | The `provider:model` exact key, the `provider:*` fallback, and an unknown provider returning `Known: false` |
| `Ledger` under concurrent `Add` (`go test -race`) | A lost call from two goroutines writing to one run's ledger — relevant once a future step type parallelizes calls, not today |
| `Metered.Complete` against a stub `Provider` | One call adds exactly one `LedgerEntry`; a failing call adds one entry with `ErrorClass` set and zero cost, not none; the span gains one event per call, not an overwritten attribute, across three calls in one span |
| `RequestHash` for `Provider` | Identical `System`/`User`/`Schema` hash equal regardless of `Temperature`/`Seed`; a changed `Schema` alone changes the hash; the same text to two different `Model`s hashes equal, with `Provider`/`Model` distinguishing the rows |
| `Judge.Ask`'s `AnnotateLastCoverage` | A judgement whose every answer has `Coverage.Represented == 0` (with `Declared > 0`) marks its entry `ZeroCoverageAnswers == TotalAnswers`; a normal judgement marks `ZeroCoverageAnswers == 0`; a question with `Declared == 0` is not counted as zero-coverage |
| `crawlsource`'s per-target recording against a stub HTTP server | One entry per target, in `Pull`'s target order; a 304 response marks `Cached: true`; a forced 429-then-200 sequence marks `RetryCount == 1`; a disallowed-by-robots target's entry carries `ErrorClass == KindDisallowed` |
| `feedsource`'s per-`Pull` recording | One entry per `Pull`; a second `Pull` against an unchanged remote marks `Cached: true` via `git.NoErrAlreadyUpToDate` |
| `tools.HTTP`'s recording | One entry per `Invoke`; `RequestHash` differs for the same URL under `GET` versus `HEAD` |
| `MeteredAgent.Do` against a stub `Agent` | One entry per turn; `InputTokens`/`OutputTokens`/`USD` stay zero on success; a failed turn's entry carries `ErrorClass` |
| `Runner.WithLedger` | A run of N steps, calling M different dependencies a known number of times each, reports a `RunLedger` with exactly that many `LedgerEntry`s, tagged with the right `Dependency`/`Operation`; a run that fails on step 2 of 3 still reports step 1's entries |
| `RecordDocument` for `ledger_run`, against real `gitdocs`/`sqlindex` (mirrors `judgementrecord_test.go`) | The document round-trips, the index row's five fields are findable by `Index.Find`, and a failing `docs.Put` (a locked test repo) does not change `Run`'s own returned error |
| `TestLedgerNeverBlocksARun` | A `Ledger`/record path forced to fail — for any of the five dependencies — still returns the run's real state or error unchanged — the reliability posture of §4, asserted, not asserted-by-reading-the-code |
| `TestNoSpanOrRecordCarriesPromptText` | The sentinel-scanning test of §6: a canary string in a rendered prompt, a crawled page, an HTTP response, or an agent turn appears in no span attribute, no span event attribute, and no recorded `ledger_run` document |
| `IsHostedEndpoint` | `localhost`, `127.0.0.1`, `::1`, a bare hostname that resolves to a loopback address, and a real hosted URL, each classified correctly, called identically from `Metered` and from `crawlsource`'s recorder |
| `TestEmbeddedDefaultsCoverOnlyLocalEngines` | A hosted provider is never `Known: true` from the embedded table alone — a mutation adding one would be the exact regression §1 warns against |
| `TestOneCommitPerRun` | A run that makes N calls across every dependency still produces exactly one `docs.Put` for its ledger — the volume claim in §3 asserted, not asserted-by-reading-the-code |

## Deliberately not in this increment

| Out | Why |
|---|---|
| Budgets, limits, or any enforcement over spend or call count, for any dependency | §7. A policy decision with an opposite reliability posture, for a later epic |
| Pricing a crawl fetch, a git fetch, an HTTP fetch, or an agent turn in USD | §1: nobody bills atlas per request for these on the free-laptop-or-free-tier stack; only the model provider has a per-token price table |
| Pricing an agent's (`agent.do`) own model calls | Structurally invisible: an agent is a subprocess atlas hands a prompt to and cannot see inside, exactly as Epic 13.5 already found for its egress table |
| A CLI-side consent gate reading this epic's exposure facts | Epic 13.5's own "Not built" list already names this; this epic supplies the facts, not the gate |
| Per-run disclosure computed from the actual request content (what data class was sent) | Go must not know what a CV is (the vocabulary rule); `RequestHash` proves *which* content was sent, never *what kind* of content it was |
| Re-pricing a past run when `Spend.PricingPath` changes | The record is immutable, like every other document in it; a roll-up freezes the price it computed |
| A new generic reader tool for ledger records | `ports.Index.Find` already answers it since Epic 1; Epic 13's `index.find`/`docs.get` will surface it for free without this epic adding a duplicate |
| Costing a failed `Provider.Complete` call that still consumed input tokens on the vendor's side | `Completion` is zero-valued on error; there is no `Usage` to read. A known, named gap, not a silent one — narrower than the first draft's version of this row, since the failed call is now recorded (§2.1), just not priced |
| A retrying decorator over `Provider` | §2.1's `RetryCount` field is real and ready; the decorator that would make it non-zero is PR #52's `ports.ErrRejected` classification and whatever retry policy is built over it, referenced here and not designed |
| One `LedgerEntry` per crawl target as its own committed document | §3's volume analysis: a document per call turns a 200-role run into hundreds of commits; one document per run holds every entry instead |
| Retention, expiry, or cross-run aggregation of `ledger_run` documents | §3's volume analysis: no evidence yet that git history growth from one document per run needs bounding beyond what every other record kind already accepts; building an aggregation now means guessing at the shape a real need would actually want |
| A one-word cache field distinguishing "served from local disk cache" from "served after a revalidation round-trip" | `Cached` collapses both under one bool for now; both are "the network was not asked to resend the body," which is what the optimisation question actually wants to know |
| Redacting an error message that echoes a vendor's or a crawl target's own truncated response snippet | Named in §6 as a residual risk; fixing it is a change to `openaiprov`'s and `crawlsource`'s error paths, not to this epic's scope |
| A hosted price list embedded in the binary | §1: a fact that goes stale the moment it is written; the file is the update path |

## Open for the human

- **The pricing file's format** (JSON or YAML) is not fixed here. Packs already use YAML for
  everything hand-edited; a pricing table is also hand-edited (§1: "updating the table is
  editing that file"), which favors YAML for consistency, but nothing in this design depends on
  the choice.
- **Resolved by this widening: `RequestHash` covers `System`+`User`+`Schema`, not sampling, not
  model.** The first draft left this open ("whether `PromptHash` should also cover the
  schema"). §2.1 now answers it, with the reasoning that made the call: `Schema` changes what is
  being asked, so it is in; sampling parameters and the model do not, so they are out — see
  §2.1 for the full argument. This is worth reopening only if a real audit need surfaces a case
  the reasoning there does not cover.
- **Whether `Cached` should distinguish a local-cache hit from a network revalidation.** Both
  currently collapse to one bool (`crawlsource`'s conditional-GET 304 and `feedsource`'s
  `NoErrAlreadyUpToDate` both set it), on the reasoning that both answer "was the body resent."
  If a future question needs to tell them apart — e.g., "how often does the revalidation
  round-trip itself cost meaningful latency, even when nothing new comes back" — this is a
  one-field split, not a redesign.
- **Whether a sixth or later dependency (an email inbox source, an ATS API, a search API) gets
  a port-level decorator or adapter-internal recording** is decided the same way §2 decided it
  for the first five: by whether that dependency's own outbound method is 1:1 with one call, or
  fans out internally the way `crawlsource.Pull` does. This design states the rule rather than
  pre-building a decorator for a dependency that does not exist yet.
- **The epic number itself.** This design proposes 14, the next free number after the roadmap's
  fourteen (0-13). If another epic is inserted first, this one renumbers; nothing in the design
  depends on the number.
