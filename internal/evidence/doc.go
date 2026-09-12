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
// negative tests). Telemetry endpoints (Prometheus, Loki) arrive as
// process configuration, never as something fetched from the cluster.
//
// Defense in depth over the telemetry the collector IS allowed to read
// (Session 3.2, ADR-008): the assembler scrubs every data value of every
// item — log templates and exemplars, event messages, change-cause
// annotations, metric output, error strings — replacing credential-shaped
// text with «redacted:<kind>» markers and flagging the item; env
// variables are represented by name only. Raw log lines never enter a
// bundle: the Loki collector (logs.go, subpackage logs) hands over only
// Drain-style templates with a count and one exemplar (ADR-007).
//
// Specification: docs/02-LLD.md §6. Types from Phase 2 (Session 2.3);
// collectors, assembly, caps and storage naming from Session 3.1; log
// templating and redaction from Session 3.2.

package evidence
