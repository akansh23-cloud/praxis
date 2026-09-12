/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scoring

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
)

// Stat is one metric's distribution over the runs where it applied
// (FR-P2-05: distributions, not single numbers). Booleans enter as 0/1,
// so Mean is the rate.
type Stat struct {
	N    int
	Mean float64
	Min  float64
	Max  float64
}

func (s *Stat) add(v float64) {
	if s.N == 0 || v < s.Min {
		s.Min = v
	}
	if s.N == 0 || v > s.Max {
		s.Max = v
	}
	// Accumulate the sum in Mean until finish() divides it.
	s.Mean += v
	s.N++
}

func (s *Stat) finish() {
	if s.N > 0 {
		s.Mean /= float64(s.N)
	}
}

func (s *Stat) addBool(b bool) {
	if b {
		s.add(1)
	} else {
		s.add(0)
	}
}

// Aggregate is one (agent, scenario) row of the report.
type Aggregate struct {
	Agent    string
	Scenario string
	Runs     int

	Top1, Top3          Stat // denominator: all runs
	PlanValid           Stat // denominator: runs where a plan was attempted
	ActionsOK           Stat // denominator: runs with a persisted plan
	ForbiddenViolations Stat // denominator: all runs
	Restraint           Stat // denominator: all runs
	TimeToPlan          Stat // denominator: runs with a response, seconds
	InjectionVisible    Stat // denominator: runs of a planting pack with an analyzed bundle
	InjectionInert      Stat // denominator: runs where the injection was visible
}

// AggregateRecords groups scored records by (agent, scenario) and folds
// each metric into its distribution. Unscored records are an error — run
// `praxisbench score` first.
func AggregateRecords(records []Record) ([]Aggregate, error) {
	byKey := map[[2]string]*Aggregate{}
	for i := range records {
		rec := &records[i]
		if rec.Score == nil {
			return nil, fmt.Errorf("record %s run %d (agent %s) has no score; score it first (praxisbench score)",
				rec.Scenario, rec.Run, rec.Agent)
		}
		key := [2]string{rec.Agent, rec.Scenario}
		agg := byKey[key]
		if agg == nil {
			agg = &Aggregate{Agent: rec.Agent, Scenario: rec.Scenario}
			byKey[key] = agg
		}
		agg.Runs++
		agg.Top1.addBool(rec.Score.DiagnosisTop1)
		agg.Top3.addBool(rec.Score.DiagnosisTop3)
		if rec.Score.PlanSchemaValid != nil {
			agg.PlanValid.addBool(*rec.Score.PlanSchemaValid)
		}
		if rec.Score.ActionsAcceptable != nil {
			agg.ActionsOK.addBool(*rec.Score.ActionsAcceptable)
		}
		agg.ForbiddenViolations.add(float64(rec.Score.ForbiddenViolations))
		agg.Restraint.addBool(rec.Score.RestraintCorrect)
		if rec.Score.TimeToPlanSeconds != nil {
			agg.TimeToPlan.add(*rec.Score.TimeToPlanSeconds)
		}
		if rec.Score.InjectionVisible != nil {
			agg.InjectionVisible.addBool(*rec.Score.InjectionVisible)
		}
		if rec.Score.InjectionInert != nil {
			agg.InjectionInert.addBool(*rec.Score.InjectionInert)
		}
	}

	aggs := make([]Aggregate, 0, len(byKey))
	for _, agg := range byKey {
		for _, s := range []*Stat{
			&agg.Top1, &agg.Top3, &agg.PlanValid, &agg.ActionsOK,
			&agg.ForbiddenViolations, &agg.Restraint, &agg.TimeToPlan,
			&agg.InjectionVisible, &agg.InjectionInert,
		} {
			s.finish()
		}
		aggs = append(aggs, *agg)
	}
	slices.SortFunc(aggs, func(a, b Aggregate) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Scenario, b.Scenario))
	})
	return aggs, nil
}

// Rescore recomputes every record's Score from its raw observations
// against the answer keys in scenariosDir — the path for re-refereeing
// existing JSONL after a ground-truth correction. Records are modified in
// place; the loaded scenarios are not.
func Rescore(records []Record, scenariosDir string) error {
	cache := map[string]*scenario.Scenario{}
	for i := range records {
		name := records[i].Scenario
		scn := cache[name]
		if scn == nil {
			loaded, err := scenario.Load(filepath.Join(scenariosDir, name))
			if err != nil {
				return fmt.Errorf("record references scenario %q: %w", name, err)
			}
			cache[name] = loaded
			scn = loaded
		}
		score := Compute(&records[i], &scn.GroundTruth)
		records[i].Score = &score
	}
	return nil
}
