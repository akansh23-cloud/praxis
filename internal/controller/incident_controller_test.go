/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// Phase 1 keeps the incident controller to pure bookkeeping (LLD §4.1):
// Detected on creation, Remediating once any plan references the incident.
// Collecting and Analyzed belong to the Phase 3 evidence pipeline.

var _ = Describe("Incident phase bookkeeping", func() {
	var reconciler *IncidentReconciler

	ctx := context.Background()

	BeforeEach(func() {
		reconciler = &IncidentReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	reconcileIncident := func(r *IncidentReconciler, incident *praxisv1alpha1.Incident) ctrl.Result {
		GinkgoHelper()
		result, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: incident.Namespace, Name: incident.Name},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx,
			types.NamespacedName{Namespace: incident.Namespace, Name: incident.Name}, incident)).To(Succeed())
		return result
	}

	It("marks a fresh Incident Detected", func() {
		incident := newTestIncident(uniqueName("fresh-inc"))
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseDetected))
		Expect(incident.Status.ObservedGeneration).To(Equal(incident.Generation))
	})

	It("moves a Detected Incident to Remediating once a plan references it", func() {
		incident := newTestIncident(uniqueName("referenced-inc"))
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())
		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseDetected))

		plan := newTestPlan(uniqueName("referencing-plan"), incident.Name, evidenceHash)
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseRemediating))
	})

	It("ignores plans pinned to a different Incident incarnation (UID)", func() {
		incident := newTestIncident(uniqueName("pinned-inc"))
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		plan := newTestPlan(uniqueName("stale-plan"), incident.Name, evidenceHash)
		plan.Spec.IncidentRef.UID = types.UID("11111111-2222-3333-4444-555555555555")
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseDetected))
	})

	It("re-reconciling a settled Incident performs zero status writes", func() {
		incident := newTestIncident(uniqueName("settled-detected-inc"))
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())
		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseDetected))

		plan := newTestPlan(uniqueName("settling-plan"), incident.Name, evidenceHash)
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		reconcileIncident(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseRemediating))

		counting := &statusWriteCountingClient{Client: k8sClient}
		countingReconciler := &IncidentReconciler{Client: counting, Scheme: k8sClient.Scheme()}
		versionBefore := incident.ResourceVersion
		for range 3 {
			result := reconcileIncident(countingReconciler, incident)
			Expect(result).To(Equal(ctrl.Result{}))
		}
		Expect(counting.statusWrites).To(BeZero())
		Expect(incident.ResourceVersion).To(Equal(versionBefore))
	})
})
