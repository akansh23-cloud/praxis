/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package audit is the append-only, hash-chained record of every decision,
// and later the runbook compiler and scenario fingerprinter.
//
// Single responsibility: make the history of what was proposed, by which
// model, from which evidence, under which policy, approved by whom, and with
// what outcome, tamper-evident and replayable.
//
// Specification: docs/02-LLD.md §15, §3. Implemented in Phase 5 (compiler in
// Phase 6).

package audit
