/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package agents defines the Agent interface — Analyze then Plan — and hosts
// its implementations: rulebased/ (the deterministic baseline the benchmark
// measures against) and llm/ (the model-backed hypothesis engine and
// planner, Session 3.3).
//
// Single responsibility: produce ranked hypotheses and a candidate
// RemediationPlanSpec from an evidence bundle. Restraint ("no action") is a
// valid, scored outcome, not a failure — and so is a refusal (outcomes.go):
// an agent that detects its own output must not become a plan returns
// CitationError or SchemaError, which callers record, never repair.
//
// Import boundary (lint-enforced): nothing under internal/executor, verify,
// rollback, risk, policy, simulate, approve or audit may import this package.
//
// Specification: docs/02-LLD.md §5. Implemented in Phase 3.

package agents
