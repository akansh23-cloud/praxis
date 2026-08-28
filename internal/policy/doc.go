/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package policy is the PolicyEvaluator: the Kyverno engine plus CEL
// built-ins, evaluated over both the plan object and its projected targets.
//
// Single responsibility: return a verdict listing every policy consulted and
// the git revision of the bundle that produced it. The policy repository is
// the final authority — there is no bypass flag anywhere in this codebase,
// and a grep-guard test asserts it.
//
// Specification: docs/02-LLD.md §9. Implemented in Phase 4.

package policy
