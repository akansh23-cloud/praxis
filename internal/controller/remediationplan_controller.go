/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/approve"
	"github.com/akansh23-cloud/praxis/internal/validate"
)

// RemediationPlanReconciler drives the plan phase machine of LLD §4.2. The
// Phase 1 slice covers Pending → Validating → AwaitingApproval plus terminal
// Rejected; the approval mechanism arrives in Session 1.2 and execution in
// Phase 5. Handlers read all their work from spec and status — never from
// memory — so a restarted controller resumes exactly where it left off.
type RemediationPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Citations and Scope are the LLD §5 validation seams. Phase 1 wires
	// the stubs from internal/validate, whose StubbedInPhase1 reason is
	// copied verbatim onto the plan's conditions.
	Citations validate.CitationValidator
	Scope     validate.ScopeChecker
}

// +kubebuilder:rbac:groups=praxis.dev,resources=remediationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.dev,resources=remediationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.dev,resources=remediationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=praxis.dev,resources=incidents,verbs=get;list;watch

// Reconcile advances a RemediationPlan one step through the LLD §4.2 machine.
// It stays a thin switch: each per-phase handler returns the next phase and
// an optional requeue delay, and exactly one status write happens per
// reconcile — and only when something actually changed, so settled and
// terminal objects are strict no-ops.
func (r *RemediationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	plan := &praxisv1alpha1.RemediationPlan{}
	if err := r.Get(ctx, req.NamespacedName, plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Terminal plans are settled history (LLD §4.2): not even a status write.
	if isTerminalPlanPhase(plan.Status.Phase) {
		return ctrl.Result{}, nil
	}

	before := plan.Status.DeepCopy()

	var next praxisv1alpha1.PlanPhase
	var requeueAfter time.Duration
	var err error
	switch plan.Status.Phase {
	case "":
		// Pickup: a just-created plan enters the machine at Pending, the
		// initial state of LLD §4.2, so the whole walk is observable.
		next = praxisv1alpha1.PlanPhasePending
	case praxisv1alpha1.PlanPhasePending:
		next, requeueAfter, err = r.handlePending(ctx, plan)
	case praxisv1alpha1.PlanPhaseValidating:
		next, requeueAfter, err = r.handleValidating(ctx, plan)
	case praxisv1alpha1.PlanPhaseAwaitingApproval:
		next, requeueAfter, err = r.handleAwaitingApproval(ctx, plan)
	default:
		// Executing, Verifying and RollingBack are unreachable for the
		// Phase 1 machine; leave such objects alone rather than guess.
		log.Info("Ignored RemediationPlan in phase outside the Phase 1 machine", "phase", plan.Status.Phase)
		return ctrl.Result{}, nil
	}
	if err != nil {
		// Transient trouble (API hiccups, seam machinery): no phase change,
		// controller-runtime retries with backoff.
		return ctrl.Result{}, err
	}

	plan.Status.Phase = next
	plan.Status.ObservedGeneration = plan.Generation
	if !apiequality.Semantic.DeepEqual(before, &plan.Status) {
		if err := r.Status().Update(ctx, plan); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// handlePending seeds the four Phase 1 conditions as Unknown/NotYetEvaluated
// and moves on to Validating. Seeding first means a later rejection leaves
// an honest trail: every check that never ran still says so.
//
//nolint:unparam // handlers share the (next, requeueAfter, error) shape of LLD §4.3.
func (r *RemediationPlanReconciler) handlePending(
	_ context.Context, plan *praxisv1alpha1.RemediationPlan,
) (praxisv1alpha1.PlanPhase, time.Duration, error) {
	for _, condType := range []string{
		praxisv1alpha1.ConditionEvidenceValid,
		praxisv1alpha1.ConditionCitationsResolved,
		praxisv1alpha1.ConditionScopeValid,
		praxisv1alpha1.ConditionApproved,
	} {
		setPlanCondition(plan, condType, metav1.ConditionUnknown,
			praxisv1alpha1.ReasonNotYetEvaluated, "Validation has not evaluated this check yet")
	}
	return praxisv1alpha1.PlanPhaseValidating, 0, nil
}

// handleValidating runs the Phase 1 checks cheapest-first per LLD §4.2:
// evidence hash, then citations, then scope. The first failure rejects the
// plan and the later checks never run — their conditions keep
// NotYetEvaluated. Risk, policy, RBAC and dry-run join this chain in
// Phase 4.
//
//nolint:unparam // handlers share the (next, requeueAfter, error) shape of LLD §4.3.
func (r *RemediationPlanReconciler) handleValidating(
	ctx context.Context, plan *praxisv1alpha1.RemediationPlan,
) (praxisv1alpha1.PlanPhase, time.Duration, error) {
	incident := &praxisv1alpha1.Incident{}
	err := r.Get(ctx, types.NamespacedName{Namespace: plan.Namespace, Name: plan.Spec.IncidentRef.Name}, incident)
	switch {
	case apierrors.IsNotFound(err):
		setPlanCondition(plan, praxisv1alpha1.ConditionEvidenceValid, metav1.ConditionFalse,
			praxisv1alpha1.ReasonEvidenceMismatch,
			fmt.Sprintf("Referenced Incident %q not found in namespace %q", plan.Spec.IncidentRef.Name, plan.Namespace))
		return praxisv1alpha1.PlanPhaseRejected, 0, nil
	case err != nil:
		return plan.Status.Phase, 0, err
	}

	if uid := plan.Spec.IncidentRef.UID; uid != "" && uid != incident.UID {
		setPlanCondition(plan, praxisv1alpha1.ConditionEvidenceValid, metav1.ConditionFalse,
			praxisv1alpha1.ReasonEvidenceMismatch,
			fmt.Sprintf("Incident %q exists but its UID %s is not the referenced %s; "+
				"the incarnation this plan was built for is gone", incident.Name, incident.UID, uid))
		return praxisv1alpha1.PlanPhaseRejected, 0, nil
	}

	if incident.Status.EvidenceBundleHash != plan.Spec.EvidenceBundleHash {
		setPlanCondition(plan, praxisv1alpha1.ConditionEvidenceValid, metav1.ConditionFalse,
			praxisv1alpha1.ReasonEvidenceMismatch,
			fmt.Sprintf("Plan cites evidence bundle %s but Incident %q has %s",
				plan.Spec.EvidenceBundleHash, incident.Name, describeHash(incident.Status.EvidenceBundleHash)))
		return praxisv1alpha1.PlanPhaseRejected, 0, nil
	}
	setPlanCondition(plan, praxisv1alpha1.ConditionEvidenceValid, metav1.ConditionTrue,
		praxisv1alpha1.ReasonEvidenceHashMatches,
		"spec.evidenceBundleHash matches the referenced Incident's status.evidenceBundleHash")

	verdict, err := r.Citations.Validate(ctx, plan.Spec.Hypothesis, plan.Spec.EvidenceBundleHash)
	if err != nil {
		return plan.Status.Phase, 0, err
	}
	setPlanCondition(plan, praxisv1alpha1.ConditionCitationsResolved,
		conditionStatusFor(verdict.Passed), verdict.Reason, verdict.Message)
	if !verdict.Passed {
		return praxisv1alpha1.PlanPhaseRejected, 0, nil
	}

	verdict, err = r.Scope.Check(ctx, plan.Spec.Actions, incident.Spec.Scope)
	if err != nil {
		return plan.Status.Phase, 0, err
	}
	setPlanCondition(plan, praxisv1alpha1.ConditionScopeValid,
		conditionStatusFor(verdict.Passed), verdict.Reason, verdict.Message)
	if !verdict.Passed {
		return praxisv1alpha1.PlanPhaseRejected, 0, nil
	}

	// Every check passed. All plans require human approval until the
	// autonomy ladder arrives in Phase 6 (the L3 auto-execute row of LLD
	// §4.2 has no grants to consult yet). Entering AwaitingApproval binds
	// the approval to the exact plan and evidence (LLD §8): boundTo is the
	// only value the praxis.dev/approve annotation will be accepted with.
	boundTo, err := approve.BoundTo(plan.Spec.EvidenceBundleHash, &plan.Spec)
	if err != nil {
		return plan.Status.Phase, 0, err
	}
	if plan.Status.Approval == nil {
		plan.Status.Approval = &praxisv1alpha1.ApprovalStatus{}
	}
	plan.Status.Approval.Required = true
	plan.Status.Approval.BoundTo = boundTo
	setPlanCondition(plan, praxisv1alpha1.ConditionApproved, metav1.ConditionFalse,
		praxisv1alpha1.ReasonAwaitingApproval, "Plan passed validation and requires human approval before execution")
	return praxisv1alpha1.PlanPhaseAwaitingApproval, 0, nil
}

// handleAwaitingApproval verifies the Phase 1 approval mechanism. The human
// approves by writing annotation praxis.dev/approve; its value, the stored
// boundTo, and a binding hash recomputed FRESH from the live spec and the
// live Incident status must all agree, or the plan is Rejected with reason
// ApprovalInvalidated. Recomputing rather than trusting the stored value is
// the LLD §8 discipline: boundTo records what was true at binding time, the
// recomputation asks whether it is still true at decision time.
//
// On a verified approval the phase STAYS AwaitingApproval: the transition
// to Executing arrives with the Phase 5 executor, and until then an
// approved plan parks here with condition Approved=True.
//
//nolint:unparam // handlers share the (next, requeueAfter, error) shape of LLD §4.3.
func (r *RemediationPlanReconciler) handleAwaitingApproval(
	ctx context.Context, plan *praxisv1alpha1.RemediationPlan,
) (praxisv1alpha1.PlanPhase, time.Duration, error) {
	// A verified approval is settled history; re-reconciles are no-ops
	// until the executor picks the plan up in Phase 5.
	if meta.IsStatusConditionTrue(plan.Status.Conditions, praxisv1alpha1.ConditionApproved) {
		return plan.Status.Phase, 0, nil
	}

	annotationValue, ok := plan.Annotations[praxisv1alpha1.AnnotationApprove]
	if !ok {
		// Still waiting for the human. The annotation update triggers the
		// next reconcile through the watch — no polling, no timers.
		return plan.Status.Phase, 0, nil
	}

	incident := &praxisv1alpha1.Incident{}
	err := r.Get(ctx, types.NamespacedName{Namespace: plan.Namespace, Name: plan.Spec.IncidentRef.Name}, incident)
	switch {
	case apierrors.IsNotFound(err):
		return invalidateApproval(plan, fmt.Sprintf(
			"Referenced Incident %q is gone; the evidence this approval binds to cannot be re-verified",
			plan.Spec.IncidentRef.Name)), 0, nil
	case err != nil:
		return plan.Status.Phase, 0, err
	}
	if uid := plan.Spec.IncidentRef.UID; uid != "" && uid != incident.UID {
		return invalidateApproval(plan, fmt.Sprintf(
			"Incident %q is now incarnation %s, not the referenced %s; "+
				"the world this approval was granted in is gone", incident.Name, incident.UID, uid)), 0, nil
	}

	recomputed, err := approve.BoundTo(incident.Status.EvidenceBundleHash, &plan.Spec)
	if err != nil {
		return plan.Status.Phase, 0, err
	}

	storedBoundTo := ""
	if plan.Status.Approval != nil {
		storedBoundTo = plan.Status.Approval.BoundTo
	}
	if recomputed != storedBoundTo {
		return invalidateApproval(plan, fmt.Sprintf(
			"Binding hash recomputed from live state is %s but the plan was bound to %s; "+
				"the evidence or the plan changed after binding", recomputed, describeHash(storedBoundTo))), 0, nil
	}
	if annotationValue != recomputed {
		// The annotation value is deliberately not echoed back: telemetry
		// carries what the plan requires, not arbitrary input.
		return invalidateApproval(plan, fmt.Sprintf(
			"Annotation %s does not carry the binding hash %s this plan requires",
			praxisv1alpha1.AnnotationApprove, recomputed)), 0, nil
	}

	// All three values agree, so record the human's yes. Approval is
	// non-nil here: storedBoundTo just matched a never-empty recomputation.
	plan.Status.Approval.ApprovedBy = plan.Annotations[praxisv1alpha1.AnnotationApprovedBy]
	now := metav1.Now()
	plan.Status.Approval.ApprovedAt = &now
	setPlanCondition(plan, praxisv1alpha1.ConditionApproved, metav1.ConditionTrue,
		praxisv1alpha1.ReasonApprovedAwaitingExecutor,
		"Approval verified against the binding hash; execution arrives with the Phase 5 executor")
	return praxisv1alpha1.PlanPhaseAwaitingApproval, 0, nil
}

// invalidateApproval records the Approved=False verdict with reason
// ApprovalInvalidated and routes the plan to terminal Rejected. Nothing is
// granted on any verification failure — there is no partial approval.
func invalidateApproval(plan *praxisv1alpha1.RemediationPlan, message string) praxisv1alpha1.PlanPhase {
	setPlanCondition(plan, praxisv1alpha1.ConditionApproved, metav1.ConditionFalse,
		praxisv1alpha1.ReasonApprovalInvalidated, message)
	return praxisv1alpha1.PlanPhaseRejected
}

// setPlanCondition records one condition with the plan's generation stamped.
// meta.SetStatusCondition leaves the list untouched when nothing changed,
// which is what keeps repeated reconciles free of status writes.
func setPlanCondition(plan *praxisv1alpha1.RemediationPlan, condType string,
	status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: plan.Generation,
	})
}

func conditionStatusFor(passed bool) metav1.ConditionStatus {
	if passed {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// isTerminalPlanPhase reports whether the phase is one of LLD §4.2's four
// terminal states.
func isTerminalPlanPhase(phase praxisv1alpha1.PlanPhase) bool {
	switch phase {
	case praxisv1alpha1.PlanPhaseSucceeded,
		praxisv1alpha1.PlanPhaseFailed,
		praxisv1alpha1.PlanPhaseRolledBack,
		praxisv1alpha1.PlanPhaseRejected:
		return true
	default:
		return false
	}
}

// describeHash renders a possibly-unset hash for condition messages.
func describeHash(hash string) string {
	if hash == "" {
		return "no evidence bundle hash yet"
	}
	return hash
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemediationPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Citations == nil || r.Scope == nil {
		return errors.New("RemediationPlanReconciler needs a CitationValidator and a ScopeChecker; " +
			"wire the Phase 1 stubs from internal/validate")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&praxisv1alpha1.RemediationPlan{}).
		Named("remediationplan").
		Complete(r)
}
