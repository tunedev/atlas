# Spike — a purpose-built classifier as a second `Judge`

Spec: `docs/specs/2026-09-23-laya-spike.md`. Full findings, comparison table and
per-step detail: `.superpowers/sdd/laya-spike-findings.md` and
`.superpowers/sdd/laya-spike-step1-report.md`. This note is the short version:
question, measurement, meaning, recommendation.

## What was asked

Does a purpose-built typed-decision classifier (`receptron/laya` over
`convaiinnovations/laya`, run through ONNX Runtime in process) answer our
typed questions better than reading probability mass off an LLM's token
alternatives — specifically on the questions where our coverage is thin or
zero?

## The honesty gate

Before any comparison could mean anything, the Go port had to prove itself
against the reference implementation, with nothing tuned to get there.

- Before any weights were involved: the Go port's tokenizer and sequence
  builder reproduced the reference's 267-token sequence exactly.
- With weights loaded: all twelve values the reference fixture asserts
  reproduced exactly — the ten probability and score values to four decimals
  with zero delta, plus the token count (267) and the categorical choice
  (`billing`) matching outright. Largest delta across the run: 0.0000.

Nothing was adjusted to reach these numbers; the batching, temperature lookup
and softmax are a direct port of the reference's own code. Every later
comparison rests on this passing.

## What it means: the headline comparison

On every case where the atlas judge reported coverage 0 — `posting.focus` and
`weather.focus` — the classifier returned a real spread across the declared
options, never a collapse to one option at mass 1.0. `weather.focus`:
`other 0.394, platform 0.238, data 0.157, backend 0.106, frontend 0.105`.
`posting.focus` likewise spreads: `backend 0.383, platform 0.256,
frontend 0.229, data 0.067, other 0.065`. This is the exact failure the spike
opened with — atlas's `focus` question landing on `{data: 1.0}` while the
model's real alternatives were `AI, Building, Develop, Business, Internal`,
none of them a declared option — and it cannot recur in the classifier by
construction: it reads one logit per option's own marker, so there is no
"declared option absent from the alternatives" case to fall into.

Where atlas had decent coverage (`posting.stretch` at 2/2, `posting.seniority`
at 3/4), the two judges agreed on the top choice (`no`, `senior`). The shape
still diverged: the classifier gave real weight to `staff` (0.293) on
`posting.seniority`, an option atlas's own alternatives never named at all.
Two agreeing points is not enough to call calibration — only enough to say
the two aren't contradicting each other where atlas had something to say.

## The unplanned finding

This matters more than the headline result. Four of the five neutral test
subjects (book, film, recipe, news) made `app.Judge.Ask` return an **error**,
not a low-coverage answer:

```
judge: seniority: "s" is ambiguous between senior and staff (alternative "s", p=0.1650)
```

This is our own fragility, and it was found by running our own judge against
varied subjects rather than the one advert it was written against. Increment
3's option-set validation already rejects an option that is a prefix of
another declared option — but `senior` and `staff` are not prefixes of each
other. What broke here is a case that validation cannot see: two options that
merely **share** a prefix an engine might emit as its own token are only
detectable at read time, once a real completion produces that token, and a
shared first character (`s`) is enough to trigger it. It fired on every
neutral subject but one, deterministically (temperature 0, fixed seed), and
never on the real job posting, which had enough context for the model to
commit past the shared prefix. `seniority`'s option set (`junior, mid,
senior, staff`) ships in `packs/job-hunt.yaml` today; this is a live gap in
the current judge, independent of anything laya-specific, and worth its own
fix regardless of what happens with the classifier.

## Cost and fit

| | Atlas (Ollama, `qwen2.5-coder:7b`) | Laya (ONNX, CPU) |
|---|---|---|
| Posting (15KB subject) | 3.26s warm (6.21s on cold model load) | 2.02s |
| Neutral subjects (~500-650B) | 1.6-1.8s each | 0.4-0.55s each |

The ONNX session's own memory: RSS baseline ~10 MB rose to 1.48 GiB
immediately after opening the session (the fp32 421M-parameter checkpoint
paging in), settling at ~2.03 GiB after all six subjects. Ollama's GPU model
was measured before and after the full run: `qwen2.5-coder:7b` stayed
resident at the same VRAM footprint throughout, and the CPU-only ONNX session
never touched the GPU or evicted it — the two ran side by side on this laptop
undisturbed.

## Recommendation: the idea is validated, the dependency is not yet

All four of the spec's "worth an increment" conditions are met, and none of
the hard kill conditions fired. But one soft condition did: the Go binding
this spike used, `github.com/microsoft/onnxruntime/go`, is untagged, and the
API surface the spike needed (`Tensor`/`CreateTensor`/`TensorData`) landed on
its `main` branch hours before the spike ran. The whole upstream chain behind
that binding is days old at the point of measurement — not a hypothetical
immaturity risk, but one the spike hit directly mid-run.

Do not schedule the increment against that binding at its current untagged
state. Either re-check for a tagged release before starting, or fall back to
`github.com/yalue/onnxruntime_go` (MIT, tagged `v1.36.0`, one minor version
behind this environment's 1.30.0 runtime on the C API headers) to de-risk the
runtime dependency without waiting. A tagged release of either binding is
what would change this verdict from "wait" to "go."

## What this spike deliberately did not settle

- **Calibration.** Whether the classifier's confident answers are right more
  often than its unconfident ones is unanswered — there were no ground-truth
  labels to test against, and manufacturing them was out of scope. The
  classifier was measured on a handful of subjects (one real posting, five
  neutral texts), not evaluated for calibration against known-correct
  answers, and this note draws no conclusion beyond what was measured.
- **The `prefixOptionMatch` fragility** found above is a bug report against
  the current judge, not something this spike fixes.
- **Whether a second LLM engine would agree with atlas any better or worse**
  than laya does — only one engine (Ollama, `qwen2.5-coder:7b`) was run.
