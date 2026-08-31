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
| FR-P2-01 — `praxisbench run` on a laptop: namespaces → topology → fault → Incident → wait → score → teardown; first fault ≤10 min from a cold kind cluster | met — the full pipeline including in-run scoring and JSONL records; cold→fault 3m23s measured in 2.2, score phase adds ~0s and cannot skip teardown | 2026-08-31 | `339b528` (+ 2.1/2.2 commits) |
| FR-P2-02 — declarative scenario YAML per LLD §17 (topology ref, fault, ground truth, predicates, timeout) | met — Go types + strict loader with field-path errors; every shipped pack walked by a unit test; §17.3 diagnosis rule (`groundTruth.diagnosis`) and required `fixPredicate`/`harmPredicate` typed, validated and populated in all packs per ADR-005 (declared ground truth only — matching/evaluation land with 2.3/Phase 5) | 2026-08-31 | `7831024`, `a8c0295`, `21e4858` |
| FR-P2-03 — scorer emitting per-run JSONL + aggregates incl. restraint correctness | met — deterministic §17.3 matching (glob citations + case-insensitive exact keyphrases, no fuzzy scoring), plan schema validity via the real API server's verdict, acceptable/forbidden action match, restraint correctness (timeouts and rejected plans satisfy neither side), time-to-plan; null denominators keep aggregates honest; matcher written failing-first | 2026-08-31 | `a69b5cf`, `339b528` |
| FR-P2-04 — rule-based baseline agent behind the Agent interface | met — Agent seam (Analyze/Plan per LLD §5) in internal/agents with an import-allowlist test (no client, no SDK); rulebased = three (Event reason, involved kind) reflexes + deterministic no-action fallthrough, documented as the frozen floor; agents provably cannot read scenario identity or groundTruth (module direction + agentrun import test + per-run serialized-input leak guard) | 2026-08-31 | `21cc508`, `c79aba7`, `01ede76`, `97039ff` |
| FR-P2-05 — distributions (mean ± min/max) over N runs | met — per-(agent, scenario) mean/min/max with explicit `n=` denominators; `praxisbench report` renders deterministically | 2026-08-31 | `a69b5cf` |
| FR-P2-06 — `bench/README.md` documents adding a scenario in <30 lines | met — all six real packs are 25–29 non-comment YAML lines; README documents the add-a-scenario steps (YAML + overlay + registered fault check) | 2026-08-31 | Session 2.2 docs commit |
| Exit: all 6 scenarios run green mechanically | met — `praxisbench run --scenario all --runs 3`: 21/21 runs green, every fault manifested 3/3 with per-pack mechanical checks and no agent, graceful `NoResponse` waits, 50m01s total | 2026-08-31 | `89239d8`..`1bd4610` |
| Exit: baseline scores recorded in `bench/RESULTS.md` | met — `--scenario all --runs 5 --agent rulebased`: 35/35 runs in 13m54s, every metric identical across each pack's five runs; the floor is diagnosis 0/30, actions 5/30, restraint 0/5 on the restraint pack, 10 forbidden violations — recorded verbatim with the mechanism explained | 2026-08-31 | `993df3b` |
| Exit: CI smoke job runs one scenario nightly | partial — nightly + workflow_dispatch job authored and validated locally (bad-image-tag × 1 with the baseline, pinned kind/Go/actions); **the green manual dispatch on GitHub Actions is still owed** — nothing from Phase 2 is pushed, so it cannot yet run remotely | 2026-08-31 | `baa2063` |
| Session 2.1 — harness skeleton: bench as its own Go module, praxisbench CLI (run/score/report + flags), §17 schema loader, pinned deploy stack (Prometheus 29.27.0, loki-stack 2.10.3, Chaos Mesh 2.8.4, helm v3.21.4), shop topology, smoke scenario end-to-end **twice in a row** with its expected graceful timeout; cold→fault 3m52s, cluster footprint 1.9 GiB | met | 2026-08-31 | `716a8ce`, `7831024`, `e3c6143`, `f2c3043`, `80abebb`, `bded21f` + docs commit |
| Session 2.2 — the six scenario packs: Patch (server-side-apply) + ChaosMesh fault injection, per-scenario fault-manifested checks (`cli/faultcheck`, registry enforced by tests both ways), six packs each with overlay/fault/groundTruth/mechanism rationale, `--scenario all`, offline skip of healthy pinned releases; §17.3 diagnosis rules + effect predicates authored in every pack (ADR-005); determinism pass 21/21 (each fault 3/3), cold→oomkill fault 3m23s, Chainsaw + envtest Phase 1 suites re-verified green | met | 2026-08-31 | `e986a44`..`21e4858` + docs commits |
| Session 2.3 — scorer + the dumb baseline: §6 bundle types, Agent seam per LLD §5, intentionally dumb rulebased agent (frozen floor, no-action path), scenario-blind agentrun (identity leak fixed, four-layer proof incl. a per-run serialized-input guard), deterministic §17.3 scorer + JSONL + mean/min/max report, `--agent rulebased` wired with `--agent none` untouched, nightly CI workflow authored, 5× baseline experiment recorded in `bench/RESULTS.md`; local verification: bench+root tests and lint green, Chainsaw re-verified | met locally — remote proof owed: nightly workflow green once manually dispatched (blocked on pushing) | 2026-08-31 | `96c0700`..`993df3b` + this commit |
