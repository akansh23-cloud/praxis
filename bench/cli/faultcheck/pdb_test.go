/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"strings"
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// frozenPDB is the deadlocked shape the disruption controller reports for
// minAvailable 3 over 2 replicas; cases below undo one aspect each.
func frozenPDB() *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 1,
			DisruptionsAllowed: 0,
			CurrentHealthy:     2,
			DesiredHealthy:     3,
			Conditions: []metav1.Condition{{
				Type:   policyv1.DisruptionAllowedCondition,
				Status: metav1.ConditionFalse,
				Reason: policyv1.InsufficientPodsReason,
			}},
		},
	}
}

func TestPDBFrozen(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*policyv1.PodDisruptionBudget)
		want       bool
		wantStatus string // substring the returned evidence must contain
	}{
		{
			name:       "deadlocked",
			mutate:     func(*policyv1.PodDisruptionBudget) {},
			want:       true,
			wantStatus: "currentHealthy=2 < desiredHealthy=3",
		},
		{
			name:       "not reconciled yet",
			mutate:     func(p *policyv1.PodDisruptionBudget) { p.Generation = 2 },
			want:       false,
			wantStatus: "not reconciled",
		},
		{
			name:       "budget has headroom",
			mutate:     func(p *policyv1.PodDisruptionBudget) { p.Status.DisruptionsAllowed = 1 },
			want:       false,
			wantStatus: "disruptionsAllowed is 1",
		},
		{
			name:       "condition missing",
			mutate:     func(p *policyv1.PodDisruptionBudget) { p.Status.Conditions = nil },
			want:       false,
			wantStatus: "not reported",
		},
		{
			name: "zero allowed but sufficient pods",
			mutate: func(p *policyv1.PodDisruptionBudget) {
				p.Status.Conditions[0].Reason = policyv1.SufficientPodsReason
			},
			want:       false,
			wantStatus: "want False/InsufficientPods",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pdb := frozenPDB()
			tc.mutate(pdb)
			got, status := pdbFrozen(pdb)
			if got != tc.want {
				t.Errorf("pdbFrozen() = %v, want %v (status: %s)", got, tc.want, status)
			}
			if !strings.Contains(status, tc.wantStatus) {
				t.Errorf("pdbFrozen() status %q does not contain %q", status, tc.wantStatus)
			}
		})
	}
}
