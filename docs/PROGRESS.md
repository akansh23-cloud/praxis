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
