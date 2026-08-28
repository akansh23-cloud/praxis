/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package llm is the model-backed Agent: hypothesis engine plus
// schema-constrained planner.
//
// Single responsibility: rank hypotheses over a bounded evidence bundle
// (every claim citing an evidence ID) and emit a plan drawn from the closed
// action vocabulary. It holds no credentials, runs no commands and sees no
// secrets.
//
// Specification: docs/01-HLD.md §4, docs/02-LLD.md §5. Implemented in Phase 3.

package llm
