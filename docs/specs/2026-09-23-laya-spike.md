# Spike — a purpose-built classifier as a second `Judge`

**This is a spike.** Its output is an answer and a recommendation, not code we keep. Anything
built here is throwaway and labelled as such. It changes no port, no pack and no committed
behaviour.

## The question

> Does a purpose-built typed-decision classifier answer our questions better than reading
> probability mass off an LLM's token alternatives — specifically where our coverage is thin?

Increment 3 shipped a `Judge` that asks several typed questions in one call and reads each
answer's probability mass from the engine's per-token alternatives. Those alternatives carry the
model's **pre-constraint** distribution, so on a real posting the `focus` question came back
`{data: 1.0}` while the alternatives were `AI (0.883), Building, Develop, Business, Internal` —
none of the five declared options. Confidence 1.0, coverage 0 of 5. The schema forced a legal
answer; the probability attached to it measured nothing.

A classifier with a decision head emits one calibrated distribution over exactly the options
asked about. That failure mode cannot occur by construction. Whether it is *better* on our
subjects is the open question.

## What is being probed

`receptron/laya` (MIT wrapper) runs `convaiinnovations/laya` (Apache-2.0 weights, ~1.7 GB, a
421M-parameter ModernBERT encoder plus a decision head) through ONNX Runtime, in process, with no
hosted service and no key. It implements the same three question kinds this platform already
has — a probability of yes, one option with the distribution over all, and a position on ordered
levels.

The harness spec names TypeSafe as the `Judge` port's second implementation and calls it hosted
only. That assumption is now false: an API-compatible model runs locally under a permissive
licence. The spike tests whether it earns the seat.

## Method

The runtime decision is already taken: **Go bindings over ONNX Runtime via cgo**, in one binary,
the way `duckindex` already takes a cgo dependency. No sidecar, no second language at runtime.

1. **Fetch the bundle.** The ONNX export and tokenizer from Hugging Face into a local cache. The
   weights are never vendored into this repo; Apache 2.0's attribution applies to the artifact,
   and a note records where it came from.
2. **Prove the Go path is honest before trusting it.** The reference implementation asserts exact
   output values against the checkpoint's own Python code. Reproduce those same numbers from Go on
   the same inputs. If Go and the reference disagree, every later comparison is meaningless and the
   spike stops here with that finding.
3. **Ask both judges the same questions.** The same subjects and the same typed questions through
   `app.Judge` (Ollama, `qwen2.5-coder:7b`, pinned sampling) and through the classifier. Subjects:
   the postings the job-hunt pack already fetches, plus a handful of neutral subjects so the
   comparison is not one advert wide.
4. **Record, per question, per judge:** the chosen answer, the full distribution, our confidence
   and coverage, latency, and whether the run needed the GPU.

## What the answer looks like

A table of the same questions answered both ways, and a plain verdict on each of these:

- **Does it fix the failure that prompted this?** On the questions where our coverage was 0, does
  the classifier return a distribution spread across the declared options, or does it also collapse?
- **Do the two judges agree** where our coverage was good? Disagreement there is interesting;
  agreement is reassuring but not the point.
- **Is it calibrated in the direction it claims?** Its confident answers should be right more often
  than its unconfident ones, on questions where a human can say what the right answer was.
- **Does it fit the laptop?** It runs on CPU. Can it run while Ollama holds the GPU, and what does
  it cost in memory and wall clock?
- **Is the licence clean to depend on?** MIT wrapper, Apache-2.0 weights, attribution obligations
  named, no account or key anywhere in the path we would actually use.

## What would make this worth an increment

All of:

- Go reproduces the reference numbers exactly.
- It returns a real distribution on at least the questions where our LLM path reports coverage 0.
- It runs alongside Ollama on this laptop without evicting the model from the GPU.
- The licence position is clean.

## What kills it

Any of: the bundle will not load from Go, or the Go numbers disagree with the reference; its
answers are no better than ours where it matters; it cannot share the machine with the engine we
already run; the attribution position is murky; or the project it comes from proves too young to
depend on, in which case the finding is "the idea works, the dependency does not yet".

## Deliberately not in this spike

| Out | Why |
|---|---|
| Implementing a `Judge` adapter | That is the increment this spike decides whether to schedule |
| Changing `ports.Judge` or the record | The port already fits a second implementation; if it does not, that is a finding |
| Training or fine-tuning anything | The spike tests a published checkpoint, not our own model |
| Adopting the TypeScript package at runtime | The runtime decision is Go plus cgo; the package is a reference oracle at most |
| Widening `TopLogProbs` on our own path | A separate experiment, worth doing, not this one |

## Output

A findings note in `docs/notes/`, carrying the comparison table, the measured numbers, and one of
three recommendations: schedule the increment, drop it, or wait on the upstream project. Any code
written lives outside the module or under a clearly throwaway path, and is deleted when the note
lands.
