# Epic 9 — Tailoring — design

**Supersedes nothing.** This design details Epic 9 of `docs/plans/2026-09-17-roadmap.md`. It
rests on Epic 6 (`docs/specs/2026-09-24-epic-6-profile.md`, `docs/notes/2026-09-24-epic-6.md`):
the structured history, the evidence corpus, `extract.run`, and the verbatim-quote check
`quote.ground` (`app.Ground` / `app.Annotate`). It reuses Epic 3's `judge.ask`.

**Goal:** from the user's own record and one posting, produce a CV variant, a letter and answers
to the posting's questions in which every statement about the user traces to text the user
wrote, and every requirement the record does not support is shown as a gap — never written as
fact (story 9.3, the epic's point).

## The failure this designs against — reproduced, not assumed

`packs/job-hunt.yaml`'s `documents` step asks `model.complete` for "a tailored CV" and "a letter"
from a one-line profile, instructing it to "use only facts given" and "state a gap plainly".
Run three times against a realistic posting through `qwen2.5-coder:7b` (the configured local
model), it invented experience **3 of 3** times: employers (one run named the hiring company
itself as the current employer), dates, a degree, an "AWS Certified DevOps Engineer –
Professional" certification, and PCI-DSS, Kafka and on-call experience lifted from the posting's
own requirements. An instruction is not a control.

## What was measured before designing

A throwaway spike (not committed) ran the design below against the user's real CV, ingested
through `packs/profile-ingest.yaml` into a scratch store. The CV's content is not reproduced in
this repository; only counts are.

**Asking a model to type a verbatim quote fails on a real CV about half the time, and not by
lying.** Ingest produced 7 entries and 23 claims; 15 of 30 quotes came back `needs_review`.
None were invented: every one was a near-copy — two adjacent bullets spliced, a bullet glyph
(`●`, `•`) dropped, a phrase trimmed. Five had a verbatim run covering at least 80% of the
quote. Several claim quotes also appeared under two entries, the misattribution Epic 6's note
recorded. A tailoring design that relies on model-typed quotes would show half of a real
history as gaps: honest, and useless.

**Citing by span id removes that failure; relevance is what remains.** The CV's text was split
into 58 numbered spans and the model cited span numbers instead of typing text. Out of 6
posting requirements it produced 0 invalid ids, 1 gap, and 1 uncited letter sentence (caught).
But for 2 requirements it cited spans that do not mention the requirement at all, although the
CV does mention it elsewhere. A span id proves a quote exists, not that it supports the claim.

**A yes/no relevance question catches most of that.** One question per (requirement, cited
span) — "does the evidence, on its own, directly show the candidate meets the requirement?" —
rejected 6 of 7 irrelevant citations (every one of the two mis-cited requirements) and kept 2 of
3 relevant ones. One irrelevant citation passed; one relevant one was wrongly dropped. Good
enough to be load-bearing, not good enough to be the only line; hence the evidence travels with
every sentence (see **What the checks do not catch**).

**One ingest attempt of two crashed inside Ollama** (`CUDA error: an illegal memory access`,
HTTP 500). The retry succeeded. The GPU runner's fault, not Atlas's; the existing error path
surfaced it correctly.

## The pipeline

```
record text --text.spans--> numbered spans --extract.run (cite ids)--> citations
  --span.resolve--> quotes --quote.ground--> existence --judge.ask--> relevance
  --gap rule--> checked document --docs.put--> record --render.run--> sendable PDFs + review sheet
```

Every Go-tree name is generic. "CV", "letter", "posting", "employer" appear only in the pack's
YAML, its schema blocks and its Typst templates, as Epic 6 established.

1. **`text.spans`** (new tool, pure, `internal/core/app` function plus a tool). Splits text into
   spans at bullet glyphs and line breaks, collapses whitespace, drops spans shorter than a
   configured minimum, and gives each a stable id `<source>#<index>`. Input: one or more named
   texts. Output: `{"spans":[{"id","text"}], "listing":"[<id>] <text>\n..."}` — the listing is
   what a prompt carries. Sources are `profile/source.txt` and every file under
   `profile/evidence/`.
2. **`extract.run`** (existing, unchanged). The pack's schema makes every citation field an
   array of span ids. The requirements are quoted from the posting alone and grounded against
   it, then cited by number (`items.cite` builds one claim per requirement); the questions are
   quoted from the questions var alone and grounded against it, then answered by number as
   sentences with ids (`items.gather` gives each question its sentences, and an unanswered one
   an empty sentence). The CV bullets are ordered ids (the bullets are the spans' own text,
   never reworded); the letter is sentences, each with ids. No questions, no answers.
3. **`span.resolve`** (new tool, pure). Replaces each cited id with an object
   `{"id","quote"}` carrying the span's verbatim text. An id that does not exist resolves to an
   empty quote.
4. **`quote.ground`** (existing, unchanged), with the same spans' source text as `source`. An
   empty or non-matching quote is `needs_review`, so an invented citation is always visible.
5. **Relevance, through `judge.ask`** (existing, unchanged). For each claim, one call asks a
   `noul` question per cited span: "Does this evidence, on its own, directly show: <claim>?" A
   citation survives only if it is grounded and its mass on `yes` is at least the pack's
   `relevance_threshold`. Batching one claim's spans into one call keeps the cost to one model
   call per claim, not per span. The judgement is recorded as `judge.ask` always records it.
6. **The gap rule** (new tool, pure: `claims.settle`). Every object with a `text` key is a
   statement; one with no surviving citation, no citations list, or only whitespace for text is
   a gap, and settle always sets the gap flag. Gaps never reach a sendable document:
   - a posting requirement → listed on the review sheet under "Requirements not shown by the
     record" (the text is the posting's, so it asserts nothing about the user);
   - a CV bullet → dropped, and counted on the review sheet;
   - a letter or answer sentence → left out of the letter, and listed on the review sheet as
     removed for lack of evidence;
   - a question with no kept sentence → left out of the letter, and listed on the review sheet
     under "Questions not answered by the record".
   The output is the checked document (kept claims with their surviving citations), the kept
   text per top-level key, and the gap texts per top-level key and in one list.
7. **`docs.put`** (existing) commits the checked document as
   `applications/<subject_id>/tailored.json`, kind `tailored`.
8. **`render.run`** (new tool) — see **The output format**.

**Structure comes from history, bullets do not.** The CV's section headings — employer, title,
dates — come only from `profile/history.json` entries whose own `quote` is grounded, and only
those three fields. Bullets come from spans. So Epic 6's claim misattribution cannot leak into a
bullet. It can still leak into a heading: a grounded entry can carry the wrong dates (Epic 6's
note, "grounded is not correct"). That stays Epic 6's open fix; this epic does not paper over it.

## What the checks do not catch

A sentence can cite a relevant span and still overstate it ("led a team of three" cited for
"led engineering"). The relevance question catches some of this, because an exaggerated claim
is not "directly shown", but it is not a guarantee, and the spike measured the judge at 6/7 on
irrelevance, not on exaggeration. So every kept sentence carries its cited quotes beside it in
`tailored.json`: a reviewer checks a letter against its evidence in one diff, and a later
increment can score the relevance judge against those reviews (Epic 3's
record-beside-an-outcome-slot pattern).

## The output format (story 9.5) — decided: Typst

Decided in front of real tailored output from the spike, on the user's real CV:

| Criterion | Typst | Markdown via pandoc | Tectonic |
|---|---|---|---|
| Data stays data | Yes. The template reads the JSON with Typst's own `json()`; a hostile value (`#set page(...)`, `*bold*`, `$x$`, `<b>`, a backslash) rendered literally | No. `*bold*` in data became emphasis; every value needs markup escaping | Only with careful TeX escaping of every value |
| One binary, offline | Yes. 0.47 s, 31 MB RSS for a two-page document; no packages used | HTML and DOCX yes; PDF needs `pdflatex` (a multi-gigabyte TeX install), which fails the plane test | Fetches TeX bundle files on first use unless pointed at a local bundle |
| Output verifiable | Yes. `pdftotext` returned 7 of 8 bullets verbatim; the 8th broke at a line wrapped on a hyphen, which normalisation handles | — | — |

**Why:** the data-stays-data row decides it. Epic 6 already named "a model editing markup
plumbing" as the silently-wrong failure; a renderer that interprets data as markup reintroduces
it through the data instead of through the model. Typst is the only candidate where the template
consumes structured data natively and values cannot become markup.

### Record versus artifact

| Artifact | Where | Kind |
|---|---|---|
| `applications/<subject_id>/tailored.json` | The record (git) | The checked document: every claim with its cited ids and quotes and its gap flag; index fields hold the gap count and the dropped counts |
| `packs/tailor/*.typ` | The pack | Templates (`cv.typ`, `letter.typ`, `review.typ`); the only place use-case layout lives |
| `cv.pdf`, `letter.pdf` | A configured output directory, not git | Sendable build artifacts, regenerable from `tailored.json`: kept content only, never a gap |
| `review.pdf` | The same directory, not git | For the sender only: every gap, unanswered question and dropped count |

### The render step

- **`ports.Converter`**, in the shape the Epic 6 spec pointed at (the sibling `cana` service's
  converter): `Pair() Pair` names source and target formats, `Convert(ctx, dst io.Writer, src
  io.Reader) error` streams one to the other. It earns a port because the second implementation
  is nameable: `cana`'s, once `cana` has a conversion surface. Epic 8's local-binary pattern is
  the first adapter.
- **The Typst adapter** runs a `typst` binary found on `PATH` (configurable path), never
  installs one, and is bounded by a configured timeout. Configured but absent fails loudly at
  startup, not mid-pack. A Typst document needs its data file beside it, so the adapter compiles
  in a fresh temporary directory holding the template and the data, with `--root` set to that
  directory so a template cannot read outside it, and no package fetches (offline).
- **`render.run`** (new tool) takes a template path, the data (a JSON value from an earlier
  step) and an output path; writes the PDF; then verifies it: extracts the PDF's text with the
  existing `file.text` path (tabula), and runs `app.Ground` with that text as the source over the
  kept quotes. A kept quote missing from the rendered text is an error, not a warning — the
  artifact must say what the record says. An optional `absent` list works the other way: the
  packs pass the gap texts, and a render that prints any of them fails and publishes nothing. Normalisation additionally joins a word broken at a
  line-end hyphen. The spike measured this with `pdftotext`; the plan must re-measure it with
  tabula before relying on it, since the two extract text differently.

## Stories

| Story | How it is met |
|---|---|
| 9.1 A CV variant per role | Headings from grounded history entries; bullets are cited spans, chosen and ordered for the posting |
| 9.2 A letter that cites evidence | Every kept sentence cites spans that exist and survived the relevance check; the citations are in `tailored.json` |
| 9.3 An unsupported claim is a visible gap | The gap rule: a claim with no surviving citation is listed on the review sheet and never rendered in a sendable document |
| 9.4 Answers to the posting's own questions | Questions quoted from the questions var, answered with the letter's checks; an unanswered question is listed on the review sheet |
| 9.5 The output format decision | Typst, for the reason above |

## The job-hunt pack

`packs/job-hunt.yaml`'s `documents` step is removed and replaced by the tailoring steps. Its
one-line `profile` var goes; the pack reads `profile/source.txt` and the evidence corpus from the
record. A new `packs/tailor.yaml` runs the same steps on their own against a posting given as a
var or file, so tailoring does not need a live feed (the feed repository currently holds no
postings).

## The vocabulary guard

Epic 6 already added `cv`, `employer`, `résumé` and `curriculum vitae`. This epic adds
`applicant` and `hiring manager`. `resume` cannot be forbidden: it is an ordinary verb Epic 4
uses for sessions.

## Testing

| What | How | Needs a model or binary? |
|---|---|---|
| `text.spans` | Bullet glyphs, en dashes, blank lines, too-short spans dropped, ids stable across runs, multiple sources | No |
| `span.resolve` | A valid id yields its verbatim text; an invented or out-of-range id yields an empty quote that `quote.ground` flags | No |
| `claims.settle` | Each claim type with no surviving citation becomes the right kind of gap; a kept claim keeps only surviving citations | No |
| **The regression this epic exists for** | A canned extraction citing an invented employer's span id, an out-of-range id and an irrelevant span, with a stub judge rejecting the irrelevant one: all end as gaps; no invented text reaches `tailored.json` or the render input | No |
| Typst adapter | Hostile data renders literally; timeout honoured; missing binary fails at startup; `--root` confines reads. Skipped when `typst` is absent, like the render tests skip without Chrome | `typst` |
| Render verification | A PDF missing a kept quote, or printing a gap text, is an error; a hyphen-wrapped quote passes | `typst` |
| Live | One run of `packs/tailor.yaml` on a fabricated two-employer CV and a sample posting, through `qwen2.5-coder:7b` and Typst; the increment note records counts. No real CV content is committed | Model and `typst` |

## Deliberately not in this increment

| Out | Why |
|---|---|
| Sending anything to an employer | Epic 10 |
| A UI for reviewing gaps and citations | Epic 12; review is a diff of `tailored.json` for now |
| Semantic search over the evidence corpus | Spans are listed in full; a CV plus a small corpus fits a prompt. Revisit when it does not |
| Rewording CV bullets | Bullets are verbatim spans. Rewording is where invention enters |
| Typst packages and custom fonts | Keeps rendering offline from a cold start |
| Fixing Epic 6's claim misattribution | Epic 6's open fix; this epic keeps it out of bullets and states where it can still show |
| Scoring the relevance judge against human reviews | Needs reviews to exist; the citations kept in `tailored.json` are the slot for it |
