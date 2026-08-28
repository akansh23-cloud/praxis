/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package risk is the deterministic blast-radius scorer.
//
// Single responsibility: compute a numeric score and tier for a plan from
// the plan and its targets alone — never from the model. Same plan, same
// cluster state, same score.
//
// Specification: docs/02-LLD.md §7. Implemented in Phase 2.

package risk
