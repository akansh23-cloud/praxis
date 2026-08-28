# `bench/` — praxisbench

The benchmark is a first-class deliverable, not a test suite: it measures
**fix rate and harm rate** as distributions over N runs, so the project's
claims about itself are falsifiable.

Layout, per `docs/02-LLD.md` §2 and §17:

| Path | Holds |
|---|---|
| `cli/` | the runner and scorer |
| `scenarios/` | one directory per scenario: `scenario.yaml`, fault manifest, ground truth |
| `topology/` | kustomize bases and overlays for the workloads a scenario breaks |
| `deploy/` | cluster setup for benchmark runs |

`bench/` becomes its own Go module (`praxisbench`) so the benchmark can depend
on Praxis without Praxis depending on the benchmark. That split happens when
the runner is written — the directory is a placeholder until then.

Scenario schema, ground-truth matching rules and the harm-rate procedure are
specified in `docs/02-LLD.md` §17.

Empty in Phase 0. Built in Phase 5.
