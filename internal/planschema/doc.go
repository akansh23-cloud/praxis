/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package planschema is the planner's contract with the API server,
// derived — never hand-written — from the RemediationPlan CRD
// (playbook Session 3.3 task 2; FR-P3-05: "a JSON Schema generated from
// the CRD OpenAPI, single source of truth").
//
// hack/schema-derive reads config/crd/bases/praxis.dev_remediationplans.yaml
// and writes three artifacts into this package, which embeds them:
//
//   - remediationplanspec.openapi.json — the spec's OpenAPI v3 schema,
//     verbatim, CEL rules included. ValidateSpec runs the API server's
//     own validators (structural schema + x-kubernetes-validations) over
//     it, so a spec this package accepts is a spec admission accepts.
//   - planner.schema.json — what the planner asks the model for: a
//     verdict (plan or no-action) and, for a plan, the fields the model
//     decides (actions, verification, rollback), projected from the spec
//     schema into the subset structured outputs support. Code-owned
//     fields (incidentRef, evidenceBundleHash, hypothesis) are absent by
//     construction; the model cannot set them.
//   - hypotheses.schema.json — the hypothesis engine's output: ranked
//     hypotheses, each the CRD's own Hypothesis shape.
//
// The projections drop what structured outputs cannot express (numeric
// and string constraints, patterns, formats, Kubernetes extensions); the
// verbatim OpenAPI keeps every one of them, and ValidateSpec enforces
// them before a plan is ever created. TestDerivedSchemasAreInSyncWithTheCRD
// and `make schema-check` fail the moment the CRD changes without a
// re-derivation.
package planschema
