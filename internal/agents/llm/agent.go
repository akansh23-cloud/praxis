/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/agents"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	llmapi "github.com/akansh23-cloud/praxis/internal/llm"
	"github.com/akansh23-cloud/praxis/internal/planschema"
	"github.com/akansh23-cloud/praxis/internal/validate"
)

// Agent is the model-backed implementation of agents.Agent. It is safe
// for one incident at a time: Analyze then Plan, the way the seam is
// driven; usage and plan annotations describe the last incident worked.
type Agent struct {
	client llmapi.Client

	mu          sync.Mutex
	usage       agents.Usage
	annotations map[string]string
}

var (
	_ agents.Agent      = (*Agent)(nil)
	_ agents.Metered    = (*Agent)(nil)
	_ agents.Attributed = (*Agent)(nil)
)

// New returns an agent over the configured model client.
func New(client llmapi.Client) *Agent {
	return &Agent{client: client}
}

// The bounds of the hypothesis contract, from the CRD's Hypothesis type;
// enforced here so a malformed analysis is refused before any plan cites
// it. maxHypotheses is this agent's own choice (the CRD holds one).
const (
	maxHypotheses   = 5
	maxSummaryBytes = 1024
	maxCitations    = 64
)

// hypothesesOutput is the shape hypotheses.schema.json requests.
type hypothesesOutput struct {
	Hypotheses []praxisv1alpha1.Hypothesis `json:"hypotheses"`
}

// Analyze asks the model for ranked hypotheses over the delimited data,
// then refuses, deterministically, anything that is not a well-formed
// hypothesis citing only ids the bundle holds.
func (a *Agent) Analyze(ctx context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle) (agents.Hypotheses, error) {
	if err := checkInputs(incident, bundle); err != nil {
		return nil, err
	}
	a.reset()
	user, err := a.userMessage(incident, bundle, nil, "")
	if err != nil {
		return nil, err
	}
	schema := planschema.HypothesesSchema()

	var out hypothesesOutput
	var violations []string
	for attempt := 1; attempt <= 2; attempt++ {
		raw, err := a.complete(ctx, hypothesisSystem, user, schema)
		if err != nil {
			return nil, err
		}
		out = hypothesesOutput{}
		if violations = decodeHypotheses(raw, &out); len(violations) == 0 {
			break
		}
		if attempt == 2 {
			return nil, &agents.SchemaError{Attempts: attempt, Violations: violations}
		}
		if user, err = a.userMessage(incident, bundle, nil, strings.Join(violations, "\n")); err != nil {
			return nil, err
		}
	}

	hyps := agents.Hypotheses(out.Hypotheses)
	var unresolved []praxisv1alpha1.EvidenceID
	for i := range hyps {
		for _, id := range validate.UnresolvedCitations(hyps[i], bundle) {
			if !slices.Contains(unresolved, id) {
				unresolved = append(unresolved, id)
			}
		}
	}
	if len(unresolved) > 0 {
		return nil, &agents.CitationError{Unresolved: unresolved, Refused: hyps}
	}
	// Rank: confidence descending, the model's order as the stable tiebreak
	// (ADR-003: position, not score, is what the benchmark judges).
	slices.SortStableFunc(hyps, func(a, b praxisv1alpha1.Hypothesis) int {
		return cmp.Compare(b.ConfidencePercent, a.ConfidencePercent)
	})
	return hyps, nil
}

// decodeHypotheses parses the model's output and returns every violation
// of the hypothesis contract, empty when it is well-formed.
func decodeHypotheses(raw json.RawMessage, out *hypothesesOutput) []string {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return []string{"output is not the requested JSON shape: " + err.Error()}
	}
	var violations []string
	if len(out.Hypotheses) > maxHypotheses {
		violations = append(violations, fmt.Sprintf("hypotheses: %d returned, at most %d allowed", len(out.Hypotheses), maxHypotheses))
	}
	for i, h := range out.Hypotheses {
		at := fmt.Sprintf("hypotheses[%d]", i)
		if strings.TrimSpace(h.Summary) == "" {
			violations = append(violations, at+".summary: must not be empty")
		}
		if len(h.Summary) > maxSummaryBytes {
			violations = append(violations, fmt.Sprintf("%s.summary: %d bytes, at most %d allowed", at, len(h.Summary), maxSummaryBytes))
		}
		if h.ConfidencePercent < 0 || h.ConfidencePercent > 100 {
			violations = append(violations, fmt.Sprintf("%s.confidencePercent: %d is outside 0..100", at, h.ConfidencePercent))
		}
		if len(h.Citations) == 0 {
			violations = append(violations, at+".citations: at least one evidence id is required")
		}
		if len(h.Citations) > maxCitations {
			violations = append(violations, fmt.Sprintf("%s.citations: %d ids, at most %d allowed", at, len(h.Citations), maxCitations))
		}
	}
	return violations
}

// plannerOutput is the shape planner.schema.json requests: a verdict, an
// optional reason, and the model-owned slice of a spec.
type plannerOutput struct {
	Verdict        string      `json:"verdict"`
	NoActionReason string      `json:"noActionReason"`
	Plan           *planFields `json:"plan"`
}

type planFields struct {
	Actions      []praxisv1alpha1.Action         `json:"actions"`
	Verification praxisv1alpha1.VerificationSpec `json:"verification"`
	Rollback     praxisv1alpha1.RollbackSpec     `json:"rollback"`
}

// Plan asks the model for a plan or a no-action verdict over the same
// delimited data plus the validated hypotheses, fills the code-owned
// fields, validates the whole spec against the CRD (schema + CEL) and
// re-checks citations; one retry on violation, then agents.SchemaError.
func (a *Agent) Plan(ctx context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle,
	hypotheses agents.Hypotheses,
) (*praxisv1alpha1.RemediationPlanSpec, agents.NoAction, error) {
	if err := checkInputs(incident, bundle); err != nil {
		return nil, agents.NoAction{}, err
	}
	if len(hypotheses) == 0 {
		// No explanation, no action: the agent will not act on a guess.
		return nil, agents.NoAction{Proposed: true,
			Reason: "llm agent: the evidence supported no hypothesis, so no remediation is proposed"}, nil
	}
	// The plan's hypothesis is the validated top hypothesis — code sets it,
	// and it is checked again here so a caller-supplied list cannot smuggle
	// an unresolved citation past the analysis step.
	if unresolved := validate.UnresolvedCitations(hypotheses[0], bundle); len(unresolved) > 0 {
		return nil, agents.NoAction{}, &agents.CitationError{Unresolved: unresolved, Refused: hypotheses}
	}
	bundleHash, err := bundle.Hash()
	if err != nil {
		return nil, agents.NoAction{}, err
	}

	schema := planschema.PlannerSchema()
	user, err := a.userMessage(incident, bundle, hypotheses, "")
	if err != nil {
		return nil, agents.NoAction{}, err
	}
	var violations []string
	for attempt := 1; attempt <= 2; attempt++ {
		raw, err := a.complete(ctx, plannerSystem, user, schema)
		if err != nil {
			return nil, agents.NoAction{}, err
		}
		spec, noAction, problems := a.decidePlan(ctx, raw, incident, bundle, bundleHash, hypotheses[0])
		if len(problems) == 0 {
			if noAction.Proposed {
				return nil, noAction, nil
			}
			promptDigest, err := promptHash(a.client.Provider().Name, a.client.Provider().Model, plannerSystem, user, schema)
			if err != nil {
				return nil, agents.NoAction{}, err
			}
			a.mu.Lock()
			a.annotations = map[string]string{
				praxisv1alpha1.AnnotationPromptHash: promptDigest,
				praxisv1alpha1.AnnotationModel:      a.client.Provider().String(),
			}
			a.mu.Unlock()
			return spec, agents.NoAction{}, nil
		}
		violations = problems
		if attempt == 2 {
			return nil, agents.NoAction{}, &agents.SchemaError{Attempts: attempt, Violations: violations}
		}
		if user, err = a.userMessage(incident, bundle, hypotheses, strings.Join(violations, "\n")); err != nil {
			return nil, agents.NoAction{}, err
		}
	}
	return nil, agents.NoAction{}, errors.New("llm agent: unreachable planner state")
}

// decidePlan turns one planner output into a verdict: a validated spec,
// a no-action verdict, or the list of violations that refuse it.
func (a *Agent) decidePlan(ctx context.Context, raw json.RawMessage, incident *praxisv1alpha1.Incident,
	bundle *evidence.Bundle, bundleHash string, top praxisv1alpha1.Hypothesis,
) (*praxisv1alpha1.RemediationPlanSpec, agents.NoAction, []string) {
	var out plannerOutput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, agents.NoAction{}, []string{"output is not the requested JSON shape: " + err.Error()}
	}
	switch out.Verdict {
	case "no-action":
		reason := strings.TrimSpace(Sanitize(out.NoActionReason))
		if reason == "" {
			return nil, agents.NoAction{}, []string{"noActionReason: a no-action verdict needs a one-sentence reason"}
		}
		if len(reason) > maxSummaryBytes {
			reason = reason[:maxSummaryBytes]
		}
		return nil, agents.NoAction{Proposed: true, Reason: reason}, nil
	case "plan":
		if out.Plan == nil {
			return nil, agents.NoAction{}, []string{"plan: a plan verdict needs a plan"}
		}
	default:
		return nil, agents.NoAction{}, []string{fmt.Sprintf("verdict: %q is neither \"plan\" nor \"no-action\"", out.Verdict)}
	}

	spec := &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef:        praxisv1alpha1.IncidentRef{Name: incident.Name, UID: incident.UID},
		EvidenceBundleHash: bundleHash,
		Hypothesis:         top,
		Actions:            out.Plan.Actions,
		Verification:       out.Plan.Verification,
		Rollback:           out.Plan.Rollback,
	}
	var problems []string
	if err := planschema.ValidateSpec(ctx, spec); err != nil {
		var verr *planschema.ValidationError
		if errors.As(err, &verr) {
			problems = append(problems, verr.Violations...)
		} else {
			problems = append(problems, err.Error())
		}
	}
	for _, act := range spec.Actions {
		if act.Target.Kind != "Node" && !slices.Contains(incident.Spec.Scope.Namespaces, act.Target.Namespace) {
			problems = append(problems, fmt.Sprintf("actions: target namespace %q is outside the incident scope %v",
				act.Target.Namespace, incident.Spec.Scope.Namespaces))
		}
	}
	if unresolved := validate.UnresolvedCitations(spec.Hypothesis, bundle); len(unresolved) > 0 {
		problems = append(problems, validate.FormatUnresolved(unresolved, len(spec.Hypothesis.Citations), len(bundle.Items)))
	}
	if len(problems) > 0 {
		return nil, agents.NoAction{}, problems
	}
	return spec, agents.NoAction{}, nil
}

// userMessage renders the delimited data: incident, bundle, and — for the
// planner — the validated hypotheses; a retry carries the refusal.
func (a *Agent) userMessage(incident *praxisv1alpha1.Incident, bundle *evidence.Bundle,
	hyps agents.Hypotheses, feedback string,
) (string, error) {
	inc, err := renderIncident(incident)
	if err != nil {
		return "", fmt.Errorf("render incident: %w", err)
	}
	ev, err := renderBundle(bundle)
	if err != nil {
		return "", fmt.Errorf("render evidence: %w", err)
	}
	var b strings.Builder
	b.WriteString(dataSection(incidentOpen, string(inc), incidentClose))
	b.WriteString(dataSection(evidenceOpen, string(ev), evidenceClose))
	if hyps != nil {
		h, err := renderHypotheses(hyps)
		if err != nil {
			return "", fmt.Errorf("render hypotheses: %w", err)
		}
		b.WriteString(dataSection(hypothesesOpen, string(h), hypothesesEnd))
	}
	if feedback != "" {
		b.WriteString(dataSection(feedbackOpen, Sanitize(feedback), feedbackClose))
	}
	return b.String(), nil
}

// complete makes one model call and accounts for it.
func (a *Agent) complete(ctx context.Context, system, user string, schema []byte) (json.RawMessage, error) {
	raw, usage, err := a.client.CompleteStructured(ctx, system, user, schema)
	a.mu.Lock()
	a.usage.Calls++
	a.usage.InputTokens += usage.InputTokens
	a.usage.OutputTokens += usage.OutputTokens
	a.usage.CostUSD += usage.CostUSD
	a.usage.CostKnown = a.usage.CostKnown && usage.CostKnown
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("llm agent: %w", err)
	}
	return raw, nil
}

func (a *Agent) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.client.Provider()
	a.usage = agents.Usage{Provider: p.Name, Model: p.Model, CostKnown: true}
	a.annotations = nil
}

// Usage implements agents.Metered.
func (a *Agent) Usage() agents.Usage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

// PlanAnnotations implements agents.Attributed.
func (a *Agent) PlanAnnotations() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]string, len(a.annotations))
	maps.Copy(out, a.annotations)
	return out
}

func checkInputs(incident *praxisv1alpha1.Incident, bundle *evidence.Bundle) error {
	if incident == nil {
		return errors.New("llm agent: incident is required")
	}
	if bundle == nil {
		return errors.New("llm agent: evidence bundle is required (it may be empty, not absent)")
	}
	return nil
}
