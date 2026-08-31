/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package agentrun drives an Agent through the seam of LLD §5 against one
// filed Incident: gather observations into an evidence bundle, sanitize
// the Incident, Analyze, Plan, then persist the verdict (create the
// RemediationPlan CR, or set the praxis.dev/no-action-proposed
// annotation) so the runner observes the response from the cluster like
// it would from any real agent.
//
// This package is the firewall between the benchmark's answer keys and
// the agents it grades. It deliberately imports NEITHER the scenario
// package (identity, groundTruth) NOR the scoring package: what a
// scenario is called and what the correct answer would be cannot reach an
// Agent through here even by accident. TestAgentInputsAreScenarioBlind
// enforces the import boundary; the runner additionally checks the exact
// serialized agent inputs for identity leaks on every live run.
package agentrun

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/agents"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// Report is the referee's record of one seam invocation: the exact inputs
// the agent saw, everything it returned, and what was persisted. The
// scorer works from this plus the cluster's own copy of the plan.
type Report struct {
	// SanitizedIncident and Bundle are the agent's complete inputs,
	// exactly as passed. Kept so the runner can prove, per run, that no
	// benchmark identity leaked into them.
	SanitizedIncident *praxisv1alpha1.Incident
	Bundle            *evidence.Bundle

	BundleHash string
	Hypotheses agents.Hypotheses

	// PlanSpec is the spec the agent returned (nil when it proposed no
	// action); PlanName is the CR that was created from it, empty when
	// the API server refused it — then PlanCreateError holds the server's
	// schema/CEL rejection verbatim.
	PlanSpec        *praxisv1alpha1.RemediationPlanSpec
	PlanName        string
	PlanCreateError string

	NoAction agents.NoAction

	GatherDuration  time.Duration
	AnalyzeDuration time.Duration
	PlanDuration    time.Duration
}

// Responded reports whether the agent's verdict landed in the cluster —
// a persisted plan or the no-action annotation.
func (r *Report) Responded() bool {
	return r.PlanName != "" || r.NoAction.Proposed
}

// Respond runs one agent against one Incident. The incident passed in is
// the runner's copy; the agent only ever sees the sanitized clone.
func Respond(ctx context.Context, c client.Client, ag agents.Agent,
	inc *praxisv1alpha1.Incident, logf func(string, ...any),
) (*Report, error) {
	report := &Report{}

	start := time.Now()
	bundle, err := gatherBundle(ctx, c, inc, start)
	if err != nil {
		return report, err
	}
	report.GatherDuration = time.Since(start)
	report.Bundle = bundle
	if report.BundleHash, err = bundle.Hash(); err != nil {
		return report, err
	}
	report.SanitizedIncident = SanitizeIncident(inc)
	logf("    gathered %d evidence item(s) (%s); bundle %s", len(bundle.Items), report.GatherDuration.Round(time.Millisecond), report.BundleHash[:19]+"…")

	start = time.Now()
	report.Hypotheses, err = ag.Analyze(ctx, report.SanitizedIncident, bundle)
	report.AnalyzeDuration = time.Since(start)
	if err != nil {
		return report, fmt.Errorf("agent Analyze: %w", err)
	}

	start = time.Now()
	report.PlanSpec, report.NoAction, err = ag.Plan(ctx, report.SanitizedIncident, bundle, report.Hypotheses)
	report.PlanDuration = time.Since(start)
	if err != nil {
		return report, fmt.Errorf("agent Plan: %w", err)
	}
	if err := checkSeamContract(report.PlanSpec, report.NoAction); err != nil {
		return report, err
	}

	if report.NoAction.Proposed {
		if err := annotateNoAction(ctx, c, inc, report.NoAction.Reason); err != nil {
			return report, err
		}
		logf("    agent proposed no action; annotated Incident %s/%s", inc.Namespace, inc.Name)
		return report, nil
	}
	return report, createPlan(ctx, c, inc, report, logf)
}

// checkSeamContract enforces the Agent.Plan contract: exactly one of a
// plan or an explicit no-action verdict.
func checkSeamContract(plan *praxisv1alpha1.RemediationPlanSpec, noAction agents.NoAction) error {
	switch {
	case plan == nil && !noAction.Proposed:
		return fmt.Errorf("agent seam contract violation: Plan returned neither a plan nor a no-action verdict")
	case plan != nil && noAction.Proposed:
		return fmt.Errorf("agent seam contract violation: Plan returned both a plan and a no-action verdict")
	}
	return nil
}

// createPlan files the agent's spec as a real RemediationPlan CR. A
// schema/CEL rejection by the API server is a RECORDED OUTCOME (the
// plan-schema-validity metric), not a harness failure; any other error is.
func createPlan(ctx context.Context, c client.Client, inc *praxisv1alpha1.Incident,
	report *Report, logf func(string, ...any),
) error {
	plan := &praxisv1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      inc.Name + "-plan",
			Namespace: inc.Namespace,
		},
		Spec: *report.PlanSpec,
	}
	if err := c.Create(ctx, plan); err != nil {
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			report.PlanCreateError = err.Error()
			logf("    agent's plan was REJECTED by the API server (recorded as schema-invalid): %v", err)
			return nil
		}
		return fmt.Errorf("create RemediationPlan for Incident %s/%s: %w", inc.Namespace, inc.Name, err)
	}
	report.PlanName = plan.Name
	logf("    agent proposed RemediationPlan %s/%s (%d action(s))", plan.Namespace, plan.Name, len(plan.Spec.Actions))
	return nil
}

// annotateNoAction records the restraint verdict on the Incident, the
// contract of api/v1alpha1.AnnotationNoActionProposed.
func annotateNoAction(ctx context.Context, c client.Client, inc *praxisv1alpha1.Incident, reason string) error {
	fresh := inc.DeepCopy()
	patch := client.MergeFrom(fresh.DeepCopy())
	if fresh.Annotations == nil {
		fresh.Annotations = map[string]string{}
	}
	fresh.Annotations[praxisv1alpha1.AnnotationNoActionProposed] = reason
	if err := c.Patch(ctx, fresh, patch); err != nil {
		return fmt.Errorf("annotate Incident %s/%s with %s: %w",
			inc.Namespace, inc.Name, praxisv1alpha1.AnnotationNoActionProposed, err)
	}
	return nil
}
