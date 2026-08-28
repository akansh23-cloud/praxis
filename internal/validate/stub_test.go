/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package validate

import (
	"context"
	"testing"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// The stubs' contract is small but load-bearing: they must pass, and they
// must confess to being stubs through the reason the controller copies onto
// the plan's conditions. These tables pin both halves so a future "helpful"
// edit cannot silently turn a stub into a real-looking check.

const scopedNamespace = "shop"

func TestStubCitationValidator(t *testing.T) {
	tests := []struct {
		name       string
		hypothesis praxisv1alpha1.Hypothesis
		bundleHash string
	}{
		{
			name: "citations present",
			hypothesis: praxisv1alpha1.Hypothesis{
				Summary:           "checkout-api OOMKilled after memory limit lowered",
				ConfidencePercent: 86,
				Citations:         []praxisv1alpha1.EvidenceID{"ev/pod-status-04", "ev/event-11"},
			},
			bundleHash: "sha256:9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f",
		},
		{
			name:       "empty hypothesis and hash",
			hypothesis: praxisv1alpha1.Hypothesis{},
			bundleHash: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, err := StubCitationValidator{}.Validate(context.Background(), tt.hypothesis, tt.bundleHash)
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
			if !verdict.Passed {
				t.Errorf("Validate() Passed = false, want true")
			}
			if verdict.Reason != ReasonStubbedInPhase1 {
				t.Errorf("Validate() Reason = %q, want %q", verdict.Reason, ReasonStubbedInPhase1)
			}
			if verdict.Message == "" {
				t.Errorf("Validate() Message is empty, want an honest explanation")
			}
		})
	}
}

func TestStubScopeChecker(t *testing.T) {
	tests := []struct {
		name    string
		actions []praxisv1alpha1.Action
		scope   praxisv1alpha1.IncidentScope
	}{
		{
			name: "in-scope action",
			actions: []praxisv1alpha1.Action{{
				Type:   praxisv1alpha1.ActionRestartWorkload,
				Target: praxisv1alpha1.TargetRef{Kind: "Deployment", Namespace: scopedNamespace, Name: "checkout-api"},
			}},
			scope: praxisv1alpha1.IncidentScope{Namespaces: []string{scopedNamespace}},
		},
		{
			// A real checker must fail this one; the stub must still pass it,
			// because the stub judges nothing — that is the point of the reason.
			name: "out-of-scope action still passes the stub",
			actions: []praxisv1alpha1.Action{{
				Type:   praxisv1alpha1.ActionRestartWorkload,
				Target: praxisv1alpha1.TargetRef{Kind: "Deployment", Namespace: "prod", Name: "payments"},
			}},
			scope: praxisv1alpha1.IncidentScope{Namespaces: []string{scopedNamespace}},
		},
		{
			name:    "no actions",
			actions: nil,
			scope:   praxisv1alpha1.IncidentScope{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, err := StubScopeChecker{}.Check(context.Background(), tt.actions, tt.scope)
			if err != nil {
				t.Fatalf("Check() error = %v, want nil", err)
			}
			if !verdict.Passed {
				t.Errorf("Check() Passed = false, want true")
			}
			if verdict.Reason != ReasonStubbedInPhase1 {
				t.Errorf("Check() Reason = %q, want %q", verdict.Reason, ReasonStubbedInPhase1)
			}
			if verdict.Message == "" {
				t.Errorf("Check() Message is empty, want an honest explanation")
			}
		})
	}
}
