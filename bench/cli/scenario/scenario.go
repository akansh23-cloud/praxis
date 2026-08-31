/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package scenario implements the benchmark scenario schema of
// docs/02-LLD.md §17 as Go types plus a strict YAML loader. The schema in
// the LLD is normative: unknown fields are rejected, file references must
// resolve on disk, and every violation is reported with the field path that
// caused it so a scenario author can fix the file without reading Go.
package scenario

import (
	"path/filepath"
	"time"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// FaultKind names the mechanism that injects a scenario's fault.
type FaultKind string

const (
	// FaultManifest applies a manifest file into the scenario's first scope
	// namespace — the simplest mechanism, preferred wherever it suffices.
	FaultManifest FaultKind = "Manifest"
	// FaultPatch applies a kubectl-style patch to an existing object
	// (e.g. lowering a memory limit, swapping an image tag).
	FaultPatch FaultKind = "Patch"
	// FaultChaosMesh applies a Chaos Mesh experiment, for faults that need
	// runtime interference (pod kill, network loss) rather than spec edits.
	FaultChaosMesh FaultKind = "ChaosMesh"
)

// faultKinds is the closed set, in the order error messages list it.
var faultKinds = []FaultKind{FaultManifest, FaultPatch, FaultChaosMesh}

// Topology points at the kustomize overlay that builds the workloads the
// scenario breaks.
type Topology struct {
	// Kustomize is the overlay directory, relative to the scenario
	// directory (e.g. ../../topology/overlays/smoke).
	Kustomize string `json:"kustomize"`
}

// Fault describes how the scenario breaks the topology.
type Fault struct {
	Kind FaultKind `json:"kind"`

	// Ref is the fault payload — a manifest, patch, or Chaos Mesh
	// experiment file — relative to the scenario directory.
	Ref string `json:"ref"`

	// Notes records why this mechanism was chosen over the alternatives.
	// Required: the choice must be written down where the scenario lives
	// (playbook Session 2.2 makes recording it mandatory per pack).
	Notes string `json:"notes"`
}

// Incident seeds the synthetic Incident CR the runner files once the fault
// is injected.
type Incident struct {
	// SeverityHint becomes the Incident's spec.severity.
	SeverityHint praxisv1alpha1.Severity `json:"severityHint"`

	// ScopeNamespaces becomes the Incident's spec.scope.namespaces — the
	// boundary any remediation must stay inside. The runner also creates
	// and tears down exactly these namespaces.
	ScopeNamespaces []string `json:"scopeNamespaces"`
}

// ActionTarget pins an action expectation to one object. Namespace is
// deliberately absent: the Incident's scope namespaces are the sole
// authority on where actions may land (LLD §3).
type ActionTarget struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ActionRef names an action type from the closed vocabulary, optionally
// pinned to a target. A bare type in forbiddenActions forbids every action
// of that type regardless of target.
type ActionRef struct {
	Type praxisv1alpha1.ActionType `json:"type"`

	// +optional
	Target *ActionTarget `json:"target,omitempty"`
}

// DiagnosisRule is the LLD §17.3 deterministic matching rule (ADR-005):
// the answer key a correct diagnosis must satisfy, authored with the
// scenario so neither the scorer (Session 2.3) nor any agent ever writes
// it. Matching itself is NOT implemented here — this is ground truth only.
type DiagnosisRule struct {
	// RequiredEvidenceIDPatterns are ev/<source>-* globs the hypothesis's
	// citations must include. Source tokens are the §6 evidence types,
	// lowercased — a closed set, validated at load time.
	RequiredEvidenceIDPatterns []string `json:"requiredEvidenceIdPatterns"`

	// RequiredSummaryKeyphrases are lowercase substrings the hypothesis
	// summary must contain; §17.3 matching is case-insensitive, so
	// lowercase is the canonical stored form.
	RequiredSummaryKeyphrases []string `json:"requiredSummaryKeyphrases"`
}

// GroundTruth is what the scorer (Session 2.3) judges an agent's plan
// against. It is authored with the scenario, before any agent exists.
type GroundTruth struct {
	// RootCauseID is the scenario's diagnosis key for the deterministic
	// matching rules of LLD §17.3.
	RootCauseID string `json:"rootCauseId"`

	// Diagnosis is the §17.3 matching rule for RootCauseID. Required.
	Diagnosis DiagnosisRule `json:"diagnosis"`

	// AcceptableActions is the set of plans-content the scenario accepts.
	// Must be empty when RestraintExpected is true — a restraint
	// scenario's only correct answer is no plan at all.
	AcceptableActions []ActionRef `json:"acceptableActions"`

	// ForbiddenActions are actions that make a plan wrong even if it also
	// contains acceptable ones.
	ForbiddenActions []ActionRef `json:"forbiddenActions"`

	// RestraintExpected marks the scenarios where the correct response is
	// the praxis.dev/no-action-proposed annotation on the Incident, not a
	// RemediationPlan.
	RestraintExpected bool `json:"restraintExpected"`

	// FixPredicate and HarmPredicate are deterministic PromQL with
	// Prometheus alerting semantics (true ⇔ ≥1 sample returned): what
	// "fixed" and "harmed" mean for this scenario. Required ground truth
	// per pack (ADR-005); evaluated effect-side only from Phase 5 on.
	FixPredicate  string `json:"fixPredicate"`
	HarmPredicate string `json:"harmPredicate"`
}

// Scenario is one scenarios/<name>/scenario.yaml, loaded and validated.
type Scenario struct {
	// Name must equal the scenario's directory name, so that
	// `praxisbench run --scenario <name>` and the file on disk can never
	// disagree about identity.
	Name string `json:"name"`

	Topology    Topology    `json:"topology"`
	Fault       Fault       `json:"fault"`
	Incident    Incident    `json:"incident"`
	GroundTruth GroundTruth `json:"groundTruth"`

	// TimeoutMinutes bounds the wait for a RemediationPlan or the
	// no-action annotation after the Incident is filed.
	TimeoutMinutes int `json:"timeoutMinutes"`

	// Dir is the absolute scenario directory. Set by Load, never by YAML.
	Dir string `json:"-"`
}

// Timeout is TimeoutMinutes as a duration.
func (s *Scenario) Timeout() time.Duration {
	return time.Duration(s.TimeoutMinutes) * time.Minute
}

// TopologyDir is the absolute path of the kustomize overlay.
func (s *Scenario) TopologyDir() string {
	return filepath.Join(s.Dir, s.Topology.Kustomize)
}

// FaultPath is the absolute path of the fault payload file.
func (s *Scenario) FaultPath() string {
	return filepath.Join(s.Dir, s.Fault.Ref)
}
