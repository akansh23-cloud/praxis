/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package agentrun drives an Agent through the seam of LLD §5 against one
// filed Incident: collect the evidence bundle through the REAL pipeline
// (internal/evidence: secretless Reader → collectors → assembler with
// caps, canonical bytes and redaction), sanitize the Incident, Analyze,
// re-check every citation deterministically, Plan, then persist the
// verdict (create the RemediationPlan CR, set the no-action annotation,
// or record a refused analysis on the Incident) so the runner observes the
// response from the cluster like it would from any real agent.
//
// Since Session 3.3 there is no benchmark-side evidence implementation at
// all: the Phase 2 events-only stand-in is gone, and the bundle an agent
// analyzes here is byte-for-byte what evidence.Collector.Collect returns
// — the same code, caps, ids, hash and scrubber the manager persists.
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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/agents"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/validate"
)

// Rejection records an analysis the deterministic guards refused before
// any plan was created (ADR-009): CitationInvalid or SchemaInvalid.
type Rejection struct {
	Reason string
	Detail string
}

// Report is the referee's record of one seam invocation: the exact inputs
// the agent saw, everything it returned, and what was persisted. The
// scorer works from this plus the cluster's own copy of the plan.
type Report struct {
	// SanitizedIncident and Bundle are the agent's complete inputs,
	// exactly as passed. Kept so the runner can prove, per run, that no
	// benchmark identity leaked into them.
	SanitizedIncident *praxisv1alpha1.Incident
	Bundle            *evidence.Bundle

	// BundleHash and BundleBytes describe the canonical bundle exactly as
	// the collector assembled it; CollectionNotes are its honest omissions.
	BundleHash      string
	BundleBytes     int
	CollectionNotes []string

	// Hypotheses are the agent's VALIDATED ranked output — every citation
	// resolves in Bundle. A refused analysis leaves this empty and puts the
	// refused hypotheses in RefusedHypotheses, for forensics only.
	Hypotheses        agents.Hypotheses
	RefusedHypotheses agents.Hypotheses

	// Rejection is set when the deterministic guards refused the analysis
	// before any plan was created; the Incident carries the same record
	// as praxis.dev/analysis-rejected.
	Rejection *Rejection

	// PlanSpec is the spec the agent returned (nil when it proposed no
	// action); PlanName is the CR that was created from it, empty when
	// the API server refused it — then PlanCreateError holds the server's
	// schema/CEL rejection verbatim.
	PlanSpec        *praxisv1alpha1.RemediationPlanSpec
	PlanName        string
	PlanCreateError string

	// PlanAnnotations are the audit annotations the agent attached to its
	// plan (prompt hash, model), as written onto the CR.
	PlanAnnotations map[string]string

	NoAction agents.NoAction

	// Usage is the agent's own accounting for this incident, when the
	// agent is metered (the LLM agent); nil otherwise.
	Usage *agents.Usage

	GatherDuration  time.Duration
	AnalyzeDuration time.Duration
	PlanDuration    time.Duration
}

// Responded reports whether the agent's verdict landed in the cluster —
// a persisted plan or the no-action annotation.
func (r *Report) Responded() bool {
	return r.PlanName != "" || r.NoAction.Proposed
}

// maxAnnotationBytes bounds the rejection annotation's value.
const maxAnnotationBytes = 2048

// Respond runs one agent against one Incident. The incident passed in is
// the runner's copy; the agent only ever sees the sanitized clone and the
// bundle the real collector assembled.
func Respond(ctx context.Context, c client.Client, collector *evidence.Collector, ag agents.Agent,
	inc *praxisv1alpha1.Incident, logf func(string, ...any),
) (*Report, error) {
	if collector == nil {
		return nil, errors.New("agentrun: an evidence.Collector is required; the benchmark has no evidence path of its own")
	}
	report := &Report{}

	start := time.Now()
	collected, err := collector.Collect(ctx, inc)
	if err != nil {
		return report, fmt.Errorf("collect evidence for Incident %s/%s: %w", inc.Namespace, inc.Name, err)
	}
	report.GatherDuration = time.Since(start)
	report.Bundle = collected.Bundle
	report.BundleHash = collected.Hash
	report.BundleBytes = len(collected.Raw)
	report.CollectionNotes = collected.Notes
	report.SanitizedIncident = SanitizeIncident(inc)
	logf("    collected %d evidence item(s), %d canonical bytes (%s); bundle %s…",
		len(collected.Bundle.Items), len(collected.Raw), report.GatherDuration.Round(time.Millisecond), collected.Hash[:19])
	for _, note := range collected.Notes {
		logf("      note: %s", note)
	}

	start = time.Now()
	hyps, err := ag.Analyze(ctx, report.SanitizedIncident, report.Bundle)
	report.AnalyzeDuration = time.Since(start)
	recordUsage(report, ag)
	if err != nil {
		if rejected, ok := asRejection(err); ok {
			return report, rejectAnalysis(ctx, c, inc, report, rejected, refusedOf(err), logf)
		}
		return report, fmt.Errorf("agent Analyze: %w", err)
	}
	// The harness re-checks what any agent returns: an agent that skipped
	// its own validation, or lied about it, is caught here.
	if rejected := unresolvedAcross(hyps, report.Bundle); rejected != nil {
		return report, rejectAnalysis(ctx, c, inc, report, *rejected, hyps, logf)
	}
	report.Hypotheses = hyps

	start = time.Now()
	report.PlanSpec, report.NoAction, err = ag.Plan(ctx, report.SanitizedIncident, report.Bundle, report.Hypotheses)
	report.PlanDuration = time.Since(start)
	recordUsage(report, ag)
	if err != nil {
		if rejected, ok := asRejection(err); ok {
			return report, rejectAnalysis(ctx, c, inc, report, rejected, refusedOf(err), logf)
		}
		return report, fmt.Errorf("agent Plan: %w", err)
	}
	if err := checkSeamContract(report.PlanSpec, report.NoAction); err != nil {
		return report, err
	}

	if report.NoAction.Proposed {
		if err := annotateIncident(ctx, c, inc, praxisv1alpha1.AnnotationNoActionProposed, report.NoAction.Reason); err != nil {
			return report, err
		}
		logf("    agent proposed no action; annotated Incident %s/%s", inc.Namespace, inc.Name)
		return report, nil
	}

	// A plan must be bound to the bundle that was analyzed, and its
	// hypothesis must resolve there too — code checks, not trust.
	if report.PlanSpec.EvidenceBundleHash != report.BundleHash {
		return report, fmt.Errorf("agent seam contract violation: the plan cites bundle %s but the analyzed bundle is %s",
			report.PlanSpec.EvidenceBundleHash, report.BundleHash)
	}
	if rejected := unresolvedAcross(agents.Hypotheses{report.PlanSpec.Hypothesis}, report.Bundle); rejected != nil {
		return report, rejectAnalysis(ctx, c, inc, report, *rejected, report.Hypotheses, logf)
	}
	if attributed, ok := ag.(agents.Attributed); ok {
		report.PlanAnnotations = attributed.PlanAnnotations()
	}
	return report, createPlan(ctx, c, inc, report, logf)
}

// asRejection classifies the first-class refusals of ADR-009.
func asRejection(err error) (Rejection, bool) {
	var cerr *agents.CitationError
	if errors.As(err, &cerr) {
		return Rejection{Reason: praxisv1alpha1.ReasonCitationInvalid, Detail: cerr.Error()}, true
	}
	var serr *agents.SchemaError
	if errors.As(err, &serr) {
		return Rejection{Reason: praxisv1alpha1.ReasonSchemaInvalid, Detail: serr.Error()}, true
	}
	return Rejection{}, false
}

func refusedOf(err error) agents.Hypotheses {
	var cerr *agents.CitationError
	if errors.As(err, &cerr) {
		return cerr.Refused
	}
	return nil
}

// unresolvedAcross runs the deterministic citation check over every
// hypothesis and renders one rejection if any citation fails.
func unresolvedAcross(hyps agents.Hypotheses, bundle *evidence.Bundle) *Rejection {
	var unresolved []praxisv1alpha1.EvidenceID
	cited := 0
	for i := range hyps {
		cited += len(hyps[i].Citations)
		for _, id := range validate.UnresolvedCitations(hyps[i], bundle) {
			if !slices.Contains(unresolved, id) {
				unresolved = append(unresolved, id)
			}
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	return &Rejection{
		Reason: praxisv1alpha1.ReasonCitationInvalid,
		Detail: praxisv1alpha1.ReasonCitationInvalid + ": " + validate.FormatUnresolved(unresolved, cited, len(bundle.Items)),
	}
}

// rejectAnalysis records a refused analysis: no plan is created, the
// Incident carries praxis.dev/analysis-rejected, the report carries the
// refused hypotheses for forensics only.
func rejectAnalysis(ctx context.Context, c client.Client, inc *praxisv1alpha1.Incident, report *Report,
	rejected Rejection, refused agents.Hypotheses, logf func(string, ...any),
) error {
	report.Rejection = &rejected
	report.RefusedHypotheses = refused
	report.Hypotheses = nil
	report.PlanSpec = nil
	value := rejected.Detail
	if !strings.HasPrefix(value, rejected.Reason+":") {
		value = rejected.Reason + ": " + value
	}
	if len(value) > maxAnnotationBytes {
		value = value[:maxAnnotationBytes] + "…"
	}
	if err := annotateIncident(ctx, c, inc, praxisv1alpha1.AnnotationAnalysisRejected, value); err != nil {
		return err
	}
	logf("    analysis REFUSED by deterministic validation (%s); no plan created; annotated Incident %s/%s",
		rejected.Reason, inc.Namespace, inc.Name)
	return nil
}

func recordUsage(report *Report, ag agents.Agent) {
	if metered, ok := ag.(agents.Metered); ok {
		u := metered.Usage()
		report.Usage = &u
	}
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

// createPlan files the agent's spec as a real RemediationPlan CR, carrying
// the agent's audit annotations. A schema/CEL rejection by the API server
// is a RECORDED OUTCOME (the plan-schema-validity metric), not a harness
// failure; any other error is.
func createPlan(ctx context.Context, c client.Client, inc *praxisv1alpha1.Incident,
	report *Report, logf func(string, ...any),
) error {
	plan := &praxisv1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:        inc.Name + "-plan",
			Namespace:   inc.Namespace,
			Annotations: report.PlanAnnotations,
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

// annotateIncident records a verdict or a refusal on the Incident — the
// contracts of api/v1alpha1.AnnotationNoActionProposed and
// AnnotationAnalysisRejected.
func annotateIncident(ctx context.Context, c client.Client, inc *praxisv1alpha1.Incident, key, value string) error {
	fresh := inc.DeepCopy()
	patch := client.MergeFrom(fresh.DeepCopy())
	if fresh.Annotations == nil {
		fresh.Annotations = map[string]string{}
	}
	fresh.Annotations[key] = value
	if err := c.Patch(ctx, fresh, patch); err != nil {
		return fmt.Errorf("annotate Incident %s/%s with %s: %w", inc.Namespace, inc.Name, key, err)
	}
	return nil
}
