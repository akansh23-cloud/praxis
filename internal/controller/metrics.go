/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// praxis_plans_total is the plan outcome counter of LLD §15, labelled by
// the phase a plan entered and the reason that put it there: every
// Rejected transition names its rejecting condition's reason, so
// praxis_plans_total{phase="Rejected",reason="CitationInvalid"} counts
// the structural hallucination guard firing (FR-P3-04). An analysis the
// deterministic guards refused BEFORE a plan existed is counted under
// phase="NotCreated" from the Incident's praxis.dev/analysis-rejected
// annotation (ADR-009), so the metric sees citation failures whichever
// side caught them.
var plansTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "praxis_plans_total",
		Help: "RemediationPlan phase transitions by phase and the reason that caused them; " +
			"phase=NotCreated counts analyses refused by the deterministic guards before a plan was created.",
	},
	[]string{"phase", "reason"},
)

// PhaseNotCreated is the metric's phase label for analyses refused before
// a plan existed. It is a metric label, deliberately not a PlanPhase.
const PhaseNotCreated = "NotCreated"

func init() {
	metrics.Registry.MustRegister(plansTotal)
}

// countPlanTransition records one phase entry. The reason is the phase's
// decisive condition: a rejection's failing condition, AwaitingApproval's
// own reason, empty for the intermediate phases.
func countPlanTransition(plan *praxisv1alpha1.RemediationPlan, entered praxisv1alpha1.PlanPhase) {
	plansTotal.WithLabelValues(string(entered), transitionReason(plan, entered)).Inc()
}

func transitionReason(plan *praxisv1alpha1.RemediationPlan, entered praxisv1alpha1.PlanPhase) string {
	switch entered {
	case praxisv1alpha1.PlanPhaseRejected:
		for _, condType := range []string{
			praxisv1alpha1.ConditionEvidenceValid,
			praxisv1alpha1.ConditionCitationsResolved,
			praxisv1alpha1.ConditionScopeValid,
			praxisv1alpha1.ConditionApproved,
		} {
			if c := meta.FindStatusCondition(plan.Status.Conditions, condType); c != nil && c.Status == metav1.ConditionFalse {
				return c.Reason
			}
		}
		return "Unknown"
	case praxisv1alpha1.PlanPhaseAwaitingApproval:
		return praxisv1alpha1.ReasonAwaitingApproval
	default:
		return ""
	}
}
