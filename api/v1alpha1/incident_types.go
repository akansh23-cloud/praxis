/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// IncidentSource names what raised the incident.
// +kubebuilder:validation:Enum=Alertmanager;Probe;Manual
type IncidentSource string

const (
	IncidentSourceAlertmanager IncidentSource = "Alertmanager"
	IncidentSourceProbe        IncidentSource = "Probe"
	IncidentSourceManual       IncidentSource = "Manual"
)

// Severity follows the usual on-call ladder.
// +kubebuilder:validation:Enum=Critical;High;Medium;Low
type Severity string

// IncidentScope declares, up front, the slice of the cluster this incident
// is about. Every action in every plan for this incident must target inside
// it — the simulator enforces that, which is what makes "the plan cannot
// wander" a checkable property instead of a hope.
type IncidentScope struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +listType=set
	Namespaces []string `json:"namespaces"`

	// AllowNodeActions permits cluster-scoped node actions (CordonNode) for
	// this incident. Node objects live outside any namespace, so they need
	// an explicit grant rather than falling through the namespace check.
	// +optional
	AllowNodeActions bool `json:"allowNodeActions,omitempty"`
}

// IncidentSpec describes what fired and where remediation is allowed to act.
type IncidentSpec struct {
	Source IncidentSource `json:"source"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description"`

	Severity Severity `json:"severity"`

	Scope IncidentScope `json:"scope"`

	// AlertFingerprint carries the Alertmanager fingerprint for
	// deduplication, so one alert storm produces one Incident.
	// +optional
	AlertFingerprint string `json:"alertFingerprint,omitempty"`
}

// IncidentPhase tracks an incident through the loop.
// +kubebuilder:validation:Enum=Detected;Collecting;Analyzed;Remediating;Resolved;Closed
type IncidentPhase string

const (
	IncidentPhaseDetected    IncidentPhase = "Detected"
	IncidentPhaseCollecting  IncidentPhase = "Collecting"
	IncidentPhaseAnalyzed    IncidentPhase = "Analyzed"
	IncidentPhaseRemediating IncidentPhase = "Remediating"
	IncidentPhaseResolved    IncidentPhase = "Resolved"
	IncidentPhaseClosed      IncidentPhase = "Closed"
)

// IncidentStatus is written by the evidence collector and plan controllers.
type IncidentStatus struct {
	// +optional
	Phase IncidentPhase `json:"phase,omitempty"`

	// EvidenceBundleRef names the ConfigMap holding the collected,
	// normalised, size-capped evidence bundle.
	// +optional
	EvidenceBundleRef string `json:"evidenceBundleRef,omitempty"`

	// EvidenceBundleHash is sha256 over the canonical bundle; plans cite it
	// and approvals bind to it.
	// +optional
	EvidenceBundleHash string `json:"evidenceBundleHash,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Incident is the trigger object for the Praxis loop: something fired, here
// is what we know, and here is the boundary inside which any remediation
// must stay.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=inc
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Severity",type=string,JSONPath=`.spec.severity`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Incident struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IncidentSpec   `json:"spec,omitempty"`
	Status IncidentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IncidentList contains a list of Incident.
type IncidentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Incident `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Incident{}, &IncidentList{})
		return nil
	})
}
