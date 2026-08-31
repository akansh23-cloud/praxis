/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"context"
	"fmt"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// pdbFaultName matches metadata.name in scenarios/pdb-deadlock/fault.yaml.
const pdbFaultName = "storefront-pdb"

// pdbDeadlock observes the pdb-deadlock fault: the disruption controller
// has reconciled the misconfigured PDB and concluded no disruption can
// ever be allowed. The deadlock is pure status arithmetic, so the check
// reads exactly the fields an SRE (or an agent) would.
func pdbDeadlock(ctx context.Context, env Env) error {
	return pollUntil(ctx, env, 2*time.Minute, "the storefront PDB frozen at disruptionsAllowed=0 (InsufficientPods)",
		func(ctx context.Context) (bool, string, error) {
			var pdb policyv1.PodDisruptionBudget
			key := types.NamespacedName{Namespace: env.Namespaces[0], Name: pdbFaultName}
			if err := env.Client.Get(ctx, key, &pdb); err != nil {
				return false, fmt.Sprintf("PDB %s/%s not readable: %v", key.Namespace, key.Name, err), nil
			}
			frozen, status := pdbFrozen(&pdb)
			if frozen {
				env.Logf("    PDB %s: %s", pdb.Name, status)
			}
			return frozen, status, nil
		})
}

// pdbFrozen decides whether a PDB is in the deadlocked state the scenario
// promises: reconciled, zero disruptions allowed, and the controller
// explicitly blaming insufficient healthy pods. Returns the evidence
// either way.
func pdbFrozen(pdb *policyv1.PodDisruptionBudget) (bool, string) {
	st := &pdb.Status
	if st.ObservedGeneration < pdb.Generation {
		return false, fmt.Sprintf("not reconciled yet (observedGeneration %d < generation %d)", st.ObservedGeneration, pdb.Generation)
	}
	if st.DisruptionsAllowed != 0 {
		return false, fmt.Sprintf("disruptionsAllowed is %d, want 0", st.DisruptionsAllowed)
	}
	cond := meta.FindStatusCondition(st.Conditions, policyv1.DisruptionAllowedCondition)
	if cond == nil {
		return false, "condition DisruptionAllowed not reported yet"
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != policyv1.InsufficientPodsReason {
		return false, fmt.Sprintf("condition DisruptionAllowed is %s/%s, want False/%s",
			cond.Status, cond.Reason, policyv1.InsufficientPodsReason)
	}
	return true, fmt.Sprintf("disruptionsAllowed=0, currentHealthy=%d < desiredHealthy=%d, %s",
		st.CurrentHealthy, st.DesiredHealthy, policyv1.InsufficientPodsReason)
}
