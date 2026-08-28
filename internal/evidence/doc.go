/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package evidence assembles the bounded, canonical, hashed IncidentBundle
// that every hypothesis and plan is derived from.
//
// Single responsibility: turn live cluster and telemetry state into a
// deterministic, size-capped, redacted evidence document. Collectors, bundle
// assembly, redaction and the bundle's own hashing live here; nothing else
// decides what the model is allowed to see.
//
// Secrets are unreachable by construction, not filtered after the fact.
//
// Specification: docs/02-LLD.md §6. Implemented in Phase 2.

package evidence
