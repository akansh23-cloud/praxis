/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scoring

import (
	"fmt"
	"io"
	"strings"
)

// RenderReport writes the aggregate table: one block per (agent,
// scenario), metrics grouped the way FR-P2-03 names them — diagnosis,
// plan validity, action correctness, restraint correctness, timing. The
// output is a pure function of the aggregates (no clocks, no paths), so
// identical records render identical reports.
func RenderReport(w io.Writer, aggs []Aggregate) error {
	var b strings.Builder
	b.WriteString("praxisbench report — mean (min–max) over N runs; n=… marks a metric whose denominator is smaller than runs\n")
	agent := ""
	for i := range aggs {
		agg := &aggs[i]
		if agg.Agent != agent {
			agent = agg.Agent
			fmt.Fprintf(&b, "\nagent %s:\n", agent)
		}
		fmt.Fprintf(&b, "  %s (%d runs)\n", agg.Scenario, agg.Runs)
		fmt.Fprintf(&b, "    diagnosis   top1 %s   top3 %s\n",
			rate(&agg.Top1, agg.Runs), rate(&agg.Top3, agg.Runs))
		fmt.Fprintf(&b, "    plan        schema-valid %s\n", rate(&agg.PlanValid, agg.Runs))
		fmt.Fprintf(&b, "    actions     acceptable %s   forbidden-violations %s\n",
			rate(&agg.ActionsOK, agg.Runs), number(&agg.ForbiddenViolations, agg.Runs, "%.2f"))
		fmt.Fprintf(&b, "    restraint   correct %s\n", rate(&agg.Restraint, agg.Runs))
		fmt.Fprintf(&b, "    timing      time-to-plan %s\n", seconds(&agg.TimeToPlan, agg.Runs))
		// Only packs that plant telemetry (ADR-010) have an injection row;
		// on every other pack the metric does not exist rather than being
		// trivially "n/a".
		if agg.InjectionVisible.N > 0 || agg.InjectionInert.N > 0 {
			fmt.Fprintf(&b, "    injection   visible %s   inert %s\n",
				rate(&agg.InjectionVisible, agg.Runs), rate(&agg.InjectionInert, agg.Runs))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// notApplicable marks a metric none of the runs qualified for.
const notApplicable = "n/a (no qualifying runs)"

// rate renders a boolean metric: the mean as a percentage with the 0/1
// extremes, e.g. "40% (0–1)".
func rate(s *Stat, runs int) string {
	if s.N == 0 {
		return notApplicable
	}
	out := fmt.Sprintf("%.0f%% (%.0f–%.0f)", s.Mean*100, s.Min, s.Max)
	return out + nSuffix(s, runs)
}

// number renders a numeric metric with the given verb, e.g. "0.20 (0–1)".
func number(s *Stat, runs int, verb string) string {
	if s.N == 0 {
		return notApplicable
	}
	out := fmt.Sprintf(verb+" ("+verb+"–"+verb+")", s.Mean, s.Min, s.Max)
	return out + nSuffix(s, runs)
}

// seconds renders the timing metric, e.g. "1.4s (1.2–1.6)".
func seconds(s *Stat, runs int) string {
	if s.N == 0 {
		return notApplicable
	}
	out := fmt.Sprintf("%.1fs (%.1f–%.1f)", s.Mean, s.Min, s.Max)
	return out + nSuffix(s, runs)
}

func nSuffix(s *Stat, runs int) string {
	if s.N == runs {
		return ""
	}
	return fmt.Sprintf(" n=%d", s.N)
}
