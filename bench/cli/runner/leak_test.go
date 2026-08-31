/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akansh23-cloud/praxis/bench/cli/agentrun"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// loadAllPacks loads every shipped scenario, so these tests cover packs
// that land later without being touched.
func loadAllPacks(t *testing.T) []*scenario.Scenario {
	t.Helper()
	dir := filepath.Join("..", "..", "scenarios")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var scns []*scenario.Scenario
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		scn, err := scenario.Load(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("shipped pack %s does not load: %v", e.Name(), err)
		}
		scns = append(scns, scn)
	}
	if len(scns) < 7 {
		t.Fatalf("expected the six packs plus smoke, found %d", len(scns))
	}
	return scns
}

// realisticBundle is shaped like what the gatherer produces on a live
// run: kubelet warning events with real-world messages.
func realisticBundle(inc *evidence.IncidentRef) *evidence.Bundle {
	mk := func(seq int, reason, pod, msg string) evidence.Item {
		return evidence.Item{
			ID: evidence.ItemID(evidence.ItemTypeEvent, seq), Type: evidence.ItemTypeEvent, Source: "k8s",
			Data: map[string]string{
				evidence.EventDataType: "Warning", evidence.EventDataReason: reason,
				evidence.EventDataInvolvedKind: "Pod", evidence.EventDataInvolvedName: pod,
				evidence.EventDataInvolvedNamespace: "shop", evidence.EventDataMessage: msg,
			},
		}
	}
	return &evidence.Bundle{
		Version: evidence.SchemaVersion, Incident: *inc, CollectedAt: "2026-08-31T10:00:00Z",
		Items: []evidence.Item{
			mk(1, "Failed", "checkout-api-7d9f8-abcde",
				`Failed to pull image "registry.k8s.io/e2e-test-images/agnhost:2.53-hotfix-3417": not found`),
			mk(2, "Unhealthy", "storefront-66b9c-11111",
				`Readiness probe failed: Get "http://10.244.0.5:8081/healthz": connection refused`),
			mk(3, "BackOff", "checkout-api-7d9f8-abcde",
				"Back-off restarting failed container session-cache in pod checkout-api-7d9f8-abcde_shop"),
		},
	}
}

// TestAgentInputsCarryNoBenchmarkIdentity is the fixture-level half of the
// leak proof (the live half runs on every benchmark run via
// assertScenarioBlind): for EVERY shipped pack, the exact incident the
// runner would file — built by the same buildIncident the runner uses —
// sanitized the way agentrun sanitizes it, plus a realistic bundle, must
// contain no scenario name, no rootCauseId, and no groundTruth marker.
func TestAgentInputsCarryNoBenchmarkIdentity(t *testing.T) {
	scns := loadAllPacks(t)
	for _, scn := range scns {
		t.Run(scn.Name, func(t *testing.T) {
			inc := buildIncident(scn, 1, time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
			if inc.Labels[labelBenchScenario] != scn.Name {
				t.Fatalf("the forensic label is gone from the cluster object; the test would prove nothing")
			}

			sanitized := agentrun.SanitizeIncident(inc)
			bundle := realisticBundle(&evidence.IncidentRef{Name: inc.Name, UID: "uid-1"})
			report := &agentrun.Report{SanitizedIncident: sanitized, Bundle: bundle}

			if err := assertScenarioBlind(scn, report); err != nil {
				t.Errorf("agent inputs leak benchmark identity: %v", err)
			}
			// The neutral description really is neutral: byte-identical
			// across packs, naming neither the scenario nor the fault.
			if sanitized.Spec.Description != neutralDescription {
				t.Errorf("description %q is not the neutral text", sanitized.Spec.Description)
			}
			if strings.Contains(sanitized.Name, scn.Name) {
				t.Errorf("incident name %q embeds the scenario name", sanitized.Name)
			}
		})
	}
}

// TestAssertScenarioBlindCatchesPlants: the live guard actually fires on
// each class of leak it exists to catch.
func TestAssertScenarioBlindCatchesPlants(t *testing.T) {
	scns := loadAllPacks(t)
	scn := scns[0]
	base := func() *agentrun.Report {
		inc := buildIncident(scn, 1, time.Now())
		return &agentrun.Report{
			SanitizedIncident: agentrun.SanitizeIncident(inc),
			Bundle:            realisticBundle(&evidence.IncidentRef{Name: inc.Name, UID: "u"}),
		}
	}

	plants := map[string]func(*agentrun.Report){
		"scenario name in an event message": func(rep *agentrun.Report) {
			rep.Bundle.Items[0].Data[evidence.EventDataMessage] = "error in " + scn.Name + " workload"
		},
		"rootCauseId in a bundle field": func(rep *agentrun.Report) {
			rep.Bundle.Items[0].Data["extra"] = scn.GroundTruth.RootCauseID
		},
		"groundTruth blob on the incident": func(rep *agentrun.Report) {
			rep.SanitizedIncident.Annotations = map[string]string{"hint": `{"groundTruth":{}}`}
		},
		"unsanitized incident (bench label still present)": func(rep *agentrun.Report) {
			rep.SanitizedIncident.Labels = map[string]string{labelBenchScenario: scn.Name}
		},
	}
	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			rep := base()
			plant(rep)
			if err := assertScenarioBlind(scn, rep); err == nil {
				t.Error("the live leak guard did not catch the planted identity")
			}
		})
	}

	if err := assertScenarioBlind(scn, base()); err != nil {
		t.Fatalf("clean inputs must pass the guard: %v", err)
	}
}
