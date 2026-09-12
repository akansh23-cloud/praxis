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
// Secrets are unreachable by construction, not filtered after the fact:
// the package's only cluster view is the Reader interface, which has no
// Secrets method, no generic Get/List, and no write methods — and the
// shipped RBAC grants the manager no secrets rule (both asserted by
// negative tests). Deeper scrubbing of secret-like strings inside
// otherwise-allowed telemetry is Session 3.2's.
//
// Specification: docs/02-LLD.md §6. Types from Phase 2 (Session 2.3);
// collectors, assembly, caps and storage naming from Phase 3 (Session 3.1).

package evidence
