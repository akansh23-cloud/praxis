/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package validate holds the structural guards applied to model output
// before it has any effect: the citation validator and the scope checker.
//
// Single responsibility: reject a plan whose claims do not resolve against
// the bundle it names, or whose actions land outside the incident's declared
// scope. The citation check is the structural hallucination guard — a hard
// check, not a prompt instruction. The scope check runs before policy and
// cannot be overridden by policy.
//
// Specification: docs/02-LLD.md §5, §10. Phase 1 ships the interfaces plus
// stubs that pass with reason StubbedInPhase1 — the reason is copied onto
// the plan's conditions so the stubbing is visible in kubectl describe. The
// real citation validator lands in Phase 3, the real scope checker in
// Phase 4.
package validate
