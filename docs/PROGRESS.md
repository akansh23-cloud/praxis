# Progress — the exit-criteria ledger

One line per exit criterion and functional requirement from
[`docs/00-MASTER-PLAN.md`](00-MASTER-PLAN.md), with the date it was met and
the commit that made it true. A criterion is **met** only when its proof ran;
anything short of that is **pending** with what remains stated plainly.
Commit hashes below are on `main` at `origin`.

## Phase 0 — Foundations & repo bootstrap

| Criterion | Status | Date | Commit |
|---|---|---|---|
| FR-P0-01 — `Incident` + `RemediationPlan` served at `praxis.dev/v1alpha1` | met | 2026-08-28 | `7ad6945` |
| FR-P0-02 — `make lint test` / `make install run` from a fresh clone; versions in `docs/DEVELOPMENT.md` | met | 2026-08-28 | `92af527` |
| FR-P0-03 — CI runs lint, vet, unit + envtest on every PR; README badge | met | 2026-08-28 | `da535a3` |
| FR-P0-04 — ADR-001..004 as files in standard format | met | 2026-08-28 | `d486cc5` |
| FR-P0-05 — repo layout matches LLD §2 (placeholders with doc.go) | met | 2026-08-28 | `5aa03bb` |
| Exit: fresh-clone build passes locally and in CI | met | 2026-08-28 | `da535a3` |
| Exit: `kubectl api-resources` shows both kinds after `make install` | met | 2026-08-28 | `7ad6945` |
| Deliverable: tag `v0.0.1` | met | 2026-08-28 | annotated tag pushed to origin at `37581a5` |

## Phase 1 — Object model & phase machine

| Criterion | Status | Date | Commit |
|---|---|---|---|
| FR-P1-01 — every CEL rule verified by envtest tables through the API server (immutability, all discriminated-union rules, CordonNode/Node pairing both ways, namespace-iff, memory-or-cpu, onFailure⇒strategy, closed vocabulary incl. non-persistence) | met | 2026-08-28 | `52b09f1` |
| FR-P1-02 — phase machine per LLD §4.2 with accurate conditions (`CitationsResolved`, `ScopeValid`, `Approved`) | met | 2026-08-28 | `e04b5cf`, `100489b` |
| FR-P1-03 — missing Incident / hash mismatch ⇒ `Rejected/EvidenceMismatch` | met | 2026-08-28 | `e04b5cf` |
| FR-P1-04 — `observedGeneration` + idempotent reconciles (zero status writes, measured) | met | 2026-08-28 | `e04b5cf` |
| FR-P1-05 — printcolumns render; `kubectl get rplan` demo-ready | met | 2026-08-28 | `c016b66` |
| FR-P1-06 — Chainsaw suite: sample walk + both structural rejections | met | 2026-08-28 | `818b72f` |
| Session 1.2 — canonical JSON + sha256 (`internal/hash`), interim LLD §8 `boundTo`, annotation approval with fresh recompute, `ApprovalInvalidated` | met | 2026-08-28 | `0d0c4a4`, `1ca576d`..`3d35054` |
| Exit: Chainsaw green on kind **locally** (full walk, wrong-hash, evidence mismatch, spec-edit refusal, DeleteNamespace non-persistence) | met | 2026-08-28 | `818b72f` |
| Exit: Chainsaw green on kind **in CI on a PR** | met | 2026-08-28 | PR #1 (`phase1-verification` → `main`, head `073a776`): Tests, Lint and E2E (Chainsaw) all green; merged as `3e4df82` |
| Exit: asciinema of the walk + rejections committed under `docs/demo/` | met | 2026-08-28 | `073a776` — `docs/demo/phase1.cast`, driven by the script from `4eed68a` |
| Milestone: tag `v0.1.0-alpha.1` | met | 2026-08-28 | annotated tag pushed at `073a776`; reachable from `main` but predates merge commit `3e4df82` |

## Phase 2 — Benchmark harness, before the AI

| Criterion | Status | Date | Commit |
|---|---|---|---|
| FR-P2-01 — `praxisbench run` on a laptop: namespaces → topology → fault → Incident → wait → score → teardown; first fault ≤10 min from a cold kind cluster | partial — the pipeline runs end-to-end and the smoke fault is injected 3m52s from a cold cluster (measured); real faults arrive with the 2.2 packs, scoring with 2.3 | 2026-08-31 | `bded21f` |
| FR-P2-02 — declarative scenario YAML per LLD §17 (topology ref, fault, ground truth, predicates, timeout) | met — Go types + strict loader with field-path errors; every shipped pack is walked through the loader by a unit test | 2026-08-31 | `7831024` |
| FR-P2-03 — scorer emitting per-run JSONL + aggregates incl. restraint correctness | pending — Session 2.3; the restraint contract (`praxis.dev/no-action-proposed`) is declared and the runner already waits on it (`80abebb`) | | |
| FR-P2-04 — rule-based baseline agent behind the Agent interface | pending — Session 2.3 | | |
| FR-P2-05 — distributions (mean ± min/max) over N runs | pending — Session 2.3; `--runs N` already loops the runner | | |
| FR-P2-06 — `bench/README.md` documents adding a scenario in <30 lines | partial — schema, how-to and the 27-line smoke pack documented; the six real packs (2.2) are the proof that real scenarios stay under 30 lines | 2026-08-31 | see Session 2.1 row |
| Exit: all 6 scenarios run green mechanically | pending — Session 2.2 | | |
| Exit: baseline scores recorded in `bench/RESULTS.md` | pending — Session 2.3 | | |
| Exit: CI smoke job runs one scenario nightly | pending — wired after 2.2/2.3 | | |
| Session 2.1 — harness skeleton: bench as its own Go module, praxisbench CLI (run/score/report + flags), §17 schema loader, pinned deploy stack (Prometheus 29.27.0, loki-stack 2.10.3, Chaos Mesh 2.8.4, helm v3.21.4), shop topology, smoke scenario end-to-end **twice in a row** with its expected graceful timeout; cold→fault 3m52s, cluster footprint 1.9 GiB | met | 2026-08-31 | `716a8ce`, `7831024`, `e3c6143`, `f2c3043`, `80abebb`, `bded21f` + docs commit |
