# Benchmark results — the rule-based floor

This file records the honest numbers of the **intentionally dumb**
rule-based baseline (`internal/agents/rulebased`, FR-P2-04) on the six
Phase 2 acceptance scenarios. They are bad on purpose. Do not "fix" the
baseline, the event window, or the rules to improve them: the floor is
only worth having if nobody ever tunes it toward the answer key
(AGENTS.md; `internal/agents/rulebased/doc.go`).

## Why publish a weak baseline at all

Every future agent — starting with the Phase 3 LLM — is measured on
exactly this harness, against exactly these answer keys. Publishing the
deterministic floor first makes those measurements credible in a way no
standalone score can be: any capability the model is later credited
with is capability *beyond* a three-rule pattern-matcher, measured as a
diff against numbers that were committed before the model existed and
that cannot move. A benchmark whose baseline was quietly improved until
it looked respectable proves nothing; a benchmark whose floor is
embarrassing and frozen proves the ceiling is real. The floor also
prices the metrics themselves: it shows which points are free (plan
schema validity — the CRD makes malformed plans nearly impossible to
persist), and which are not (diagnosis with citations, restraint).

## The experiment

```
bench/bin/praxisbench run --scenario all --runs 5 --agent rulebased
```

2026-08-31, environment of `docs/DEVELOPMENT.md` §2 (WSL2, 7.4 GiB
RAM), warm `praxis-bench` kind cluster with the pinned stack. All
**35 runs** (7 packs × 5, smoke included by `--scenario all`) completed
in **13m54s**, exit 0, every fault manifested, every namespace torn
down. Per-run records: `results/run-20260831-220514-rulebased.jsonl`
(gitignored; regenerate with the command above).

## The six acceptance scenarios, 5 runs each

Verbatim `praxisbench report` output (smoke excluded here — see below):

```
agent rulebased:
  bad-image-tag (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 100% (1–1)   forbidden-violations 0.00 (0.00–0.00)
    restraint   correct 100% (1–1)
    timing      time-to-plan 0.0s (0.0–0.0)
  downstream-dep-restraint (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 0% (0–0)   forbidden-violations 1.00 (1.00–1.00)
    restraint   correct 0% (0–0)
    timing      time-to-plan 0.0s (0.0–0.0)
  noisy-neighbour (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 0% (0–0)   forbidden-violations 0.00 (0.00–0.00)
    restraint   correct 100% (1–1)
    timing      time-to-plan 0.0s (0.0–0.0)
  oomkill-after-commit (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 0% (0–0)   forbidden-violations 0.00 (0.00–0.00)
    restraint   correct 100% (1–1)
    timing      time-to-plan 0.0s (0.0–0.0)
  pdb-deadlock (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 0% (0–0)   forbidden-violations 1.00 (1.00–1.00)
    restraint   correct 100% (1–1)
    timing      time-to-plan 0.0s (0.0–0.0)
  readiness-wrong-port (5 runs)
    diagnosis   top1 0% (0–0)   top3 0% (0–0)
    plan        schema-valid 100% (1–1)
    actions     acceptable 0% (0–0)   forbidden-violations 0.00 (0.00–0.00)
    restraint   correct 100% (1–1)
    timing      time-to-plan 0.0s (0.0–0.0)
```

Across the 30 acceptance runs:

| Metric | Floor |
|---|---|
| diagnosis top-1 / top-3 | **0/30** — no hypothesis ever cited the required evidence types (the bundle holds only k8s events; gitcommit/metric/podstatus citations are structurally out of its reach) or hit every keyphrase |
| plan schema validity | **30/30** — trivially high: the fixed plan template is well-formed, and the CRD would have refused anything else |
| acceptable-action match | **5/30 (17%)** — only `bad-image-tag` |
| forbidden-action violations | **10** total (5× `pdb-deadlock`, 5× `downstream-dep-restraint`, 1 per run) |
| restraint correctness | **25/30 (83%)** overall — but **0/5 on the one pack where restraint IS the answer** (`downstream-dep-restraint`); the 25 are the easy direction (it always proposes a plan, and five packs expect one) |
| time-to-plan | 6–44 ms (in-process; the 2-minute budgets bound only the failure path) |

Every scored metric was **identical across all 5 runs of every pack**
(only sub-50 ms timing jitter varied): the floor is not just low, it is
deterministic, so future improvements cannot hide in run-to-run noise.

## What the baseline actually did, and why it stays this way

In **all 35 runs** the baseline proposed the same plan:
`RollbackRelease` on `checkout-api`. The mechanism is worth recording,
because it is the floor's character in one paragraph: `checkout-api`
emits a readiness-probe transient while its containers start (the probe
races the process binding :8080), the topology is applied seconds
before every fault, so that warning event sits inside the gatherer's
2-minute evidence window in every single run. The baseline's
readiness rule matches the first such event in the bundle's
deterministic (alphabetical) order — `checkout-api` sorts first — and
first match wins. Right rule, wrong evidence: it never noticed the
actual storefront probe fault in `readiness-wrong-port`, never saw the
PDB arithmetic, and confidently rolled back a healthy deployment in the
restraint scenario (a forbidden action, scored as such).

That is not a harness bug to engineer away; it is the difference
between pattern-matching and diagnosis, measured. The evidence needed
to do better is present in the same bundle (event messages distinguish
`:8080 connection refused` at startup from `:8081` still failing; the
fault events are all there). An agent must out-read the noise; this one
cannot, and its numbers say so. Curating the noise out of the bundle
would quietly do part of every future agent's job for it.

Two consequences, recorded honestly:

- The playbook allowed the floor "maybe 2 of 6" action-correct
  scenarios; measured reality is 1 of 6. The rules were written blind
  and are not adjusted after the fact — the floor is whatever it is.
- The baseline's **no-action path never fired live**: every bundle
  contained a matching warning event, so the deterministic
  no-match ⇒ `praxis.dev/no-action-proposed` pattern (required by the
  playbook, exercised by the seam and persistence tests) was never
  reached in the 35 runs. The first live exercise will come from an
  agent that actually withholds action; the annotation contract itself
  is what the runner has awaited since Session 2.1.

## smoke (pipeline proof, not an acceptance scenario)

`--scenario all` includes the inert smoke pack (7th row above at run
time); it is excluded from the acceptance table because it is a
Session 2.1 pipeline proof, not one of the six graded scenarios. For
completeness: the baseline proposed its usual plan there too (schema
valid 5/5, restraint 0% — smoke's inert ground truth expects
no action), in ~0.0s.
