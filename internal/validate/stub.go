/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package validate

import (
	"context"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// StubScopeChecker is the Phase-1 placeholder behind the ScopeChecker seam.
// It passes unconditionally and says so via ReasonStubbedInPhase1; the real
// checker arrives with the simulator in Phase 4 (LLD §10).
type StubScopeChecker struct{}

var _ ScopeChecker = StubScopeChecker{}

// Check implements ScopeChecker by passing every action list.
func (StubScopeChecker) Check(
	_ context.Context, _ []praxisv1alpha1.Action, _ praxisv1alpha1.IncidentScope,
) (Verdict, error) {
	return Verdict{
		Passed:  true,
		Reason:  ReasonStubbedInPhase1,
		Message: "Scope checking is stubbed until the simulator lands in Phase 4",
	}, nil
}
