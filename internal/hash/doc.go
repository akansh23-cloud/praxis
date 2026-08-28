/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package hash is the single implementation of canonical JSON and sha256
// helpers used everywhere a hash is load-bearing.
//
// Single responsibility: given equal logical content, produce equal bytes.
// Canonical JSON follows RFC 8785 (JCS): UTF-8, object keys sorted by
// UTF-16 code units, no insignificant whitespace, strings escaped the
// JSON.stringify way, and numbers rendered as ECMAScript renders IEEE-754
// doubles — so 1 and 1.0 are the same bytes, and integers beyond 2^53
// round to the nearest double exactly as JCS mandates (no Praxis-hashed
// content carries integers of that size; the property is pinned by test).
// Evidence bundle hashes, spec hashes, diff hashes and the audit chain all
// depend on this being implemented exactly once.
//
// Specification: docs/02-LLD.md §6 (canonical form and bundle hashing) and
// §8 (approval binding); RFC 8785.

package hash
