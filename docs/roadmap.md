# Roadmap

Atlas is a harness for domain-specific agents. Its first product is job search: find the
latest relevant postings, keep watching for more like them, and have every application
drafted before you open it. Atlas is also the first product built end to end by the
software factory (foundry and vouch), and the factory is built to the Agentic Coding
Paradigm (ACP).

Each stage below gets its own spec, plan and implementation, in the owning repo. A stage
starts when the previous stage's gate is met.

## The product

### Components

Each component stands alone and is composed into atlas by configuration.

| Component | Role in atlas |
|---|---|
| atlas | Harness, packs, web UI, the record |
| nerve | Storage and memory: hybrid (vector and keyword) search, sessions, durable memory |
| cana | Document conversion and extraction |
| feed | Job sources: ATS board APIs and rendered careers pages |

### The MVP demo

1. Search jobs by keyword or meaning across every source, in a glassdollar.com-style UI.
2. Add a source. Sources that need a login use browser tools; credentials never enter a
   model's context.
3. Open a job. Its details and apply link are the employer's own posting, even when it
   was found on an aggregator.
4. Pick the jobs that fit. A background run keeps searching every source for similar
   jobs and puts them on a review board: to review, apply, applied.
5. Move a job to apply. An agent reads that application's questions and documents and
   drafts every answer from your background, citing your own text. Nothing is submitted.
6. Mark it applied. The job and its drafts go to nerve as memory for what comes after.

After the MVP: interview preparation, follow-ups and outcome tracking, built on the
memory step 6 writes.

### Environments

| Environment | Where | Purpose |
|---|---|---|
| qa1 | This workstation | Always-on home deployment; per-PR previews; the evidence environment |
| hosted | A free cloud tier | The reduced stack: app and Postgres. Never billed |

There is one user. Anyone who meets the requirements that `make preflight` checks can
bring atlas up on their own machine or infrastructure with one command.

## The factory

Agents ship; the human decides what the product is, and merges. Two references shape the
factory:

- **ACP** (*Code Is Not the Product: The Agentic Coding Paradigm*). Changes are understood
  as shifts in meaning, scored by risk, and approved by evidence. Five phases, each gated:
  semantic change reports; the map as a service; scoped tokens and evidence-based
  escalation; risk lanes and a verifying judge; disposable environments, the production
  loop and contract-level scopes.
- **Kody** (Kent C. Dodds). Three layers of trust: agents make good PRs (decisions,
  primitives, a control CLI, a system recap in every PR), mistakes are caught before
  production (a deterministic ship process, AI reviewers, previews), and production
  limits the blast radius (feature flags with success metrics, tested restore). Signal
  sources feed work in: friction, dogfooding, errors, scheduled cleanup.

Where they differ, atlas takes this side:

| Question | Choice |
|---|---|
| Who writes the system recap | The agent states intent; vouch derives the recap from the code and flags a mismatch |
| Who reviews | A model from a different vendor than the author's |
| Who merges | The human, until risk lanes earn auto-merge for a lane |

## Stages

### 1. Factory A: good PRs

- atlas: `docs/decisions/` (product decisions, usually a no, each with a revisit-if), `primitives.yaml`
  (harness primitives, owning paths, invariants), a PR template (Intent, Door, Cleanup,
  System recap).
- vouch: derive the system recap per PR (primitives composed, extended or added; new
  edges; contract changes; new external dependencies) and compare it to the stated intent.
- foundry: a feature lane, for issues the human writes, beside the friction lane.

Gate: five consecutive atlas PRs carry a derived recap, and on large PRs the human prefers
the recap to the diff.

### 2. Infrastructure by product

`infra` is organised by owner: shared forge services (Gitea, registry, caches), the
workstation's own namespace (metrics, telemetry, the foundry board), atlas, and
namespaced experiments. Stacks are named by environment, shared code lives in modules,
and each environment has one up and one down command. Atlas's deployment artifacts work
without any forge-specific service, so another person can run them.

Gate: `qa1-up` converges from clean and a second run changes nothing; `qa1-down` leaves
the workstation and experiments untouched; `make preflight` names each missing
requirement.

### 3. Factory B: catch mistakes before qa1

- Atlas deploys to qa1 through GitOps.
- Every atlas PR gets a preview namespace on qa1.
- A control CLI lets an agent verify its change against a running atlas; its output is
  the PR's evidence.
- CI installs atlas from the README on a clean runner on every PR.

Gate: every atlas PR carries preview evidence, and the fresh-install job is green.

### 4. The job data pipeline

Postgres is the system of record. Pollers call each source at its own cadence with
conditional requests, change detection and a breaker per source. Changes leave through a
transactional outbox to RabbitMQ; idempotent consumers enrich, resolve the employer's
posting and merge duplicates, embed and index through nerve, and match saved searches.
New matches reach the review board as they land. Replay reads Postgres, never the broker.
Backups run with a scheduled restore test.

Gate: the freshness target (posted at source to searchable, p95, set in the spec) holds
on qa1 for seven days; replay rebuilds identical indices; a failing source or broker
loses nothing; a restore test passes.

### 5. The MVP slices

Each slice is a thin end-to-end path, shipped through the factory, in this order:

1. Keyword search and the search UI over existing sources.
2. Semantic search through nerve.
3. The employer's posting as the job's record; duplicates merged.
4. Saved searches, the background run and the review board.
5. Application drafting on apply.
6. Applied jobs written to nerve.
7. Sources behind a login.

Experiments ship behind feature flags that name a success metric.

Gate: the demo runs end to end on a clean machine by following the README.

### 6. Hosted

The reduced stack on a free cloud tier, deployed from the same artifacts as qa1.

Gate: a month with a zero bill; a restore test against the hosted database passes.

### 7. Factory C: autopilot inputs and ACP phases 2 to 5

- Work sources: dogfooding agents, errors from telemetry, scheduled docs and test
  gardening, a weekly sweep for unused code.
- ACP phase 2: the map answers questions through MCP and a CLI.
- ACP phase 3: per-task scoped tokens; escalation with evidence.
- ACP phase 4: risk lanes and a judge that re-runs the evidence.
- ACP phase 5: disposable environments by default; outcomes retrain routing; scopes move
  from paths to contracts.

Gates: as the ACP roadmap states them. Map answers match reality on a sampled set;
escalations are rare and no write lands out of scope; audits of auto-approved changes find
an acceptably low miss rate.
