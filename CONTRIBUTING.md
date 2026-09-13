# Contributing to Praxis

Thanks for looking. Before anything else, the honest state of things:

**Praxis is pre-v1 and has one maintainer.** It is at Phase 3 of an eight-phase
plan (`docs/00-MASTER-PLAN.md`): the API types are enforced, the analyzer half
(evidence bundles, the LLM agent, the benchmark) exists and is measured, and
the executor half — policy, dry-run, approval UX, execution, verification,
rollback — does not exist yet. That means a large unsolicited pull request is
likely to collide with work already sequenced for a later phase, no matter how
good it is. **Open an issue first.** For anything beyond a typo, agreement on
the approach before you write code will save you more time than it costs.

---

## What is most useful right now

In rough order:

1. **Attacks on the API surface.** Praxis claims that a malformed, mutated, or
   out-of-vocabulary `RemediationPlan` cannot exist as an object, because the
   API server rejects it. If you can persist one that should not exist, that is
   the most valuable thing you can send. See "Reporting a security issue" below.
2. **Design critique against `docs/01-HLD.md` and `docs/02-LLD.md`.** The docs
   are normative and the phases are contracted against them. A well-argued
   objection to a decision, filed as an issue, is worth more than an
   implementation of the wrong decision.
3. **Portability reports.** `docs/DEVELOPMENT.md` §2 lists the one configuration
   this has been verified on. If a fresh clone fails to build on macOS, on
   arm64, or on native Linux, that is a real bug and a very welcome issue.
4. **Benchmark scenarios.** `docs/02-LLD.md` §17 specifies the scenario schema
   and `bench/README.md` shows how a pack is added (about 30 lines of YAML plus
   a mechanical fault check). Real incidents you have actually seen — with a
   fault mechanism and a ground truth — are hard to invent and easy to
   contribute; the effect-side predicates they declare are evaluated from
   Phase 5 on.

## What will be declined

Not because the ideas are bad, but because they are load-bearing decisions
already made and recorded:

- **New action verbs**, absent a benchmark scenario that demands one. The
  closed vocabulary is the security architecture, not a limitation to route
  around. See [ADR-001](docs/adr/ADR-001.md).
- **Any bypass path** around the policy gate, the scope check, the dry-run or
  the approval binding — a flag, an annotation, an "emergency mode", a
  `--force`. There is deliberately no such path anywhere in the codebase, and a
  test greps for it.
- **Weakened validation markers** to make something pass. If a CEL rule or enum
  is genuinely wrong, that is an ADR and a schema change, not a deletion.
- **LLM code outside `internal/llm` and `internal/agents`.** The import boundary
  is what makes "the process that acts has no model" checkable.
- **Work from a later phase**, arriving early. The phase order exists because
  each phase's exit criteria are the contract for the next one.

---

## Ground rules that are not negotiable

These come from `docs/00-MASTER-PLAN.md` and apply to every change:

- The analyzer path never gains cluster write RBAC. The executor path never
  gains LLM or internet egress.
- No Secret data is ever read into an evidence bundle or a prompt — enforced by
  type and RBAC, not by filtering after the fact.
- Telemetry (logs, Events, annotations) is **data**, never instructions.
- Anything a model produces is validated by deterministic code before it has
  any effect.

If a change makes one of these harder to verify, it needs a very good argument.

---

## Setting up

`docs/DEVELOPMENT.md` is the full guide. The short version:

```bash
git clone https://github.com/akansh23-cloud/praxis.git
cd praxis
make lint test build          # green before you change anything
make pre-commit-install       # optional, recommended
```

Get a green baseline *before* your first edit. If `make test` is red on a
clean checkout, that is a bug worth reporting on its own.

---

## Making a change

1. **Branch** from `main`.

2. **Small, logical commits.** One change per commit. A commit that both moves
   code and changes its behaviour is two commits.

3. **Conventional commit messages.** `<type>(<scope>): <subject>`, imperative
   mood, no trailing period.

   ```
   feat(api): add ScaleWorkload replica-delta ceiling
   fix(controller): requeue on conflict instead of returning error
   docs(adr): record why snapshotRef lives in status
   test(risk): cover the PDB-selected StatefulSet case
   chore(deps): bump controller-runtime to v0.24.1
   refactor(hash): extract canonical JSON into a single implementation
   ```

   Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `ci`, `perf`.
   Scope is the package or area (`api`, `controller`, `risk`, `policy`, `ci`).
   Breaking changes get a `!` — `feat(api)!:` — and an explanation in the body.

4. **Regenerate after touching `api/`.** Non-negotiable:

   ```bash
   make generate manifests
   ```

   Never hand-edit `config/crd/bases/`, `config/rbac/role.yaml`, or any
   `zz_generated.*.go`. Commit the regenerated output alongside the change that
   caused it. The `manifests-current` pre-commit hook checks this, because a
   types change without regeneration leaves the schema the API server enforces
   out of step with the code — silently.

5. **Write an ADR when a decision diverges from the LLD.** `docs/02-LLD.md`
   says it plainly: when code must diverge, update the LLD *and* write an ADR in
   the same pull request. Copy the shape of `docs/adr/ADR-001.md` —
   Status / Context / Decision / Consequences — number it sequentially, and be
   candid in Consequences. An ADR that lists only benefits is not finished.
   `docs/adr/ADR-004.md` is the model here: it states outright which link in the
   safety chain is currently the weakest.

6. **Green before you push:**

   ```bash
   make lint test build
   ```

   And if you touched the CRDs, prove it against a real API server:

   ```bash
   make kind-up install
   kubectl api-resources --api-group=praxis.dev
   ```

---

## Tests

- **Unit and envtest** live beside the code and run with `make test`. envtest
  gives a real API server, so validation behaviour is tested against the same
  admission path a production cluster runs — not a mock.
- **Validation changes need a test that proves the rule bites.** Assert the
  rejection, not just the acceptance. A CEL rule with only a happy-path test is
  untested.
- **Deterministic components stay deterministic.** The risk scorer, the hasher
  and the canonical JSON encoder are table-driven; `docs/02-LLD.md` §7 supplies
  worked examples that exist specifically to become fixtures.
- **e2e** (`make test-e2e`) creates and destroys its own kind cluster
  (`praxis-test-e2e`). It never touches the dev cluster (`praxis`).

---

## Pull requests

- Say **what changed and why**. The why is the part review cannot reconstruct.
- Link the issue that agreed the approach.
- If you changed a validation rule, a policy, or an RBAC grant, **say so in the
  description explicitly.** Those are the changes that need a slow read.
- Keep generated-file churn in its own commit so the human-authored diff is
  legible.
- CI runs lint and tests on every pull request. Green is the baseline for
  review, not the goal of it.

---

## Reporting a security issue

Do not open a public issue for a vulnerability in the guarantees Praxis claims —
a way to persist an invalid plan, to bypass the policy gate or scope check, to
reach a Secret through the analyzer, to give the executor egress, or to void the
approval binding without invalidating it.

Use GitHub's **private vulnerability reporting** on this repository
(Security → Report a vulnerability). If that is unavailable, open an issue
saying only that you have a security report and asking for a contact — no
details.

Include what you tried, what you expected, and what happened. A reproduction
against a kind cluster is ideal. Since Praxis is pre-v0.1 with one maintainer,
there is no formal response-time commitment; you will get an acknowledgement as
soon as it is seen.

A full threat model is scheduled for Phase 7 (`docs/THREAT-MODEL.md`); the
summary lives in `docs/01-HLD.md` §8.

---

## Licensing

Praxis is licensed under the **Apache License 2.0** (see [`LICENSE`](LICENSE)).
By contributing, you agree your contributions are licensed under the same terms —
this is Apache-2.0 §5, and there is no separate CLA.

New source files carry the standard header:

```go
/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/
```

---

## Conduct

Be straightforward and assume good faith. Argue with the design, not the
person. Harassment is not tolerated, and the maintainer will remove content and
contributors that make participation unpleasant.
