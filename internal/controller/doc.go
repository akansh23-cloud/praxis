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
// Specification: docs/02-LLD.md §4. Phase 1 implements the Incident
// bookkeeping (Detected, Remediating) and the plan machine through
// Pending → Validating → AwaitingApproval plus terminal Rejected; the
// remaining phases arrive with their phases of the master plan.
package controller
