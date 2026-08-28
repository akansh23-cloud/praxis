/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package controller wires the reconcilers together: IncidentReconciler,
// PlanReconciler and the verifier loop.
//
// Single responsibility: drive the phase machines. Each per-phase handler
// reads the work it must do from spec and status — never from memory — so a
// restarted controller resumes exactly where it left off. Deadlines are
// computed from timestamps in status via RequeueAfter, not from in-process
// timers.
//
// Specification: docs/02-LLD.md §4. The phase machines land in Phase 1; the
// reconcilers here are still the generated scaffold.
package controller
