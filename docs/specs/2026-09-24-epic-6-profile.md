# Epic 6 — Profile — design

**Supersedes nothing.** This design details Epic 6 of `docs/plans/2026-09-17-roadmap.md` and
sits under `docs/specs/2026-09-17-job-hunt-harness-design.md`, which it does not contradict.
It rests on Epic 1 (`Docs`/`Index`, `docs/design/the-record.md`) and reuses the typed-question
machinery Epic 3 built (`docs/design/the-judge.md`, `docs/specs/2026-09-22-judge-design.md`).

**Goal:** the user's own CV, preferences, deal-breakers, evidence and decisions become a
git-backed record the user can correct, without Epic 7's fit judgement — which consumes all
of it — needing to exist yet.

## The decision already taken, and why

CV ingest is atlas-local: extract with the `Provider` port and a JSON Schema, reusing the
`Judge`'s typed-question machinery rather than depending on the sibling `cana` document
service, whose `/extract` endpoint does not exist. Atlas therefore needs to get plain text
out of a PDF and a DOCX file itself, offline, for free, without a system binary if avoidable.

**Text extraction: `github.com/tsawler/tabula`.** Verified, not assumed: MIT-licensed,
`go.mod` module `github.com/tsawler/tabula`, builds pure Go with no cgo for PDF and DOCX
(confirmed against its README and `pkg.go.dev` listing), commits as recent as June 2026 across
160+ commits. One call handles both formats: `tabula.Open(path).Text()` returns `(string,
[]Warning, error)`, sniffing the format from the file itself. It transparently rebuilds a
PDF's cross-reference table when that is corrupt, and decrypts a PDF using the standard
security handler with an empty password — both realistic for a CV exported from an
unfamiliar tool.

**The honest caveat, and the named fallback.** Tabula's non-OCR path returns empty text for a
scanned, image-only PDF; its OCR path (`-tags ocr`) pulls in Tesseract, jbig2dec, openjpeg and
poppler-utils — system binaries, not pure Go. A born-digital CV (exported from Word, Google
Docs, LaTeX, or a PDF printer) is the overwhelming case for a tech CV and needs none of this.
Scanned CVs are out of scope for this increment (see **Deliberately not in this increment**);
if tabula's layout handling proves too lossy on a real CV, the honest fallback is shelling out
to `pdftotext` (poppler-utils) the way Epic 8 shells out to the user's own Chrome via
`launcher.LookPath()` rather than bundling one — look for a binary that is already there,
never install one. Neither is needed to start.

## The vocabulary tension — read this before the rest

`internal/arch/vocabulary_test.go` forbids use-case vocabulary anywhere under `internal/` or
`cmd/`, test fixtures included, and the global constraint is stronger than the enumerated list:
**nothing in the Go tree knows what a job posting is**, and by the same reasoning, nothing in
the Go tree should know what a CV, an employer, a salary floor or an on-call rotation is.
Epic 6's own story language — "CV", "roles", "salary floor", "deal-breakers" — is entirely
use-case vocabulary. This is a genuine strain, worse than any prior epic's, because a profile
*is* the user's job-hunt-specific data, not a generic capability like storage or judgement.

This spec resolves it the way the codebase already resolves it for `judge.ask`'s questions:
**the schema is data, not code.** Every job-hunt-specific word — `employer`, `title`, `salary`,
`on_call`, `deal_breaker` — appears only inside JSON Schemas, JSON documents and pack YAML,
all of which live in `packs/` or in the user's own record repository, never as a Go identifier,
constant or string literal in `internal/` or `cmd/`. The Go tree contributes a small set of
generic, pack-agnostic primitives, named the way `judgement`, `subject`, `question` and
`answer` already are:

| Go-tree word | What it means generically | Why it is not use-case vocabulary |
|---|---|---|
| `profile` | A document the record holds under a fixed path, alongside `judgements/` | As generic as "judgement" — any pack could keep a profile |
| `evidence` | A citable source document | Generic evidentiary term, not job-hunt-specific |
| `decision` | A labelled choice about a subject | Any pack scoring subjects can log a decision |
| `claim` (used only in prose here, not as a Go identifier — see below) | A statement with a supporting quote | — |
| `quote` | A verbatim excerpt, as a JSON object key | Structural convention, like `options`/`levels` already are for `judge.ask` |

**This is a judgement call, not something the current test enforces.** `vocabulary_test.go`'s
forbidden list does not yet contain `cv`, `resume`, `salary`, `deal-breaker` or `dealbreaker`;
nothing mechanically stops a future contributor from typing `SalaryFloor` into a Go struct.
Flagged here as a real finding: **the vocabulary test should grow these words into its
forbidden list as part of implementing this epic**, and the boundary in the table above should
be ratified (or overruled) by whoever writes the implementation plan, not silently assumed.

## The profile's shape

The profile is five documents, not one. Each is either structured JSON (comparable,
queryable) or prose (citable, not parsed). Nothing here is a Go struct — Go never names these
fields — so the schemas below are the concrete artifact this story asks for.

| Document | Shape | Authored by |
|---|---|---|
| Structured history | JSON, schema below | Ingest (6.1), then corrected by hand (6.2) |
| Preferences | JSON, loose scalar fields | The user, directly |
| Deal-breakers | JSON, schema below | The user, directly |
| Evidence corpus | Prose, one file per item | The user, copied or pasted in |
| Decision log | JSON, one file per decision | Written by a tool, never hand-edited |

### Structured history — the CV ingest target

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["entries"],
  "additionalProperties": false,
  "properties": {
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["employer", "title", "start", "claims", "quote"],
        "additionalProperties": false,
        "properties": {
          "employer": { "type": "string" },
          "title":    { "type": "string" },
          "start":    { "type": "string", "pattern": "^[0-9]{4}(-[0-9]{2})?$" },
          "end":      { "type": ["string", "null"], "pattern": "^[0-9]{4}(-[0-9]{2})?$" },
          "ongoing":  { "type": "boolean" },
          "quote":    { "type": "string", "description": "Verbatim substring of the source CV text naming this employer and title." },
          "claims": {
            "type": "array",
            "items": {
              "type": "object",
              "required": ["text", "quote"],
              "additionalProperties": false,
              "properties": {
                "text":  { "type": "string" },
                "quote": { "type": "string", "description": "Verbatim substring of the source CV text supporting text." }
              }
            }
          }
        }
      }
    }
  }
}
```

Every `quote` field is load-bearing: it is how the design defends against invention (see
**Ingest, end to end**). This schema is authored inline in a pack, as a YAML block scalar, the
same textual-block shape `judge.ask`'s `questions:` block already uses — no new pack-file
mechanism is needed.

### Preferences — loose, user-authored

```json
{
  "goals": "Senior backend role, systems with real scale, staying hands-on.",
  "salary_floor": 145000,
  "currency": "USD",
  "locations": ["Remote", "Berlin"],
  "remote_only": false
}
```

No schema is enforced here beyond "a JSON object of scalars and string arrays" — preferences
vary too much per person to justify a fixed shape, and nothing downstream needs one yet.

### Deal-breakers

```json
{
  "rules": [
    {
      "id": "salary-floor",
      "statement": "Base salary must be at least $145,000.",
      "kind": "comparable",
      "op": ">=",
      "value": 145000
    },
    {
      "id": "no-on-call",
      "statement": "No on-call or pager rotation.",
      "kind": "judged",
      "threshold": 0.6
    }
  ]
}
```

`kind` is declared by the user, never inferred — see **Deal-breakers** below for why.

### Evidence corpus

One file per item, prose, under `profile/evidence/`. No schema: a past write-up or review is
quoted, not parsed, so structuring it would throw away exactly the wording that makes it
citable.

### Decision log

```json
{
  "subject_id": "greenhouse:gitlab:12345",
  "decision": "apply",
  "when": "2026-09-24T10:03:00.000Z",
  "reason": "Good comp, remote, matches backend focus.",
  "judgement_path": "judgements/greenhouse:gitlab:12345/2026-09-24T09-55-00.000Z.json",
  "verdict_at_decision": "stretch"
}
```

`decision` and `verdict_at_decision` are free strings a pack defines the meaning of (`apply`,
`skip`, or whatever a later pack chooses) — Go never enum-constrains them, the same way
`ports.Answer.Chosen` is a free string today.

## Where each part lives

All of it lives in the **same local record repository** `Docs`/`Index` already serve — never
in the postings feed repository (Epic 5), which the app only ever pulls from, and which
Epic 5's story 5.8 already asserts holds postings and nothing else. Nothing in this epic gives
the app a reason to write to that repository, and nothing here changes that.

| Path | Kind (index) | Written by |
|---|---|---|
| `profile/history.json` | `profile` | Ingest (6.1), then corrections (6.2) |
| `profile/preferences.json` | `profile` | The user, directly |
| `profile/dealbreakers.json` | `profile` | The user, directly |
| `profile/source/<filename>` | `profile` | Ingest, once, kept for re-extraction |
| `profile/evidence/<slug>.md` | `evidence` | The user, as items are added |
| `decisions/<subject-id>/<timestamp>.json` | `decision` | A tool, per apply/skip |

**Indexed** (`sqlindex`, path-keyed, per `docs/design/the-record.md`): one row per profile
document (`kind=profile`, a `section` field of `history`/`preferences`/`dealbreakers`/`source`,
and for `history`, a `needs_review` field of `"true"`/`"false"` — see **Ingest**); one row per
evidence file (`kind=evidence`, whatever tag the user gave it); one row per decision
(`kind=decision`, `subject_id`, `decision`, `verdict_at_decision`). This is enough to answer
"does a profile exist", "is anything in it flagged", and "what did the user decide about
subject X" without reading the repository — exactly what `Index` is for.

**Document-only:** every claim's text and quote, every preference value, every deal-breaker's
statement, the full evidence prose. `Index.Query`'s `Match` is exact-string-per-field with no
nesting, so anything that needs to be read in full or matched on content stays in the document,
consistent with how a judgement's `Distribution` never appears in its index row.

**Not indexed at all, deliberately:** profile revision history. `duckindex` (revision-keyed)
buys queries that scan history; nothing in this epic asks "how has the salary floor changed
over six months," so wiring it here would be a port used because it exists, not because a
query needs it.

## Corrections as commits (6.2)

There is no UI yet (Epic 12) and no CLI verb beyond "run a pack" (`cmd/atlas/main.go` only
ever does `packfile.Load` → `runner.Run`). The correction act is therefore:

1. The user opens `<store.root>/profile/history.json` — a real file in a real git working
   tree, since `gitdocs`'s `Put` writes the worktree before it commits — in their own editor
   and changes it.
2. They run a small pack (e.g. `packs/record-commit.yaml`, generic, not job-hunt-specific)
   whose steps are `file.read` (reads that path's current bytes off disk) then `docs.put`
   (commits those bytes as a new revision, with a message the user supplies as a pack var).

`Docs.Put` already guarantees the rest: a byte-identical save is a no-op (`docs/design/the-record.md`,
"a byte-identical write is a no-op"), a real change becomes a new revision, and nothing
already committed is overwritten in place. **Retrieving the original needs no new tool at
all** — it is a real git repository, so `git log -p profile/history.json` or `git show
<rev>:profile/history.json`, run directly by the user in that directory, already answers it.
Atlas only needs to own the *write* path, because that is the one operation with an invariant
(`Docs`'s no-overwrite guarantee, and the index staying in step) worth protecting in code.

This means 6.2 needs exactly one new generic tool beyond what Epic 1 built: `file.read`
(bounded, local, `os.ReadFile` under a configured max size) plus a thin `docs.put` tool
wrapping `ports.Docs.Put` — both pack-agnostic, both usable by any future pack that needs to
commit an edited file, not only this one.

## Ingest, end to end (6.1)

```
file.text(cv_path)  --text-->  extract.run(text, schema)  --fields+status-->  docs.put(profile/history.json)
```

1. **`file.text`** — a new tool, `internal/adapters/outbound/tools`, wrapping `tabula.Open`
   directly (no port: see below). Returns `{"text": "...", "warnings": [...]}`. `warnings` is
   tabula's own `[]Warning`, stringified — a page whose cross-reference table needed rebuilding
   still yields text, but says so, the same "never fail silently" instinct as Epic 5's
   "needs rendering" and Epic 8's staleness reporting.

2. **`extract.run`** — a new tool wrapping the new `ports.Extractor` port:

   ```go
   // Extractor turns text into a value shaped by schema, a JSON Schema.
   type Extractor interface {
       Extract(ctx context.Context, text string, schema []byte) (json.RawMessage, error)
   }
   ```

   A port, not a bare function, because a second implementation is nameable today, not
   hypothetically: `cana`'s `/extract` endpoint, once it exists, is exactly this operation
   over HTTP instead of over `Provider`. This is the same shape as `Judge`'s
   local-versus-TypeSafe split — a dedicated interface for "text plus schema in, structured
   value out" earns its place the same way a dedicated interface for "questions plus subject
   in, typed answers out" already did, rather than treating it as one more thing reachable
   through the generic `http.request` tool.

   The local implementation lives in `internal/core/app`, beside `Judge`, because it composes
   only `Provider` (core-over-core, no I/O of its own):

   ```go
   type ExtractorConfig struct {
       Temperature float64
       MaxTokens   int
   }

   type Extractor struct {
       provider ports.Provider
       cfg      ExtractorConfig
   }

   func NewExtractor(p ports.Provider, cfg ExtractorConfig) *Extractor
   func (e *Extractor) Extract(ctx context.Context, text string, schema []byte) (json.RawMessage, error)
   ```

   `Extract` sends one `ports.Prompt` with `Schema` set to the caller's schema and
   `Temperature` pinned to 0 (same CV, same structured output, which is what makes the
   grounding tests in **Testing** meaningful), a system message instructing the model to
   extract only what the text states and to give a verbatim quote for anything it claims, and
   parses the reply as JSON. No logprobs, no answer-token walking — unlike `Judge`, nothing
   here reads probability mass, so none of Epic 3's token-locating machinery is reused; only
   its typed-question mechanism (below) is.

3. **Grounding — the anti-invention defence.** `app.Ground`, a pure function beside `Judge`'s
   own helpers, no I/O, no model:

   ```go
   // Ground reports, as a JSON Pointer per field, every "quote" value in
   // extracted that is not a verbatim (case-insensitive, whitespace-normalised)
   // substring of source. It knows nothing about what a "quote" belongs to;
   // it only looks for the key.
   func Ground(extracted json.RawMessage, source string) ([]string, error)
   ```

   Because `quote` is a **required** schema property, an invented role cannot omit it — the
   model must either fabricate quote text (which, having no basis in the real CV, mechanically
   fails the substring check) or fail schema validation outright. `extract.run`'s `Invoke`
   calls `Extract`, then `Ground`, then **annotates**: it walks the same tree and adds a
   sibling `"status": "grounded"` or `"status": "needs_review"` next to every quote-bearing
   object, so the pack never has to do JSON surgery through Go templates — a purely structural
   operation, no job-hunt vocabulary, on the same "add a sibling key next to `quote`"
   convention. The tool returns `{"fields": <annotated JSON>, "needs_review": <count>}`.

   A `needs_review` claim is **kept, not dropped** — a false negative from an over-strict
   substring check (odd whitespace, a hyphenated line break in the PDF) would silently erase a
   real job otherwise, which is a worse failure than an over-cautious flag. It surfaces exactly
   the way Epic 9.3 already plans for an untraceable claim: shown as a gap, not written as
   fact, until the user corrects it (6.2) or leaves it, in which case downstream consumers
   (Epic 7, Epic 9) see `needs_review` and can refuse to build on it.

4. **`docs.put`** commits the annotated result to `profile/history.json`, exactly as a
   correction would (step 2 above) — ingest and correction share the same write path by
   design; ingest is simply the first correction.

**A concrete gap this spec does not resolve:** piping `extract.run`'s structured `fields`
result into `docs.put`'s `body` needs either a template helper that serialises a step's
output back to JSON text, or a `with` value that can reference a prior step's raw output
rather than only a rendered string. `internal/core/domain.Step.With` is `map[string]string`
today, and neither exists. This is mechanical, not a design fork, but it is real work the
implementation plan must account for.

**The Judge is available for a second, semantic layer**, not required for v1: for a claim
that passes the verbatim check but might still overstate what the quote supports (a real
paraphrase risk substring-matching cannot catch), the existing `judge.ask` tool can ask a
`noul` — "Given the quote, is the claim a fair characterisation, not an exaggeration?" — the
same mechanism story 6.1 was told to reuse. Whether this runs on every claim, a sample, or not
at all in v1 is a cost/quality call for the implementation plan, not this spec.

## Deal-breakers (6.3)

| `kind` | Evaluated by | Shape | Example |
|---|---|---|---|
| `comparable` | Code | `op` + `value`, compared against a number or string Epic 7 reads off a scored posting | Salary floor, a location match |
| `judged` | The model, via `Judge` | `statement` (prose) + an optional `threshold` (default 0.5) | "No on-call", "no return-to-office mandate" |

**Classification is the user's own act, not inferred by code or the model.** Reading
"no on-call" as a comparison would need a code path that does not exist and should not:
whether a role has on-call is not a field any posting reliably states as data. Reading a
salary floor as "judged" would waste a model call on an exact arithmetic comparison a
`>=` already answers deterministically, and — more importantly — would make the deal-breaker's
firing depend on a probability threshold where the user expects an exact line. Epic 6 stores
the shape and the classification; **Epic 7 is where either kind actually runs** — a
`comparable` rule as a pure comparison function, a `judged` rule as one more `noul` folded into
the same one-call `judge.ask` batch Epic 3 already does for a posting, so a deal-breaker check
costs no extra round trip. This spec settles the taxonomy and where each kind's logic will
live; it does not implement the evaluation, which is explicitly Epic 7's (roadmap: "7.2 A
tripped deal-breaker is named — not 'not a fit' but which rule fired").

## The evidence corpus (6.4)

Plain files, plain indexing, no extraction pipeline: `docs.put` under `profile/evidence/`, one
row per file in the index (`kind=evidence`) carrying whatever tag the user gave it as a flat
field. There is no grounding concern here — these are pre-existing artefacts (a letter, a
write-up, a review) being stored, not claims being derived from a source, so nothing about
Epic 6 needs to verify them against anything. Finding a *specific* quote inside the corpus at
tailoring time is Epic 9's problem, not this one's; Epic 6 only has to make sure the corpus is
committed and listed.

## The decision log (6.5), and how it connects to Epic 3

**It does not duplicate `RecordJudgement`; it points at it.** A judgement (Epic 3) is
recorded whenever `judge.ask` runs, keyed by subject, with an `outcome` field that starts
`null` and is filled only much later, by Epic 11, with a real-world result (interview, offer,
rejection). A decision (Epic 6) is a different event entirely: the user's own act of choosing
to apply or skip, which may happen with no judgement at all (a manual application), may
happen well after a judgement, and is never itself the "outcome" — it is upstream of it.

```go
// Decision is one apply/skip choice about a subject. Choice is a free string
// a pack defines the meaning of. JudgementPath and VerdictAtDecision are
// empty when no judgement preceded the decision.
type Decision struct {
    SubjectID         string
    Choice            string
    Reason            string
    JudgementPath     string
    VerdictAtDecision string
    When              time.Time
}

func RecordDecision(ctx context.Context, docs ports.Docs, index ports.Index, d Decision) (string, error)
```

`RecordDecision` mirrors `RecordJudgement`'s shape exactly (`decisions/<subject>/<timestamp>.json`
via `Docs`, one index row via `Index`, `kind=decision`) but is its own function, because a
`Decision` is not a `Judgement` — it carries no `Answers`, no `Sampling`, nothing Epic 3 owns.
`VerdictAtDecision` is a snapshot, taken at write time from the judgement's own `Chosen`
verdict (if one exists), so a later reader can see the user overrode the model — Epic 7.3's
"the user overriding a verdict is training data" — without re-reading a judgement document
that has since had its `outcome` filled in and might, in principle, be read differently later.
The **outcome itself never appears in `Decision`**; only the judgement document owns that
slot, exactly as Epic 3 designed it. `RecordJudgement` and `RecordDecision` sharing an
identical write shape (subject-keyed document plus a flat index row) is a real pattern the two
epics now have in common — worth a shared helper if a third case shows up, not worth
extracting for two.

## The record versus the rendered artifact (deferred to 9.5)

**The record holds structured history and text. A rendered document is a build artifact,
not a record.** `profile/history.json` (6.1) and the evidence corpus (6.4) are the record;
a PDF produced from either is something derived from them, on demand, never itself the
thing committed as the profile. Git holding diffable text keeps a tailored CV or letter
reviewable as a diff between one application and the next — two paragraphs changed for two
different postings should show up as two changed lines, not two opaque binary blobs. This
also softens, without resolving, the harness spec's open question about binary artifacts in
git (`docs/specs/2026-09-17-job-hunt-harness-design.md`, "Binary artifacts in git"): if
Epic 9 never commits the rendered PDF at all, that question narrows to whatever it produces
as a *build output* (regenerable, arguably untracked) rather than as part of the record
itself.

**The direction Epic 9 should inherit — a direction, not a decision here:** content as
data, markup as a template rendering that data, and the renderer as a swappable step behind
it. Concretely, a CV variant's content lives in structured JSON (drawn from
`profile/history.json`), a template turns that JSON into markup (Typst source, LaTeX, or
similar), and a renderer turns that markup into the final document. The renderer is a
natural `Converter` pair — markup format in, PDF out — in the shape the sibling `cana`
service already defines: `cana`'s `Converter` interface names the `Pair` of formats it
serves (`Pair() Pair`) and streams `src` to `dst` (`Convert(ctx, dst io.Writer, src
io.Reader) error`), with no format arguments on the method itself and no per-format
identifier; a registry indexes implementations by `Pair`, and a `Chain` composes two
converters into a third so the registry cannot tell a chained conversion from a direct one.
That port is a plausible long-term home for the markup-to-PDF step once `cana` has a
conversion surface at all — **today it does not**: `cana` is scaffolding, `canad` resolves
config and serves `/ops/config`, and no `Converter` is registered or wired for any pair.
Until then, the realistic path is a local single-binary renderer invoked directly, the same
"look for a binary that is already there" instinct this spec already applies to
`pdftotext`.

**Why the source of truth is the structured history, not the markup.** Story 9.1 wants "a
CV variant per role: drawn from the structured history, ordered for this posting" — that is
a data operation (select and reorder entries from `profile/history.json`), not a markup
operation. If the record were the markup file instead, producing a variant would mean a
model editing LaTeX or Typst source directly, and a model editing markup plumbing (a stray
brace, an unbalanced environment, a broken template include) can produce a document that
still renders — silently wrong, not loudly broken — which is a strictly worse failure mode
than a data-shape error would be.

**The toolchain constraint, factually, without choosing.** A full TeX distribution is a
multi-gigabyte install, which fails this project's "installed by a friend" bar outright; the
plane test in `../CLAUDE.md` needs a renderer that is one binary, works offline, and needs
no separate install step. Two realistic single-binary candidates exist, named here without
choosing between them:

- **Typst** — a single self-contained CLI binary (on the order of 15 MB), needing no TeX
  installation, whose core compilation works fully offline. Third-party packages it
  references are fetched over the network on first use and then cached locally, so a
  document using only the built-in library compiles offline from a cold start, and one
  using packages needs one prior online run (or a pre-populated cache) to be reproducible
  offline afterward.
- **Tectonic** — also a single self-contained binary (a modernized TeX/LaTeX engine over
  XeTeX and TeXLive), but by default it resolves the packages a document needs from a
  network-hosted "bundle" (a zip of TeX Live files) the first time each one is used, rather
  than requiring a preinstalled TeX tree. Fully offline operation is possible but means
  pointing it at a local bundle explicitly, not the out-of-the-box behaviour.

(Verified by searching current documentation and community sources for each project's
distribution model and offline behaviour, rather than asserting from training memory; not
verified against a real tailored-output test, which is what 9.5 should do before choosing.)
Epic 9 story 9.5 is where either is actually picked, in front of real tailored output, not
here.

**One consequence for Epic 6 itself.** Whatever the ingest path stores must not assume a
PDF is the artifact of record. Checked against this spec: `profile/source/<filename>`
(**Where each part lives**) stores the user's *uploaded* CV — ingest's input, kept for
re-extraction — never a rendered output, so nothing in this spec currently treats a PDF as
the record. This paragraph exists to keep it that way as Epic 9 is built: the record stays
`profile/history.json` and the evidence corpus, and any future rendered document, of any
format, is a build artifact derived from them, not a replacement for them.

## Testing (including offline, no model)

| What | How | Needs a model? |
|---|---|---|
| `Ground` | Table-driven: a grounded quote passes; a fabricated quote (present nowhere in source) fails; nested arrays; a missing `quote` key is skipped, not an error; whitespace/case normalisation | No |
| `file.text` over tabula | Fixture PDF and DOCX files under `testdata/`, asserting known text comes back, and that a corrupt fixture reports a warning rather than silence | No |
| `app.Extractor` | A stub `ports.Provider` (the same pattern `judge_test.go` uses), asserting `Prompt.Schema` carries the caller's schema on the wire, and that a non-JSON completion is an error, never a best-effort parse | No |
| The ingest pipeline end to end | A canned stub completion containing one grounded claim and one fabricated one (its quote absent from the fixture source text), asserted through `Extract` → `Ground` → annotate, checking the fabricated claim lands `needs_review` and the real one does not | No — this is the epic's central regression test, and it must never need one |
| `RecordDecision` | Against the real `gitdocs`/`sqlindex` adapters, mirroring `judgementrecord_test.go`: the document and index row both land, and a second decision for the same subject does not overwrite the first | No |
| A real engine actually honouring the extraction schema | One test gated on `ATLAS_LIVE_PROVIDER`, mirroring story 3.3's live constrained-decoding proof | Yes, deliberately, and skipped by default |

Story 3.6's "offline" bar is met the same way Epic 3 met it: everything above the live-gated
row runs with no network, because the model is a stub everywhere the model's actual output
would otherwise be the thing under test.

## Deliberately not in this increment

| Out | Why |
|---|---|
| The `cana` `/extract` adapter | Second `Extractor` implementation; the endpoint does not exist yet. The port is the seam it lands behind, not a promise it lands now |
| OCR for scanned CVs | Needs system binaries (Tesseract, poppler-utils) via tabula's `-tags ocr` build; out until a real scanned CV is in front of us |
| Shelling out to `pdftotext` | Only worth adding once tabula's pure-Go extraction is measured insufficient on a real CV, not before |
| Automatic classification of a deal-breaker as `comparable` or `judged` | Misclassifying one silently disables it; the user states it explicitly |
| Evaluating deal-breakers against postings | Epic 7's job; this epic only defines the shape |
| A UI for corrections | Epic 12; corrections are file-edit-plus-pack for now |
| Full-text or semantic search over the evidence corpus | Epic 9's job, when tailoring needs to find a quote |
| Per-claim semantic ("is this a fair characterisation") judging by default | Available via `judge.ask`; not mandated here, to keep ingest to one model call by default |
| DuckDB indexing of profile revisions | No query scans profile history yet; wiring it would be a port used because it exists |
| True deletion of a committed profile document | Unresolved — see below |
| The rendering toolchain (Typst, Tectonic, or otherwise) | Epic 9 story 9.5 decides, with real tailored output in front of it — see **The record versus the rendered artifact** |

## Open questions

- **Deletion collides with git's append-only design.** `ports.Docs` has no `Delete`, and even
  if it did, removing a path from `HEAD` would not purge it from history — a mis-ingested CV
  or an accidentally pasted secret stays recoverable from a prior revision forever, which is
  the exact opposite of what "nothing already committed is overwritten in place" was built to
  guarantee for everything else. What "delete" should mean for a git-backed personal record —
  never commit it in the first place, rewrite history (which the whole design otherwise
  refuses to do), or accept that deletion only ever means "no longer current" — is a decision
  for a human, and this spec does not make it.
- **The vocabulary boundary this spec draws** (`profile`, `evidence`, `decision`, `quote` as
  acceptable core-tree vocabulary; `employer`, `salary`, `on_call` as pack-only) is a judgement
  call `vocabulary_test.go` does not yet enforce. It should be ratified, adjusted, or overruled
  when this epic is implemented — and the forbidden-word list extended either way.
- **The pack-template gap** between a step's structured output and another step's string
  input (needed to pipe `extract.run` into `docs.put`) is real and unresolved; see **Ingest**.
- **Binary CV storage.** Committing the raw PDF/DOCX under `profile/source/` gives ingest
  something to re-run against later, but git does not diff binaries usefully, and the harness
  spec already flags "binary artifacts in git" as unresolved for Epic 9's generated PDFs. The
  same question applies here and is not re-solved by this spec.
- **Whether the semantic judge-check on grounded claims runs at all in v1**, and if so on every
  claim or a sample, is a cost/quality tradeoff left to the implementation plan.
