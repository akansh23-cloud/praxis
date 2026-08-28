/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package rollback restores snapshotted state and detects when it cannot
// safely do so.
//
// Single responsibility: reverse an executed plan, or refuse loudly. If a
// third party mutated a target since the executor last wrote it, the answer
// is RollbackUnsafe and escalation — never a force-write.
//
// Specification: docs/02-LLD.md §13. Implemented in Phase 5.

package rollback
