/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// ActionType is the closed action vocabulary. This enum is the single most
// important line in the security architecture: anything the planner emits
// outside this list is rejected by the API server's schema validation before
// it exists as an object. Prompt injection cannot invent a verb.
//
// ADR-001: extend this vocabulary only when a benchmark scenario demands it.
// +kubebuilder:validation:Enum=RestartWorkload;ScaleWorkload;RollbackRelease;PatchResourceLimits;CordonNode
type ActionType string

const (
	ActionRestartWorkload     ActionType = "RestartWorkload"
	ActionScaleWorkload       ActionType = "ScaleWorkload"
	ActionRollbackRelease     ActionType = "RollbackRelease"
	ActionPatchResourceLimits ActionType = "PatchResourceLimits"
	ActionCordonNode          ActionType = "CordonNode"
)

// TargetRef identifies exactly one object an action operates on. Wildcards
// and label selectors are deliberately unsupported: a plan that cannot
// enumerate its targets cannot have its blast radius computed.
type TargetRef struct {
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet;Node
	Kind string `json:"kind"`

	// Namespace of the target. Required for namespaced kinds; must be
	// omitted for Node (enforced by CEL on Action).
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// ScaleWorkloadParams bounds a replica change in both directions.
type ScaleWorkloadParams struct {
	// FromReplicas is the replica count observed when the plan was created.
	// The executor refuses to act if the live value has drifted — optimistic
	// concurrency at the semantic level, not just resourceVersion.
	// +kubebuilder:validation:Minimum=0
	FromReplicas int32 `json:"fromReplicas"`

	// ToReplicas is the desired count. This schema ceiling is the hard
	// structural bound; the risk scorer and policy gate impose the real,
	// per-namespace limits on top of it.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	ToReplicas int32 `json:"toReplicas"`
}

// RollbackReleaseParams selects the revision to return to.
type RollbackReleaseParams struct {
	// ToRevision pins the exact revision. When omitted, the executor
	// targets the previous revision and records which one it chose.
	// +kubebuilder:validation:Minimum=1
	// +optional
	ToRevision *int64 `json:"toRevision,omitempty"`
}

// QuantityChange records both sides of a resource change so the diff is
// reviewable from the plan alone and drift is detectable at execution time.
type QuantityChange struct {
	From resource.Quantity `json:"from"`
	To   resource.Quantity `json:"to"`
}

// PatchResourceLimitsParams adjusts container resources.
// +kubebuilder:validation:XValidation:rule="has(self.memory) || has(self.cpu)",message="at least one of memory or cpu must be set"
type PatchResourceLimitsParams struct {
	// +kubebuilder:validation:MinLength=1
	Container string `json:"container"`

	// +optional
	Memory *QuantityChange `json:"memory,omitempty"`

	// +optional
	CPU *QuantityChange `json:"cpu,omitempty"`
}

// Action is a discriminated union: exactly the parameter block matching Type
// must be set. The CEL rules below make the API server enforce this, so a
// malformed plan is impossible to persist, not merely flagged.
// +kubebuilder:validation:XValidation:rule="(self.type == 'ScaleWorkload') == has(self.scaleWorkload)",message="scaleWorkload params must be set iff type is ScaleWorkload"
// +kubebuilder:validation:XValidation:rule="(self.type == 'PatchResourceLimits') == has(self.patchResourceLimits)",message="patchResourceLimits params must be set iff type is PatchResourceLimits"
// +kubebuilder:validation:XValidation:rule="self.type == 'RollbackRelease' || !has(self.rollbackRelease)",message="rollbackRelease params are only valid for type RollbackRelease"
// +kubebuilder:validation:XValidation:rule="self.type == 'CordonNode' ? self.target.kind == 'Node' : self.target.kind != 'Node'",message="CordonNode must target a Node; no other action may"
// +kubebuilder:validation:XValidation:rule="self.target.kind == 'Node' ? !has(self.target.namespace) : has(self.target.namespace)",message="namespace is required for namespaced kinds and forbidden for Node"
type Action struct {
	Type ActionType `json:"type"`

	Target TargetRef `json:"target"`

	// +optional
	ScaleWorkload *ScaleWorkloadParams `json:"scaleWorkload,omitempty"`

	// +optional
	RollbackRelease *RollbackReleaseParams `json:"rollbackRelease,omitempty"`

	// +optional
	PatchResourceLimits *PatchResourceLimitsParams `json:"patchResourceLimits,omitempty"`
}

// EvidenceID references one item in an evidence bundle, e.g. "ev/podstatus-04".
// The token before the sequence number comes from the closed ADR-005 source
// vocabulary (the LLD §6 evidence types, lowercased).
// +kubebuilder:validation:Pattern=`^ev/[a-z0-9][a-z0-9-]*$`
// +kubebuilder:validation:MaxLength=128
type EvidenceID string

// Hypothesis is the analyzer's ranked explanation. Every claim must be
// traceable to an evidence ID; the citation validator (controller-side,
// Phase 3) rejects plans whose citations don't resolve against the bundle
// named by spec.evidenceBundleHash. That is the structural hallucination
// guard — a hard check, not a prompt instruction.
type Hypothesis struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Summary string `json:"summary"`

	// ConfidencePercent in [0,100]. An integer percentage rather than a
	// float: Kubernetes API conventions forbid floating point in APIs
	// (serialization ambiguity across clients), and nothing downstream
	// needs sub-percent precision. (ADR-003)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	ConfidencePercent int32 `json:"confidencePercent"`

	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	Citations []EvidenceID `json:"citations"`
}

// FailureAction is what the verifier does when the predicate fails at the
// deadline.
// +kubebuilder:validation:Enum=Rollback;Escalate
type FailureAction string

const (
	FailureActionRollback FailureAction = "Rollback"
	FailureActionEscalate FailureAction = "Escalate"
)

// VerificationSpec is the falsifiable success contract, declared before
// execution. The LLM proposes it but never evaluates it — the verifier does,
// against Prometheus, at the deadline.
//
// ADR-004: v1alpha1 accepts free-form PromQL. The roadmap replaces this with
// a template library the planner selects from, so a model cannot author a
// predicate that trivially passes.
type VerificationSpec struct {
	// Predicate is a PromQL expression that must hold for the sustained
	// window for the plan to be Verified.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Predicate string `json:"predicate"`

	// Window the predicate must hold for, measured from execution
	// completion.
	Window metav1.Duration `json:"window"`

	OnFailure FailureAction `json:"onFailure"`
}

// RollbackStrategy names how a failed plan is reversed.
// +kubebuilder:validation:Enum=RestorePreviousSpec;None
type RollbackStrategy string

const (
	RollbackRestorePreviousSpec RollbackStrategy = "RestorePreviousSpec"
	RollbackNone                RollbackStrategy = "None"
)

// RollbackSpec declares intent only. The snapshot that makes rollback
// possible is recorded by the executor in status.execution.snapshotRef —
// status, not spec, because spec is immutable after creation and the
// snapshot cannot exist until execution begins. (ADR-002; deliberately
// departs from the research dossier's sketch, which placed snapshotRef in
// spec, for exactly this reason.)
type RollbackSpec struct {
	Strategy RollbackStrategy `json:"strategy"`
}

// IncidentRef binds a plan to the incident it remediates.
type IncidentRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// UID pins the plan to a specific incarnation of the incident so a
	// recreated incident with the same name cannot inherit stale plans.
	// +optional
	UID types.UID `json:"uid,omitempty"`
}

// RemediationPlanSpec is immutable after creation (transition rule below):
// approvals are cryptographically bound to a hash of the exact plan, so an
// edited plan is by definition a different plan. A change of mind means a
// new object.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="RemediationPlan spec is immutable; create a new plan instead of editing"
// +kubebuilder:validation:XValidation:rule="self.verification.onFailure != 'Rollback' || self.rollback.strategy != 'None'",message="onFailure Rollback requires a rollback strategy"
type RemediationPlanSpec struct {
	IncidentRef IncidentRef `json:"incidentRef"`

	// EvidenceBundleHash binds this plan to the exact evidence it was
	// derived from.
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	EvidenceBundleHash string `json:"evidenceBundleHash"`

	Hypothesis Hypothesis `json:"hypothesis"`

	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=5
	Actions []Action `json:"actions"`

	Verification VerificationSpec `json:"verification"`

	Rollback RollbackSpec `json:"rollback"`
}

// PlanPhase tracks a plan through the control loop.
// +kubebuilder:validation:Enum=Pending;Validating;AwaitingApproval;Executing;Verifying;Succeeded;Failed;RolledBack;Rejected
type PlanPhase string

const (
	PlanPhasePending          PlanPhase = "Pending"
	PlanPhaseValidating       PlanPhase = "Validating"
	PlanPhaseAwaitingApproval PlanPhase = "AwaitingApproval"
	PlanPhaseExecuting        PlanPhase = "Executing"
	PlanPhaseVerifying        PlanPhase = "Verifying"
	PlanPhaseSucceeded        PlanPhase = "Succeeded"
	PlanPhaseFailed           PlanPhase = "Failed"
	PlanPhaseRolledBack       PlanPhase = "RolledBack"
	PlanPhaseRejected         PlanPhase = "Rejected"
)

// RiskTier buckets the numeric blast-radius score for policy and display.
// +kubebuilder:validation:Enum=Low;Medium;High
type RiskTier string

// BlastRadius is computed deterministically by the risk scorer from the
// plan alone — never by the model.
type BlastRadius struct {
	Score      int32    `json:"score"`
	Tier       RiskTier `json:"tier"`
	Objects    int32    `json:"objects"`
	Namespaces int32    `json:"namespaces"`
	Reversible bool     `json:"reversible"`
}

// VerdictResult is the policy gate's decision.
// +kubebuilder:validation:Enum=Allow;Deny
type VerdictResult string

// PolicyVerdict records which policies, at which version, said what.
// Rejections are first-class outcomes: "the policy caught it" is a headline
// metric, not a failure.
type PolicyVerdict struct {
	Result VerdictResult `json:"result"`

	// Engine that produced the verdict, e.g. "kyverno".
	Engine string `json:"engine"`

	// +optional
	Policies []string `json:"policies,omitempty"`

	// PolicyVersion is the git revision of the policy bundle evaluated.
	// +optional
	PolicyVersion string `json:"policyVersion,omitempty"`
}

// SimulationOutcome is the server-side dry-run result.
// +kubebuilder:validation:Enum=Passed;Failed;Skipped
type SimulationOutcome string

// SimulationResult is the proof-of-safety artifact reference.
type SimulationResult struct {
	DryRun SimulationOutcome `json:"dryRun"`

	// DiffRef names the ConfigMap holding the rendered server-side diff.
	// +optional
	DiffRef string `json:"diffRef,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

// ApprovalStatus is written only by the approval gate.
type ApprovalStatus struct {
	Required bool `json:"required"`

	// BoundTo is the approval binding hash of LLD §8: sha256 over the
	// evidence bundle hash and the sha256 of the canonical-JSON spec, so
	// the approval is void the moment either changes. The diff and policy
	// segments join the hash when those artifacts exist (Phase 4).
	// +optional
	BoundTo string `json:"boundTo,omitempty"`

	// +optional
	ApprovedBy string `json:"approvedBy,omitempty"`

	// +optional
	ApprovedAt *metav1.Time `json:"approvedAt,omitempty"`
}

// ExecutionStatus is written only by the executor.
type ExecutionStatus struct {
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// SnapshotRef names the ConfigMap capturing pre-change object state,
	// written by the executor before the first mutation. (ADR-002)
	// +optional
	SnapshotRef string `json:"snapshotRef,omitempty"`
}

// RemediationPlanStatus is the deterministic machinery's ledger. Condition
// types used by the controllers (LLD §14): EvidenceValid, CitationsResolved,
// ScopeValid, PolicyPassed, SimulationPassed, Approved, Executed, Verified,
// RolledBack.
type RemediationPlanStatus struct {
	// +optional
	Phase PlanPhase `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	BlastRadius *BlastRadius `json:"blastRadius,omitempty"`

	// +optional
	PolicyVerdict *PolicyVerdict `json:"policyVerdict,omitempty"`

	// +optional
	Simulation *SimulationResult `json:"simulation,omitempty"`

	// +optional
	Approval *ApprovalStatus `json:"approval,omitempty"`

	// +optional
	Execution *ExecutionStatus `json:"execution,omitempty"`
}

// RemediationPlan turns an LLM's proposed fix into a typed, policy-validated,
// dry-run-proven, human-approvable, SLO-verified, automatically reversible
// change. The model that reasons has no credentials; the process that acts
// has no model.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rplan
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Risk",type=string,JSONPath=`.status.blastRadius.tier`
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.status.policyVerdict.result`
// +kubebuilder:printcolumn:name="Approved-By",type=string,JSONPath=`.status.approval.approvedBy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type RemediationPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationPlanSpec   `json:"spec,omitempty"`
	Status RemediationPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RemediationPlanList contains a list of RemediationPlan.
type RemediationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemediationPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RemediationPlan{}, &RemediationPlanList{})
		return nil
	})
}
