/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Command executor is the acting half of Praxis: it scores, gates, simulates,
// applies, verifies and reverses RemediationPlans.
//
// Single responsibility: act on a plan only after every deterministic gate has
// passed. It runs with scoped write RBAC and no network egress at all, so
// compromising it yields no path to exfiltrate anything. It contains no LLM
// code, and an import-boundary lint keeps it that way.
//
// Specification: docs/01-HLD.md §4, docs/02-LLD.md §2. Wired up across Phases
// 2-5; this is a placeholder entrypoint.
package main
