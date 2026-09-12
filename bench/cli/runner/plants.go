/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/akansh23-cloud/praxis/bench/cli/faultcheck"
	"github.com/akansh23-cloud/praxis/bench/cli/scoring"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
)

// The planted-telemetry half of the runner (ADR-010): for a pack that
// plants strings into a pod log, make sure the injection had every chance
// to reach the bundle before the Incident is filed, then record — never
// assume — whether it did.

// plantsSettleTimeout bounds the wait for Loki to serve every planted
// string. Promtail ships lines within seconds; two minutes is a bound on
// a broken pipeline, not an expectation.
const plantsSettleTimeout = 2 * time.Minute

// awaitPlantsAtLoki polls Loki with the collector's own selector and
// window until every plant appears in a served line, or the timeout
// passes. It never fails the run: a timeout is reported and the run
// continues, because the visibility metric records the consequence
// honestly and a flaky Loki must not abort a campaign.
func awaitPlantsAtLoki(ctx context.Context, lc logs.Client, namespaces, plants []string,
	timeout time.Duration, logf func(string, ...any),
) {
	if lc == nil {
		logf("    WARNING: no --loki-url, so the planted telemetry cannot reach any bundle: the run will record the injection as not visible")
		return
	}
	nss := slices.Clone(namespaces)
	slices.Sort(nss)
	var missing []string
	start := time.Now()
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		now := time.Now()
		var lines strings.Builder
		for _, ns := range nss {
			streams, err := lc.QueryRange(ctx, `{namespace="`+ns+`"}`, now.Add(-evidence.LogWindow), now, evidence.MaxLogLines)
			if err != nil {
				missing = plants
				return false, nil //nolint:nilerr // Loki not answering yet: keep polling until the timeout rules
			}
			for i := range streams {
				for _, raw := range streams[i].Lines {
					lines.WriteString(logs.Normalize(raw))
					lines.WriteByte('\n')
				}
			}
		}
		missing = faultcheck.MissingPlants(lines.String(), plants)
		return len(missing) == 0, nil
	})
	if err != nil {
		logf("    WARNING: after %s Loki still does not serve %d of %d planted lines; filing the Incident anyway — the run will record what the bundle actually held",
			timeout, len(missing), len(plants))
		return
	}
	logf("    Loki serves every planted line (%d) after %s", len(plants), time.Since(start).Round(time.Second))
}

// observePlants reports, in the pack's order, which planted strings occur
// verbatim in a data value of the analyzed bundle and which items carry
// them. Exact substring matching, the same rule as faultcheck.MissingPlants.
func observePlants(bundle *evidence.Bundle, plants []string) []scoring.PlantObservation {
	out := make([]scoring.PlantObservation, 0, len(plants))
	for _, plant := range plants {
		obs := scoring.PlantObservation{Text: plant}
		for i := range bundle.Items {
			it := &bundle.Items[i]
			for _, v := range it.Data {
				if strings.Contains(v, plant) {
					obs.Items = append(obs.Items, it.ID)
					break
				}
			}
		}
		out = append(out, obs)
	}
	return out
}

// describePlants renders the observation for the run log.
func describePlants(observed []scoring.PlantObservation) string {
	visible := 0
	var ids []string
	for _, o := range observed {
		if len(o.Items) > 0 {
			visible++
			ids = append(ids, o.Items...)
		}
	}
	summary := fmt.Sprintf("%d/%d planted strings visible in the analyzed bundle", visible, len(observed))
	if len(ids) > 0 {
		summary += " (" + strings.Join(ids, ", ") + ")"
	}
	return summary
}
