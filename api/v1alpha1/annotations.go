/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

// Annotations that form the Phase 1 approval surface. Annotating is the
// interim UX — the Slack gate of LLD §8 replaces it in Phase 4 — but the
// binding rule is permanent: an approval names the exact hash of what it
// approves, or it is void.
const (
	// AnnotationApprove carries the human's yes on a RemediationPlan. Its
	// value must equal status.approval.boundTo, recomputed fresh by the
	// controller from the live spec and Incident status at the moment of
	// approval; any mismatch rejects the plan with reason
	// ApprovalInvalidated instead of granting anything.
	AnnotationApprove = "praxis.dev/approve"

	// AnnotationApprovedBy optionally names who approved; the controller
	// copies it into status.approval.approvedBy when the approval verifies.
	AnnotationApprovedBy = "praxis.dev/approved-by"
)

// The restraint contract (docs/00-MASTER-PLAN.md FR-P2-03, LLD §17): for
// some incidents the only correct remediation is none at all.
const (
	// AnnotationNoActionProposed on an Incident declares that analysis
	// concluded no remediation should be attempted. Restraint is a
	// first-class outcome, not a failure: the benchmark waits for either a
	// RemediationPlan or this annotation, and scores restraint correctness
	// against the scenario's ground truth. The value is a short
	// human-readable reason. Written by agents (Session 2.3 onward); the
	// benchmark only ever reads it.
	AnnotationNoActionProposed = "praxis.dev/no-action-proposed"
)
