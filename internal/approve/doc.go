/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package approve is the human approval gate: hash binding, and later card
// rendering and expiry.
//
// Single responsibility: bind an approval to a hash of exactly what was
// approved and refuse it if anything has drifted since. This is what closes
// the time-of-check/time-of-use gap.
//
// Specification: docs/02-LLD.md §8. The binding hash ships now in its
// Phase-1 interim form (bundle hash + canonical spec hash); the diff and
// policy-revision segments, the Slack gate and the approval TTL arrive in
// Phase 4 (Session 4.3).

package approve
