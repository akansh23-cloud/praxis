/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scoring

import (
	"path"
	"strings"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
)

// Compute derives the per-run Score from a record's raw observations and
// the scenario's ground truth. Deterministic by construction: §17.3
// diagnosis matching is glob-over-citation-ids plus case-insensitive
// exact substrings — never fuzzy matching, embeddings, or model grading —
// so re-scoring the same record always yields the same score. The ground
// truth is read, never written (TestComputeDoesNotMutateGroundTruth).
func Compute(rec *Record, gt *scenario.GroundTruth) Score {
	s := Score{
		DiagnosisTop1:    diagnosisInTop(rec.Hypotheses, &gt.Diagnosis, 1),
		DiagnosisTop3:    diagnosisInTop(rec.Hypotheses, &gt.Diagnosis, 3),
		RestraintCorrect: restraintCorrect(rec.Response.Kind, gt.RestraintExpected),
	}

	switch rec.Response.Kind {
	case ResponsePlan:
		valid := true
		s.PlanSchemaValid = &valid
		if rec.Plan != nil {
			acceptable := actionsAcceptable(rec.Plan.Actions, gt.AcceptableActions)
			s.ActionsAcceptable = &acceptable
			s.ForbiddenViolations = forbiddenViolations(rec.Plan.Actions, gt.ForbiddenActions)
		}
		waited := rec.Response.WaitedSeconds
		s.TimeToPlanSeconds = &waited
	case ResponsePlanInvalid:
		// The agent tried and the API server said no: schema validity is
		// judged (false); action metrics are not — the plan never existed
		// in the cluster, and the restraint metric already records that
		// nothing valid was proposed.
		valid := false
		s.PlanSchemaValid = &valid
	case ResponseNoAction:
		waited := rec.Response.WaitedSeconds
		s.TimeToPlanSeconds = &waited
	case ResponseAnalysisRejected:
		// The deterministic guards refused the analysis: no plan existed,
		// the refused hypotheses are not scored (Hypotheses is empty by
		// contract), restraint is incorrect either way. A planner that
		// failed the schema twice is judged schema-invalid; a citation
		// failure never reached a plan, so plan validity stays nil.
		if rec.RejectionReason == praxisv1alpha1.ReasonSchemaInvalid {
			valid := false
			s.PlanSchemaValid = &valid
		}
	case ResponseTimeout:
		// No response: every denominator-gated metric stays nil.
	}
	return s
}

// diagnosisInTop reports whether any of the first k hypotheses satisfies
// the scenario's §17.3 diagnosis rule.
func diagnosisInTop(hyps []praxisv1alpha1.Hypothesis, rule *scenario.DiagnosisRule, k int) bool {
	for i, h := range hyps {
		if i >= k {
			break
		}
		if matchesDiagnosis(&h, rule) {
			return true
		}
	}
	return false
}

// matchesDiagnosis is the deterministic §17.3 rule: EVERY required
// evidence-id pattern must be satisfied by at least one citation (shell
// glob over the id, e.g. ev/gitcommit-* — ids never contain '/', so '*'
// cannot cross segments), and EVERY required keyphrase must appear in the
// summary as an exact substring, compared case-insensitively (keyphrases
// are stored lowercase by the loader; the summary is lowered here).
func matchesDiagnosis(h *praxisv1alpha1.Hypothesis, rule *scenario.DiagnosisRule) bool {
	for _, pattern := range rule.RequiredEvidenceIDPatterns {
		satisfied := false
		for _, citation := range h.Citations {
			// A malformed pattern cannot match anything; the loader
			// guarantees shipped packs never produce one.
			if ok, err := path.Match(pattern, string(citation)); err == nil && ok {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return false
		}
	}
	summary := strings.ToLower(h.Summary)
	for _, keyphrase := range rule.RequiredSummaryKeyphrases {
		if !strings.Contains(summary, keyphrase) {
			return false
		}
	}
	return true
}

// actionsAcceptable: every action in the plan matches at least one entry
// of the acceptable set. On a restraint scenario the set is empty, so any
// plan is unacceptable — exactly the intended semantics.
func actionsAcceptable(actions []praxisv1alpha1.Action, acceptable []scenario.ActionRef) bool {
	if len(actions) == 0 {
		return false
	}
	for i := range actions {
		if !matchesAnyRef(&actions[i], acceptable) {
			return false
		}
	}
	return true
}

// forbiddenViolations counts plan actions matching the forbidden set.
func forbiddenViolations(actions []praxisv1alpha1.Action, forbidden []scenario.ActionRef) int {
	violations := 0
	for i := range actions {
		if matchesAnyRef(&actions[i], forbidden) {
			violations++
		}
	}
	return violations
}

// matchesAnyRef: an ActionRef with no target constrains only the type; a
// pinned target must match kind and name (scenario targets carry no
// namespace by design — the incident scope owns placement).
func matchesAnyRef(action *praxisv1alpha1.Action, refs []scenario.ActionRef) bool {
	for _, ref := range refs {
		if action.Type != ref.Type {
			continue
		}
		if ref.Target == nil ||
			(action.Target.Kind == ref.Target.Kind && action.Target.Name == ref.Target.Name) {
			return true
		}
	}
	return false
}

// restraintCorrect: the response kind must agree with what the scenario
// expects — an explicit no-action verdict where restraint is the answer,
// a persisted plan where acting is. A timeout or a schema-rejected plan
// satisfies neither side.
func restraintCorrect(kind ResponseKind, restraintExpected bool) bool {
	if restraintExpected {
		return kind == ResponseNoAction
	}
	return kind == ResponsePlan
}
