/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

// Condition types written on RemediationPlan.status.conditions (LLD §14).
// Phase 1 populates the four below; PolicyPassed, SimulationPassed,
// Executed, Verified and RolledBack arrive with their phases.
const (
	// ConditionEvidenceValid records whether spec.evidenceBundleHash equals
	// the referenced Incident's status.evidenceBundleHash.
	ConditionEvidenceValid = "EvidenceValid"

	// ConditionCitationsResolved records whether every evidence ID the
	// hypothesis cites resolves in the named bundle. Stubbed in Phase 1.
	ConditionCitationsResolved = "CitationsResolved"

	// ConditionScopeValid records whether every action targets inside the
	// incident's declared scope. Stubbed in Phase 1.
	ConditionScopeValid = "ScopeValid"

	// ConditionApproved records the human decision. False with reason
	// AwaitingApproval while the plan waits.
	ConditionApproved = "Approved"
)

// Condition reasons used by the Phase 1 machine. Reasons are part of the
// observable API: tests and dashboards match on them, so they change only
// with an ADR.
const (
	// ReasonNotYetEvaluated seeds a condition when the plan enters
	// Validating, before the corresponding check has run. A rejected plan
	// keeps this reason on every check that never ran — the honest record
	// of the cheapest-first ordering.
	ReasonNotYetEvaluated = "NotYetEvaluated"

	// ReasonEvidenceHashMatches marks a successful evidence-hash check.
	ReasonEvidenceHashMatches = "EvidenceHashMatches"

	// ReasonEvidenceMismatch rejects a plan whose referenced Incident is
	// missing, is a different incarnation (UID), or whose
	// status.evidenceBundleHash differs from the plan's spec value.
	ReasonEvidenceMismatch = "EvidenceMismatch"

	// ReasonAwaitingApproval marks a plan that passed every Validating
	// check and now waits for a human.
	ReasonAwaitingApproval = "AwaitingApproval"

	// ReasonApprovedAwaitingExecutor marks a plan whose hash-bound approval
	// verified. The phase stays AwaitingApproval: execution arrives with
	// the executor in Phase 5, and an approved plan parks here until then.
	ReasonApprovedAwaitingExecutor = "ApprovedAwaitingExecutor"

	// ReasonApprovalInvalidated rejects a plan whose approval could not be
	// verified against a freshly recomputed binding hash: the annotation
	// value, the stored boundTo and the recomputation must all agree, so a
	// changed evidence bundle, a vanished Incident or a wrong annotation
	// all land here rather than granting anything.
	ReasonApprovalInvalidated = "ApprovalInvalidated"
)
