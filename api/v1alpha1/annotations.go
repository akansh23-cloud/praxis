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

// Recent-change context (LLD §6 GitCommit evidence). Until the real Git
// integration lands in Phase 6, the deployed-change trail lives in workload
// annotations stamped by whatever applied the change — the bench fault packs
// already do, and so does any pipeline that runs `kubectl apply` with a
// change-cause. The evidence collector only ever READS these; when they are
// absent there simply is no GitCommit evidence — history is never invented.
const (
	// AnnotationCommit carries the VCS revision behind the workload's
	// current spec. Read together with the upstream convention
	// kubernetes.io/change-cause into GitCommit evidence items.
	AnnotationCommit = "praxis.dev/commit"
)
