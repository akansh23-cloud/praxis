/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package hash is the single implementation of canonical JSON and sha256
// helpers used everywhere a hash is load-bearing.
//
// Single responsibility: given equal logical content, produce equal bytes.
// Canonical JSON is UTF-8, sorted keys, no insignificant whitespace, and
// RFC 8785-style number formatting. Evidence bundle hashes, spec hashes,
// diff hashes and the audit chain all depend on this being implemented
// exactly once.
//
// Specification: docs/02-LLD.md §6. Implemented in Phase 2.

package hash
