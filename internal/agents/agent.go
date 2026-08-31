/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agents

import (
	"context"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// Hypotheses is an agent's ranked explanation of an incident, best first.
// The ordering carries meaning: the benchmark's diagnosis-top-1 metric
// judges element 0 and top-3 judges the first three (LLD §17.3).
type Hypotheses []praxisv1alpha1.Hypothesis

// NoAction is the restraint verdict: the agent concludes no remediation
// should be attempted. It is a first-class outcome, not a failure — for
// some incidents (an external dependency down, say) any plan is wrong.
// The caller records it as the praxis.dev/no-action-proposed annotation
// on the Incident.
type NoAction struct {
	// Proposed is true when the agent explicitly proposes taking no action.
	Proposed bool

	// Reason is the human-readable justification; it becomes the
	// annotation's value, so it should read as one sentence.
	Reason string
}

// Agent is the analysis seam of LLD §5, implemented by the rule-based
// baseline (Phase 2) and the LLM agent (Phase 3). It is deliberately
// provider- and model-neutral: nothing in the signatures names a model, a
// prompt, or a vendor.
//
// The two inputs are the agent's ENTIRE observable world. An Agent holds
// no cluster client and receives no benchmark state: if a fact is not in
// the Incident or the evidence bundle, the agent cannot know it. That is
// what makes the benchmark honest — the bench hands every agent the same
// inputs a production analyzer would get, and nothing else.
//
// Implementations must not mutate incident or bundle.
type Agent interface {
	// Analyze returns ranked hypotheses about the root cause, best first.
	// Every claim must be traceable to bundle item ids via citations.
	// An empty result is legal: it means the agent has no explanation.
	Analyze(ctx context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle) (Hypotheses, error)

	// Plan turns the analysis into exactly one of:
	//   - a candidate RemediationPlanSpec (plan != nil, noAction.Proposed false), or
	//   - an explicit no-action verdict (plan == nil, noAction.Proposed true).
	// Returning neither, or both, is a contract violation the caller must
	// reject. The spec is a proposal only — the caller persists it and the
	// deterministic machinery (validation, policy, approval) judges it.
	Plan(ctx context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle,
		hypotheses Hypotheses) (plan *praxisv1alpha1.RemediationPlanSpec, noAction NoAction, err error)
}
