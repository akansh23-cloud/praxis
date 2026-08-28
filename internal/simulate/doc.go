/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package simulate proves a plan is safe before it is applied: server-side
// apply dry-run plus a structured diff renderer.
//
// Single responsibility: produce evidence of what a change would do —
// admission responses and a field-level diff — without changing anything.
// Any failure is a rejection carrying the verbatim admission message.
//
// Specification: docs/02-LLD.md §10. Implemented in Phase 4.

package simulate
