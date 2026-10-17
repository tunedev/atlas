# Factory A: good PRs — design

**Supersedes nothing.** This spec details the four atlas items of stage 1 in
`docs/plans/2026-10-17-roadmap.md`: `atlas/decisions`, `atlas/primitives`,
`atlas/decision-self-hosted` and `atlas/pr-template`. Together they give an agent three
things to read before it opens a PR (the noes already decided, the map of harness
primitives, and the PR shape) and give vouch one file to derive a system recap from.

Each item is one issue and one implementation PR. The vouch and foundry items of stage 1 are
out of scope; this spec only fixes what atlas supplies to them.

| Item | Owns | Needs |
|---|---|---|
| `atlas/decisions` | `docs/decisions/README.md`, `docs/decisions/0000-template.md`, `internal/arch/decisions_test.go` | |
| `atlas/primitives` | `docs/architecture/primitives.yaml`, `internal/arch/primitives_test.go`, the docs-tier step in `.github/workflows/build.yml` | |
| `atlas/decision-self-hosted` | `docs/decisions/0001-self-hosted.md`, `README.md`, the intro and Status sections of `CLAUDE.md`, one sentence of the roadmap | `atlas/decisions` |
| `atlas/pr-template` | `.github/pull_request_template.md`, `internal/arch/template_test.go`, the Layout, Before proposing a primitive and Pull requests sections of `CLAUDE.md` | `atlas/decisions`, `atlas/primitives` |

Each file has one owning item, so the four PRs run in parallel sessions without editing the
same lines. `CLAUDE.md` is split by section for the same reason.

---

## 1. `atlas/decisions`

A decision record is a product-shaped decision already made, usually a no, with the
condition that would reopen it. The folder is a steering veto list: an agent reads it before
proposing a new primitive, surface, storage home or dependency, so it does not re-propose
something already refused.

### Files

```
docs/decisions/
  README.md            the index and the rules below
  0000-template.md     the shape every record copies
  NNNN-<slug>.md       one record each
```

### The template

`docs/decisions/0000-template.md`:

```markdown
# NNNN: Short decision title (the no)

- **Status:** accepted
- **Date:** YYYY-MM-DD

<!-- Status is accepted, or superseded by [NNNN](./NNNN-slug.md). Delete this comment. -->

## Context

The proposal or situation that forced the decision: the system state and the constraint,
with links to code or docs. A few sentences, written after the decision is made.

## Decision

What was decided, in one or two sentences. Usually a no. Name the existing primitive that
covers the need.

## Consequences

What stays simple and what gets harder.

## Revisit if

The concrete, observable condition that reopens this decision.
```

A record is about half a page.

### The index

`docs/decisions/README.md` holds, in this order:

1. One paragraph: what the folder is (a steering veto list), and the instruction "Open the
   steering list before proposing a new primitive, surface, storage home or dependency."
2. **When to add a record.** After deciding not to build something the next agent would
   otherwise propose. Copy `0000-template.md` to the next unused number on `main`, with a
   kebab-case slug. Do not write a record on every PR. Do not record a layout or UI tweak, or
   a library pick the code already encodes, unless it is a no that will be re-argued.
3. **Statuses.** Two only: `accepted`, and `superseded by NNNN`. A later record that changes
   a decision marks the old one superseded and moves it to the Superseded list; the old
   record's text is not edited or deleted.
4. **Numbers.** Unique. When two PRs take the same number, the one that merges second
   renumbers before it merges.
5. **Records are point-in-time.** They are exempt from the repo's rule that docs describe
   current behaviour only. Everything else under `docs/` still follows it.
6. **Steering list.** One line per accepted record: number, title as a link, and at most one
   clause of gloss.
7. **Superseded.** One line per superseded record, naming its successor.

### The check

`internal/arch/decisions_test.go`, `TestDecisionRecordsAreWellFormed`. It reads
`docs/decisions/` (resolved from `../..` like the other arch tests) and fails when:

- a file other than `README.md` and `0000-template.md` does not match
  `^[0-9]{4}-[a-z0-9]+(-[a-z0-9]+)*\.md$`;
- two records share a number (the template is not a record and is not counted);
- a record's Status line, after `- **Status:** ` and with surrounding whitespace trimmed, is
  neither exactly `accepted` nor exactly `superseded by [NNNN](./NNNN-<slug>.md)` naming a
  record that exists;
- a record lacks any of the headings `## Context`, `## Decision`, `## Consequences`,
  `## Revisit if`;
- `README.md` does not link a record, or links one that does not exist.

It is the Go counterpart of kody's numbering script, placed where atlas enforces its other
rules.

---

## 2. `atlas/primitives`

`docs/architecture/primitives.yaml` is the map of harness primitives: what they are, which
paths own them, and the invariants a change can touch. vouch reads it to label each PR's
changes; atlas only supplies the file and keeps it valid.

### Shape (fixed)

```yaml
version: 1
groups:
  - id: <kebab>
    name: <text>
primitives:
  - id: <kebab>
    group: <group id>
    name: <text>
    summary: <one line, at most 120 characters>
    code: [<repo-relative paths, coarse ownership roots>]
    docs: [<repo-relative paths>]
invariants:
  - id: <kebab>
    summary: <one line>
    docs: [<repo-relative paths, may include docs/decisions/...>]
```

A changed path belongs to the primitive with the longest matching `code` prefix. vouch
derives a label per primitive per PR: `composes` (wires existing primitives without changing
them), `extends` (changes a primitive's behaviour, shape or contract), `adds` (a new
primitive; this file changes in the same PR). The shape and these semantics are a contract
with vouch: a new field means `version: 2`, agreed with vouch first.

### Code entries

- A directory entry ends in `/` (`internal/adapters/outbound/gitdocs/`), so it never matches
  a sibling that shares its prefix.
- A file entry names one file (`internal/core/app/judge.go`). File entries exist because
  `internal/core/app` and `internal/core/ports` are flat packages holding several primitives.
- In `internal/adapters/outbound/tools`, only the registry is separated. Each tool wrapper
  (`tools/judge.go`, `tools/ground.go` and the rest) belongs to `generic-tools` by design: a
  tool's name and config are the generic-tools contract, and the logic it wraps lives in the
  owning primitive's core files, so a change to that logic shows on that primitive. A file entry lists its `_test.go` file beside it, since
  a prefix match on `judge.go` does not cover `judge_test.go`.
- In a flat package, one primitive owns the directory and the others own their files. A new
  file in that package belongs to the directory owner until the map gives it to another.
- Every file under `internal/`, `cmd/`, `proto/` and `web/src/` has an owner. `packs/`,
  `docs/` and `.github/` have none: a pack change is configuration, not a primitive.

### Header comment

The file opens with a comment saying: this is the map vouch reads to label PRs; the three
labels; update it only when adding, removing or reshaping a primitive (its id, meaning or
code roots), never for ordinary feature work; read `docs/decisions/README.md` before adding a
primitive; the code entry rules above.

### First version

Groups:

| id | name |
|---|---|
| `surfaces` | Entry points |
| `runtime` | Blueprint execution |
| `model` | Model boundary |
| `record` | The record |
| `integrations` | Outbound integrations |
| `platform` | Process infrastructure |

Primitives. Code roots are derived from `internal/`, `cmd/`, `web/` and `proto/` as they
stand on `main`. "+ tests" means each listed file's `_test.go` sibling is also listed.

| id | group | name | code |
|---|---|---|---|
| `cli` | surfaces | CLI and composition root | `cmd/atlas/` |
| `mcp-server` | surfaces | MCP server | `internal/adapters/inbound/mcpserve/` |
| `web-ui` | surfaces | Web UI | `internal/adapters/inbound/web/`, `web/`, `proto/atlas/web/` |
| `pack-loader` | runtime | Pack loader | `internal/adapters/inbound/packfile/` |
| `blueprint-runner` | runtime | Blueprint runner | `internal/core/app/` (directory owner), `internal/core/domain/` |
| `tool-registry` | runtime | Tool registry | `internal/core/ports/` (directory owner), `internal/adapters/outbound/tools/registry.go` + tests |
| `generic-tools` | runtime | Generic tools | `internal/adapters/outbound/tools/` (directory owner) |
| `permission-engine` | runtime | Permission engine | `internal/core/app/permission.go` + tests, `internal/adapters/outbound/termprompt/` |
| `policy-rules` | runtime | Policy rules | `internal/core/app/{rules,policy,suggest}.go` + tests |
| `provider-routing` | model | Provider routing | `internal/core/app/{chain,breaker}.go` + tests, `internal/core/ports/provider.go` + tests, `internal/adapters/outbound/openaiprov/` |
| `judge` | model | Judge | `internal/core/app/{judge,assess,agreement,calibrate,mass,fingerprint,answerschema,answertokens,judgementrecord}.go` + tests, `calibrate_internal_test.go`, `internal/core/ports/judge.go` + tests |
| `extraction` | model | Extraction | `internal/core/app/extract.go` + tests, `internal/core/ports/extract.go` |
| `grounding` | model | Grounding and citations | `internal/core/app/{ground,citations,citeitems,spans,lines}.go` + tests |
| `record` | record | The record | `internal/core/app/{record,rebuild,rebuild_history,decisionrecord,outcome,stage}.go` + tests, `subjectid_test.go`, `internal/core/ports/{docs,index}.go`, `internal/adapters/outbound/{gitdocs,sqlindex,duckindex}/` |
| `sources` | integrations | Sources | `internal/core/ports/source.go`, `internal/adapters/outbound/{crawlsource,feedsource}/` |
| `agent-delegation` | integrations | Agent delegation | `internal/core/app/agentsession.go` + tests, `internal/core/ports/agent.go`, `internal/adapters/outbound/acpagent/` |
| `conversion` | integrations | Document conversion | `internal/core/ports/converter.go`, `internal/adapters/outbound/typstconv/` |
| `config` | platform | Layered config | `internal/config/` |
| `telemetry` | platform | Telemetry | `internal/telemetry/` |
| `store-lock` | platform | Store lock | `internal/pidlock/` |
| `arch-rules` | platform | Architecture rules | `internal/arch/` |

Brace lists in the table are shorthand; the file lists each path in full. Summaries are
written from each package's doc comment, in harness vocabulary only. The first `docs` entries
to use, each kept only if it describes the primitive:

| Primitive | docs |
|---|---|
| `cli`, `mcp-server`, `config`, `telemetry`, `store-lock`, `arch-rules` | `docs/design/surfaces.md` |
| `web-ui` | `docs/design/web.md` |
| `pack-loader`, `blueprint-runner`, `tool-registry`, `generic-tools` | `docs/specs/2026-08-24-agent-substrate-design.md` |
| `permission-engine` | `docs/specs/2026-09-09-increment-1-boundaries-design.md` |
| `policy-rules` | `docs/specs/2026-09-28-epic-10-apply-loop.md` |
| `provider-routing` | `docs/design/the-provider.md` |
| `judge` | `docs/design/the-judge.md` |
| `extraction`, `grounding` | `docs/specs/2026-09-28-epic-untrusted-content.md` |
| `record` | `docs/design/the-record.md` |
| `sources` | `docs/specs/2026-09-24-epic-5-feed.md` |
| `agent-delegation` | `docs/specs/2026-09-24-epic-4-agent.md` |
| `conversion` | `docs/specs/2026-09-27-epic-9-tailoring.md` |

The isolation sandbox, the credential broker and the suspend outcome are not in the map:
`internal/` holds no code for them. Each joins with an `adds` PR when it is built.

Invariants. Each lists the doc that states it and the test that enforces it, where one
exists. `credentials-never-in-model-context` has no test yet: the credential broker that
would enforce it is not built.

| id | summary | docs |
|---|---|---|
| `no-use-case-vocabulary` | The Go tree, the web source and this map name no pack's vocabulary. | `README.md`, `internal/arch/vocabulary_test.go` |
| `core-imports-no-adapter` | The core imports no adapter, driver, network or TLS package. | `docs/design/surfaces.md`, `internal/arch/arch_test.go` |
| `inbound-adapters-isolated` | An inbound adapter imports only the core and its own subtree. | `docs/design/surfaces.md`, `internal/arch/surfaces_test.go` |
| `one-tool-registry` | The composition root builds the tool registry once; every surface shares it. | `docs/design/surfaces.md`, `cmd/atlas/main_test.go` |
| `credentials-never-in-model-context` | No credential reaches a model's context or a blueprint. | `README.md` |
| `crawler-holds-no-credential` | The crawler's config and target types have nowhere to hold a credential. | `internal/arch/conduct_test.go` |
| `drafts-never-submit` | No tool sends a write method to a remote host other than the model endpoint. | `README.md`, `internal/arch/submit_test.go` |
| `git-is-the-record` | Git is the system of record; every index rebuilds from git alone. | `docs/design/the-record.md` |

### The check

`internal/arch/primitives_test.go`. It decodes the file with `gopkg.in/yaml.v3` and
`KnownFields(true)`, so a misspelt key fails rather than vanishing.

`TestPrimitivesMapIsValid` fails when:

- `version` is not 1;
- a group, primitive or invariant id is not kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`), or
  repeats within its list;
- a primitive names a group that does not exist, or a group has no primitive;
- a primitive has no `code` entry, or a primitive or invariant has no `docs` entry;
- a `code` or `docs` path does not exist; an entry ending in `/` is not a directory, or one
  without it is not a file;
- two primitives list the same `code` entry;
- a `name` or `summary` is empty, holds a newline, or a summary exceeds 120 characters
  (counted in runes).

`TestPrimitivesMapCoversEveryHarnessFile` walks `internal/`, `cmd/`, `proto/` and `web/src/`
and fails, naming the file, when a file has no longest-prefix owner. Both walks follow
`witnessModuleTree` in `surfaces_test.go`, so a file added or removed invalidates Go's cached
test result.

`TestPrimitivesMapIsFreeOfUseCaseVocabulary` runs the existing `checkFileVocabulary` over the
whole map, paths included, so a `docs` entry whose file name carries pack words (such as
`docs/specs/2026-09-17-job-hunt-harness-design.md`) cannot be used.

### CI

`.github/scripts/classify-ci.sh` puts a change under `docs/` alone in the `docs` tier, which
skips every Go test. A PR that edits only the map or a decision record, or deletes a doc the
map references, would merge unchecked. The build job gains one step on `ubuntu-latest` in the
docs tier:

```yaml
- name: Docs map
  if: needs.classify.outputs.tier == 'docs' && matrix.os == 'ubuntu-latest'
  run: go test ./internal/arch -run 'TestPrimitives|TestDecision|TestPullRequestTemplate'
```

The pattern matches tests added by the other two items; until they land it matches fewer.

---

## 3. `atlas/decision-self-hosted`

### The record

`docs/decisions/0001-self-hosted.md`, titled "0001: Atlas is self-hosted on a cluster; it
does not run outside one". Outline:

- **Context.** `README.md` says "Everything runs on a laptop, for free. If it does not work
  on a plane, it is a bug." `docs/plans/2026-09-17-roadmap.md` set the constraint
  "Local-first: no account, no server we operate." Atlas now depends on Postgres, RabbitMQ,
  nerve, cana and a telemetry pipeline, and has a user who needs it reachable when the
  workstation is off. `docs/plans/2026-10-17-roadmap.md` decided the change; this records it.
- **Decision.** Atlas runs in Kubernetes and nowhere else. Postgres, RabbitMQ, nerve, cana
  and the telemetry pipeline run beside it in the same cluster. Two environments: qa1, a
  cluster on the user's workstation, and hosted, the same stack on a free-tier cluster.
  Self-hosted means a cluster its user operates, at home or on a free tier; nothing about a
  user leaves infrastructure they chose; a month bills nothing. The single binary is a
  development and test tool, not a way to run atlas. The no: no supported install outside a
  cluster, no service we operate for users, no paid tier.
- **Consequences.** Development runs in a local kind cluster, which works offline once
  `make preflight` has cached its images; the plane rule now governs development. Running
  atlas costs a cluster, so a user without one cannot run it. The binary keeps its tests and
  its `-serve` mode for development.
- **Revisit if.** A second user cannot run atlas because operating a cluster is the blocker;
  or no free tier can host the stack, so hosted cannot stay at a zero bill; or the kind
  cluster cannot come up offline after `make preflight`.

The steering list in `docs/decisions/README.md` gains its line.

### `README.md`

- **Status.** Replaces "Design, no code" and "`internal/` does not exist yet" with what is
  built: the harness as a single binary, with the record, the provider chain, the judge,
  grounding, the CLI, MCP and web surfaces, and the job-hunt pack. It says plainly that the
  isolation sandbox, the credential broker and the suspend outcome described in "The claim"
  are not built, and that the cluster deployment is stage 2 of the roadmap. The document
  table links the current roadmap, `docs/design/`, `docs/decisions/` and
  `docs/architecture/primitives.yaml`, replacing the four-spec list.
- **The web UI.** Retitled "Running for development". The `atlas -serve` instructions stay,
  introduced as the development binary.
- **Context.** The last paragraph becomes: atlas is self-hosted, running in a Kubernetes
  cluster its user operates, with everything it needs in that cluster
  ([0001](docs/decisions/0001-self-hosted.md)). Development runs in a local kind cluster for
  free; if development does not work on a plane, it is a bug.

### `CLAUDE.md`, intro and Status

- **Intro.** "Personal tooling built on a resource-kernel architecture" becomes the README's
  description: a harness for domain-specific AI agents, and the packs that run on it. The
  paragraph pointing at `../CLAUDE.md` stays.
- **Status.** Two sentences: the harness runs as a single binary for development and test;
  atlas itself runs in a cluster ([0001](docs/decisions/0001-self-hosted.md)), and that
  deployment is not built yet. README's Status section is the detail.

### Roadmap

The sentence "Until that item merges, README's plane rule reads as governing development:
the kind cluster, not a binary outside it." in `docs/plans/2026-10-17-roadmap.md` is removed,
since README now says it.

---

## 4. `atlas/pr-template`

### The template

`.github/pull_request_template.md` uses the five headings `foundry ship` writes on agent PRs
(`foundry-work/internal/core/ship/recap.go`, `RecapHeadings`), in the same order, so human
and agent PRs read alike. Door and Cleanup are lines inside Risk, after the level, so the
Risk section still opens with `low`, `medium` or `high`. The derived system recap is not in
the template: vouch posts it separately.

```markdown
## What and why

<!-- The goal of this change and why it is needed. Not how it works. -->

## Touched

<!-- What changed, in short bullets: paths or areas. -->

## Risk

low | medium | high: one reason.

**Door:** two-way | one-way
**Cleanup:** none | needed | done
**To delete:** <!-- only when Cleanup is needed: what to remove, and when -->

<!--
two-way: cheap to reverse; ship it and change it if wrong.
one-way: expensive to undo: a stored shape, a public contract (a .proto, a pack schema,
primitives.yaml), or deleted user data. Spend the review here.
needed: this change leaves an old path to delete later. done: already deleted here.
-->

## Verified

<!-- Commands run and what they printed. -->

## Links

<!-- Closes #N for an issue this fixes; Related to #N otherwise. Specs and decisions. -->
```

### The check

`internal/arch/template_test.go`, `TestPullRequestTemplateMatchesTheRecapHeadings`. It fails
when the template's `## ` headings are not exactly What and why, Touched, Risk, Verified,
Links in that order, or the Risk section lacks a line starting `**Door:**` or
`**Cleanup:**`. The heading list is a copy of foundry's, held in the test: atlas cannot
import foundry, and a copy that drifts fails here instead of in a PR body.

### `CLAUDE.md`, the remaining sections

`## Layout` replaces the current one:

```
cmd/atlas/                         CLI and the composition root
internal/core/{domain,ports,app}   entities, the ports the core owns, use cases
internal/adapters/inbound/<name>   surfaces and the pack loader
internal/adapters/outbound/<name>  implementations of core ports
internal/{config,telemetry,pidlock}  process infrastructure, wired only in cmd/atlas
internal/arch/                     architecture tests
packs/                             every use case's knowledge, as configuration
web/, proto/                       the web UI and its connect contract
docs/specs, plans, notes           specs, plans, increment notes
docs/design                        how each harness part works today
docs/decisions                     decision records: the steering veto list
docs/architecture/primitives.yaml  the map of harness primitives
```

`## Before proposing a primitive` is new:

> Before proposing a new primitive, surface, storage home or dependency, read the steering
> list in `docs/decisions/README.md` and the map in `docs/architecture/primitives.yaml`. A PR
> that adds a primitive adds it to the map in the same PR.

`## Pull requests` is new:

> PR bodies use the headings in `.github/pull_request_template.md`: What and why, Touched,
> Risk, Verified, Links. Risk opens with low, medium or high and one reason, then the Door
> and Cleanup lines.

The instruction is one hop from the file every session loads: `CLAUDE.md` names the two
files, and the files carry the detail.

### Roadmap

The `atlas/pr-template` row in `docs/plans/2026-10-17-roadmap.md` changes from "Intent, Door,
Cleanup and System recap sections" to "The five recap headings, with Door and Cleanup in
Risk", and its Needs cell names `atlas/decisions` and `atlas/primitives`.

---

## Acceptance checks

Each PR's Verified section pastes the output.

| Item | Check |
|---|---|
| `atlas/decisions` | `go test ./internal/arch -run TestDecision` passes. Two well-formed scratch records `0002-a.md` and `0002-b.md`, both linked from README, fail it on the duplicate number; a linked record without `## Revisit if` fails it on the heading. Each is shown and then removed. |
| `atlas/primitives` | `go test ./internal/arch -run TestPrimitives` passes, and `go test -race ./...` passes. Each of a repeated id, an unknown group, a missing path, a 121-character summary, a directory entry without `/`, and a new file under `internal/core/` moved outside every root makes a named failure, shown and then reverted. |
| `atlas/decision-self-hosted` | `TestDecision` passes with 0001. `grep -n "Design, no code\|does not exist\|Everything runs on a laptop" README.md CLAUDE.md` prints nothing. The PR is docs-tier, so its checks show the Docs map step ran. |
| `atlas/pr-template` | `go test ./internal/arch -run TestPullRequestTemplate` passes, and fails when Touched and Risk swap places, shown and then reverted. |

Stage gate evidence (five PRs with a derived recap) belongs to `vouch/recap`, not here.

---

## Out of scope

- vouch's derivation: path classification, the composes/extends/adds labels, edges, contract
  and dependency detection, comparing the recap to intent, and posting it.
- Foundry changes: `foundry ship` validating Door and Cleanup lines, and the recap headings
  themselves.
- Backfilling records for noes already written elsewhere, such as drafting without
  submitting and the credential-free crawler.
- Splitting `internal/core/app` into packages so primitives get directory roots.
- Building the sandbox, the broker or suspend, and the cluster deployment (stage 2).
- Branch protection settings, such as requiring a branch to be current with `main`.

---

## Decisions for the reviewer

1. **The five foundry headings replace the roadmap's "Intent, Door, Cleanup and System
   recap" sections.** What and why carries intent; Door and Cleanup are lines in Risk; the
   system recap is vouch's separate post. Rejected: separate Intent, Door, Cleanup and System
   recap headings, which would give human PRs a second heading set.
2. **Code entries may be files.** Three flat packages hold several primitives each. Rejected:
   directory-only roots, which make every core change "extends blueprint-runner" and the
   recap useless.
3. **A directory entry ends in `/`, a file entry does not, and the test checks which.**
   Rejected: bare prefixes, where `internal/core/app` also matches a future
   `internal/core/apps`.
4. **Every file under `internal/`, `cmd/`, `proto/` and `web/src/` must have an owner;
   `packs/`, `docs/` and `.github/` have none.** Rejected: allowing unowned harness files, so
   a new package silently drops out of every recap.
5. **One `record` primitive covers git and both indices.** Rejected: a separate
   `record-index` primitive; the indices are derived and change with the record.
6. **The index is `docs/decisions/README.md`.** Rejected: `index.md`, which GitHub does not
   show when browsing the folder.
7. **Revisit if is its own heading, checked by the test.** Rejected: kody's placement inside
   Consequences, which a test cannot check.
8. **No backfilled records.** Only 0001 ships. Rejected: seeding records for noes already in
   README and the arch tests now, which widens a stage meant to set the shape.
9. **No self-hosted invariant in the map.** Rejected: an invariant pointing at 0001; it
   matches no path, so vouch could never flag a change touching it, and it would tie the two
   PRs' order.
10. **The docs CI tier runs the map tests.** Rejected: dropping `docs/` from the docs-only
    pattern, which runs the full suite on every spec PR.
11. **`atlas/pr-template` needs `atlas/decisions` and `atlas/primitives`, and owns the
    Layout and pointer sections of `CLAUDE.md`; `atlas/decision-self-hosted` owns README and
    the CLAUDE.md intro and Status.** Rejected: the roadmap's empty Needs for
    `atlas/pr-template`, which would link files that do not exist yet.
12. **README says the sandbox, broker and suspend are not built.** Rejected: leaving "The
    claim" to read as built.
13. **No `AGENTS.md`.** `CLAUDE.md` links the two files one hop away. Rejected: a kody-style
    agent index file, a second entry point to keep in step.
14. **The template's headings are checked in atlas; PR bodies are not.** Rejected: a CI job
    reading PR bodies, which duplicates what foundry and vouch own.
15. **Strict decoding of the map.** Rejected: lenient decoding, under which a misspelt
    `code:` key leaves a primitive without roots and every check still passes.

---

## Review focus

Likely failure modes, in the order they would bite:

1. **Misattribution in flat packages.** A new file in `internal/core/app` lands on
   `blueprint-runner` and the coverage test still passes. Check the file entries cover every
   current file that belongs to another primitive.
2. **The docs tier.** Without the CI step, a docs-only PR that deletes a referenced doc or
   repeats a decision number merges green and breaks the next code PR.
3. **Cached test results.** `go test` caches; a walk that is not witnessed lets a stale pass
   stand after a file moves. Confirm the walks follow `witnessModuleTree`.
4. **Parallel number collisions.** Two sessions both take 0002; each PR passes alone. The
   check catches it only on a branch rebased onto `main`, or on the push to `main`.
5. **README overstating.** The Status rewrite must not describe the cluster deployment, the
   sandbox, the broker or suspend as built.
6. **Pack vocabulary in summaries.** `sources` and `conversion` invite pack words; the
   vocabulary test covers the map, so read the summaries against it.
7. **The template passes unedited.** Its Risk line starts with `low`, so a template body
   pasted as an agent recap passes foundry's level check. The line is a prompt for humans;
   foundry's validation is unchanged.
8. **Contract drift with vouch.** The shape is fixed. A field added here without
   `version: 2` breaks vouch's decoder or is silently ignored by it.
