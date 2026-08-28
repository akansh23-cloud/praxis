/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package verify evaluates the pre-declared verification predicate over its
// sustained window and enforces the deadline.
//
// Single responsibility: decide whether the fix demonstrably worked. It
// consults nothing but Prometheus and the clock, and it never assumes
// success — an unreachable backend is a failure, not a pass.
//
// Specification: docs/02-LLD.md §12. Implemented in Phase 5.

package verify
