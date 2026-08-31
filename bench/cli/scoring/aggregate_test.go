/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scoring

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// scoredRecord builds a record with its score precomputed from real
// ground truth, the way `run` writes them.
func scoredRecord(scenarioName string, run int, kind ResponseKind, waited float64,
	hyps []praxisv1alpha1.Hypothesis, plan *praxisv1alpha1.RemediationPlanSpec,
) Record {
	rec := Record{
		Schema:     RecordSchema,
		Scenario:   scenarioName,
		Run:        run,
		Agent:      "rulebased",
		Incident:   IncidentMeta{Namespace: "shop", Name: "bench-x"},
		Response:   Response{Kind: kind, WaitedSeconds: waited},
		Hypotheses: hyps,
		Plan:       plan,
	}
	gt := oomkillGT()
	if kind == ResponseNoAction {
		gt = restraintGT()
	}
	score := Compute(&rec, gt)
	rec.Score = &score
	return rec
}

func TestAggregateRecordsDenominators(t *testing.T) {
	acceptable := planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"))
	violating := planWith(action(praxisv1alpha1.ActionScaleWorkload, "checkout-api"))
	records := []Record{
		scoredRecord("alpha", 1, ResponsePlan, 1.0, []praxisv1alpha1.Hypothesis{matchingHyp()}, acceptable),
		scoredRecord("alpha", 2, ResponsePlan, 3.0, nil, violating),
		scoredRecord("alpha", 3, ResponseTimeout, 120, nil, nil),
		scoredRecord("beta", 1, ResponseNoAction, 2.5, nil, nil),
	}

	aggs, err := AggregateRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggs) != 2 || aggs[0].Scenario != "alpha" || aggs[1].Scenario != "beta" {
		t.Fatalf("aggregates = %+v, want sorted alpha then beta", aggs)
	}

	alpha := aggs[0]
	if alpha.Runs != 3 {
		t.Errorf("alpha.Runs = %d, want 3", alpha.Runs)
	}
	// Diagnosis over ALL runs: 1 of 3 matched top1.
	if alpha.Top1.N != 3 || alpha.Top1.Mean < 0.33 || alpha.Top1.Mean > 0.34 ||
		alpha.Top1.Min != 0 || alpha.Top1.Max != 1 {
		t.Errorf("alpha.Top1 = %+v, want N=3 mean 1/3 min 0 max 1", alpha.Top1)
	}
	// Plan validity only over the two attempts.
	if alpha.PlanValid.N != 2 || alpha.PlanValid.Mean != 1 {
		t.Errorf("alpha.PlanValid = %+v, want N=2 mean 1", alpha.PlanValid)
	}
	// Actions only over the two persisted plans; one was acceptable.
	if alpha.ActionsOK.N != 2 || alpha.ActionsOK.Mean != 0.5 {
		t.Errorf("alpha.ActionsOK = %+v, want N=2 mean 0.5", alpha.ActionsOK)
	}
	// Forbidden violations over ALL runs: 0, 1, 0.
	if alpha.ForbiddenViolations.N != 3 || alpha.ForbiddenViolations.Max != 1 {
		t.Errorf("alpha.ForbiddenViolations = %+v, want N=3 max 1", alpha.ForbiddenViolations)
	}
	// Restraint over all runs: plan, plan, timeout on an actionable
	// scenario ⇒ 2/3 correct.
	if alpha.Restraint.N != 3 || alpha.Restraint.Mean < 0.66 || alpha.Restraint.Mean > 0.67 {
		t.Errorf("alpha.Restraint = %+v, want N=3 mean 2/3", alpha.Restraint)
	}
	// Timing only over the two responses (1.0s and 3.0s).
	if alpha.TimeToPlan.N != 2 || alpha.TimeToPlan.Mean != 2.0 ||
		alpha.TimeToPlan.Min != 1.0 || alpha.TimeToPlan.Max != 3.0 {
		t.Errorf("alpha.TimeToPlan = %+v, want N=2 mean 2 min 1 max 3", alpha.TimeToPlan)
	}

	beta := aggs[1]
	if beta.Restraint.Mean != 1 || beta.TimeToPlan.N != 1 || beta.TimeToPlan.Mean != 2.5 {
		t.Errorf("beta = %+v, want restraint 1.0 and one 2.5s response", beta)
	}
}

func TestAggregateRejectsUnscoredRecords(t *testing.T) {
	records := []Record{{Schema: RecordSchema, Scenario: "alpha", Run: 1, Agent: "none"}}
	if _, err := AggregateRecords(records); err == nil ||
		!strings.Contains(err.Error(), "no score") {
		t.Errorf("want an unscored-record error, got %v", err)
	}
}

func TestRenderReportDeterministic(t *testing.T) {
	records := []Record{
		scoredRecord("alpha", 1, ResponsePlan, 1.0, []praxisv1alpha1.Hypothesis{matchingHyp()},
			planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"))),
		scoredRecord("alpha", 2, ResponseTimeout, 120, nil, nil),
		scoredRecord("beta", 1, ResponseNoAction, 2.5, nil, nil),
	}
	aggs, err := AggregateRecords(records)
	if err != nil {
		t.Fatal(err)
	}

	var one, two strings.Builder
	if err := RenderReport(&one, aggs); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&two, aggs); err != nil {
		t.Fatal(err)
	}
	if one.String() != two.String() {
		t.Error("report output is not deterministic")
	}

	for _, wantLine := range []string{
		"agent rulebased:",
		"  alpha (2 runs)",
		"    diagnosis   top1 50% (0–1)   top3 50% (0–1)",
		"    plan        schema-valid 100% (1–1) n=1",
		"    actions     acceptable 100% (1–1) n=1   forbidden-violations 0.00 (0.00–0.00)",
		"    restraint   correct 50% (0–1)",
		"    timing      time-to-plan 1.0s (1.0–1.0) n=1",
		"  beta (1 runs)",
		"    plan        schema-valid n/a (no qualifying runs)",
		"    restraint   correct 100% (1–1)",
		"    timing      time-to-plan 2.5s (2.5–2.5)",
	} {
		if !strings.Contains(one.String(), wantLine+"\n") {
			t.Errorf("report is missing the line %q; got:\n%s", wantLine, one.String())
		}
	}
}

func TestRecordsRoundTripThroughJSONL(t *testing.T) {
	dir := t.TempDir()
	records := []Record{
		scoredRecord("alpha", 1, ResponsePlan, 1.0, []praxisv1alpha1.Hypothesis{matchingHyp()},
			planWith(action(praxisv1alpha1.ActionPatchResourceLimits, "checkout-api"))),
		scoredRecord("beta", 1, ResponseNoAction, 2.5, nil, nil),
	}

	file := filepath.Join(dir, "run-1.jsonl")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	for i := range records {
		if err := AppendRecord(f, &records[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	fromFile, err := ReadRecords(file)
	if err != nil {
		t.Fatal(err)
	}
	fromDir, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, fromFile) || !reflect.DeepEqual(records, fromDir) {
		t.Error("records did not round-trip through JSONL identically")
	}
}

// TestRescoreAgainstShippedPacks drives Rescore over the real answer keys
// in bench/scenarios, with a record shaped like the baseline's actual
// bad-image-tag response: the action set matches the shipped key, the
// diagnosis does not (no gitcommit citation).
func TestRescoreAgainstShippedPacks(t *testing.T) {
	rec := Record{
		Schema: RecordSchema, Scenario: "bad-image-tag", Run: 1, Agent: "rulebased",
		Response: Response{Kind: ResponsePlan, WaitedSeconds: 1.2},
		Hypotheses: []praxisv1alpha1.Hypothesis{hyp(
			"image pull failure on pod checkout-api-a-b of deployment checkout-api; rolling back the release",
			"ev/event-01")},
		Plan: planWith(action(praxisv1alpha1.ActionRollbackRelease, "checkout-api")),
	}
	records := []Record{rec}
	if err := Rescore(records, filepath.Join("..", "..", "scenarios")); err != nil {
		t.Fatal(err)
	}
	s := records[0].Score
	if s == nil {
		t.Fatal("Rescore left the record unscored")
	}
	if s.DiagnosisTop1 || s.DiagnosisTop3 {
		t.Error("an event-only citation set must fail bad-image-tag's diagnosis rule (gitcommit required)")
	}
	if s.ActionsAcceptable == nil || !*s.ActionsAcceptable {
		t.Error("RollbackRelease on checkout-api is the shipped acceptable action; scorer disagrees")
	}
	if s.ForbiddenViolations != 0 || !s.RestraintCorrect {
		t.Errorf("unexpected score %+v", s)
	}

	records[0].Scenario = "no-such-pack"
	if err := Rescore(records, filepath.Join("..", "..", "scenarios")); err == nil {
		t.Error("a record naming an unknown scenario must fail rescore")
	}
}
