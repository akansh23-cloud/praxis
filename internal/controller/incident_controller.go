/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/hash"
)

// IncidentReconciler drives the LLD §4.1 incident machine as far as
// Session 3.1 takes it: Detected → Collecting → Analyzed, with Analyzed
// reached only once a real evidence bundle has been assembled, persisted
// to its ConfigMap and its hash recorded on status — and Remediating once
// a RemediationPlan references an analyzed incident. Resolved and Closed
// belong to Phase 5+.
//
// Collection runs only while status.evidenceBundleHash is empty, exactly
// once per incident: evidence is a snapshot the analysis and every later
// approval hash are bound to, so a settled incident never re-collects and
// never churns. A hash that was already set through the status
// subresource (the Phase 1-era test and demo channel) is honoured, not
// overwritten — visible as reason EvidencePresupplied instead of
// EvidenceStored.
type IncidentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// APIReader reads without the manager cache. Evidence collection is
	// one-shot per incident, so informers over every pod, event and
	// workload in the cluster would cost memory for nothing.
	APIReader client.Reader

	// Collector assembles the §6 bundle. Its only cluster view is the
	// secretless evidence.Reader — the write methods below never reach it.
	Collector *evidence.Collector
}

// +kubebuilder:rbac:groups=praxis.dev,resources=incidents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.dev,resources=incidents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.dev,resources=incidents/finalizers,verbs=update
// +kubebuilder:rbac:groups=praxis.dev,resources=remediationplans,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods;events,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;replicasets;statefulsets;daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;update

// Reconcile advances the phase machine one honest step and writes status
// through the status subresource only when something changed. Collection
// failures are surfaced on the EvidenceCollected condition and returned
// as errors so controller-runtime retries with backoff — an incident is
// never advanced to Analyzed past a failure.
func (r *IncidentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	incident := &praxisv1alpha1.Incident{}
	if err := r.Get(ctx, req.NamespacedName, incident); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	before := incident.Status.DeepCopy()
	advanceErr := r.advance(ctx, incident)

	incident.Status.ObservedGeneration = incident.Generation
	if !apiequality.Semantic.DeepEqual(before, &incident.Status) {
		if err := r.Status().Update(ctx, incident); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, advanceErr
}

// advance mutates incident.Status one machine step. It returns an error
// only for retryable collection failures; the condition it sets first is
// persisted by the caller either way.
func (r *IncidentReconciler) advance(ctx context.Context, incident *praxisv1alpha1.Incident) error {
	phase := incident.Status.Phase
	if phase == "" {
		phase = praxisv1alpha1.IncidentPhaseDetected
	}

	switch phase {
	case praxisv1alpha1.IncidentPhaseDetected:
		if incident.Status.EvidenceBundleHash != "" {
			r.acceptPresuppliedHash(incident)
			phase = praxisv1alpha1.IncidentPhaseAnalyzed
			break
		}
		phase = praxisv1alpha1.IncidentPhaseCollecting
		setIncidentCondition(incident,
			metav1.ConditionFalse, praxisv1alpha1.ReasonCollectionInProgress,
			fmt.Sprintf("Assembling the evidence bundle for namespaces %s",
				strings.Join(incident.Spec.Scope.Namespaces, ", ")))

	case praxisv1alpha1.IncidentPhaseCollecting:
		if incident.Status.EvidenceBundleHash != "" {
			r.acceptPresuppliedHash(incident)
			phase = praxisv1alpha1.IncidentPhaseAnalyzed
			break
		}
		if err := r.collectAndPersist(ctx, incident); err != nil {
			setIncidentCondition(incident,
				metav1.ConditionFalse, praxisv1alpha1.ReasonCollectionFailed,
				fmt.Sprintf("Evidence collection failed: %v", err))
			incident.Status.Phase = phase
			return err
		}
		phase = praxisv1alpha1.IncidentPhaseAnalyzed
	}

	// Remediating follows Analyzed (LLD §4.1) — never Detected or
	// Collecting — and only for an incident whose evidence is recorded.
	if phase == praxisv1alpha1.IncidentPhaseAnalyzed && incident.Status.EvidenceBundleHash != "" {
		referenced, err := r.referencedByAnyPlan(ctx, incident)
		if err != nil {
			incident.Status.Phase = phase
			return err
		}
		if referenced {
			phase = praxisv1alpha1.IncidentPhaseRemediating
		}
	}

	incident.Status.Phase = phase
	return nil
}

// acceptPresuppliedHash honours an evidenceBundleHash that was already on
// status before the collector ran. Production RBAC reserves incident
// status writes for the manager; this channel exists for the Phase 1
// chainsaw fixtures and manual walkthroughs, and the distinct reason
// keeps it observable — never mistakable for a real collection.
func (r *IncidentReconciler) acceptPresuppliedHash(incident *praxisv1alpha1.Incident) {
	setIncidentCondition(incident,
		metav1.ConditionTrue, praxisv1alpha1.ReasonEvidencePresupplied,
		"status.evidenceBundleHash was already set; collection skipped and the recorded hash honoured")
}

// collectAndPersist produces the bundle and stores it before any status
// field is touched: ConfigMap first, then ref+hash — so Analyzed can only
// ever follow a persisted bundle. If a previous attempt persisted the
// ConfigMap but lost the status write, the stored bytes are adopted and
// re-hashed instead of collected again, keeping the reconcile idempotent.
func (r *IncidentReconciler) collectAndPersist(ctx context.Context, incident *praxisv1alpha1.Incident) error {
	logger := log.FromContext(ctx)
	name := evidence.BundleConfigMapName(incident.UID)
	key := types.NamespacedName{Namespace: incident.Namespace, Name: name}

	existing := &corev1.ConfigMap{}
	err := r.APIReader.Get(ctx, key, existing)
	switch {
	case err == nil:
		if !metav1.IsControlledBy(existing, incident) {
			return fmt.Errorf("configmap %s exists but is not owned by this incident incarnation", name)
		}
		raw, ok := existing.Data[evidence.BundleConfigMapKey]
		if !ok {
			return fmt.Errorf("configmap %s is missing key %s", name, evidence.BundleConfigMapKey)
		}
		r.recordBundle(incident, name, hash.SHA256Prefixed([]byte(raw)),
			"Adopted the previously persisted bundle")
		logger.Info("Adopted existing evidence bundle", "configMap", name)
		return nil

	case apierrors.IsNotFound(err):
		result, collectErr := r.Collector.Collect(ctx, incident)
		if collectErr != nil {
			return collectErr
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: incident.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":      "praxis",
					"app.kubernetes.io/component": "evidence-bundle",
					"praxis.dev/incident":         incident.Name,
				},
			},
			Data: map[string]string{evidence.BundleConfigMapKey: string(result.Raw)},
		}
		if err := controllerutil.SetControllerReference(incident, cm, r.Scheme); err != nil {
			return fmt.Errorf("set owner reference on %s: %w", name, err)
		}
		if err := r.Create(ctx, cm); err != nil {
			return fmt.Errorf("create configmap %s: %w", name, err)
		}
		message := fmt.Sprintf("Bundle %s persisted: %d items, %d bytes",
			name, len(result.Bundle.Items), len(result.Raw))
		if len(result.Notes) > 0 {
			message += " (" + strings.Join(result.Notes, "; ") + ")"
		}
		r.recordBundle(incident, name, result.Hash, message)
		logger.Info("Persisted evidence bundle",
			"configMap", name, "items", len(result.Bundle.Items), "bytes", len(result.Raw), "hash", result.Hash)
		return nil

	default:
		return fmt.Errorf("read configmap %s: %w", name, err)
	}
}

func (r *IncidentReconciler) recordBundle(incident *praxisv1alpha1.Incident, ref, bundleHash, message string) {
	incident.Status.EvidenceBundleRef = ref
	incident.Status.EvidenceBundleHash = bundleHash
	setIncidentCondition(incident,
		metav1.ConditionTrue, praxisv1alpha1.ReasonEvidenceStored, message)
}

// setIncidentCondition records the EvidenceCollected condition —
// Session 3.1's only incident condition — generation-stamped;
// meta.SetStatusCondition leaves the list untouched when nothing changed,
// which keeps settled reconciles free of status writes.
func setIncidentCondition(incident *praxisv1alpha1.Incident,
	status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&incident.Status.Conditions, metav1.Condition{
		Type:               praxisv1alpha1.ConditionEvidenceCollected,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: incident.Generation,
	})
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
