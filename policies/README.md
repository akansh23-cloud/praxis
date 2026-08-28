# `policies/` — the policy bundle

The Kyverno and CEL rules that the policy gate evaluates against every
`RemediationPlan` and its projected targets. This directory is the **final
authority** on what Praxis is allowed to do: there is no bypass flag anywhere
in the codebase, and a grep-guard test asserts that.

The bundle's git revision is stamped into every `status.policyVerdict`, so a
verdict can always be traced back to the exact rules that produced it.

Two evaluation subjects, per `docs/02-LLD.md` §9:

- **plan-shape policies** — verb allow-lists per namespace class, replica-delta
  caps, risk-tier ceilings, freeze windows;
- **projected-target policies** — workload invariants (limit ceilings,
  PDB-respect) checked against a pure projection of the post-change object.

Empty in Phase 0. Populated in Phase 4, when the policy gate is built.
