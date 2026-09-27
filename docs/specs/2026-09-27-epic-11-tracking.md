# Epic 11 — Tracking — design

**Supersedes nothing.** This design details Epic 11 of `docs/plans/2026-09-17-roadmap.md` and
sits under `docs/specs/2026-09-17-job-hunt-harness-design.md`, which it does not contradict.
It rests on Epic 1 (`Docs`/`Index`, `docs/design/the-record.md`), the judgement record Epic 3
built (`docs/design/the-judge.md`, `internal/core/app/judgementrecord.go`), and the decision
log Epic 6 built (`docs/specs/2026-09-24-epic-6-profile.md` §6.5, `internal/core/app/decisionrecord.go`).

**Goal, per the roadmap's own words:** "the corpus only becomes useful once outcomes are
attached to decisions." Mechanically, this epic fills the `outcome` slot Epic 3 left `null`
in every judgement document, and scores what was predicted against what happened.

## Stories, as the roadmap words them

| Story | Acceptance |
|---|---|
| 11.1 Pipeline state per application | Where each one is, and for how long |
| 11.2 What has gone quiet | Staleness measured from the declared send |
| 11.3 Outcomes recorded through to offer or rejection | The label epic 3's recorded judgements were waiting for |
| 11.4 The first calibration check | Predicted probability against observed outcome. The number stops being an assertion |

## What this epic inherits

`docs/notes/2026-09-24-epic-3-closing.md` ("What epic 11 will need and does not yet have") is
explicit: "The outcome slot exists (`"outcome": null` in the document, `"outcome": "pending"`
in the index row) and nothing fills it. No prediction has been scored against an outcome —
there are no outcomes yet to score against. And a coverage-0 judgement should probably be
excluded from any calibration sample built later, since its probability never measured real
competition among the declared options — but nothing marks it as such today; `Coverage` sits
on the record, unused by anything that reads it." This design is the epic that answers both
open questions.

It also inherits Epic 6's decision log (`internal/core/app/decisionrecord.go`): `Decision`
already carries `JudgementPath` and `VerdictAtDecision`, a snapshot of what the model chose at
decision time — built, per that spec, precisely so a later reader (this epic) can see the user
overrode the model without re-reading a judgement document whose outcome has since changed.

## Sequencing: what is blocked on Epic 10, and what is not

The roadmap's own sequencing note: "11 needs 10, and closes the loop back to 3.5." That
dependency is real for half this epic and not the other half.

| Part | Needs Epic 10? | Why |
|---|---|---|
| 11.1 Pipeline state per application | Yes | "Where each one is" presumes a pipeline exists to be in. Nothing today declares an application drafted, sent, or in any stage — that vocabulary is Epic 10's (`10.1`, `10.4`) |
| 11.2 What has gone quiet | Yes | Staleness is explicitly "measured from the declared send" (roadmap's own words). Epic 10.4 is what makes "drafted and sent are distinct states" and starts that clock. No clock exists to read from without it |
| 11.3 Outcomes recorded | **No** | A judgement document already exists, from Epic 3, with a subject id and an empty outcome slot. Attaching a real-world result to it needs `Docs`, `Index`, and the user's own knowledge — nothing from Epic 10. This holds whether or not the application ever went through a declared send: a judgement made about a role the user applied to by hand, before Epic 10 existed, is just as attachable |
| 11.4 The first calibration check | **No** | Scoring reads recorded `Answer`s and attached outcomes. Neither requires a pipeline or a send-declaration to exist |

**This spec designs 11.3 and 11.4 as a shippable increment on their own**, over `Docs` and
`Index` alone, and leaves 11.1/11.2 as a second increment that starts once Epic 10 lands. The
two halves do not share code beyond the outcome slot itself, so shipping the first does not
foreclose the second's design.

## The vocabulary constraint, briefly

`internal/arch/vocabulary_test.go` forbids use-case vocabulary in `internal/` and `cmd/`. An
outcome's actual values — `offer`, `rejected`, `ghosted`, `not_applied` — are job-hunt
vocabulary, exactly the way `apply`/`stretch`/`skip` are for `ports.Answer.Chosen` and
`Decision.Choice`. This design keeps `Outcome.State` a free string a pack defines the meaning
of, the same way Epic 6 keeps `Decision.Choice` free. Nowhere below is a Go constant named
`offer` or `rejected`; those appear only in prose, as illustrations of what a job-hunt pack
would choose to write.

## What an outcome is

An outcome is the real-world result of a judged subject, supplied by the user, once and
whenever they know it. There is no automatic detection — no email parsing, no ATS
integration — because none exists and adding one is its own epic's worth of adapter, not a
tracking primitive.

```go
// Outcome is the real-world result of a judged subject. State is a free
// string a pack defines the meaning of, the same way Decision.Choice is.
// There is no in-progress Outcome value: "in progress" is the null the
// judgement document already carries before one is attached.
type Outcome struct {
    State string
    When  time.Time // when the state became true, not when it was recorded
    Note  string
}
```

**Every attached outcome is terminal, structurally, not by a list of names Go has to know.**
Before attachment the document's `outcome` key reads `null` (Epic 3's own design). After
attachment it holds an `Outcome` value. There is no third, "in-progress" shape — an
in-progress application is exactly the case that stays `null`. This means "terminal" needs no
enum in the core: non-null is terminal, by construction.

A job-hunt pack is expected to use states along these lines (illustration, not Go). The
"Default disposition" column is the illustrative pack's own choice — see "What is excluded
from the sample, and why" for the reasoning behind each — not a Go-enforced classification:

| Illustrative state | What it means | Who declares it, and when | Default disposition |
|---|---|---|---|
| *(null, unattached)* | Not yet known | Nobody yet — this is most judgements, most of the time | n/a — still pending |
| `not_applied` | The subject was judged, then skipped; no application will ever exist | The user, at (or soon after) the skip decision — this is a known fact, not a prediction | Inconclusive |
| `offer` | An offer was extended | The user, whenever they learn it — no time bound | Positive |
| `rejected` | The application was declined | The user, whenever they learn it — the roadmap's own example is two months later | Negative |
| `withdrawn` | The candidate pulled out before either side decided | The user, whenever it happens | Inconclusive |
| `ghosted` | The user declares silence long enough to treat as a non-response | The user's own judgement call of "long enough" — Epic 11.2, once Epic 10 exists, can inform that call with a real staleness number, but nothing stops declaring it by hand today | Inconclusive |

The roadmap's own example is worth restating precisely: **a rejection two months later and a
role never applied to are different, and neither is a bug.** `rejected` is a resolved trial —
the model's prediction was tested and the answer came back negative. `not_applied` is no trial
at all — the predicted event was never given a chance to happen, because the user's own
decision (unrelated to the model being right or wrong) removed it from play. Conflating the
two would silently count "never tried" as "tried and failed," which is a worse number than no
number.

## How an outcome attaches

Git is the record; the index is derived (`docs/design/the-record.md`). Attaching an outcome is
not a new document — it is **a later revision of the same judgement document**, exactly as
`docs/design/the-judge.md` already promises: "The outcome slot is filled by a later revision of
the same document, which is why the document rather than the index is the record."

```go
// AttachOutcome writes o into the outcome slot of the judgement document at
// judgementPath and re-indexes it. It returns the new revision.
//
// A judgement document's outcome can be attached more than once: an
// earlier ghosted declaration corrected to a real rejected two weeks
// later is a legitimate second call, the same way a profile correction
// (Epic 6.2) is a commit, not a rewrite. History still holds the prior
// state; Get and Find resolve to the latest, per "Reads resolve at HEAD."
func AttachOutcome(ctx context.Context, docs ports.Docs, index ports.Index, judgementPath string, o Outcome) (ports.Revision, error)

// AttachOutcomeForSubject finds every judgement document recorded for
// subjectID (there can be more than one — judging the same subject twice
// adds a sibling document, per docs/design/the-judge.md) and attaches o to
// each. The real-world result belongs to the subject, not to one judge
// call, so a re-judged subject should not be left with a stale "pending"
// sibling once the outcome is known.
func AttachOutcomeForSubject(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, o Outcome) ([]ports.Revision, error)
```

Mechanically, `AttachOutcome`:

1. `docs.Get(ctx, judgementPath)` and unmarshal into the same `judgementDoc` shape
   `internal/core/app/judgementrecord.go` already writes and `decisionrecord.go`'s `VerdictAt`
   already reads back — no new document type.
2. Set `Outcome` to a JSON object (`{"state": ..., "when": ..., "note": ...}`) in place of
   `nil`. The field is still present, still keyed `"outcome"`, still marshalled with
   `json.MarshalIndent` the same way — only its value changes, from `null` to an object.
3. Re-marshal and call `RecordDocument` (`internal/core/app/record.go`) with the **same
   `Path`**, the new body, a message such as `"Attach outcome for <subject id>"`, `Kind:
   "judgement"` unchanged, and `Fields` built the same way `judgementFields` is today except
   the `"outcome"` entry becomes `o.State` instead of the literal `"pending"`.
4. `RecordDocument`'s existing `docs.Put` call produces the new revision (a same-path,
   different-body write is never the no-op case `docs/design/the-record.md` describes, since
   the body genuinely changed); its `index.Upsert` replaces the current row for that path,
   because `sqlindex` is path-keyed — "one row per path — current state" — so the prior
   `"outcome": "pending"` row is exactly what gets replaced, not appended to.

The index row's `When` field is left as the judgement's own `When` — what the row has always
represented ("when was this judgement made") — not the outcome's `When`. The outcome's own
timestamp lives in the document body alone, the same asymmetry `the-record.md` already
describes between what the document holds in full and what the index mirrors as a flat field.

**Finding the path to attach to**, when the caller only has a subject id: `index.Find(ctx,
ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": subjectID}})` returns
every judgement row recorded for it — this is exactly what the index exists for, per
`docs/design/the-record.md`'s "Documents are findable by field without reading the
repository." `AttachOutcomeForSubject` is this lookup plus a loop over `AttachOutcome`.

**A note on Epic 6, not a change to it:** a job-hunt pack's decision-log step could call
`AttachOutcome` with a pack-chosen "not applied" state immediately after recording a skip
decision, since a skip is a known fact the moment it is made, not a future prediction. That
wiring belongs in the pack (or a use-case-level tool composing the two calls), not in
`internal/core/app/decisionrecord.go` — teaching `RecordDecision` what "skip" means would be
exactly the use-case vocabulary leak Epic 6 was careful to keep out of the core. Left as a
recommendation for whichever plan implements this, not mandated here.

## How a prediction is scored

**Metric: Brier score, plus a reliability table.** Brier score is a strictly proper scoring
rule for a probability against a binary event — it is minimized only by reporting your true
belief, unlike accuracy or log-loss's harsher tail behavior — and it decomposes cleanly into
the reliability curve the roadmap asks for by name ("your 80% judgements came true 55% of the
time" is a reliability table row, read aloud). Both are standard for exactly this
question (weather forecasting, election modeling) and neither requires assuming anything about
*why* a probability was wrong, only whether it was.

**What gets scored, generically.** A `Judgement` may carry several answers (`stretch`,
`seniority`, `focus` in today's job-hunt pack); only one of them, per scoring run, is "the
prediction" — which one, and which outcome states count as it having come true, come false,
or tested nothing at all, is pack knowledge, not core knowledge, for the same vocabulary
reason `Decision.Choice` is a free string:

```go
// Prediction names which recorded answer predicts a real-world result, and
// sorts every outcome state into one of three scoring dispositions: it
// came true (Positive), it came false (Negative), or it is not evidence
// either way (Inconclusive). All three are pack-declared free strings,
// matched against Outcome.State. Only Positive and Negative enter the
// calibration sample; Inconclusive is excluded on purpose, the same way a
// coverage-0 answer is, because the outcome never gave the prediction a
// real trial.
//
// A resolved outcome whose State is in none of the three lists is also
// excluded, but counted separately (ExclusionCounts.Unclassified): the
// pack author declared no opinion on what it means, which is a gap worth
// surfacing, not the same thing as declaring it Inconclusive on purpose.
type Prediction struct {
    QuestionID   string
    Positive     []string
    Negative     []string
    Inconclusive []string
}
```

For a `noul` question, the natural pack choice is `Positive: []string{"yes"}` and the
predicted probability is `Distribution["yes"]`. For a `choice` or `score` verdict (Epic 7's
apply/stretch/skip, once it exists), the same machinery generalizes: predicted probability is
the summed `Distribution` mass over whichever of the question's own option strings the pack
lists under `Positive` (e.g. `Positive: []string{"apply"}` scores "did applying pay off," using
`Distribution["apply"]` as the predicted probability). Nothing here is specific to `noul`; the
generalization is exactly what lets this epic not wait on Epic 7's verdict shape being fixed.

```go
// Calibrate scores every resolved judgement whose outcome falls under
// Prediction's Positive or Negative against the probability mass it
// assigned to Positive, and reports a Brier score and reliability table
// when there are enough points for either to mean something.
func Calibrate(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction, opts CalibrateOptions) (Calibration, error)

type CalibrateOptions struct {
    Provider string // "" scores every provider pooled; set, scores one
    Model    string // "" scores every model pooled; set, scores one
}

type Calibration struct {
    N         int
    Excluded  ExclusionCounts
    Brier     *float64         // nil below MinSample
    Bins      []ReliabilityBin // always computed; ObservedRate nil per-bin below MinBinSample
}

type ExclusionCounts struct {
    Pending      int // outcome still null
    ZeroCoverage int // resolved, but the scored answer's Coverage.Represented == 0
    Inconclusive int // resolved, State is in Prediction.Inconclusive by pack declaration
    Unclassified int // resolved, but State is in none of Positive, Negative, or Inconclusive
}

type ReliabilityBin struct {
    Low, High     float64
    N             int
    MeanPredicted float64
    ObservedRate  *float64 // nil below MinBinSample
}

const (
    MinSample    = 30 // headline Brier score
    MinBinSample = 10 // one reliability row
)
```

**Why 30, and why 10, not a round "enough."** The standard error of an observed proportion is
`sqrt(p(1-p)/n)`, maximized at `p=0.5`. At `n=9` — the roadmap's own example — that is ≈16.7%,
meaning a reported "55%" could plausibly be anywhere from the low 20s to the high 80s at two
standard errors: "a lie with a decimal point," exactly as the instructions call it. At `n=30`
the same figure is ≈9.1%, tight enough that a headline number means something without
pretending to false precision; `n=30` is also the conventional threshold for treating a sample
proportion's sampling distribution as approximately normal. `MinBinSample=10` (≈15.8% SE)
applies the same reasoning per bin, since a reliability table with one bin holding two points
is the same lie spread across more rows. Below either threshold, `Calibrate` still returns the
count (`N`, or a bin's own `N`) with `Brier`/`ObservedRate` left `nil` — the caller prints "n=7,
too few for a Brier score" rather than a number that looks precise and is not.

**Bins are fixed-width deciles**, `[0.0,0.1)` through `[0.9,1.0]`, not quantile bins. Quantile
bins adapt to the sample and would produce degenerate, unequal-width bins at exactly the small
sample sizes where this matters most; fixed deciles are the plainer, more falsifiable choice,
and match the "your 80% judgements" framing directly — the reader looks up the 0.8 bin.

## What is excluded from the sample, and why

An outcome maps to one of three scoring dispositions — **positive**, **negative**, or
**inconclusive** — never two. Collapsing the third into "not negative" would silently count
silence as a passed test; collapsing it into "not positive" would silently count it as a
failed one. Both are worse than the honest answer, which is that the outcome tested nothing.
Only positive and negative enter the calibration sample. Four exclusions, each counted
separately in `ExclusionCounts` rather than silently dropped:

1. **Still pending.** The outcome slot is `null`. Nothing to score yet; not a defect, just not
   time yet.
2. **Zero coverage.** `docs/notes/2026-09-24-epic-3-closing.md` measured this directly: when
   none of a question's declared options appear among the engine's alternatives, the own-text
   fallback still produces a legal answer, and normalising one member always yields `1.0` —
   "measured nothing," in that note's own words, about real competition among the options.
   `Answer.Coverage.Represented == 0` for the scored answer is exactly this case, and it is
   excluded outright. A `1.0` that never competed against anything would either inflate the
   Brier score's apparent confidence when it happens to match the outcome, or make the model
   look falsely overconfident-and-wrong when it does not — either way, scoring it says nothing
   about whether the model's real judgement was calibrated, since no real judgement was made at
   that token. Partial coverage (`Represented` between 1 and `Declared`) **is** included: at
   least one real competing alternative was seen, so the mass reflects genuine, if incomplete,
   competition — the narrow-`TopLogProbs` problem that produces partial coverage is real (see
   `docs/design/the-judge.md`'s known gaps) but is a reason to widen the window later, not a
   reason to throw away every judgement made under today's window.
3. **Inconclusive outcome, by pack declaration.** A resolved outcome whose `State` the pack's
   `Prediction` lists under `Inconclusive`. This is the general form of a principle the human
   ruled for one specific state: **a ghosted application is inconclusive, not negative.** No
   reply is not evidence the judgement was wrong; it is evidence of nothing. Scoring it as a
   miss would punish a verdict that may have been entirely right. The same reasoning applies
   to every state the spec illustrates as inconclusive, each for its own reason:
   - `not_applied` — no trial at all. The predicted event was never given a chance to happen,
     because the user's own decision (unrelated to whether the model was right) removed it
     from play.
   - `withdrawn` — a trial that started but was called off before either side decided. The
     process ended for a reason orthogonal to the model's prediction, the same way `ghosted`
     ends for a reason (silence) orthogonal to it.
   - `ghosted` — per the ruling above: silence is not a verdict.

   All three share the same shape: something happened, but it was not the thing the prediction
   was about. Excluding them by pack declaration, rather than hardcoding their names in Go,
   keeps the vocabulary constraint intact — Go core has no opinion on what `ghosted` means, only
   on the fact that *some* disposition must be chosen for it deliberately.
4. **Unclassified outcome, by omission.** A resolved outcome whose `State` is in none of
   `Positive`, `Negative`, or `Inconclusive` — the pack simply has not yet said what it means.
   This is counted separately from Inconclusive precisely because it is a different fact: an
   Inconclusive state was considered and deliberately ruled out as evidence; an Unclassified
   state fell through a gap in the pack's own vocabulary and is worth the pack author's
   attention, the same way a coverage-0 count is worth the reader's attention rather than a
   silent zero.

**A future refinement this design leaves room for, without building it.** The human has noted
that at some point a pack author may want a long-enough `ghosted` silence to convert to
`Negative` rather than stay `Inconclusive` — a presumed-dead application eventually is
evidence, even without a reply. This spec does not build that rule: `Prediction`'s lists are
static membership, not time-conditioned. Nothing here forbids adding it later, though, because
the document already carries what such a rule would need: `Outcome.When` ("when the state
became true," present on every attached outcome since the first version of this design) and
the judgement's own recorded time are both already written to every judgement document. A
later rule can be expressed either way without touching a single already-recorded document:
- **As a pack convention, today, with zero core changes** — the user (or the pack step that
  calls `AttachOutcome`) chooses a more specific `State` string at attach time, e.g.
  `ghosted_recent` versus `ghosted_stale`, computed from elapsed time at the moment of
  attachment, and lists the two under different `Prediction` buckets. `State` is already a free
  string; nothing stops a pack from being this granular.
- **As a core addition, later** — `Calibrate` could accept an as-of time and a
  duration-per-state threshold, promoting a `State` from `Inconclusive` to `Negative` once
  `asOf.Sub(Outcome.When)` clears it. This would extend `Prediction` and `CalibrateOptions`,
  not `Outcome` or the judgement document shape, because the timestamp the rule needs is
  already there.

Either path reads `Outcome.When` as it already exists; **no change to the recorded document
shape is needed now to keep this door open**, which matters because a document shape change is
exactly the kind of thing that cannot be applied retroactively to outcomes already committed.

**Provider and sampling differences are a grouping choice, not silently pooled.**
`Judgement.Provider`, `Judgement.Model` and `Judgement.Sampling` are already recorded (`the
judge.md`: "Every `Judgement` records which provider answered... and the `Sampling` that
produced it"), because a probability read under a different engine or window is not
automatically the same measurement. `CalibrateOptions.Provider`/`Model` default to pooling
everything, but the default *report* this epic prints groups by `(Provider, Model)` — the
index row already carries both as flat fields, so grouping costs no extra document reads — and
labels a pooled-across-engines number as pooled, never presenting it as if it came from one
instrument. `Sampling.TopLogProbs` is not its own grouping dimension: it varies less than
provider/model in practice, and `Coverage`'s own per-answer exclusion already catches the
concrete failure mode a narrower window causes (a coverage-0 answer), which is a sharper filter
than bucketing by window width would be.

## What the user sees

Atlas has no UI (Epic 13 is last, deliberately). Calibration surfaces the way every other tool
result does today: a pack step. A `judge.calibrate` tool (composition-root-registered beside
`judge.ask`, `internal/core/app/decisionrecord.go`'s decision tools, and the rest) takes a
`Prediction` the same way `judge.ask` takes questions — parsed from the step's `with` block —
and returns `Calibration` as the step's JSON result, printed the same way `cmd/atlas -pack
...` already prints every step's output (Epic 0.1's own acceptance).

The report itself is **never persisted to git.** Everything `Calibrate` needs —
`Distribution`, `Coverage`, `Provider`, `Model`, the attached `Outcome` — already lives in the
judgement documents and their index rows. A calibration report is a derived view exactly the
way the two indices themselves are derived (`docs/design/the-record.md`: "Neither holds
anything a rebuild could not reconstruct"); persisting it invites the report to go stale
relative to the record it summarizes, and rebuilding it is one more pack run, not a migration.

What it says, concretely, given enough samples: a Brier score for the pooled or grouped sample,
and a reliability table — "your 80% judgements came true 55% of the time," per bin, with the
bin's own count so the reader can see how much weight sits behind that row. This is the whole
product of the epic; the instructions' own phrasing is the correct one to print, not a
euphemism for it.

## The decision log's role

Epic 6.5's `Decision` records the user overriding (or agreeing with) the model's own verdict,
and it is available immediately — `VerdictAtDecision` is a snapshot taken the moment the
decision is made, not something that trickles in over months the way a rejection does. Epic
7.3's acceptance calls this "training data," and it is — but it is not the same label as an
outcome, and this design does not pool the two into one calibration sample.

**A disagreement is not evidence the model's probability was miscalibrated.** The user can
override `apply` to `skip` for reasons that have nothing to do with whether the model's
predicted probability of success was accurate — a newly discovered deal-breaker, a change of
mind about the company, a scheduling conflict. Scoring disagreement as if it were an outcome
would also introduce a timing bias the roadmap's own framing warns against: disagreement labels
arrive in bulk, immediately, at decision time, while real outcomes trickle in over weeks or
months; pooling them would let the fast, dense, differently-meant signal quietly dominate the
slow, sparse, correctly-meant one.

Instead, disagreement is tracked as its own simple statistic — an **agreement rate**: how often
`Decision.Choice` matches `VerdictAtDecision`, optionally broken out by the judgement's own
`Confidence` at the time (does the model get overridden less when it was more confident?). This
is a plain proportion, not a Brier score — there is no probability of disagreement recorded to
score against — and it carries the same `MinSample`-style honesty: report the count, refuse a
rate below it. It answers a different, still useful question: is the model's verdict worth
reading before deciding, not whether its predicted probability came true.

## Testing

No network, no real outcomes, per the same discipline `docs/design/the-judge.md` already
follows for the judge itself.

- **Calibration maths is pure and deterministic.** `Calibrate`'s Brier score and reliability
  binning are computed from `[]ScoredPoint`-shaped inputs the tests construct directly —
  fixed `(predicted, actual)` pairs with a hand-computed expected Brier score and bin
  membership, table-driven, the same style `mass_test.go` already uses for entropy and mass
  arithmetic. No `Docs`/`Index` adapter is needed to prove the arithmetic is right.
- **The exclusion rules are tested as their own cases**: a coverage-0 answer excluded even
  when its `Chosen` matches the outcome; a state the pack lists under `Prediction.Inconclusive`
  (e.g. `ghosted`) excluded and counted under `ExclusionCounts.Inconclusive`; a resolved state
  in none of the pack's three lists excluded and counted separately under
  `ExclusionCounts.Unclassified`, proving the two are distinguished rather than merged; and a
  still-pending judgement excluded and counted separately from all three.
- **`AttachOutcome` is tested against the real `gitdocs`/`sqlindex` adapters**, mirroring
  `judgementrecord_test.go`'s pattern (a temp git repo, a temp SQLite file): record a
  judgement, attach an outcome, assert `docs.Get` now returns the outcome object where `null`
  was, assert `docs.History` shows two revisions (the original judgement, then the
  attachment) with the first still retrievable via `GetAt`, and assert the index row's
  `outcome` field changed from `"pending"` to the attached state without a second row
  appearing. A second `AttachOutcome` call (a correction) is tested the same way: a third
  revision, the second revision still retrievable, one row, latest state.
- **`AttachOutcomeForSubject` is tested** against a subject judged twice (two sibling
  documents, per `docs/design/the-judge.md`'s own identity rule), asserting both get the
  outcome and neither collides with the other's path.

## Deliberately not in this epic

| Out | Why |
|---|---|
| 11.1 Pipeline state per application | Needs Epic 10's drafted/sent vocabulary, which does not exist yet. Second increment, once Epic 10 lands |
| 11.2 Staleness from declared send | Same dependency: "measured from the declared send" presumes a send declaration to measure from |
| Automatic outcome detection (email, ATS scraping) | No such adapter exists; building one is its own epic's worth of scope, and the local-first constraint ("nothing about a user leaves their machine unchosen") means it would need real design, not a tracking primitive |
| Scoring across mixed question ids in one bucket | A `Prediction` scores one `QuestionID` at a time; pooling e.g. `stretch` and a future `will_succeed` question together would average two different measurements into one meaningless number |
| Automatic classification of which outcome states are Positive/Negative/Inconclusive | Pack-declared, per the vocabulary constraint — Go core has no opinion on what `ghosted` means |
| A time-conditioned rule that promotes a long-silent `ghosted` from Inconclusive to Negative | Worth designing room for (see "A future refinement" above), not worth building — no evidence yet on what threshold, or whether one, is right |
| Widening `TopLogProbs`, rephrasing `choice` options, or the `laya` classifier | Carried over, unresolved, from `docs/notes/2026-09-24-epic-3-closing.md`'s three candidate routes; this epic consumes `Coverage`, it does not fix what produces a low one |
| A second engine's numbers compared automatically | `CalibrateOptions` supports grouping by provider/model; nothing here runs a second engine against the same subjects to compare, which is unmeasured today per the closing note |
| Rejecting or gating on a low `Confidence`/`Coverage` at judgement time | A policy decision (Epic 7's deal-breakers, Epic 10's allow/ask/deny), not tracking's job — this epic measures, it does not act |
| A UI | Epic 13, last, deliberately |
| Persisting the calibration report as a git document | Fully derivable from the record on every run; persisting it risks staleness relative to newly attached outcomes, the same reason the two indices themselves are disposable |

## Open for the human

Which question id in Epic 7's eventual verdict is the one a job-hunt pack should name as
`Prediction.QuestionID` is not this design's call — Epic 7's shape (`docs/plans/2026-09-17-roadmap.md`'s
"Apply, stretch or skip, with reasons") is not fixed yet, and this spec deliberately does not
wait on it: `Prediction` is generic enough to score whichever question the pack eventually
names.

This design does rule, per the human's own instruction, that the shipped job-hunt pack
should list `ghosted` under `Prediction.Inconclusive`, not `Negative` — silence is not a
verdict. What remains a pack-authoring decision, not a core one, and is left open here, is
whether and when that should change: whether a long enough silence should later convert to
`Negative`, and if so at what threshold. "What is excluded from the sample, and why" above
lays out how a pack (or a later core addition) could express that rule without any change to
already-recorded documents; it does not pick the threshold.
