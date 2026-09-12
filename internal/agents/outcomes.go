/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agents

import (
	"fmt"
	"strings"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// The first-class refusals of playbook Session 3.3 (ADR-009). An Agent
// that detects, deterministically, that its own output must not become a
// plan returns one of these instead of the output. The caller records the
// refusal (the benchmark as an AnalysisRejected run, the Incident through
// praxis.dev/analysis-rejected) — it is an outcome, never a harness
// failure, and never something to "fix up" and continue from.

// CitationError: at least one hypothesis cites an evidence id the bundle
// does not hold. The refused hypotheses travel along for forensics only;
// no caller may treat them as valid.
type CitationError struct {
	Unresolved []praxisv1alpha1.EvidenceID
	Refused    Hypotheses
}

// Error implements error; it names the reason and the unresolved ids.
func (e *CitationError) Error() string {
	ids := make([]string, 0, len(e.Unresolved))
	for _, id := range e.Unresolved {
		ids = append(ids, string(id))
	}
	return fmt.Sprintf("%s: %d citation(s) do not resolve to any item of the evidence bundle: %s",
		praxisv1alpha1.ReasonCitationInvalid, len(e.Unresolved), strings.Join(ids, ", "))
}

// SchemaError: the model's structured output violated the CRD-derived
// contract on the first attempt and again on the single permitted retry.
type SchemaError struct {
	Attempts   int
	Violations []string
}

// Error implements error; it names the reason and every violation.
func (e *SchemaError) Error() string {
	return fmt.Sprintf("%s: output still violates the plan schema after %d attempt(s): %s",
		praxisv1alpha1.ReasonSchemaInvalid, e.Attempts, strings.Join(e.Violations, "; "))
}

// Usage is an agent's provider-neutral accounting over one incident: how
// many model calls it made and what they cost. Agents that call no model
// report nothing.
type Usage struct {
	Provider     string
	Model        string
	Calls        int
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	CostKnown    bool
}

// Metered is implemented by agents that can account for what they spent
// on the incident they last worked on.
type Metered interface {
	Usage() Usage
}

// Attributed is implemented by agents that attach audit metadata to the
// plan they last proposed — the prompt hash and model of LLD §15 —
// which the caller copies onto the created RemediationPlan as
// annotations.
type Attributed interface {
	PlanAnnotations() map[string]string
}
