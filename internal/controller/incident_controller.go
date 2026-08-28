/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// IncidentReconciler keeps the incident phase honest. Phase 1 is pure
// bookkeeping on the LLD §4.1 machine: Detected on creation, Remediating
// once any RemediationPlan references the incident. Collecting and Analyzed
// belong to the Phase 3 evidence pipeline; Resolved and Closed to Phase 5+.
type IncidentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=praxis.dev,resources=incidents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.dev,resources=incidents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.dev,resources=incidents/finalizers,verbs=update
// +kubebuilder:rbac:groups=praxis.dev,resources=remediationplans,verbs=get;list;watch

// Reconcile writes the phase an incident has factually reached. Nothing here
// acts on the cluster; status is derived entirely from what exists, so the
// reconcile is idempotent and a settled incident costs zero writes.
func (r *IncidentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	incident := &praxisv1alpha1.Incident{}
	if err := r.Get(ctx, req.NamespacedName, incident); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	before := incident.Status.DeepCopy()

	phase := incident.Status.Phase
	if phase == "" {
		phase = praxisv1alpha1.IncidentPhaseDetected
	}
	if phase == praxisv1alpha1.IncidentPhaseDetected {
		referenced, err := r.referencedByAnyPlan(ctx, incident)
		if err != nil {
			return ctrl.Result{}, err
		}
		if referenced {
			phase = praxisv1alpha1.IncidentPhaseRemediating
		}
	}

	incident.Status.Phase = phase
	incident.Status.ObservedGeneration = incident.Generation
	if !apiequality.Semantic.DeepEqual(before, &incident.Status) {
		if err := r.Status().Update(ctx, incident); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// referencedByAnyPlan reports whether at least one RemediationPlan in the
// incident's namespace references this incarnation of it. A plan pinned to
// a different UID references a previous incarnation, not this one.
func (r *IncidentReconciler) referencedByAnyPlan(
	ctx context.Context, incident *praxisv1alpha1.Incident,
) (bool, error) {
	plans := &praxisv1alpha1.RemediationPlanList{}
	if err := r.List(ctx, plans, client.InNamespace(incident.Namespace)); err != nil {
		return false, err
	}
	for i := range plans.Items {
		ref := plans.Items[i].Spec.IncidentRef
		if ref.Name != incident.Name {
			continue
		}
		if ref.UID != "" && ref.UID != incident.UID {
			continue
		}
		return true, nil
	}
	return false, nil
}

// mapPlanToIncident requeues the incident a plan references, so the plan
// that flips an incident to Remediating triggers that flip by event, not by
// polling.
func mapPlanToIncident(_ context.Context, obj client.Object) []reconcile.Request {
	plan, ok := obj.(*praxisv1alpha1.RemediationPlan)
	if !ok || plan.Spec.IncidentRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      plan.Spec.IncidentRef.Name,
	}}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *IncidentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&praxisv1alpha1.Incident{}).
		Watches(&praxisv1alpha1.RemediationPlan{}, handler.EnqueueRequestsFromMapFunc(mapPlanToIncident)).
		Named("incident").
		Complete(r)
}
