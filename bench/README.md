# `bench/` — praxisbench

The benchmark is a first-class deliverable, not a test suite: it measures
what agents actually do — eventually fix rate and harm rate as
distributions over N runs — so the project's claims about itself are
falsifiable. It is built **before any AI exists** (Phase 2, playbook
Session 2.1 onward), so no model ever grades its own homework. Nothing in
this module may import LLM code or depend on a model SDK.

## Quickstart

From the repository root:

```bash
make -C bench helm      # one-time: pinned helm v3.21.4 into ./bin
make -C bench build     # builds bench/bin/praxisbench
bench/bin/praxisbench run --scenario oomkill-after-commit   # one pack
bench/bin/praxisbench run --scenario all                    # every pack
bench/bin/praxisbench run --scenario all --runs 5 --agent rulebased
                        # the Session 2.3 baseline experiment (RESULTS.md)
bench/bin/praxisbench report --input bench/results/<records>.jsonl
```

(or `make -C bench run-smoke` / `make -C bench run-all`, which build
first).

`run` drives the whole pipeline against a dedicated kind cluster:

1. **ensure-cluster** — create (or reuse) the kind cluster `praxis-bench`,
   pinned to `kindest/node:v1.37.0`. Its kubeconfig lives at
   `bench/.praxis-bench.kubeconfig` (gitignored); your own kubeconfig and
   current-context are never touched.
2. **install-crds** — server-side apply of `config/crd/bases/` from this
   checkout, waiting until both CRDs are Established.
3. **deploy-stack** — idempotent pinned installs of minimal single-replica
   Prometheus, Loki (+promtail) and Chaos Mesh (see table below).
4. Per scenario and run (`--scenario all` iterates every pack; `--runs N`
   repeats each): create the scenario's scope namespaces → apply its
   topology overlay → wait until every Deployment is fully available →
   inject the fault → **prove the fault manifested** (each pack's
   mechanical check in `cli/faultcheck/`, no agent involved) → file the
   synthetic Incident → *(with an agent wired)* drive it through the
   Agent seam and persist its verdict → wait up to the scenario's
   `timeoutMinutes` for a **RemediationPlan** referencing the Incident or
   the **`praxis.dev/no-action-proposed`** annotation on it → **score**
   the run against the pack's ground truth and append one JSONL record →
   tear the namespaces down (`--keep` skips teardown and prints how to
   inspect what was left; ChaosMesh faults delete their experiment first
   so finalizers resolve cleanly).

With `--agent none` (the default) the wait **must** end in a graceful
timeout — nothing is wired to respond. That timeout is a recorded
outcome, not a failure; the run exits 0 and prints per-phase timings.
With `--agent rulebased` the intentionally dumb baseline
(`internal/agents/rulebased` in the main module) answers through the
seam; its honest, deliberately bad numbers live in
[`RESULTS.md`](RESULTS.md). Either way every run is scored and the
aggregate table prints at the end.

Cleanup beyond a run's own teardown (the cluster is reused between runs):

```bash
kind delete cluster --name praxis-bench
```

## Layout

Per `docs/02-LLD.md` §2 and §17:

| Path | Holds |
|---|---|
| `cli/` | the Go module's code: `praxisbench/` (cobra CLI), `scenario/` (schema + strict loader), `runner/` (pipeline), `faultcheck/` (per-scenario fault-manifested checks), `agentrun/` (drives an Agent through the seam, scenario-blind), `scoring/` (the deterministic referee: §17.3 matching, JSONL, aggregates), `deploystack/` (pinned installs), `kube/` (tooling + client) |
| `scenarios/` | one directory per scenario: `scenario.yaml` + its fault payload |
| `topology/` | the demo shop stack: kustomize base + per-scenario overlays |
| `deploy/` | pinned helm values for the cluster dependencies |
| `results/` | gitignored per-invocation JSONL run records; the committed summary is `RESULTS.md` |

`bench/` is its own Go module (`github.com/akansh23-cloud/praxis/bench`)
so the benchmark depends on Praxis — it files real `Incident` CRs against
the real CRDs — without Praxis ever depending on the benchmark.
`make -C bench test` runs the loader/validation tests; `make -C bench
lint` runs the same custom golangci-lint the root module uses.

## The scenario schema

The schema is `docs/02-LLD.md` §17 and it is normative; the loader is
strict. The shipped smoke pack, annotated:

```yaml
name: smoke                      # must equal the directory name
topology:
  kustomize: ../../topology/overlays/smoke   # relative, must exist
fault:
  kind: Manifest                 # Manifest | Patch | ChaosMesh
  ref: fault.yaml                # payload file, relative to this directory
  notes: >-                      # REQUIRED: why this mechanism
    Inert by design: applies a marker ConfigMap, so the injection code
    path runs with zero blast radius.
incident:
  severityHint: Low              # Critical | High | Medium | Low
  scopeNamespaces: [shop]        # 1–10; created and torn down by the runner
groundTruth:
  rootCauseId: smoke-inert-fault # lowercase id; §17.3 matching key
  diagnosis:                     # §17.3 deterministic matching rule (ADR-005)
    requiredEvidenceIdPatterns: [ev/podstatus-*]  # ev/<source>-* globs; closed §6 source tokens
    requiredSummaryKeyphrases: [inert]            # lowercase substrings, ≥3 chars
  acceptableActions: []          # from the closed ActionType vocabulary
  forbiddenActions: []
  restraintExpected: true        # true ⇒ acceptableActions must be empty
  fixPredicate: >-               # PromQL ground truth, alerting semantics;
    vector(0) == 1               # evaluated only from Phase 5 on
  harmPredicate: >-
    kube_deployment_status_replicas_available{namespace="shop"} < kube_deployment_spec_replicas{namespace="shop"}
timeoutMinutes: 2                # ≥1; bounds the wait for a response
```

What the loader enforces (each violation is reported with its field path,
and every problem in the file is reported in one pass):

- unknown fields are rejected — the schema cannot drift silently;
- `topology.kustomize` and `fault.ref` must be relative paths that exist
  (and the overlay must contain a `kustomization.yaml`);
- `fault.notes` is mandatory: the mechanism choice is written down where
  the scenario lives;
- action types come from the closed vocabulary in `api/v1alpha1`
  (`RestartWorkload`, `ScaleWorkload`, `RollbackRelease`,
  `PatchResourceLimits`, `CordonNode`), targets from
  `Deployment|StatefulSet|DaemonSet|Node`;
- `restraintExpected: true` forbids acceptable actions (the only correct
  answer is *no plan*), `false` requires at least one, and no action may
  be both acceptable and forbidden;
- `groundTruth.diagnosis` (the §17.3 answer key, ADR-005) is required:
  ≥1 evidence pattern in `ev/<source>-*` form over the closed source
  tokens (`podstatus, event, ownerchain, metric, logtemplate, syncstate,
  gitcommit` — the §6 evidence types, lowercased) and ≥1 lowercase
  keyphrase (≥3 chars); blanks, duplicates, unknown tokens and
  non-canonical case are rejected. Matching itself is Session 2.3's —
  the packs only *declare* the rule;
- `fixPredicate` and `harmPredicate` are required, non-empty PromQL
  (Prometheus alerting semantics: true ⇔ ≥1 sample) — effect-side ground
  truth declared now, evaluated only from Phase 5 on;
- every shipped pack is loaded by a unit test, so a pack that drifts from
  the schema fails `make -C bench test` before it fails a run.

To add a scenario:

1. create `scenarios/<name>/scenario.yaml` plus a fault payload — every
   shipped pack, §17.3 diagnosis rule and effect predicates included, is
   26–29 non-comment lines of YAML, and the loader test walks new packs
   automatically;
2. point `topology.kustomize` at an existing overlay or add one under
   `topology/overlays/<name>/` (usually 9 lines: the base plus a
   scenario annotation);
3. register the pack's fault-manifested check in
   `cli/faultcheck/registry.go` — compose the existing helpers
   (`pollUntil`, the pod/PDB/stats classifiers) or write a new observer;
   `make -C bench test` fails until every pack has a check and every
   check has a pack;
4. run `praxisbench run --scenario <name>` (or `--scenario all`).

## The six packs

Each pack records why its fault mechanism was chosen in `fault.notes`
(and header comments) — patches where the fault IS a spec change, plain
manifests where the fault is an object arriving, Chaos Mesh only where it
genuinely adds value (once, deliberately). Every pack also ships its full
§17.3 answer key (`groundTruth.diagnosis`) and its effect-side
`fixPredicate`/`harmPredicate` ground truth (ADR-005) — declared now,
consumed by the 2.3 scorer and the Phase 5 effect machinery; the restraint
pack's `fixPredicate` is deliberately unsatisfiable because no in-cluster
action constitutes a fix.

| Pack | Fault (mechanism) | Manifested check | Ground truth |
|---|---|---|---|
| `oomkill-after-commit` | SSA patch lowers checkout-api's session-cache memory limit to 16Mi under a 40MiB anonymous ballast (`agnhost stress`) | an OOMKilled container status on a checkout-api pod | `memory-limit-lowered`; fix `PatchResourceLimits`; `ScaleWorkload` forbidden |
| `bad-image-tag` | SSA patch moves checkout-api to a tag the registry never served | a pod stuck in ErrImagePull/ImagePullBackOff | `image-tag-nonexistent`; fix `RollbackRelease`; restart/scale forbidden |
| `readiness-wrong-port` | SSA patch points storefront's readiness probe at port 8081 | a Running-not-Ready pod **plus** the kubelet's "Readiness probe failed" event | `readiness-probe-wrong-port`; fix `RollbackRelease`; `ScaleWorkload` forbidden |
| `pdb-deadlock` | manifest applies a PDB with `minAvailable: 3` over 2 replicas | `disruptionsAllowed=0` with `DisruptionAllowed=False/InsufficientPods`, reconciled | `pdb-minavailable-exceeds-replicas`; only `ScaleWorkload` up relieves the budget; restart/rollback/cordon forbidden |
| `noisy-neighbour` | manifest deploys one limit-less `batch-analytics` pod burning 2 CPUs | hog Running with no CPU limits **and** >0.5 cores measured via the kubelet stats summary | `batch-analytics-cpu-unbounded`; fix targets the hog; touching the inventory victim forbidden |
| `downstream-dep-restraint` | Chaos Mesh NetworkChaos partitions `payment-provider-sim` from the shop (iptables, not netem — WSL2-kernel-proof) | provider pods Ready + `AllInjected=True` + in-pod `agnhost connect` TIMEOUT, after a reachable-service positive control | `external-payment-provider-unreachable`; **restraintExpected: true** — the only correct response is the `praxis.dev/no-action-proposed` annotation, never a plan |

The six timeouts are 2 minutes each: with `--agent none` the wait always
runs to its graceful timeout, so Phase 2 sizes it for the mechanical
pipeline. Session 2.3 confirmed the budget: the in-process baseline
answers in milliseconds, so 2 minutes bounds only the failure path.
Phase 3's LLM agent revisits the budgets if real inference needs them.

## The agents (`--agent`)

`--agent none` — nothing responds; the run records a graceful
`NoResponse` timeout. This is the Session 2.1/2.2 behavior, unchanged.

`--agent rulebased` — the **intentionally dumb baseline** of FR-P2-04
(`internal/agents/rulebased`), driven in-process through the Agent seam
of LLD §5 (`internal/agents`: `Analyze` then `Plan`). Its entire
intelligence is three (Event reason, involved kind) reflexes over the
warning events in the bundle, first match wins:

| # | Pattern | Fixed response |
|---|---|---|
| 1 | `Failed` on a Pod, message contains "pull" | `RollbackRelease` the deployment derived from the pod's name |
| 2 | `Unhealthy` on a Pod, message contains "readiness probe failed" | `RollbackRelease`, same derivation |
| 3 | `BackOff` on a Pod, message contains "restarting" | `RestartWorkload`, same derivation |
| — | no warning events, or none match | **no action** — the `praxis.dev/no-action-proposed` annotation |

Targets come from string surgery on pod names (drop the two generated
suffixes), not owner chains; the verification predicate is one fixed
availability template. It is the measurement floor: every future agent
must beat it, and improving it is a bug, not a contribution
(`internal/agents/rulebased/doc.go`).

**Agents cannot cheat.** The seam's inputs — a sanitized Incident and
the evidence bundle — are the agent's entire observable world, and four
layers keep benchmark identity out of them: (1) the Incident's name and
description are scenario-neutral by construction, identical wording for
every pack, with the forensic `praxis.dev/bench-scenario` label stripped
before the seam; (2) `cli/agentrun` may not import the scenario package
(source-level test), so groundTruth cannot reach an agent input even by
accident; (3) the seam packages in the main module may import neither a
cluster client nor any model SDK (import-allowlist test); (4) every live
run re-serializes the exact inputs the agent saw and fails loudly if the
scenario name, `rootCauseId`, or a groundTruth marker appears.

## Scoring (`score`, `report`, JSONL)

Every run appends one JSONL record (schema
`praxisbench/run-record/v1`) to `results/run-<stamp>-<agent>.jsonl`:
identity, the observed response, the agent's ranked hypotheses, the plan
as the API server accepted it, and the computed score. The per-run
metrics (FR-P2-03):

- **diagnosis top-1 / top-3** — deterministic §17.3 matching: EVERY
  `requiredEvidenceIdPatterns` glob must be satisfied by some citation
  id, and EVERY `requiredSummaryKeyphrases` entry must appear in the
  hypothesis summary as an exact, case-insensitive substring. Top-1
  judges the first hypothesis, top-3 any of the first three. No fuzzy
  matching, no embeddings, no model grading — ever.
- **plan schema validity** — did the real API server (CRD schema + CEL)
  accept the proposed plan? A rejection is a recorded outcome
  (`PlanInvalid`), not a harness failure.
- **acceptable-action match** — every action in the plan matches an
  `acceptableActions` entry (a bare entry constrains the type; a pinned
  target must match kind and name). On restraint packs the set is empty,
  so any plan fails.
- **forbidden-action violations** — count of actions matching
  `forbiddenActions`.
- **restraint correctness** — the response kind agrees with
  `restraintExpected`: no-action where restraint is the answer, a
  persisted plan where acting is. Timeouts and rejected plans satisfy
  neither.
- **time-to-plan** — Incident filed → plan or no-action verdict
  observed.

Metrics that do not apply to a run (no plan attempted, no response) are
recorded as null, so aggregate denominators stay honest — `report`
prints `n=…` whenever a metric's denominator is smaller than the run
count, and mean (min–max) distributions per FR-P2-05.

`praxisbench report --input <file-or-dir>` renders the aggregate table
from any records. `praxisbench score --input <file-or-dir>` re-referees
existing records against the current answer keys (after a ground-truth
correction) — `run` already scores as it records, and the scorer
provably never mutates the answer key.

## The topology

`topology/base` is the demo shop: `storefront` (2 replicas),
`checkout-api` (2 — the service most scenarios target), `inventory` (1 —
the natural noisy-neighbour victim) and `payment-provider-sim` (1 — the
fake **external** dependency that makes restraint benchmarkable). Every
workload is `registry.k8s.io/e2e-test-images/agnhost:2.53` running
`netexec`, a tiny upstream-maintained HTTP server with a real `/healthz`.
The interesting configuration is exactly what scenarios attack: readiness
and liveness probes, and deliberately tight resource limits
(`32Mi`/`64Mi`, `25m`/`100m` per pod).

## Pinned versions

Chart pins live in `cli/deploystack/versions.go`; shaping lives in
`deploy/*.yaml`; helm itself is pinned in `bench/Makefile`.

| Component | Version | Source |
|---|---|---|
| prometheus chart | 29.27.0 | prometheus-community.github.io/helm-charts |
| loki-stack chart | 2.10.3 | grafana.github.io/helm-charts |
| chaos-mesh chart | 2.8.4 | charts.chaos-mesh.org |
| helm | v3.21.4 | `make -C bench helm` → `./bin/helm` |
| kind node image | kindest/node:v1.37.0 | `cli/deploystack/versions.go` |

Notable shaping decisions (reasoning recorded in the values files):
alertmanager/pushgateway/node-exporter off, kube-state-metrics on,
15s scrape, no persistence anywhere, retention 2h; `loki-stack` is
deprecated upstream but is the lightest Loki+promtail path and the
master plan explicitly sanctions it; Chaos Mesh gets the containerd
socket kind requires, single controller replica, dashboard and DNS
server off.

## Footprint (measured)

Measured on the environment of `docs/DEVELOPMENT.md` §2 (WSL2, 7.4 GiB
RAM, cold image cache for the stack):

| Number | Value |
|---|---|
| Session 2.1 baseline: cold start → smoke fault injected | **3m52s** |
| Session 2.2: cold start → oomkill fault injected (create cluster 32s, CRDs 0.7s, stack 2m41s, topology healthy 9s, fault 0.2s) | **3m23s** |
| warm re-run of one pack end-to-end (healthy pinned releases skipped offline, 2m graceful wait included) | ~3m |
| whole-cluster memory with stack + topology + Incident live (`docker stats` on the node container, 7.4 GiB host) — re-measured in 2.2 with the oomkill overlay's ballast containers and its fault injected: 1.887 GiB | **1.9 GiB** |

The budget is ≤10 minutes cold to fault-ready and ≤4 GiB for one
scenario; both hold with room to spare.

## Determinism (measured)

Playbook Session 2.2 requires each fault to manifest 3/3 times. The
canonical pass is:

```bash
bench/bin/praxisbench run --scenario all --runs 3
```

Measured 2026-08-31 on the environment above: all 21 runs (7 scenarios
× 3) completed, every fault manifested, every wait ended in the expected
graceful `NoResponse` timeout with `--agent none`, exit code 0, 50m01s
total. Fault-manifested phase time per run:

| Scenario | Mechanism | r1 | r2 | r3 | Result |
|---|---|---|---|---|---|
| `oomkill-after-commit` | Patch (SSA) | 3s | 3s | 3s | **3/3** |
| `bad-image-tag` | Patch (SSA) | 3s | 3s | 3s | **3/3** |
| `readiness-wrong-port` | Patch (SSA) | 3s | 3s | 3s | **3/3** |
| `pdb-deadlock` | Manifest | 3s | 5ms | 3s | **3/3** |
| `noisy-neighbour` | Manifest | 15s | 12s | 12s | **3/3** |
| `downstream-dep-restraint` | ChaosMesh | 9s | 9s | 9s | **3/3** |
| `smoke` (2.1 pipeline proof, inert) | Manifest | 3ms | 1ms | 2ms | 3/3 |
