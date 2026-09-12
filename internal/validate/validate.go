/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package validate

import (
	"context"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// ReasonStubbedInPhase1 is the Verdict reason the remaining Phase-1 stub
// (the scope checker, real in Phase 4) returns. It lands verbatim in
// Condition.Reason so `kubectl describe` shows the check passed only
// because it is not implemented yet — visible honesty over a silent fake.
const ReasonStubbedInPhase1 = "StubbedInPhase1"

// Verdict is the outcome of one validation check, expressed in the
// vocabulary status conditions want: a pass/fail bit, a CamelCase machine
// reason, and a human-readable message. A failed Verdict is a terminal
// judgment on the plan (phase Rejected); machinery trouble while checking is
// returned as an error instead, so the controller can retry with backoff.
type Verdict struct {
	Passed  bool
	Reason  string
	Message string
}

// CitationValidator is the structural hallucination guard from LLD §5: every
// evidence ID a hypothesis cites must resolve in the bundle the plan names.
// The seam takes the bundle itself (LLD §5: Validate(Hypotheses, Bundle));
// the caller is responsible for handing over the EXACT bundle the plan's
// evidenceBundleHash names — the controller loads it from the persisted
// ConfigMap and checks the bytes hash to that value first.
type CitationValidator interface {
	Validate(ctx context.Context, hypothesis praxisv1alpha1.Hypothesis, bundle *evidence.Bundle) (Verdict, error)
}

// ScopeChecker enforces LLD §10's first gate: every action's target must lie
// inside the incident's declared scope, and Node targets require the
// explicit allowNodeActions grant. The scope check runs before policy and
// cannot be overridden by policy.
type ScopeChecker interface {
	Check(ctx context.Context, actions []praxisv1alpha1.Action, scope praxisv1alpha1.IncidentScope) (Verdict, error)
}
