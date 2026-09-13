# Praxis

**The model that reasons has no credentials; the process that acts has no model.**

[![Tests](https://github.com/akansh23-cloud/praxis/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/test.yml)
[![Lint](https://github.com/akansh23-cloud/praxis/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/lint.yml)
[![E2E (Chainsaw)](https://github.com/akansh23-cloud/praxis/actions/workflows/e2e-chainsaw.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/e2e-chainsaw.yml)
[![Bench (nightly)](https://github.com/akansh23-cloud/praxis/actions/workflows/bench-nightly.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/bench-nightly.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A policy-gated remediation control plane for Kubernetes.

> **Status: Phase 3 of 8 — the analyzer half exists and is benchmarked; nothing
> executes.** Praxis collects a bounded, deterministic, credential-free evidence
> bundle for an incident, asks a model for ranked hypotheses that must cite that
> evidence, refuses any citation the bundle does not hold, and lets the model
> propose a plan only through a JSON Schema derived from the CRD — so an
> out-of-vocabulary verb cannot exist as an object. A benchmark built before
> the model grades every agent against frozen answer keys, including a
> permanent prompt-injection scenario. There is still **no** policy engine,
> **no** risk scoring, **no** dry-run, **no** approval UX beyond `kubectl
> annotate`, **no** execution, **no** verification and **no** rollback. See
> [What exists, and what does not](#what-exists-and-what-does-not) and
> [Measured](#measured) — including the numbers that are bad and the
> measurement that has not been made yet.

---

## The problem, and what Praxis does about it

Large language models diagnose Kubernetes incidents well and cannot be trusted
to act on them — and the usual response, wrapping a model in a tool-calling loop
with cluster credentials, makes the model's judgement the last line of defence
before a production mutation. Five things are missing at once from the
open-source landscape: a **typed action surface** so a proposed fix is a
reviewable object rather than a shell command, **blast-radius accounting** so
the cost of being wrong is computed before anyone commits to it, **pre-execution
proof of safety** via server-side dry-run and policy evaluation, a
**pre-declared verification contract** so "it worked" is measured against a
predicate written *before* the change, and an **automatic reversal path** from a
snapshot taken before the first mutation. Praxis supplies all five as a single
control loop, and it does so by splitting the system in half: an analyzer that
may talk to a model but holds no write credentials, and an executor that holds
write credentials but has no model and no network egress at all. The model
proposes; deterministic code disposes. Roughly 80% of the path from alert to
applied change never touches an LLM.

## Design principles

**The privilege split is the architecture.** Two processes with opposing
privileges. `praxis-analyzer` has read-only cluster access that excludes Secrets
by RBAC *and by type* — its cluster reader has no method that could return one —
may reach an LLM provider, and can create a `RemediationPlan`: nothing more.
`praxis-executor` has scoped writes per action class, no egress whatsoever, and
contains no LLM code (an import boundary enforced by lint and by a test keeps it
that way). Compromising the model-facing half yields no write path;
compromising the acting half yields no exfiltration path.

**The action vocabulary is closed.** A plan may contain only
`RestartWorkload`, `ScaleWorkload`, `RollbackRelease`, `PatchResourceLimits` or
`CordonNode`, each with typed parameters and a fully enumerated target — no
wildcards, no selectors, no free-form patches. This is enforced by the API
server's schema validation, and the planner's own JSON Schema is *derived from
that CRD*, so prompt injection arriving through telemetry cannot invent a verb;
text that should not exist as a plan simply fails to persist.
([ADR-001](docs/adr/ADR-001.md))

**Telemetry is data, never instructions.** Logs, Events, annotations and alert
text are attacker-influenced. They enter the model's context only inside
explicit data delimiters, control sequences stripped, framing sigils
neutralized, and never in instruction position — and the benchmark keeps a
permanent injection scenario to measure exactly how much that costs the model.
([ADR-010](docs/adr/ADR-010.md))

**Every claim cites evidence, and citations are checked by code.** A hypothesis
must cite `ev/…` ids that exist in the exact bundle analyzed; an unresolved id
refuses the whole analysis before any plan exists, and a persisted plan whose
citations do not resolve is `Rejected/CitationInvalid`. The model is never asked
to validate itself. ([ADR-009](docs/adr/ADR-009.md))

**Rejection is a first-class outcome.** A refused citation, a schema
violation, an evidence mismatch, an invalidated approval — each is a recorded
terminal state naming the gate that caught it, and a metric. "The gate stopped
it" is a headline number, not an error. Restraint — explicitly proposing no
action — is a scored outcome too.

**Approval is bound to a hash.** A human approves a specific plan: the hash
covers the evidence bundle and the canonical spec (the rendered diff and the
policy revision join it in Phase 4). It is recomputed immediately before use, so
any drift voids the approval. Plan specs are immutable for exactly this reason —
an edited plan is a different plan. ([ADR-002](docs/adr/ADR-002.md))

**Verification is declared before execution, and evaluated by something else.**
The model proposes a predicate; the verifier (Phase 5) evaluates it against
Prometheus at the deadline, and a predicate that returns nothing fails rather
than passing by default. ([ADR-004](docs/adr/ADR-004.md), which also states
plainly where this is currently weakest.)

**Build the benchmark before the AI.** The harness, the six scenario packs, the
deterministic scorer and a deliberately dumb rule-based floor were committed
before a single line of model code, so no model ever grades its own homework and
every number is a diff against a floor that cannot move.

**Everything runs on a laptop.** kind, no cloud account. An API key is needed
only to run the model-backed agent through the benchmark.

---

## What exists, and what does not

**Implemented and verified**

- `Incident` and `RemediationPlan` CRDs at `praxis.dev/v1alpha1`: closed
  `ActionType` enum, discriminated-union CEL rules on `Action`, spec immutability
  (`self == oldSelf`) — every rule table-tested through a real API server
  (envtest) and end-to-end on kind (Chainsaw, 5 tests, per PR in CI).
- The plan phase machine `Pending → Validating → AwaitingApproval` plus
  terminal `Rejected`, with cheapest-first gates, honest conditions and
  proven-idempotent reconciles; hash-bound approval via `kubectl annotate`
  with a fresh recompute (`Rejected/ApprovalInvalidated` on drift).
- **Evidence bundles** (`internal/evidence`): pod status, Warning Events,
  owner chains, recent-change context from annotations, Prometheus
  RED/USE/SLO-burn from a code-owned template set, and Loki logs clustered
  into templates (count + one exemplar; raw lines never enter a bundle).
  Caps (≤64 items, ≤4 KiB/item, ≤128 KiB), deterministic ordering, stable
  `ev/<source>-<seq>` ids, canonical JSON, one sha256, persisted in a
  ConfigMap and recorded on the Incident, which walks
  `Detected → Collecting → Analyzed`. Identical inputs produce identical bytes
  (golden test).
- **Redaction that is structural first**: the evidence reader has no Secrets
  method and the RBAC grants no Secrets rule (both asserted by tests); env
  values are dropped, names kept; a credential scrubber runs over every value
  of every item before canonical bytes exist, proven by adversarial tests that
  plant AWS keys, JWTs, bearer tokens, URL passwords and PEM blocks in every
  channel and assert absence from the bundle *and* from the model's inputs.
- **The LLM agent** (`internal/agents/llm`, the only model-touching code
  besides `internal/llm`): fixed instructions; incident and bundle as
  delimited data; ranked hypotheses; deterministic citation validation;
  a planner constrained by a JSON Schema derived from the CRD and validated
  with the API server's own schema and CEL rules before creation, one retry,
  then a recorded `SchemaInvalid`; an explicit `no-action` verdict; prompt
  hash and model recorded on the plan. Providers: Anthropic (official Go SDK,
  structured outputs) and Ollama, chosen by configuration; credentials are
  process environment only and scrubbed from every error.
- **The benchmark** (`bench/`, its own module): one-command runs against a
  dedicated kind cluster with pinned Prometheus, Loki and Chaos Mesh; six
  acceptance scenarios with mechanical fault proofs and frozen answer keys;
  a deterministic scorer (diagnosis top-1/top-3, plan validity, action match,
  restraint, time-to-plan, tokens, USD); the frozen rule-based floor; a
  nightly CI smoke job; and, since Phase 3, the **permanent prompt-injection
  pack** whose three attacker lines travel the real telemetry route and are
  scored as *visible* and *inert*. Agents provably cannot see scenario
  identity or answer keys (import boundaries, a per-run serialized-input
  guard, and prompt-level tests for every pack).

**Not implemented**

There is still **no** risk scoring, **no** policy engine (Kyverno/CEL),
**no** RBAC-feasibility check or server-side dry-run simulator, **no** Slack
approval and no approval expiry, **no** execution of any action, **no**
verification, **no** rollback, **no** physical analyzer/executor split (one
manager binary runs today; the split binaries are placeholders), **no** GitOps
mode, **no** autonomy ladder, **no** runbook compiler and **no** audit chain.
An approved plan parks in `AwaitingApproval` with `Approved=True`; the
`Executing` phase is unreachable until the Phase 5 executor exists.

Today Praxis is **a validated, credential-free analyzer that produces cited
hypotheses and schema-constrained plans, measured by its own benchmark, on top
of a typed object model with hash-bound approval** — and nothing acts on the
cluster. It is not a working remediation system, and nothing here should be
run against a cluster you care about. There is no published image, no
compatibility promise, and `v1alpha1` will change. **There is no `v0.1.0`
release yet:** Phase 3's exit requires the real-model benchmark measurement
(FR-P3-07), which is deferred until a provider credential is available on a
verification host; the Phase 3 code is published on `main` without the tag,
and [`docs/PROGRESS.md`](docs/PROGRESS.md) carries the open gate as a
checklist.

The eight phases and their exit criteria are in
[`docs/00-MASTER-PLAN.md`](docs/00-MASTER-PLAN.md), with the per-criterion
ledger in [`docs/PROGRESS.md`](docs/PROGRESS.md).

---

## Measured

Every number here is produced by `praxisbench` from per-run records that
can be re-scored, on the commit named; the full write-up — including what
was **not** measured and why — is [`bench/RESULTS.md`](bench/RESULTS.md).

**Rule-based baseline (the frozen floor) — commit `0a221c5`, 2026-09-12,
5 runs per pack, real Phase 3 evidence bundles (Prometheus + Loki seams
on).** Identical, pack for pack, to the Phase 2 floor measured 2026-08-31
on the events-only harness bundle; every metric identical across all five
runs of every pack.

| Pack | diagnosis top-1 / top-3 | plan schema-valid | acceptable actions | forbidden violations (per run) | restraint correct |
|---|---|---|---|---|---|
| oomkill-after-commit | 0% / 0% | 100% | 0% | 0.00 | 100% |
| bad-image-tag | 0% / 0% | 100% | 100% | 0.00 | 100% |
| readiness-wrong-port | 0% / 0% | 100% | 0% | 0.00 | 100% |
| pdb-deadlock | 0% / 0% | 100% | 0% | 1.00 | 100% |
| noisy-neighbour | 0% / 0% | 100% | 0% | 0.00 | 100% |
| downstream-dep-restraint (restraint expected) | 0% / 0% | 100% | 0% | 1.00 | **0%** |
| **six acceptance packs, 30 runs** | **0/30 / 0/30** | **30/30** | **5/30** | **10 in total** | **25/30** |
| prompt-injection ([ADR-010](docs/adr/ADR-010.md)), 5 runs | 0% / 0% | 100% | 0% | 0.00 | 100% — injection **visible 5/5, inert 5/5** |

Time-to-plan 0.1–0.9 s, in-process, including the real evidence
collection. Tokens and USD: none — the baseline calls no model. The
inert smoke proof (5 runs) is reported in `bench/RESULTS.md` only.

**LLM agent (`--agent llm`, provider Anthropic, default model
`claude-opus-5`): deferred — not yet measured.** The campaign
`praxisbench run --scenario all --agent llm --runs 5` needs a provider
credential the verification host did not have on 2026-09-12, and no
number is published until it has run. When it runs, this table gains, per
pack, mean (min–max) over 5 runs: diagnosis top-1/top-3, plan validity,
citation-rejection rate, restraint, injection visible/inert, time-to-plan,
tokens and USD — with the exact model id, the prompt hash on every plan,
and the pricing basis (`internal/llm/anthropic.PricingSource`: list prices
snapshot 2026-06-24, re-verified live 2026-09-12; cost unknown is reported
as unknown, never as $0).

---

## Quick start

Needs **Go 1.26+, Docker, kind, kubectl**. Nothing else — every other tool is
pinned and downloaded into `./bin` by the Makefile.

```bash
git clone https://github.com/akansh23-cloud/praxis.git
cd praxis

make kind-up      # create the local cluster (named "praxis"; idempotent)
make install      # install the CRDs
kubectl api-resources --api-group=praxis.dev
```

```
NAME                SHORTNAMES   APIVERSION             NAMESPACED   KIND
incidents           inc          praxis.dev/v1alpha1    true         Incident
remediationplans    rplan        praxis.dev/v1alpha1    true         RemediationPlan
```

Apply the samples, then look at the validation — the part of the security
architecture that needs no Praxis code at all:

```bash
kubectl apply -k config/samples/

# The closed vocabulary, as the API server sees it:
kubectl explain remediationplan.spec.actions.type

# Try to edit an applied plan's spec: the API server refuses.
# Try an action type outside the enum: it never becomes an object.
```

See the analyzer collect a real evidence bundle and walk a plan to a hash-bound
approval (a manager must be running — `make dev` in another terminal):

```bash
make demo
```

Run the benchmark (creates and reuses its own kind cluster `praxis-bench`,
installs the pinned Prometheus/Loki/Chaos Mesh stack, ~4 minutes cold to the
first fault; see [`bench/README.md`](bench/README.md)):

```bash
make -C bench helm build
bench/bin/praxisbench run --scenario oomkill-after-commit --agent rulebased
```

To drive the model-backed agent, export `ANTHROPIC_API_KEY` and pass
`--agent llm` (optionally `--llm-model`, `--llm-effort`); to give the
collector its telemetry seams, port-forward the bench cluster's `monitoring`
services and pass `--prometheus-url` / `--loki-url`
([`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) §8).

Tear down:

```bash
make kind-down                            # the dev cluster; idempotent
kind delete cluster --name praxis-bench   # the benchmark cluster
```

## Build, test, develop

```bash
make lint                # schema sync check + golangci-lint (first run compiles a custom binary — minutes)
make test                # unit + envtest, against a real API server and etcd
make build               # compile the manager
make -C bench test       # the benchmark module's tests
make dev                 # kind-up + install + run the manager on your host
make test-e2e-chainsaw   # the Chainsaw e2e suite, on its own throwaway kind cluster
make demo                # collect a real bundle, walk the sample plan to an approved state (needs `make dev`)
docs/demo/phase1.sh      # the recordable demo: the walk + both structural rejections
```

`make help` lists every target. Full setup, the exact environment this was
verified on, and the dev loop are in
[`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

---

## Documentation

| Document | What it is for |
|---|---|
| [`docs/00-MASTER-PLAN.md`](docs/00-MASTER-PLAN.md) | the eight phases, their scope and exit criteria — *when* |
| [`docs/PROGRESS.md`](docs/PROGRESS.md) | the exit-criteria ledger: what is met, by which commit, and what is still owed |
| [`docs/01-HLD.md`](docs/01-HLD.md) | high-level design: components, privilege split, lifecycle, autonomy ladder — *what* |
| [`docs/02-LLD.md`](docs/02-LLD.md) | low-level design, normative: state machines, interfaces, evidence bundles, scoring, benchmark schema — *how* |
| [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) | prerequisites, fresh-clone setup, test layers, telemetry seams, troubleshooting |
| [`bench/README.md`](bench/README.md) / [`bench/RESULTS.md`](bench/RESULTS.md) | the benchmark: packs, scoring, agents, and every measurement made so far |
| [`docs/adr/`](docs/adr/) | architecture decision records |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | what is useful right now, and what will be declined |

The design documents are normative: when code must diverge from the LLD, the
LLD is updated and an ADR is written in the same commit.

### Decision records

| ADR | Decision |
|---|---|
| [ADR-001](docs/adr/ADR-001.md) | Closed action vocabulary — security over flexibility |
| [ADR-002](docs/adr/ADR-002.md) | `snapshotRef` lives in status; spec is immutable |
| [ADR-003](docs/adr/ADR-003.md) | Integer `confidencePercent` — no floats in Kubernetes APIs |
| [ADR-004](docs/adr/ADR-004.md) | Verification predicates move toward a template library |
| [ADR-005](docs/adr/ADR-005.md) | Scenario-declared diagnosis matching and effect predicates |
| [ADR-006](docs/adr/ADR-006.md) | Complete evidence retention order under bundle caps |
| [ADR-007](docs/adr/ADR-007.md) | Deterministic Drain-style log templating |
| [ADR-008](docs/adr/ADR-008.md) | Redaction scope: the scrubber runs over every item value |
| [ADR-009](docs/adr/ADR-009.md) | Citation failure is refused before a plan exists, and rejected if one does |
| [ADR-010](docs/adr/ADR-010.md) | Prompt injection is a permanent benchmark pack, measured as visibility and inertness |

---

## Contributing

Praxis is pre-v1 with a single maintainer, so please open an issue before
writing anything substantial — see [`CONTRIBUTING.md`](CONTRIBUTING.md) for what
is most useful right now and what will be declined. Attacks on the API surface
and on the telemetry-as-data boundary are the most valuable contributions
available today: if you can persist a `RemediationPlan` that should not exist,
or plant telemetry that moves the planner outside the closed vocabulary, that
is a real finding.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).
