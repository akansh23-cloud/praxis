# Praxis

**The model that reasons has no credentials; the process that acts has no model.**

[![Tests](https://github.com/akansh23-cloud/praxis/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/test.yml)
[![Lint](https://github.com/akansh23-cloud/praxis/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/lint.yml)
[![E2E (Chainsaw)](https://github.com/akansh23-cloud/praxis/actions/workflows/e2e-chainsaw.yml/badge.svg?branch=main)](https://github.com/akansh23-cloud/praxis/actions/workflows/e2e-chainsaw.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A policy-gated remediation control plane for Kubernetes.

> **Status: Phase 1 of 8 complete — lifecycle and approval binding proven
> end-to-end; no execution yet.** A hand-written plan walks
> Pending → Validating → AwaitingApproval on a real cluster, a human's
> approval is cryptographically bound to the exact plan and evidence, and
> tampering — an edited spec, a wrong hash, an out-of-vocabulary verb — is
> structurally rejected. Praxis still does not collect evidence, call a
> model, score risk, evaluate policy, execute anything, verify anything, or
> roll anything back. See [Current status](#current-status) for exactly what
> does and does not exist.

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
by RBAC, may reach an LLM provider, and can create a `RemediationPlan` — nothing
more. `praxis-executor` has scoped writes per action class, no egress
whatsoever, and contains no LLM code (an import boundary keeps it that way).
Compromising the model-facing half yields no write path; compromising the acting
half yields no exfiltration path.

**The action vocabulary is closed.** A plan may contain only
`RestartWorkload`, `ScaleWorkload`, `RollbackRelease`, `PatchResourceLimits` or
`CordonNode`, each with typed parameters and a fully enumerated target — no
wildcards, no selectors, no free-form patches. This is enforced by the API
server's schema validation, so prompt injection arriving through telemetry
cannot invent a verb; text that should not exist as a plan simply fails to
persist. ([ADR-001](docs/adr/ADR-001.md))

**Rejection is a first-class outcome.** Scope violation, policy denial, RBAC
infeasibility, a failed dry-run, an expired approval — each is a recorded
terminal state naming the gate that caught it. "The policy stopped it" is a
headline metric, not an error.

**Approval is bound to a hash.** A human approves a specific plan: the hash
covers the evidence bundle, the canonical spec, the rendered diff and the policy
revision. It is recomputed immediately before execution, so any drift voids the
approval. Plan specs are immutable for exactly this reason — an edited plan is a
different plan. ([ADR-002](docs/adr/ADR-002.md))

**Verification is declared before execution, and evaluated by something else.**
The model proposes a predicate; the verifier evaluates it against Prometheus at
the deadline, and a predicate that returns nothing fails rather than passing by
default. The model never grades its own work. ([ADR-004](docs/adr/ADR-004.md),
which also states plainly where this is currently weakest.)

**Autonomy is earned, never assumed.** Five tiers from observe-only to compiled
deterministic runbooks, entered per action class on measured history. The
highest tier uses the *least* AI — the goal is to convert probabilistic
reasoning into deterministic automation over time.

**Everything runs on a laptop.** kind, no cloud account, no API key required for
anything that exists today.

---

## Current status

Phase 1 of eight complete. What that means, concretely:

**Implemented and verified**

- `Incident` and `RemediationPlan` CRDs, served at `praxis.dev/v1alpha1`.
- Closed `ActionType` vocabulary enforced as a schema enum — out-of-vocabulary
  verbs are rejected by the API server before the object exists.
- Discriminated-union CEL validation on `Action`: the parameter block must match
  the verb, `CordonNode` must target a `Node` and nothing else may, and
  namespace is required for namespaced kinds and forbidden for `Node`.
- `RemediationPlanSpec` immutability, enforced by a `self == oldSelf` CEL
  transition rule. Every one of these admission rules is table-tested through a
  real API server (envtest) and end-to-end on kind (Chainsaw).
- The plan phase machine: `Pending → Validating → AwaitingApproval` plus
  terminal `Rejected`, with per-phase handlers, honest conditions
  (`EvidenceValid`, `CitationsResolved`, `ScopeValid`, `Approved`), cheapest-
  first validation, and proven-idempotent reconciles (zero status writes on
  settled objects — measured, not assumed).
- A real evidence-hash check: a plan citing evidence its Incident does not
  hold is `Rejected/EvidenceMismatch`, even though bundles themselves arrive
  in Phase 3 (hashes are hand-set until then).
- Hash-bound approval: entering `AwaitingApproval` computes
  `status.approval.boundTo` (canonical-JSON + sha256, the LLD §8 interim
  form); a human approves via the `praxis.dev/approve` annotation, the
  controller recomputes the hash fresh from live state, and any disagreement
  lands the plan in `Rejected/ApprovalInvalidated`. Approval UX is
  deliberately just `kubectl annotate` until the Slack gate (Phase 4).
- Citation validation and scope checking as interfaces with stub
  implementations that say so: their conditions read `StubbedInPhase1` in
  `kubectl describe` — visible honesty over silent fakes. Real
  implementations land in Phases 3–4.
- End-to-end proof on kind: a Chainsaw suite covering the full walk, the
  approval round-trip, wrong-hash rejection, evidence mismatch, the spec-edit
  refusal and the DeleteNamespace non-persistence — run per PR in CI and
  reproducible locally with `make test-e2e-chainsaw`. A scripted, repeatable
  demo lives at `docs/demo/phase1.sh` (`make demo-record` to record it).

**Not implemented**

There is still **no** evidence collection, **no** LLM integration of any
kind, **no** hypothesis generation, **no** real citation or scope validation
(stubs only, labelled as such), **no** risk scoring, **no** policy
evaluation, **no** dry-run simulation, **no** execution, **no**
verification, and **no** rollback. An approved plan parks in
`AwaitingApproval` with `Approved=True` — the `Executing` phase is
unreachable until the Phase 5 executor exists. The `praxis-analyzer` and
`praxis-executor` binaries are placeholder entrypoints that exit non-zero
telling you which phase implements them.

Today Praxis is a **validated lifecycle with a cryptographically bound
approval on top of a typed object model** — a plan can be walked, approved
and structurally protected, but nothing acts on the cluster. It is not a
working remediation system, and nothing here should be run against a cluster
you care about. There is no release, no published image, no compatibility
promise, and `v1alpha1` will change.

The eight phases and their exit criteria are in
[`docs/00-MASTER-PLAN.md`](docs/00-MASTER-PLAN.md), with the per-criterion
ledger in [`docs/PROGRESS.md`](docs/PROGRESS.md); a publishable v0.1 is
scheduled for the end of Phase 3.

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

Apply the samples, then look at the part that actually works today — the
validation:

```bash
kubectl apply -k config/samples/

# The closed vocabulary, as the API server sees it:
kubectl explain remediationplan.spec.actions.type

# Try to edit an applied plan's spec: the API server refuses.
# Try an action type outside the enum: it never becomes an object.
```

Tear down:

```bash
make kind-down    # idempotent
```

## Build, test, develop

```bash
make lint                # golangci-lint (first run compiles a custom binary — minutes)
make test                # unit + envtest, against a real API server and etcd
make build               # compile the manager
make dev                 # kind-up + install + run the manager on your host
make test-e2e-chainsaw   # the Phase 1 e2e suite, on its own throwaway kind cluster
make demo                # walk the sample plan to an approved state (needs `make dev`)
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
| [`docs/02-LLD.md`](docs/02-LLD.md) | low-level design, normative: state machines, interfaces, scoring, RBAC — *how* |
| [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) | prerequisites, fresh-clone setup, test layers, troubleshooting |
| [`docs/adr/`](docs/adr/) | architecture decision records |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | what is useful right now, and what will be declined |

The design documents are normative: when code must diverge from the LLD, the
LLD is updated and an ADR is written in the same pull request.

### Decision records

| ADR | Decision |
|---|---|
| [ADR-001](docs/adr/ADR-001.md) | Closed action vocabulary — security over flexibility |
| [ADR-002](docs/adr/ADR-002.md) | `snapshotRef` lives in status; spec is immutable |
| [ADR-003](docs/adr/ADR-003.md) | Integer `confidencePercent` — no floats in Kubernetes APIs |
| [ADR-004](docs/adr/ADR-004.md) | Verification predicates move toward a template library |

---

## Contributing

Praxis is pre-v0.1 with a single maintainer, so please open an issue before
writing anything substantial — see [`CONTRIBUTING.md`](CONTRIBUTING.md) for what
is most useful right now and what will be declined. Attacks on the API surface
are the most valuable contribution available today: if you can persist a
`RemediationPlan` that should not exist, that is a real finding.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).
