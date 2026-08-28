/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package executor holds one ActionExecutor per verb in the closed
// vocabulary, plus snapshotting and the circuit breaker.
//
// Single responsibility: apply a validated, approved plan through bounded,
// idempotent API calls, having first captured enough state to reverse it.
// Precheck, Snapshot, Apply, Revert — in that order, per action.
//
// This package has write RBAC and no network egress. It must never import
// internal/llm or internal/agents (lint-enforced).
//
// Specification: docs/02-LLD.md §11, §13. Implemented in Phase 4.

package executor
