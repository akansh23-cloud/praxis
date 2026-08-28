/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Command analyzer is the model-facing half of Praxis: it collects evidence,
// ranks hypotheses, and proposes RemediationPlans.
//
// Single responsibility: turn an Incident into a candidate plan. It runs with
// read-only cluster RBAC that excludes Secrets, and it is the only process
// permitted LLM egress. It holds no write credentials, so compromising it
// yields no path to change the cluster.
//
// Specification: docs/01-HLD.md §4, docs/02-LLD.md §2. Wired up in Phase 3;
// this is a placeholder entrypoint.
package main
