/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package logs turns raw Loki log lines into the LogTemplate evidence of
// docs/02-LLD.md §6: a bounded Loki query client and a deterministic,
// Drain-style clustering of lines into templates carrying template text, a
// count and exactly one exemplar (ADR-007).
//
// Raw lines exist here only transiently, to derive templates. Nothing in
// this package writes a line anywhere — not into an evidence item, a log
// message, an error string or a test fixture — and the collector in the
// parent package (internal/evidence, logs.go) hands only clusters onward.
// Credential scrubbing over the resulting template and exemplar text is the
// assembler's job (internal/evidence, redact.go), so every item crosses one
// scrubber regardless of which collector produced it.
//
// The package imports only the standard library: the seam import allowlist
// (internal/agents/boundary_test.go) covers it, so no log-parsing
// dependency, no cluster client and no model SDK can appear here.
//
// Playbook Session 3.2 task 1.
package logs
