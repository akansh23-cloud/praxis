/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package rulebased is the deterministic Agent baseline: no model, no
// network, fixed rules over the evidence bundle.
//
// Single responsibility: establish the floor the LLM agent must beat on the
// benchmark. A capability the rule-based agent already has is not evidence
// that the model is contributing anything.
//
// Specification: docs/02-LLD.md §5. Implemented in Phase 3.

package rulebased
