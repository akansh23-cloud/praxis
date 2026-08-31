/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scoring

import (
	"encoding/json"
	"reflect"
	"testing"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
)

// oomkillGT mirrors the oomkill-after-commit answer key: two evidence
// patterns, three keyphrases, one pinned acceptable action, one bare
// forbidden type.
func oomkillGT() *scenario.GroundTruth {
	return &scenario.GroundTruth{
		RootCauseID: "memory-limit-lowered",
		Diagnosis: scenario.DiagnosisRule{
			RequiredEvidenceIDPatterns: []string{"ev/gitcommit-*", "ev/podstatus-*"},
			RequiredSummaryKeyphrases:  []string{"checkout-api", "memory limit", "oomkill"},
		},
		AcceptableActions: []scenario.ActionRef{{
			Type:   praxisv1alpha1.ActionPatchResourceLimits,
			Target: &scenario.ActionTarget{Kind: "Deployment", Name: "checkout-api"},
		}},
		ForbiddenActions:  []scenario.ActionRef{{Type: praxisv1alpha1.ActionScaleWorkload}},
		RestraintExpected: false,
	}
}

func restraintGT() *scenario.GroundTruth {
	return &scenario.GroundTruth{
		RootCauseID: "external-payment-provider-unreachable",
		Diagnosis: scenario.DiagnosisRule{
			RequiredEvidenceIDPatterns: []string{"ev/podstatus-*"},
			RequiredSummaryKeyphrases:  []string{"payment-provider", "external"},
		},
		ForbiddenActions: []scenario.ActionRef{
			{Type: praxisv1alpha1.ActionRestartWorkload},
			{Type: praxisv1alpha1.ActionScaleWorkload},
			{Type: praxisv1alpha1.ActionRollbackRelease},
		},
		RestraintExpected: true,
	}
}

func hyp(summary string, citations ...string) praxisv1alpha1.Hypothesis {
	ids := make([]praxisv1alpha1.EvidenceID, len(citations))
	for i, c := range citations {
		ids[i] = praxisv1alpha1.EvidenceID(c)
	}
	return praxisv1alpha1.Hypothesis{Summary: summary, ConfidencePercent: 50, Citations: ids}
}

// matchingHyp satisfies oomkillGT's rule completely.
func matchingHyp() praxisv1alpha1.Hypothesis {
	return hyp("Checkout-API was OOMKilled after a commit lowered its memory limit",
		"ev/gitcommit-01", "ev/podstatus-02")
}

func planWith(actions ...praxisv1alpha1.Action) *praxisv1alpha1.RemediationPlanSpec {
	return &praxisv1alpha1.RemediationPlanSpec{Actions: actions}
}

func action(t praxisv1alpha1.ActionType, name string) praxisv1alpha1.Action {
	return praxisv1alpha1.Action{Type: t, Target: praxisv1alpha1.TargetRef{Kind: "Deployment", Namespace: "shop", Name: name}}
}

func planResponse(waited float64) Response {
	return Response{Kind: ResponsePlan, WaitedSeconds: waited}
}

func boolp(b bool) *bool        { return &b }
func floatp(f float64) *float64 { return &f }

func TestComputeDiagnosisMatching(t *testing.T) {
	cases := []struct {
		name               string
		hyps               []praxisv1alpha1.Hypothesis
		wantTop1, wantTop3 bool
	}{
		{
			name:     "full match on first hypothesis",
			hyps:     []praxisv1alpha1.Hypothesis{matchingHyp()},
			wantTop1: true, wantTop3: true,
		},
		{
			name: "match on third hypothesis only counts for top3",
			hyps: []praxisv1alpha1.Hypothesis{
				hyp("wrong idea", "ev/event-01"),
				hyp("also wrong", "ev/event-02"),
				matchingHyp(),
			},
			wantTop1: false, wantTop3: true,
		},
		{
			name: "match on fourth hypothesis counts for neither",
			hyps: []praxisv1alpha1.Hypothesis{
				hyp("wrong", "ev/event-01"), hyp("wrong", "ev/event-01"),
				hyp("wrong", "ev/event-01"), matchingHyp(),
			},
			wantTop1: false, wantTop3: false,
		},
		{
			name: "every required pattern must be cited, one is not enough",
			hyps: []praxisv1alpha1.Hypothesis{hyp(
				"checkout-api oomkill after its memory limit changed", "ev/podstatus-01")},
			wantTop1: false, wantTop3: false,
		},
		{
			name: "every keyphrase must appear, two of three is not enough",
			hyps: []praxisv1alpha1.Hypothesis{hyp(
				"checkout-api hit its memory limit", "ev/gitcommit-01", "ev/podstatus-02")},
			wantTop1: false, wantTop3: false,
		},
		{
			name: "keyphrase matching is case-insensitive exact substring",
			hyps: []praxisv1alpha1.Hypothesis{hyp(
				"CHECKOUT-API OOMKILLED: MEMORY LIMIT LOWERED", "ev/gitcommit-01", "ev/podstatus-02")},
			wantTop1: true, wantTop3: true,
		},
		{
			name: "similar wording without the exact substring does not match (no fuzzy scoring)",
			hyps: []praxisv1alpha1.Hypothesis{hyp(
				// "memory limits" (plural, split) and "out-of-memory kill":
				// semantically right, textually wrong — must NOT count.
				"checkout-api suffered an out-of-memory kill; its memory limits were reduced",
				"ev/gitcommit-01", "ev/podstatus-02")},
			wantTop1: false, wantTop3: false,
		},
		{
			name:     "no hypotheses, no diagnosis",
			hyps:     nil,
			wantTop1: false, wantTop3: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &Record{Response: planResponse(1), Hypotheses: tc.hyps,
				Plan: planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"))}
			s := Compute(rec, oomkillGT())
			if s.DiagnosisTop1 != tc.wantTop1 || s.DiagnosisTop3 != tc.wantTop3 {
				t.Errorf("top1=%v top3=%v, want top1=%v top3=%v",
					s.DiagnosisTop1, s.DiagnosisTop3, tc.wantTop1, tc.wantTop3)
			}
		})
	}
}

func TestComputeActionMetrics(t *testing.T) {
	cases := []struct {
		name           string
		plan           *praxisv1alpha1.RemediationPlanSpec
		gt             *scenario.GroundTruth
		wantAcceptable bool
		wantForbidden  int
	}{
		{
			name:           "the pinned acceptable action matches",
			plan:           planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api")),
			gt:             oomkillGT(),
			wantAcceptable: true, wantForbidden: 0,
		},
		{
			name:           "same type, wrong target: not acceptable",
			plan:           planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "storefront")),
			gt:             oomkillGT(),
			wantAcceptable: false, wantForbidden: 0,
		},
		{
			name:           "a bare forbidden type matches any target",
			plan:           planWith(action(praxisv1alpha1.ActionScaleWorkload, "checkout-api")),
			gt:             oomkillGT(),
			wantAcceptable: false, wantForbidden: 1,
		},
		{
			name: "one acceptable plus one forbidden: violation recorded, plan not acceptable",
			plan: planWith(
				action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"),
				action(praxisv1alpha1.ActionScaleWorkload, "checkout-api"),
			),
			gt:             oomkillGT(),
			wantAcceptable: false, wantForbidden: 1,
		},
		{
			name:           "restraint scenario: any plan is unacceptable, all three reflexes violate",
			plan:           planWith(action(praxisv1alpha1.ActionRestartWorkload, "checkout-api")),
			gt:             restraintGT(),
			wantAcceptable: false, wantForbidden: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &Record{Response: planResponse(2), Plan: tc.plan}
			s := Compute(rec, tc.gt)
			if s.ActionsAcceptable == nil || *s.ActionsAcceptable != tc.wantAcceptable {
				t.Errorf("actionsAcceptable = %v, want %v", s.ActionsAcceptable, tc.wantAcceptable)
			}
			if s.ForbiddenViolations != tc.wantForbidden {
				t.Errorf("forbiddenViolations = %d, want %d", s.ForbiddenViolations, tc.wantForbidden)
			}
			if s.PlanSchemaValid == nil || !*s.PlanSchemaValid {
				t.Errorf("planSchemaValid = %v, want true for a persisted plan", s.PlanSchemaValid)
			}
		})
	}
}

func TestComputeRestraintAndDenominators(t *testing.T) {
	cases := []struct {
		name string
		kind ResponseKind
		gt   *scenario.GroundTruth
		want Score
	}{
		{
			name: "no-action on a restraint scenario is the one right answer",
			kind: ResponseNoAction, gt: restraintGT(),
			want: Score{RestraintCorrect: true, TimeToPlanSeconds: floatp(3)},
		},
		{
			name: "no-action on an actionable scenario is wrong",
			kind: ResponseNoAction, gt: oomkillGT(),
			want: Score{RestraintCorrect: false, TimeToPlanSeconds: floatp(3)},
		},
		{
			name: "timeout answers nothing: wrong on both scenario kinds, no denominators",
			kind: ResponseTimeout, gt: restraintGT(),
			want: Score{},
		},
		{
			name: "a schema-rejected plan is an attempt: plan-validity false, nothing else granted",
			kind: ResponsePlanInvalid, gt: oomkillGT(),
			want: Score{PlanSchemaValid: boolp(false)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &Record{Response: Response{Kind: tc.kind, WaitedSeconds: 3}}
			if tc.kind == ResponsePlanInvalid {
				rec.Plan = planWith(action(praxisv1alpha1.ActionScaleWorkload, "checkout-api"))
				rec.PlanCreateError = "spec invalid"
			}
			got := Compute(rec, tc.gt)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Compute = %s, want %s", mustJSON(t, got), mustJSON(t, tc.want))
			}
		})
	}
}

// TestPlanResponseRestraint: a persisted plan is restraint-correct exactly
// when the scenario expects action.
func TestPlanResponseRestraint(t *testing.T) {
	rec := &Record{Response: planResponse(2),
		Plan: planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"))}
	if s := Compute(rec, oomkillGT()); !s.RestraintCorrect {
		t.Error("a plan on an actionable scenario must be restraint-correct")
	}
	if s := Compute(rec, restraintGT()); s.RestraintCorrect {
		t.Error("a plan on a restraint scenario must be restraint-incorrect")
	}
}

// TestComputeDoesNotMutateGroundTruth: the scorer reads the answer key,
// it never writes it.
func TestComputeDoesNotMutateGroundTruth(t *testing.T) {
	gt := oomkillGT()
	before := mustJSON(t, gt)
	rec := &Record{
		Response:   planResponse(1),
		Hypotheses: []praxisv1alpha1.Hypothesis{matchingHyp()},
		Plan: planWith(
			action(praxisv1alpha1.ActionScaleWorkload, "checkout-api"),
			action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"),
		),
	}
	_ = Compute(rec, gt)
	_ = Compute(rec, gt)
	if after := mustJSON(t, gt); after != before {
		t.Errorf("Compute mutated the ground truth:\nbefore %s\nafter  %s", before, after)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
