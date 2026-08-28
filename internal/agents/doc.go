/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package agents defines the Agent interface — Analyze then Plan — and hosts
// its implementations: rulebased/ (the deterministic baseline the benchmark
// measures against) and llm/ (hypothesis engine and planner).
//
// Single responsibility: produce ranked hypotheses and a candidate
// RemediationPlanSpec from an evidence bundle. Restraint ("no action") is a
// valid, scored outcome, not a failure.
//
// Import boundary (lint-enforced): nothing under internal/executor, verify,
// rollback, risk, policy, simulate, approve or audit may import this package.
//
// Specification: docs/02-LLD.md §5. Implemented in Phase 3.

package agents
