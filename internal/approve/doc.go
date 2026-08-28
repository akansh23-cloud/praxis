/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package approve is the human approval gate: card rendering, hash binding
// and expiry.
//
// Single responsibility: bind an approval to a hash of exactly what was
// approved (bundle, spec, diff, policy revision) and refuse it if anything
// has drifted since. This is what closes the time-of-check/time-of-use gap.
//
// Specification: docs/02-LLD.md §8. Implemented in Phase 4.

package approve
