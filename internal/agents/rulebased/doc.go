/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package rulebased is the deterministic Agent baseline: no model, no
// network, no cluster client — fixed rules over (Event reason, involved
// kind) patterns in the evidence bundle, exactly as FR-P2-04 prescribes.
//
// It is INTENTIONALLY DUMB, and must stay that way. Its purpose is to be
// the floor every future agent must beat: it establishes what a trivial
// pattern-matcher scores on the benchmark, so any capability the LLM agent
// is later credited with is capability beyond this. It reads only kubelet
// warning events; it resolves targets by string surgery on pod names
// instead of owner chains; it knows nothing about metrics, logs, commits,
// PDB arithmetic or external dependencies. By design it produces a
// plausible plan for only a minority of the six Phase 2 scenarios, and its
// published numbers in bench/RESULTS.md are expected to be bad. Improving
// this package to score better is a bug, not a contribution.
//
// Single responsibility: implement the Agent seam over the dumbest honest
// rules. When no rule matches, it proposes NO ACTION — the restraint path
// (praxis.dev/no-action-proposed) is part of the floor, wrongly or rightly.
//
// Specification: docs/02-LLD.md §5 (seam), §17 (benchmark);
// docs/00-MASTER-PLAN.md FR-P2-04. Implemented in playbook Session 2.3.

package rulebased
