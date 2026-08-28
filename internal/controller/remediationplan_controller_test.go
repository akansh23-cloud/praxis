/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/validate"
)

// These specs drive the reconciler by hand, one Reconcile call per step, so
// every transition of LLD §4.2's Phase 1 slice is observable and assertable:
// Pending → Validating → AwaitingApproval, plus terminal Rejected.

var _ = Describe("RemediationPlan phase machine", func() {
	var reconciler *RemediationPlanReconciler

	ctx := context.Background()

	BeforeEach(func() {
		reconciler = &RemediationPlanReconciler{
			Client:    k8sClient,
			Scheme:    k8sClient.Scheme(),
			Citations: validate.StubCitationValidator{},
			Scope:     validate.StubScopeChecker{},
		}
	})

	// reconcilePlan runs one Reconcile for the plan and refreshes it.
	reconcilePlan := func(r *RemediationPlanReconciler, plan *praxisv1alpha1.RemediationPlan) ctrl.Result {
		GinkgoHelper()
		result, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: plan.Namespace, Name: plan.Name},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: plan.Namespace, Name: plan.Name}, plan)).To(Succeed())
		return result
	}

	// walkToSettled reconciles until the phase stops changing (bounded).
	walkToSettled := func(r *RemediationPlanReconciler, plan *praxisv1alpha1.RemediationPlan) {
		GinkgoHelper()
		for range 5 {
			before := plan.Status.Phase
			reconcilePlan(r, plan)
			if plan.Status.Phase == before {
				return
			}
		}
	}

	expectCondition := func(plan *praxisv1alpha1.RemediationPlan, condType string,
		status metav1.ConditionStatus, reason string) {
		GinkgoHelper()
		cond := meta.FindStatusCondition(plan.Status.Conditions, condType)
		Expect(cond).NotTo(BeNil(), "condition %s should exist", condType)
		Expect(cond.Status).To(Equal(status), "condition %s status", condType)
		Expect(cond.Reason).To(Equal(reason), "condition %s reason", condType)
		Expect(cond.Message).NotTo(BeEmpty(), "condition %s message", condType)
	}

	Describe("the happy walk", func() {
		It("walks Pending → Validating → AwaitingApproval with correct conditions at each step", func() {
			incident := newTestIncident(uniqueName("walk-inc"))
			Expect(k8sClient.Create(ctx, incident)).To(Succeed())
			incident.Status.EvidenceBundleHash = evidenceHash
			Expect(k8sClient.Status().Update(ctx, incident)).To(Succeed())

			plan := newTestPlan(uniqueName("walk-plan"), incident.Name, evidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("step 1: pickup — the new plan becomes Pending")
			reconcilePlan(reconciler, plan)
			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhasePending))
			Expect(plan.Status.Conditions).To(BeEmpty())
			Expect(plan.Status.ObservedGeneration).To(Equal(plan.Generation))

			By("step 2: Pending → Validating with all four conditions seeded Unknown")
			reconcilePlan(reconciler, plan)
			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhaseValidating))
			for _, condType := range []string{
				praxisv1alpha1.ConditionEvidenceValid,
				praxisv1alpha1.ConditionCitationsResolved,
				praxisv1alpha1.ConditionScopeValid,
				praxisv1alpha1.ConditionApproved,
			} {
				expectCondition(plan, condType, metav1.ConditionUnknown, praxisv1alpha1.ReasonNotYetEvaluated)
			}

			By("step 3: Validating → AwaitingApproval with each check's verdict recorded")
			reconcilePlan(reconciler, plan)
			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhaseAwaitingApproval))
			expectCondition(plan, praxisv1alpha1.ConditionEvidenceValid,
				metav1.ConditionTrue, praxisv1alpha1.ReasonEvidenceHashMatches)
			expectCondition(plan, praxisv1alpha1.ConditionCitationsResolved,
				metav1.ConditionTrue, validate.ReasonStubbedInPhase1)
			expectCondition(plan, praxisv1alpha1.ConditionScopeValid,
				metav1.ConditionTrue, validate.ReasonStubbedInPhase1)
			expectCondition(plan, praxisv1alpha1.ConditionApproved,
				metav1.ConditionFalse, praxisv1alpha1.ReasonAwaitingApproval)

			By("recording that approval is required but not yet bound (boundTo arrives in Session 1.2)")
			Expect(plan.Status.Approval).NotTo(BeNil())
			Expect(plan.Status.Approval.Required).To(BeTrue())
			Expect(plan.Status.Approval.BoundTo).To(BeEmpty())
			Expect(plan.Status.Approval.ApprovedBy).To(BeEmpty())
			Expect(plan.Status.ObservedGeneration).To(Equal(plan.Generation))
		})
	})

	DescribeTable("evidence rejections end in Rejected without running the later checks",
		func(makeWorld func(planName string) *praxisv1alpha1.RemediationPlan) {
			plan := makeWorld(uniqueName("reject-plan"))
			walkToSettled(reconciler, plan)

			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhaseRejected))
			expectCondition(plan, praxisv1alpha1.ConditionEvidenceValid,
				metav1.ConditionFalse, praxisv1alpha1.ReasonEvidenceMismatch)
			// Cheapest-first means the failure short-circuits: citations and
			// scope must still read NotYetEvaluated, and no approval is owed.
			expectCondition(plan, praxisv1alpha1.ConditionCitationsResolved,
				metav1.ConditionUnknown, praxisv1alpha1.ReasonNotYetEvaluated)
			expectCondition(plan, praxisv1alpha1.ConditionScopeValid,
				metav1.ConditionUnknown, praxisv1alpha1.ReasonNotYetEvaluated)
			Expect(plan.Status.Approval).To(BeNil())
		},
		Entry("the referenced Incident does not exist", func(planName string) *praxisv1alpha1.RemediationPlan {
			plan := newTestPlan(planName, uniqueName("no-such-incident"), evidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			return plan
		}),
		Entry("the plan cites a different hash than the Incident holds", func(planName string) *praxisv1alpha1.RemediationPlan {
			incident := newTestIncident(uniqueName("mismatch-inc"))
			Expect(k8sClient.Create(ctx, incident)).To(Succeed())
			incident.Status.EvidenceBundleHash = evidenceHash
			Expect(k8sClient.Status().Update(ctx, incident)).To(Succeed())
			plan := newTestPlan(planName, incident.Name, differentEvidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			return plan
		}),
		Entry("the Incident has no evidence hash yet", func(planName string) *praxisv1alpha1.RemediationPlan {
			incident := newTestIncident(uniqueName("hashless-inc"))
			Expect(k8sClient.Create(ctx, incident)).To(Succeed())
			plan := newTestPlan(planName, incident.Name, evidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			return plan
		}),
		Entry("the plan is pinned to a different Incident incarnation (UID)", func(planName string) *praxisv1alpha1.RemediationPlan {
			incident := newTestIncident(uniqueName("recreated-inc"))
			Expect(k8sClient.Create(ctx, incident)).To(Succeed())
			incident.Status.EvidenceBundleHash = evidenceHash
			Expect(k8sClient.Status().Update(ctx, incident)).To(Succeed())
			plan := newTestPlan(planName, incident.Name, evidenceHash)
			plan.Spec.IncidentRef.UID = types.UID("11111111-2222-3333-4444-555555555555")
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			return plan
		}),
	)

	Describe("idempotency", func() {
		It("re-reconciling a settled AwaitingApproval plan performs zero status writes", func() {
			incident := newTestIncident(uniqueName("settled-inc"))
			Expect(k8sClient.Create(ctx, incident)).To(Succeed())
			incident.Status.EvidenceBundleHash = evidenceHash
			Expect(k8sClient.Status().Update(ctx, incident)).To(Succeed())
			plan := newTestPlan(uniqueName("settled-plan"), incident.Name, evidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			walkToSettled(reconciler, plan)
			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhaseAwaitingApproval))

			counting := &statusWriteCountingClient{Client: k8sClient}
			countingReconciler := &RemediationPlanReconciler{
				Client:    counting,
				Scheme:    k8sClient.Scheme(),
				Citations: validate.StubCitationValidator{},
				Scope:     validate.StubScopeChecker{},
			}
			versionBefore := plan.ResourceVersion
			statusBefore := plan.Status.DeepCopy()
			for range 3 {
				result := reconcilePlan(countingReconciler, plan)
				Expect(result).To(Equal(ctrl.Result{}))
			}
			Expect(counting.statusWrites).To(BeZero())
			Expect(plan.ResourceVersion).To(Equal(versionBefore))
			Expect(plan.Status).To(Equal(*statusBefore))
		})

		It("re-reconciling a terminal Rejected plan performs zero status writes", func() {
			plan := newTestPlan(uniqueName("terminal-plan"), uniqueName("gone-inc"), evidenceHash)
			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			walkToSettled(reconciler, plan)
			Expect(plan.Status.Phase).To(Equal(praxisv1alpha1.PlanPhaseRejected))

			counting := &statusWriteCountingClient{Client: k8sClient}
			countingReconciler := &RemediationPlanReconciler{
				Client:    counting,
				Scheme:    k8sClient.Scheme(),
				Citations: validate.StubCitationValidator{},
				Scope:     validate.StubScopeChecker{},
			}
			versionBefore := plan.ResourceVersion
			for range 3 {
				result := reconcilePlan(countingReconciler, plan)
				Expect(result).To(Equal(ctrl.Result{}))
			}
			Expect(counting.statusWrites).To(BeZero())
			Expect(plan.ResourceVersion).To(Equal(versionBefore))
		})
	})
})
