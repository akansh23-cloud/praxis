/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

// Condition types written on Incident.status.conditions.
const (
	// ConditionEvidenceCollected records the outcome of evidence collection
	// (LLD §6, playbook Session 3.1): True once a bundle has been
	// assembled, persisted and its hash recorded on status — or once a
	// pre-set hash was found and honoured — False while collection is
	// still running or failing. Failures stay visible here instead of
	// being hidden behind an optimistic Analyzed phase.
	ConditionEvidenceCollected = "EvidenceCollected"

	// ConditionAnalysisAccepted records, on the Incident, an analysis the
	// deterministic guards refused BEFORE any plan was created (ADR-009):
	// False with reason CitationInvalid when a hypothesis cited evidence
	// the bundle does not hold, SchemaInvalid when the planner's output
	// failed the CRD schema twice. The controller sets it from the
	// praxis.dev/analysis-rejected annotation and counts it once in
	// praxis_plans_total{phase="NotCreated",reason}. It is never set True:
	// an accepted analysis is visible as the RemediationPlan it produced.
	ConditionAnalysisAccepted = "AnalysisAccepted"
)

// Condition reasons used by the incident evidence machine (Session 3.1).
const (
	// ReasonCollectionInProgress marks an incident that has entered
	// Collecting and whose bundle is not yet assembled.
	ReasonCollectionInProgress = "CollectionInProgress"

	// ReasonCollectionFailed marks a failed collection attempt; the
	// message carries the error. The phase stays Collecting — an incident
	// never reaches Analyzed on the back of a failure.
	ReasonCollectionFailed = "CollectionFailed"

	// ReasonEvidenceStored marks the real thing: bundle assembled from
	// live evidence, persisted to its ConfigMap, hash recorded on status.
	ReasonEvidenceStored = "EvidenceStored"

	// ReasonEvidencePresupplied marks an incident whose evidenceBundleHash
	// was already set (through the status subresource) before the
	// collector ran, so collection was skipped and the recorded hash
	// honoured. This is how Phase 1-era flows (chainsaw fixtures, manual
	// walkthroughs) keep working; the reason keeps the difference from a
	// real collection observable.
	ReasonEvidencePresupplied = "EvidencePresupplied"
)

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

	// ReasonCitationsResolved marks a hypothesis whose every citation
	// names an item of the persisted evidence bundle (Session 3.3; the
	// real check behind ConditionCitationsResolved).
	ReasonCitationsResolved = "CitationsResolved"

	// ReasonCitationInvalid rejects a plan — or, on the Incident, an
	// analysis refused before a plan existed — because a citation does
	// not resolve in the exact evidence bundle analyzed, or because no
	// persisted bundle exists to resolve against. This is the structural
	// hallucination guard of LLD §5 (FR-P3-04): an id that merely looks
	// like ev/<source>-<seq> is not evidence. Counted in
	// praxis_plans_total{reason="CitationInvalid"}.
	ReasonCitationInvalid = "CitationInvalid"

	// ReasonSchemaInvalid marks, on the Incident, a planner output that
	// failed the CRD-derived schema on the first attempt and again on the
	// single permitted retry (FR-P3-05); no plan was created.
	ReasonSchemaInvalid = "SchemaInvalid"
)
