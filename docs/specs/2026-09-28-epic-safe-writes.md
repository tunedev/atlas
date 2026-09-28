# Epic 14 — Safe writes — design

**Supersedes nothing.** This design closes three carried gaps, each named where it was
deferred: idempotency and the provider chain from increment 2
(`docs/notes/2026-09-21-increment-2.md`, `docs/design/the-provider.md`'s Known gaps),
expected-revision writes from `docs/design/the-record.md`'s Known gaps, and the
apply loop's dependence on the record staying correct under retry
(`docs/specs/2026-09-28-epic-10-apply-loop.md`).

**Why now, and why one epic.** All three gaps share a shape: a retry or a race can leave the
record wrong, silently. None was in scope when first noticed — increment 2 explicitly
deferred error classification "to the increment that gives the chain a production caller";
epic 10 shipped two new read-modify-write call sites (`policy.add`, `stage.declare`) without
revisiting the record's own known gap. Bundling them is not scope creep: fixing one without
the others leaves the record's safety story half told, and the code touched overlaps
(`internal/core/app/record.go` is the funnel for two of the three).

**Epic number and sequencing.** The roadmap (`docs/plans/2026-09-17-roadmap.md`) runs 0–13.
This is **Epic 14**. It needs Epic 1 (`Docs`/`Index`, `Put`/`Revision`/`History` already
exist), Epic 2 (`Provider`/`Chain`/`Breaker`, built and tested but unwired), and Epic 10
(`decision.record`, `policy.add`, `stage.declare` are its concrete call sites). It is
independent of 11, 12 and 13 and can land in parallel with any of them, any time after 10.

---

## Where a duplicate write is possible today

Every write in Atlas ultimately funnels through `app.RecordDocument`
(`internal/core/app/record.go:38`), which calls `ports.Docs.Put` then `ports.Index.Upsert`.
`Put` itself is already idempotent **per path**: a byte-identical body at a path that already
holds it is a no-op and returns the existing revision (`docs/design/the-record.md`, "A
byte-identical write is a no-op"). The gap is not in `Put` — it is in what decides the path.

| Call site | Path is | Duplicate-safe? | Why |
|---|---|---|---|
| `docs.put` (`tools/docs.go:42`) | Caller-supplied (`with["path"]`) | Yes, if the caller reuses a stable path | `Put`'s no-op rule applies directly; every existing pack (`tailored.json`, `stage.json`) already uses a fixed path per subject |
| `decision.record` (`tools/decision.go:40`, `app.RecordDecision`, `internal/core/app/decisionrecord.go:52`) | `decisions/<subject_id>/<time.Now() to the millisecond>.json` | **No** | `When` is captured fresh inside `Invoke` on every call. A retried call gets a new timestamp, hence a new path, hence a new document — `Put`'s no-op never triggers because the two writes never target the same path |
| `judge.ask` (`tools/judge.go:44`, `app.RecordJudgement` → `judgementPath`, `internal/core/app/judgementrecord.go:139`) | `judgements/<subject_id>/<j.When to the millisecond>.json`, `j.When` set by `Judge.Ask` (`internal/core/app/judge.go:81`) | **No** | Same shape as `decision.record`: no reuse check exists in this path at all |
| `judge.each` (`tools/each.go`, `app.RecordAssessedJudgement`) | Same `judgementPath`, but preceded by `t.reuse` (`tools/each.go:239`), an index lookup on `(subject_id, fingerprint)` | **Mostly** | `reuse` finds an already-recorded judgement by fingerprint before asking or recording, so a rerun of `judge.each` over an unchanged item is a no-op *by construction*. The lookup depends on the **index row**, which `RecordDocument`'s own doc comment admits can be missing after a successful `Put`: "A failed upsert after a successful put still returns the path". If `Upsert` fails after `Put` succeeds, `t.one` returns the error without ever reading back the path (`tools/each.go`, `path, err := app.RecordAssessedJudgement(...); if err != nil { return nil, false, err }`) — the git document exists, the index row does not, and a retry re-judges and re-records under a new timestamp |
| `policy.add` (`tools/suggest.go:97`) | Fixed (`profile/policy.json`), but read-modify-write: `readRules` (List+Get) then `RecordDocument` | N/A for duplication — **exposed to a lost-update race instead**, see next section |
| `stage.declare` (`app.DeclareStage`, `internal/core/app/stage.go:44`) | Fixed (`applications/<subject_id>/stage.json`), read-modify-write: `readStage` then `writeStage` | Declaring the *same* stage twice is already a documented no-op (`doc.Stage == s.Stage` check). A concurrent write to a *different* stage is the same lost-update race as `policy.add` |

**The realistic retry surface.** Epic 4 (`internal/adapters/inbound/mcpserve/server.go:58`)
serves tool calls over HTTP (`mcpserve.Listen`, `server.go:164`), bounded by
`cfg.Agent.TurnTimeout`/`CallTimeout`. An agent process (Claude Code via `claude-agent-acp`)
is a separate process talking to this endpoint over a real, if local, network connection —
Tenet 5's "the network is reliable" fallacy applies on localhost too. If the agent's HTTP
client times out waiting for a response that the server already committed, a client that
retries the call replays the same `with` map with nothing to make Atlas recognize it as a
repeat. `decision.record` and `judge.ask` have no defense against exactly this.

**Conclusion: `docs.put` needs no new idempotency work.** `decision.record` and `judge.ask`
do. `judge.each` needs one targeted fix for the index-miss window, not a new mechanism.

---

## 1. Idempotency

### `decision.record` and `judge.ask`

Add an optional idempotency key to both tools' `with` map. When supplied, the write's path is
derived from the key instead of wall-clock time; when absent, behavior is byte-for-byte what
it is today (unique path per call). This is additive — no existing pack changes behavior.

```go
// internal/core/app/decisionrecord.go
type Decision struct {
    SubjectID         string
    Choice            string
    Reason            string
    JudgementPath     string
    VerdictAtDecision string
    IdempotencyKey    string // empty: path keyed by When, as today
    When              time.Time
}
```

```go
func decisionPath(d Decision) string {
    if d.IdempotencyKey != "" {
        return fmt.Sprintf("decisions/%s/%s.json", d.SubjectID, keyDigest(d.IdempotencyKey))
    }
    return fmt.Sprintf("decisions/%s/%s.json", d.SubjectID, d.When.UTC().Format(recordTimeFormat))
}

// keyDigest hashes an idempotency key so an arbitrary caller-supplied string
// is always a safe, bounded-length path component.
func keyDigest(key string) string {
    sum := sha256.Sum256([]byte(key))
    return hex.EncodeToString(sum[:])
}
```

The same change applies to `judgementPath` (`internal/core/app/judgementrecord.go:139`) via an
`IdempotencyKey` field added to the tool's `with` map, read in `tools/judge.go`'s `Invoke` and
threaded into `RecordJudgement`.

**On a repeat:**

| Retry shape | Result |
|---|---|
| Same key, same content | `Put`'s existing no-op fires: same path, byte-identical body, existing revision returned, no new commit. **Silent success** — this is the retry case the surface above describes |
| Same key, different content | Not rejected. A new revision is recorded at the same path, exactly like any other repeated-path write already does elsewhere in this codebase (`docs/design/the-record.md` never treats "same path, different body" as an error). This is a deliberate non-goal — see the table at the end |
| No key supplied | Unchanged: always a new path, always a new document |

**Who supplies the key.** A pack step that wants retry safety derives one deterministically
from data already in its template scope (for example the source posting id plus the declared
choice); an agent retrying its own tool call is expected to reuse the same key across
attempts, the same discipline any HTTP client implementing idempotent retries already needs.
Atlas does not mint or remember keys on the caller's behalf — that would require session
state this design does not add.

### `judge.each`

`judge.each` already has a reuse check; it just does not survive the one window
`RecordDocument`'s own gap creates. `t.one` (`tools/each.go`) already computes `fp` via
`app.Fingerprint` (`internal/core/app/fingerprint.go:18`) *before* asking or recording. Using
`fp` as the judgement path's key — instead of `j.When` — means a retry after an index-miss
lands on the exact path the first attempt used, and `Put`'s no-op protects it, provided the
re-ask reproduces the same answer. It does: `Judge.Ask` pins temperature and seed
(`docs/design/the-judge.md`, "Sampling controls"), so "the same question against the same
engine gives the same answer" by design.

```go
// RecordAssessedJudgement gains a path override; JudgeEach is the one caller
// that has a fingerprint to give it.
func RecordAssessedJudgement(ctx context.Context, docs ports.Docs, index ports.Index,
    subjectID string, qs []ports.Question, j ports.Judgement, a Assessed) (string, error)
```

becomes keyed by `a.Fingerprint` when non-empty (which `judge.each` always supplies) and by
`j.When` when empty (the bare `judge.ask` path, which has no fingerprint and keeps today's
behavior unless it separately opts into the new `IdempotencyKey` field above). No change to
`judge.each`'s public shape or its pack-facing `with` map.

### `docs.put`

No change. Its path is already caller-supplied, and `Put`'s no-op rule already makes an exact
retry safe. Extending it with its own key would duplicate a property it already has.

---

## 2. Expected-revision writes

### Why only two call sites need this

`Docs.Get` returns `([]byte, error)` — no revision. A pack cannot express "write only if
nothing changed since I read" because there is no `docs.get` *tool* at all today (checked:
`internal/adapters/outbound/tools` has no `docs.get`; only `docs.put` is a generic pack
primitive). Read-modify-write only happens in Go, in two places, both shipped in Epic 10:

- `policy.add` (`tools/suggest.go:97`): reads `profile/policy.json` — **user-authored**,
  per `docs/specs/2026-09-28-epic-10-apply-loop.md`'s policy section — appends a rule, writes
  the whole document back.
- `stage.declare` (`app.DeclareStage`, `internal/core/app/stage.go:44`): reads
  `applications/<subject>/stage.json`, decides the next stage, writes it back.

Both are lost-update races today: if the file changes between the read and the write (a
person hand-editing `profile/policy.json` while `policy.promote.yaml` runs; two
`stage.declare` calls for the same subject close together), the second write silently
discards whatever the first one added, because plain `Put` only asks "does this path's HEAD
already hold this exact body" — never "has HEAD moved since I looked."

### The port addition

```go
// internal/core/ports/docs.go
type Docs interface {
    Put(ctx context.Context, path string, body []byte, message string) (Revision, error)
    // PutExpecting behaves like Put, but fails with ErrRevisionMismatch when
    // path's current revision is not exactly expect. A path with nothing
    // committed yet has current revision "" (Revision's zero value), so
    // expect="" asserts "create — path must not already exist." A body
    // byte-identical to what expect already holds is still a no-op, exactly
    // as Put.
    PutExpecting(ctx context.Context, path string, body []byte, message string, expect Revision) (Revision, error)
    Get(ctx context.Context, path string) ([]byte, error)
    List(ctx context.Context, prefix string) ([]string, error)
    History(ctx context.Context, path string) ([]DocMeta, error)
    GetAt(ctx context.Context, path string, rev Revision) ([]byte, error)
}

var ErrRevisionMismatch = errors.New("docs: current revision does not match expect")
```

`PutExpecting` is a new method, not a changed signature on `Put`. Grep shows exactly two
production call sites of `Put` (`app.RecordDocument`, `app.RecordAgentSession`) and one
production implementation (`gitdocs.Store`); changing `Put` itself would force every one of
those, plus every test fake, to pass an always-empty `expect` for no reason. Adding a sibling
method costs nothing at the fifteen-odd call sites that never need the safety property, keeps
`Put`'s existing no-op contract untouched, and gives `expect=""` an unambiguous meaning inside
the one method where checking is the entire point — no overloaded empty string.

`gitdocs.Store.PutExpecting` reuses the same "current revision" lookup `unchangedRevision`
already performs (`internal/adapters/outbound/gitdocs/repo.go`): resolve `path`'s current
revision from `History`, compare to `expect`, refuse before touching the working tree if they
differ, then fall through to exactly `Put`'s own write path.

A matching core helper:

```go
// internal/core/app/record.go
func RecordDocumentExpecting(ctx context.Context, docs ports.Docs, index ports.Index,
    d Document, expect ports.Revision) (ports.Revision, error)
```

mirrors `RecordDocument` but calls `docs.PutExpecting` instead of `docs.Put`.

### The caller's obligation, and what happens on mismatch

A caller doing a safe read-modify-write:

1. `history, err := docs.History(ctx, path)` — `expect := ""`; if `len(history) > 0`,
   `expect = history[0].Rev`.
2. Read the body (`Get`, or `GetAt(path, expect)` for the stricter read-exactly-what-you-
   assert discipline), compute the new body.
3. `RecordDocumentExpecting(ctx, docs, index, Document{...}, expect)`.

On `ErrRevisionMismatch`, `PutExpecting` returns before any git write, so
`RecordDocumentExpecting` returns before ever calling `index.Upsert` — **the index is
untouched**, exactly the same "nothing partially applied on this failure path" property
`RecordDocument` already has for its own Put-then-Upsert sequence. The caller (`policy.add`,
`stage.declare`) surfaces the error to whoever ran the tool; there is no auto-merge. A human
or an agent re-runs the tool, which re-reads the new HEAD and decides whether to reapply.

`policy.add` and `stage.declare` are updated to this pattern. `docs.put` is not — there is no
paired `docs.get` tool to source an `expect` from, so a pack cannot supply one yet.
Extending `docs.put` waits until a `docs.get` tool exists to read a revision from; see the
"deliberately not" table.

---

## 3. Error classification

### The sentinel

```go
// internal/core/ports/provider.go (beside Provider)
var ErrRejected = errors.New("provider: request rejected")
```

Core-owned, per Tenet 1 ("ports never speak protobuf," and by the same logic never speak
HTTP): `ports.ErrRejected` names a *kind* of failure, not a status code. The mapping from
status code to sentinel lives in the adapter.

### The mapping, and where it lives

`openaiprov.Client.Complete` (`internal/adapters/outbound/openaiprov/client.go:90`) currently
builds one plain `fmt.Errorf` for every non-2xx status, with no classification at all. It
gains:

```go
// rejectedStatus reports whether code names a malformed request that will
// fail identically against any OpenAI-compatible endpoint, never a
// provider-specific or transient condition.
func rejectedStatus(code int) bool {
    return code == http.StatusBadRequest || code == http.StatusUnprocessableEntity
}
```

| Status | Classification | Why |
|---|---|---|
| 400 Bad Request | `ErrRejected` | Malformed body or schema violation — the same request fails at every OpenAI-compatible endpoint, not just this one |
| 422 Unprocessable Entity | `ErrRejected` | Same shape as 400 for servers that use it for schema validation (vLLM/Ollama-compatible backends) |
| 401 Unauthorized, 403 Forbidden | plain error, counted as an ordinary `Failure()` | Provider-specific credential problem. A different provider in the chain likely has a valid key — trying it is not pointless |
| 404 Not Found | plain error, ordinary `Failure()` | Wrong endpoint or model name for *this* provider, not evidence the request itself is bad |
| 408 Request Timeout, 429 Too Many Requests | plain error, ordinary `Failure()` | Transient by definition — explicitly **not** `ErrRejected`. A provider that is rate-limiting should trip its own breaker so the chain moves on, not abort the whole call |
| 5xx | plain error, ordinary `Failure()` | Transient, provider-specific |

Only 400 and 422 name a defect in the request itself, reproducible against any provider that
speaks the same protocol; every other 4xx is either provider-specific (skip this provider,
try the next) or transient (let the breaker count it). This is the concrete answer to "which
deliberately do not" — 401/403/404/408/429 all stay ordinary failures.

`Client.Complete`'s error path becomes:

```go
if resp.StatusCode < 200 || resp.StatusCode > 299 {
    snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorSnippetMaxBytes))
    err := fmt.Errorf("openaiprov: %s returned status %d: %s", c.name, resp.StatusCode, snippet)
    if rejectedStatus(resp.StatusCode) {
        err = fmt.Errorf("%w: %w", err, ports.ErrRejected)
    }
    return ports.Completion{}, err
}
```

(Go 1.27: `%w` on more than one verb wraps both; `errors.Is(err, ports.ErrRejected)` holds.)

### Chain's behavior

`Chain.Complete` (`internal/core/app/chain.go:40`) already has the exact shape this needs —
the `ctx.Err() != nil` branch already "return immediately, untouched by breaker state, without
trying the next provider." `ErrRejected` gets identical treatment, for the same reason: the
client left, or the request was bad — in neither case did the *provider* fail, so it is unfair
and pointless to count it against that provider's breaker or to retry against a sibling that
will see the identical malformed request.

```go
completion, err := provider.Complete(ctx, p)
if err == nil {
    breaker.Success()
    return completion, nil
}
if ctx.Err() != nil {
    return ports.Completion{}, fmt.Errorf("chain: %s: %w", provider.Name(), ctx.Err())
}
if errors.Is(err, ports.ErrRejected) {
    return ports.Completion{}, fmt.Errorf("chain: %s: %w", provider.Name(), err)
}
breaker.Failure()
errs = append(errs, fmt.Sprintf("%s: %v", provider.Name(), err))
```

### Proof

`TestChainRejectedNeverOpensBreaker`: a stub `ports.Provider` returns an `ErrRejected`-wrapped
error on every call; `Chain` built with `threshold=1` (opens on the very first ordinary
failure); call `Complete` five times. Assert: every call returns the rejected error (never
"breaker open"), and a second stub provider appended to the chain is never called (`errors.Is`
short-circuits `Complete` before the loop reaches it). This is the test that fails the moment
a 4xx starts opening a breaker — today's `Chain.Complete` has no such branch, and this test
does not compile against it without the added `errors.Is` check having somewhere to live.

---

## 4. Wiring the chain for real

### What config a second provider needs

`cfg.Model` (`internal/config/config.go:46`) is explicitly "the one model provider atlas is
wired to." Rather than turning it into a list — which breaks every existing
`-model-base-url`/`-model-name` flag and `ATLAS_MODEL_*` env var — add one optional fallback,
same shape, same layering convention (`ATLAS_FALLBACK_MODEL_BASE_URL`,
`ATLAS_FALLBACK_MODEL_NAME`, `ATLAS_FALLBACK_MODEL_API_KEY` env-only per the existing secret
convention, plus matching flags), and a `ChainConfig` for the breaker:

```go
// internal/config/config.go
type Config struct {
    // ...
    Chain ChainConfig
}

type ChainConfig struct {
    Threshold int
    Cooldown  time.Duration
    // Fallbacks holds zero or one additional provider today; config
    // validation refuses more than one. A slice, not a single optional
    // ModelConfig, so buildRegistry can use range instead of a presence
    // check — see the AST guard note below.
    Fallbacks []ModelConfig
}
```

Deliberately one fallback, not an arbitrary list — the same scoping call increment 2 already
made for the chain itself ("multi-provider config is its own increment"). An arbitrary-N
list is revisited on evidence a second fallback is wanted, per the Forge's own refusal table
style.

### `buildRegistry`, and the one-provider case

`buildRegistry` is one of the seven functions `TestCompositionPassesNoLiterals`
(`cmd/atlas/main_test.go:226`) walks for `*ast.BasicLit` — **no literal of any kind**, not
even `""` or `0`, may appear in its body. The existing code already solves this once
(`startAgent`'s `if cfg.Agent.Tools != nil` uses the predeclared identifier `nil`, never a
literal); the chain wiring follows the same idiom, using `range` over a config-driven slice
instead of a literal-bearing length check:

```go
provider := openaiprov.New(openaiprov.Config{
    Name: cfg.Model.Name, BaseURL: cfg.Model.BaseURL, Model: cfg.Model.Name,
    APIKey: cfg.Model.APIKey, Timeout: cfg.Model.Timeout, MaxBytes: cfg.Model.MaxBytes,
})
providers := []ports.Provider{provider}
for _, fb := range cfg.Chain.Fallbacks {
    providers = append(providers, openaiprov.New(openaiprov.Config{
        Name: fb.Name, BaseURL: fb.BaseURL, Model: fb.Name,
        APIKey: fb.APIKey, Timeout: fb.Timeout, MaxBytes: fb.MaxBytes,
    }))
}
chain := app.NewChain(cfg.Chain.Threshold, cfg.Chain.Cooldown, providers...)
```

`cfg.Chain.Fallbacks []ModelConfig`, filled by at most one entry today (config validation
enforces the cap), keeps the guard test passing with zero new literals and reads as "zero or
more fallbacks," matching the slice-driven idiom the codebase already uses for `Vars` in
`PackConfig`.

`judge := app.NewJudge(chain, ...)` and `tools.NewModel(chain)` replace `provider` with
`chain` at exactly those two call sites — a drop-in substitution, since `Chain` "is a
`ports.Provider` itself" (`chain.go:16`). Nothing else in `buildRegistry` changes.

**With exactly one provider configured** (`cfg.Chain.Fallbacks` empty, the default and the
common case), `Chain.Complete` runs its loop once: `breaker.Allow()` is true (zero failures
recorded), `provider.Complete` is called exactly once, and on success it returns immediately.
This is the same single HTTP round trip `tools.NewModel(provider)` made directly before this
change, plus one in-memory mutex-guarded counter check (`Breaker.Allow`) — no added network
call, no added latency worth measuring, and no new failure mode: a failure with one provider
configured produces the same "chain: <name>: <err>" shape `Chain.Complete` already produces
when `errs` has exactly one entry. Reliability cannot regress because there is no second
provider to fail differently than the first already would have.

---

## 5. Dry run

**Out. Epic 10's refusal already covers the one external write that matters.** Dry run exists
to preview an action before an irreversible external effect. The only irreversible external
effect in Atlas is submitting an application, and Epic 10 refuses that unconditionally, at an
architecture-test-enforced boundary (`http.request` restricted to GET/HEAD, no tool may
advertise a write to a remote host) — a refusal that does not need a dry-run mode because
there is nothing to preview a dry run of; the action itself cannot happen.

Every write this epic touches (a decision, a judgement, a policy rule, a stage) is a commit to
the user's own local git repository. It is already cheap to inspect (`git log`, `git diff`)
and cheap to reverse (`git revert`) before anything leaves the machine — Atlas pushes nothing
on its own. A dry-run mode for these would add a parallel code path (build the document,
print it, don't commit) that protects against a risk `git`'s own history already covers for
free. Not proposed.

---

## Testing

| What | How |
|---|---|
| `decision.record` idempotency | Same key + same content twice → one document, one revision, second call's returned path equals the first's; same key + different content → two revisions at the same path, neither error nor silent loss (`History` shows both); no key → unchanged, two calls produce two documents |
| `judge.ask` idempotency | Same shape as `decision.record`, over `RecordJudgement`'s new key |
| `judge.each` fingerprint-keyed path | A judgement recorded, its index row then deleted directly (simulating a failed `Upsert`), `judge.each` rerun over the same item: one document exists at the fingerprint-derived path, not two |
| `PutExpecting` | Matching `expect` and changed body succeeds and returns a new revision; mismatched `expect` returns `ErrRevisionMismatch` and writes nothing (`List`/`Get` unchanged after); `expect=""` against an existing path fails; `expect=""` against an absent path creates it |
| `policy.add` race | Two `policy.add` calls built from the same starting revision; the second returns `ErrRevisionMismatch`; the index still names only the first rule |
| `stage.declare` race | Same shape as `policy.add`, over two different next stages from one starting stage |
| `openaiprov` status mapping | 400 and 422 wrap `ports.ErrRejected`; 401, 403, 404, 408, 429 and 500 do not |
| `Chain` on `ErrRejected` | `TestChainRejectedNeverOpensBreaker` (above): the breaker never opens, the error returned is the rejection, and a second configured provider is never called |
| `Chain` on an ordinary failure | Unchanged existing tests: breaker opens after `threshold`, next provider is tried |
| `buildRegistry`, one provider | `TestChainWithOneProviderMatchesDirectCall`: a stub provider wired through `cfg.Chain.Fallbacks == nil`; assert exactly one call reaches the stub per `Complete`, and the returned error on failure names only that provider |
| `buildRegistry`, two providers | A failing primary and a succeeding fallback via `cfg.Chain.Fallbacks`; the run succeeds, and the primary's breaker recorded the failure |
| `TestCompositionPassesNoLiterals` | Unchanged test, rerun — proves the chain-wiring diff introduced no literal into `buildRegistry` |

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| Idempotency for `docs.put` | Already has it, via `Put`'s existing no-op rule and a caller-supplied stable path |
| Rejecting a reused idempotency key with different content | No second call site asks for a conflict primitive; a new revision at the same path is already normal, undestructive behavior everywhere else in this codebase |
| A `docs.get` tool | Needed before `docs.put` could usefully accept an `expect` revision from a pack; a real gap, but its own increment |
| Expected-revision writes for `docs.put` | Blocked on the above |
| An arbitrary-length provider list / provider registry | One fallback covers the case in evidence; revisit if a second fallback is ever wanted |
| Retry-with-backoff inside `Chain` or the provider adapter | Tenet 3: exactly one layer retries, and nothing here identified where that layer should live yet — Chain trying the *next provider* is not the same as retrying the *same* one |
| Dry run for any write | Epic 10's refusal already makes the one irreversible write impossible; every other write is a cheap, local, revertible git commit |
| Purging or rewriting a decision/judgement history | Epic 6.6's own carried gap; unrelated to retry safety |

---

## Sequencing

Independent internally: idempotency (1), expected-revision writes (2) and error
classification plus chain wiring (3, 4) touch disjoint files (`record.go`/`decisionrecord.go`/
`judgementrecord.go`/`each.go`; `ports/docs.go`/`gitdocs`/`suggest.go`/`stage.go`;
`ports/provider.go`/`openaiprov`/`chain.go`/`config`/`main.go`) and can land as three separate
PRs in any order. Dry run needs no work, so it is a statement in this spec, not a story.
