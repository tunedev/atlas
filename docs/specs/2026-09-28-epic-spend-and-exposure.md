# Epic 14 — Spend and exposure — design

**Supersedes nothing.** This design proposes Epic 14 of `docs/plans/2026-09-17-roadmap.md`,
which the roadmap does not yet name. It sits under `docs/specs/2026-09-17-job-hunt-harness-design.md`
and rests on Epic 2's `Provider` (`docs/design/the-provider.md`'s stories, delivered as
`internal/core/ports/provider.go`), Epic 1's record (`Docs`/`Index`), and Epic 12's `Runner`
seam. It is written against code that exists today, not against a future increment.

**Goal:** two questions nothing in atlas answers today — what a run cost, and what left the
machine — answered from the record, without turning either measurement into a reason a run
can fail.

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

## Approaches considered

**Where to meter a call.**

| | Approach | Verdict |
|---|---|---|
| A | Teach every tool that calls `Provider` (`model.go`, `judge.go`, `extract.go`, `citejudge.go`, and whatever Epic 9/10 add) to record its own usage and cost | Rejected. This is the exact incident `../CLAUDE.md`'s "Fix placement" section warns about: it fixes today's four call sites and leaves every future one a landmine, and it is precisely how `judge.each` went unmeasured — `model.complete` was taught, nothing else was. |
| B | Decorate `ports.Provider` once, at `buildRegistry`'s one construction site, the same shape `Chain` already uses (a `Provider` implemented over another `Provider`) | **Chosen.** Every current and future consumer of `Provider` is metered without a line changing in `model.go`, `judge.go`, `extract.go`, or whichever tool Epic 9 or 10 adds. |
| C | Have `Runner.execStep` inspect a tool's returned `map[string]any` for a conventional `usage` key | Rejected. `execStep`'s own doc comment is "nothing here knows what any tool does"; reading a tool's result shape by convention breaks that, and it still only sees one number per step, not one per call inside `judge.each`'s loop. |

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

## 2. Metering every call, once

```go
// internal/core/app/meter.go

// CallSpend is what one Provider.Complete call cost, priced against the
// table in force when it was made.
type CallSpend struct {
    StepID, Tool               string
    Provider, Model             string
    InputTokens, OutputTokens   int
    USD                         float64
    Priced                      bool
    Hosted                      bool
    Endpoint                    string
    LatencyMS                   int64
    PromptHash                  string // sha256 of System+User, never the text itself
}

// Metered wraps a Provider, adding no behavior to Complete beyond recording
// what it cost: onto the run's Ledger, when the call's context carries one,
// and as a span event on whatever span is already open. It never changes
// what next returns.
type Metered struct {
    next     ports.Provider
    prices   PricingTable
    hosted   bool
    endpoint string
}

func NewMetered(next ports.Provider, prices PricingTable, hosted bool, endpoint string) *Metered {
    return &Metered{next: next, prices: prices, hosted: hosted, endpoint: endpoint}
}

func (m *Metered) Name() string { return m.next.Name() }

func (m *Metered) Complete(ctx context.Context, p ports.Prompt) (ports.Completion, error) {
    c, err := m.next.Complete(ctx, p)
    if err != nil {
        return c, err // no Usage on a failed call; see "Deliberately not in this increment"
    }
    price := m.prices.Lookup(m.next.Name(), c.Model)
    cs := CallSpend{
        StepID: stepIDFrom(ctx), Tool: toolFrom(ctx),
        Provider: m.next.Name(), Model: c.Model,
        InputTokens: c.Usage.PromptTokens, OutputTokens: c.Usage.CompletionTokens,
        USD: Cost(c.Usage, price), Priced: price.Known,
        Hosted: m.hosted, Endpoint: m.endpoint,
        LatencyMS: c.Latency.Milliseconds(),
        PromptHash: promptHash(p),
    }
    if l := ledgerFrom(ctx); l != nil {
        l.Add(cs)
    }
    trace.SpanFromContext(ctx).AddEvent("atlas.model.call", trace.WithAttributes(
        attribute.String("gen_ai.response.model", c.Model),
        attribute.Int("gen_ai.usage.input_tokens", c.Usage.PromptTokens),
        attribute.Int("gen_ai.usage.output_tokens", c.Usage.CompletionTokens),
        attribute.Int64("atlas.model.latency_ms", c.Latency.Milliseconds()),
        attribute.Float64("atlas.model.usd", cs.USD),
        attribute.Bool("atlas.model.priced", cs.Priced),
        attribute.Bool("atlas.model.hosted", cs.Hosted),
    ))
    return c, nil
}
```

**Why an event, not `SetAttributes`.** `tools.Model.Invoke` today calls `span.SetAttributes`
(model.go:46-51), which overwrites a key on repeated calls. That is harmless for
`model.complete` (one call per step) and wrong for `judge.each` (many calls, one span): the
second call would silently erase the first's numbers. `Metered.Complete` uses `AddEvent`
instead — one timestamped, independent record per call, on however many calls one step span
covers. `model.go`'s own `SetAttributes` call is deleted: `Metered` now sets these four
attributes for every call, `model.complete` included, so the tool no longer needs to.

**Wired once, at the one place `Provider` is built:**

```go
// cmd/atlas/main.go, buildRegistry
provider := openaiprov.New(openaiprov.Config{ /* unchanged */ })
metered := app.NewMetered(provider, pricingTable, app.IsHostedEndpoint(cfg.Model.BaseURL), cfg.Model.BaseURL)
judge := app.NewJudge(metered, app.JudgeConfig{ /* unchanged */ })
extractor := app.NewExtractor(metered, app.ExtractorConfig{ /* unchanged */ })
// tools.NewModel(metered) in place of tools.NewModel(provider)
```

Zero lines change in `judge.go`, `extract.go`, `model.go`, `each.go`, `citejudge.go`. A future
tool Epic 9 or 10 adds, built over `judge` or `extractor` or a fresh `tools.NewModel(metered)`
call, is metered from the day it is written, the same way it will be traced from the day it is
written — no one has to remember.

`app.IsHostedEndpoint` (a host/loopback check) is new in this epic and is exactly the rule
`docs/specs/2026-09-27-epic-13-web-ui.md` §13.5 already describes for its egress table
("`hosted`... true unless the host is `localhost` or resolves to a loopback IP") but does not
yet implement. This epic builds it once, in `internal/core/app`; Epic 13 should import it for
the egress table rather than reimplementing the same check a second time.

**An agent's calls are structurally invisible here, same as to Epic 13.5.** `agent.do` runs a
subprocess over ACP; it never calls `ports.Provider`. Nothing in this design can price or meter
what a configured agent sends — atlas cannot see inside it, the same finding §13.5 already
states for its egress table ("atlas cannot see where an agent sends what it is given"). The
roll-up records an agent step's wall-clock time (already on its span) and nothing about tokens
or cost. This is not a gap this epic leaves open by choice; it is what "an agent is a
subprocess we hand a prompt to" already means.

## 3. The per-run roll-up

```go
// internal/core/app/ledger.go

// Ledger accumulates one run's CallSpends as they happen. It is created
// fresh per Run call, never reused across runs and never shared as a
// Runner field, so two runs sharing a registry (Epic 13.1's server) never
// interleave.
type Ledger struct {
    mu    sync.Mutex
    calls []CallSpend
}

func (l *Ledger) Add(c CallSpend) { l.mu.Lock(); defer l.mu.Unlock(); l.calls = append(l.calls, c) }
func (l *Ledger) Snapshot() []CallSpend { l.mu.Lock(); defer l.mu.Unlock(); return append([]CallSpend{}, l.calls...) }

// RunSpend is every call a Ledger recorded over one Runner.Run.
type RunSpend struct {
    Blueprint string
    Calls     []CallSpend
}
```

**How the ledger reaches `Metered` without changing any tool's signature.** The same way a
span already does: `Run` attaches a fresh `Ledger` to the context it passes down, and
`execStep` attaches the current step's id and tool name alongside it, right where it already
opens that step's span:

```go
func (r *Runner) Run(ctx context.Context, b domain.Blueprint) (*domain.State, error) {
    ledger := &Ledger{}
    ctx = withLedger(ctx, ledger)
    ctx, span := r.tracer.Start(ctx, "blueprint."+b.Name)
    defer span.End()
    ...
    state, err := /* existing step loop, unchanged */
    r.spend(RunSpend{Blueprint: b.Name, Calls: ledger.Snapshot()})
    return state, err
}
```

`withLedger`/`ledgerFrom` are an unexported context key and accessor pair, exactly the
mechanism `otel` itself already uses to carry the active span through this codebase's own
`ctx` (`trace.SpanFromContext` in `model.go:45` reads back what `tracer.Start` put there). This
is not a new idiom for this codebase's `ctx`; it is the second use of the one already in it.
This also names the constraint plainly: a tool that calls `Provider` from a `context.Context`
not derived from the one `Invoke` received would go unmetered. Nothing does this today —
`judge.each`'s calls run one after another on the same context, a decision Epic 7 made for
measured throughput reasons, not for this one — but it is a real constraint on the mechanism,
not an oversight to paper over.

**`Runner` gains a spend sink, exactly as it gained progress in Epic 12.1:**

```go
// WithSpend returns a copy of the Runner that reports each run's totals to
// f once Run finishes, success or failure. The receiver is unchanged. f
// runs synchronously after every step has been attempted, so a run that
// fails partway is still reported with whatever it actually spent.
func (r *Runner) WithSpend(f func(RunSpend)) *Runner
```

Default: a no-op, so a caller that does not wire one pays nothing, the same guarantee
`WithProgress` already makes.

**Where it is recorded.** The composition root wires `WithSpend` to a function that writes a
document through `RecordDocument`, the same call `RecordJudgement` already makes:

```go
Document{
    Path:    fmt.Sprintf("spend/%s/%s.json", b.Blueprint, time.Now().UTC().Format(recordTimeFormat)),
    Body:    /* RunSpend, plus one Totals row per (provider, model) computed from Calls */,
    Message: "Record spend for " + b.Blueprint,
    Kind:    "spend_run",
    Fields:  map[string]string{
        "blueprint":       b.Blueprint,
        "total_usd":       fmt.Sprintf("%.4f", totalUSD),
        "unpriced_calls":  strconv.Itoa(unpricedCount),
        "hosted":          strconv.FormatBool(anyHosted),
    },
}
```

Git holds every call's full detail (provider, model, tokens, USD, priced, hosted, endpoint,
latency, prompt hash); the index row is the four flat fields above, queryable without reading
the repository — the same split `docs/design/the-record.md` already describes for a judgement.
A per-(provider, model) breakdown, and the local-engine time column, live in the document body;
"per what dimension" in the brief's own words is answered there, not by adding more index
fields no query needs.

**Where a user sees it.** Two places, neither of them a new UI:

- The CLI already prints one line per step as it runs (Epic 0.1's own acceptance). It gains one
  line after a run finishes: total USD, unpriced call count if any, and total model time,
  written from the same `RunSpend` `WithSpend` receives — no new print path.
- The record itself, queryable today: `ports.Index.Find(ctx, ports.Query{Kind: "spend_run",
  Match: map[string]string{"blueprint": "job-hunt"}})` answers "what has this pack cost me,"
  available since Epic 1, no new tool required. Once Epic 13's `index.find`/`docs.get`
  generic tools land, the same rows surface through them for free, the same way a judgement
  does — this design adds no reader of its own to duplicate that.

## 4. Reliability posture: spend is never load-bearing

The reference gap analysis this epic translates from (`qonstrue-guardrail-gaps.scratchpad.md`
gap 27: agent traffic partly unobserved because the recording path was allowed to matter) states
the rule this design follows: a cost ledger is best-effort and never blocks serving traffic.
Concretely:

- `Metered.Complete` never fails a call because pricing or recording had a problem — pricing is
  a pure function that cannot error (§1), and `Ledger.Add` cannot fail.
- The one place recording *can* fail is `WithSpend`'s function writing the roll-up document
  (`RecordDocument`'s `docs.Put`/`index.Upsert`, over a git repository and SQLite file that can
  themselves be full, locked, or briefly unavailable). That function logs the failure at `warn`
  and returns nothing; `Run`'s own return value — the state or the error the pack's steps
  actually produced — is entirely unaffected. A run that judged 190 of 200 postings and then
  failed to write its spend summary still returns its real result to the caller.
- Recording happens **after** every step has been attempted (success or failure), not only on a
  clean run, so a run that fails partway is not reported as free — it wasn't.

## 5. Exposure: what leaves the machine

**What Go can know without knowing what a CV is.** `internal/arch/vocabulary_test.go` forbids
use-case vocabulary in `internal/`; Go cannot classify a prompt's content. What it *can* state,
mechanically, on every call: which endpoint it went to, whether that endpoint is hosted
(§2's `IsHostedEndpoint`), the provider and model, and — via `PromptHash` (§2) — a SHA-256 over
the exact `System`+`User` text sent, the same technique `internal/core/app/fingerprint.go`
already uses to prove "was this exact text sent" without storing the text. That hash is what
this epic can offer as evidence; it cannot say "this contained a CV."

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

**The rule.** A span, a span event, a log line, or a recorded document produced by this epic
may carry: counts, durations, byte lengths, hashes, model/provider/endpoint identifiers, and
booleans. **Never prompt text, never completion text, never a field's raw value.** This is not
a new restriction — §"What exists today" found that nothing in `internal/` puts text on
telemetry today — it is the rule this design must not be the first thing to break, since
`CallSpend`/`RunSpend` are the first place a call's *content* (via `PromptHash`) is referenced
by this epic's own code, and a hash is the boundary that must hold.

**How it is enforced, not promised.** A test in `internal/core/app`, in the shape
`docs/specs/2026-09-27-epic-13-web-ui.md`'s own `TestNoResponseCarriesTheAPIKey` already uses
for a different secret: run a blueprint whose rendered prompt contains a sentinel string
(`"CANARY-38f2..."`, unique per test run) through a real `Runner` with a
`tracetest.NewInMemoryExporter` (`go.opentelemetry.io/otel/sdk/trace/tracetest`, already
available — `otel/sdk` is already a module dependency, no new one needed) in place of the OTLP
exporter, then scan every recorded span's attributes and every event's attributes, across every
span, for the sentinel substring. The test fails the moment any future change — a debug
`AddEvent`, a verbose error wrap, a well-meaning "let's log the prompt for once" — puts prompt
text back on a span. The same sentinel is checked against the spend document `RecordSpend`
writes, so a future field added to `CallSpend` that captures more than a hash is caught before
it reaches git.

**A residual risk, named rather than solved.** `openaiprov`'s own `errorSnippetMaxBytes`
(`client.go`) already truncates a non-2xx response body into an error message; if a vendor's
own error response happens to echo request content, that snippet — bounded, but not redacted —
reaches `err.Error()`, and from there a step's `span.SetStatus(codes.Error, err.Error())`
(`runner.go:69,104,110,116,122`). This predates this design and is not solved by it; it is
recorded here because §"Redaction" asked what is enforced versus promised, and this is a case
that is neither yet.

## 7. Budgets are explicitly not this epic

Measuring what was spent and refusing to spend more are different jobs with different failure
postures. This epic's posture is §4: spend recording must never block a run. A budget's entire
point is the opposite — it must be allowed to block a run, deliberately, the way Epic 10's
allow/ask/deny already blocks a submission. Building both in one epic would mean one code path
serving two contradictory reliability rules. If a budget is ever wanted, it is a policy decision
over the roll-up this epic already records (`spend_run` rows queried by `Index.Find`, the same
way Epic 10's rules read Epic 6's deal-breakers) — sequenced after this epic, and after Epic 10
for the allow/ask/deny vocabulary it would reuse. Nothing in this design forecloses it; nothing
in this design builds it.

## Sequencing (proposed; the roadmap itself is not edited here)

Epic 14 needs only Epic 2 (`Provider` exists to decorate). It does not need Epic 3, 7, 9, 10,
11, 12 or 13 — `Metered` wraps a port that exists after Epic 2 alone, and the roll-up uses
`Docs`/`Index` from Epic 1. Unlike Epic 12 ("needs only 4, and nothing from 5-11; it can land
any time afterward"), Epic 14 can land any time after 2, including *before* 3. Landing it before
Epic 7's board-scoring sees real traffic buys retroactive observability over the epic that
generates the most calls in the system, at zero cost to Epic 7's own design — `Metered` is a
composition-root change, transparent to every tool built on `Provider`. Recommended slot:
directly after Epic 2, in parallel with Epic 3 the way Epic 4 already runs in parallel with it.
Epic 13 §13.5 should be re-read once this lands, since `app.IsHostedEndpoint` (§2) is built here
first and 13's own egress table should import it rather than reimplement it.

## Testing

No network, no engine, per the discipline every other epic's design already follows.

| Test | Catches |
|---|---|
| `Cost` table-driven over `(Usage, Price)` pairs, including `Known: false` | A priced call that should read 0 does not, or an unpriced call silently costs something |
| `PricingTable.Lookup` | The `provider:model` exact key, the `provider:*` fallback, and an unknown provider returning `Known: false` |
| `Ledger` under concurrent `Add` (`go test -race`) | A lost call from two goroutines writing to one run's ledger — relevant once a future step type parallelizes calls, not today |
| `Metered.Complete` against a stub `Provider` | One call adds exactly one `CallSpend`; a failing call adds none; the span gains one event per call, not an overwritten attribute, across three calls in one span |
| `Runner.WithSpend` | A run of N steps, M of which call `Provider` a known number of times, reports a `RunSpend` with exactly that many `CallSpend`s; a run that fails on step 2 of 3 still reports step 1's calls |
| `RecordSpend` against real `gitdocs`/`sqlindex` (mirrors `judgementrecord_test.go`) | The document round-trips, the index row's four fields are findable by `Index.Find`, and a failing `docs.Put` (a locked test repo) does not change `Run`'s own returned error |
| `TestSpendNeverBlocksARun` | A `Ledger`/record path forced to fail still returns the run's real state or error unchanged — the reliability posture of §4, asserted, not asserted-by-reading-the-code |
| `TestNoSpanOrRecordCarriesPromptText` | The sentinel-scanning test of §6: a canary string in a rendered prompt appears in no span attribute, no span event attribute, and no recorded `spend_run` document |
| `IsHostedEndpoint` | `localhost`, `127.0.0.1`, `::1`, a bare hostname that resolves to a loopback address, and a real hosted URL, each classified correctly |
| `TestEmbeddedDefaultsCoverOnlyLocalEngines` | A hosted provider is never `Known: true` from the embedded table alone — a mutation adding one would be the exact regression §1 warns against |

## Deliberately not in this increment

| Out | Why |
|---|---|
| Budgets, limits, or any enforcement over spend | §7. A policy decision with an opposite reliability posture, for a later epic |
| Pricing an agent's (`agent.do`) calls | Structurally invisible: an agent is a subprocess atlas hands a prompt to and cannot see inside, exactly as Epic 13.5 already found for its egress table |
| A CLI-side consent gate reading this epic's exposure facts | Epic 13.5's own "Not built" list already names this; this epic supplies the facts, not the gate |
| Per-run disclosure computed from the actual prompt content (what data class was sent) | Go must not know what a CV is (the vocabulary rule); `PromptHash` proves *which* text was sent, never *what kind* of text it was |
| Re-pricing a past run when `Spend.PricingPath` changes | The record is immutable, like every other document in it; a roll-up freezes the price it computed |
| A new generic reader tool for spend records | `ports.Index.Find` already answers it since Epic 1; Epic 13's `index.find`/`docs.get` will surface it for free without this epic adding a duplicate |
| Costing a failed `Provider.Complete` call that still consumed input tokens on the vendor's side | `Completion` is zero-valued on error; there is no `Usage` to read. A known, named gap, not a silent one |
| Redacting an error message that echoes a vendor's own truncated response snippet | Named in §6 as a residual risk; fixing it is a change to `openaiprov`'s error path, not to this epic's scope |
| A hosted price list embedded in the binary | §1: a fact that goes stale the moment it is written; the file is the update path |

## Open for the human

- **The pricing file's format** (JSON or YAML) is not fixed here. Packs already use YAML for
  everything hand-edited; a pricing table is also hand-edited (§1: "updating the table is
  editing that file"), which favors YAML for consistency, but nothing in this design depends on
  the choice.
- **Whether `PromptHash` should also cover the schema** (as `app.Fingerprint` does for a
  judgement) is left open. A judgement's fingerprint includes the schema because it decides
  cache reuse; a spend record's hash exists only as evidence of what was sent, so the weaker
  form (System+User only) may be sufficient. Worth revisiting once a real audit need appears.
- **The epic number itself.** This design proposes 14, the next free number after the roadmap's
  fourteen (0-13). If another epic is inserted first, this one renumbers; nothing in the design
  depends on the number.
