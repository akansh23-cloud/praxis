/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package llm is the model-backed Agent: hypothesis engine plus
// schema-constrained planner (playbook Session 3.3; FR-P3-04..06).
//
// Single responsibility: rank hypotheses over a bounded, redacted evidence
// bundle — every claim citing an evidence id that exists — and emit a
// plan drawn from the closed action vocabulary, or an explicit no-action
// verdict. It holds no credentials, runs no commands and sees no secrets:
// its only world is the (Incident, Bundle) the seam hands it, and its only
// outward path is the llm.Client interface.
//
// Trust boundary B1 (docs/01-HLD.md §8) is enforced structurally here:
//
//   - the system instructions are fixed constants; nothing from the
//     cluster is ever concatenated into them;
//   - the incident and the bundle travel in the user message inside
//     explicit data delimiters, control sequences stripped, as canonical
//     JSON — data, never instructions;
//   - every hypothesis the model returns is checked by deterministic code
//     (internal/validate) against the exact bundle; an unresolved citation
//     refuses the whole analysis (agents.CitationError), nothing is
//     repaired, and the model is never asked to validate itself;
//   - the planner's output is constrained by the CRD-derived schema
//     (internal/planschema) and validated by the API server's own rules
//     before it can become a plan; one retry on violation, then
//     agents.SchemaError;
//   - code, not the model, sets incidentRef, evidenceBundleHash and the
//     plan's hypothesis, so the model cannot bind a plan to evidence it
//     did not analyze.
//
// Specification: docs/01-HLD.md §4, docs/02-LLD.md §5. Imports only the
// llm interface package, never a provider (internal/agents/boundary_test.go).
package llm
