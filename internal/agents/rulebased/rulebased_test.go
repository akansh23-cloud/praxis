/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package rulebased

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

func event(seq int, reason, kind, name, message string) evidence.Item {
	return evidence.Item{
		ID:     evidence.ItemID(evidence.ItemTypeEvent, seq),
		Type:   evidence.ItemTypeEvent,
		Source: "k8s",
		Data: map[string]string{
			evidence.EventDataType:              "Warning",
			evidence.EventDataReason:            reason,
			evidence.EventDataInvolvedKind:      kind,
			evidence.EventDataInvolvedName:      name,
			evidence.EventDataInvolvedNamespace: nsShop,
			evidence.EventDataMessage:           message,
		},
	}
}

func bundleOf(items ...evidence.Item) *evidence.Bundle {
	return &evidence.Bundle{
		Version:     evidence.SchemaVersion,
		Incident:    evidence.IncidentRef{Name: "bench-20260831-r1", UID: "uid-1"},
		CollectedAt: "2026-08-31T10:00:00Z",
		Items:       items,
	}
}

func incident() *praxisv1alpha1.Incident {
	return &praxisv1alpha1.Incident{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-20260831-r1", Namespace: nsShop, UID: "uid-1"},
		Spec: praxisv1alpha1.IncidentSpec{
			Source:      praxisv1alpha1.IncidentSourceManual,
			Description: "synthetic incident",
			Severity:    "High",
			Scope:       praxisv1alpha1.IncidentScope{Namespaces: []string{"shop"}},
		},
	}
}

const (
	pullMsg      = `Failed to pull image "registry.k8s.io/e2e-test-images/agnhost:2.53-hotfix-3417": not found`
	readinessMsg = `Readiness probe failed: Get "http://10.244.0.5:8081/healthz": connection refused`
	backoffMsg   = `Back-off restarting failed container session-cache in pod checkout-api-abc-xyz`
	pullBackoff  = `Back-off pulling image "registry.k8s.io/e2e-test-images/agnhost:2.53-hotfix-3417"`

	nsShop         = "shop"
	deployCheckout = "checkout-api"
	deployStore    = "storefront"
	firstEventID   = "ev/event-01"
	fragPull       = "pull"
)

func TestRuleTable(t *testing.T) {
	cases := []struct {
		name          string
		items         []evidence.Item
		wantAction    praxisv1alpha1.ActionType
		wantTarget    string
		wantCitations []string
		wantInSummary []string
	}{
		{
			name:          "image pull failure proposes rollback of the derived deployment",
			items:         []evidence.Item{event(1, "Failed", "Pod", "checkout-api-7d9f8-abcde", pullMsg)},
			wantAction:    praxisv1alpha1.ActionRollbackRelease,
			wantTarget:    deployCheckout,
			wantCitations: []string{firstEventID},
			wantInSummary: []string{deployCheckout, fragPull},
		},
		{
			name:          "readiness probe failure proposes rollback",
			items:         []evidence.Item{event(1, "Unhealthy", "Pod", "storefront-66b9c-11111", readinessMsg)},
			wantAction:    praxisv1alpha1.ActionRollbackRelease,
			wantTarget:    deployStore,
			wantCitations: []string{firstEventID},
			wantInSummary: []string{deployStore, "readiness probe"},
		},
		{
			name:          "crash-loop back-off proposes restart",
			items:         []evidence.Item{event(1, "BackOff", "Pod", "checkout-api-7d9f8-abcde", backoffMsg)},
			wantAction:    praxisv1alpha1.ActionRestartWorkload,
			wantTarget:    deployCheckout,
			wantCitations: []string{firstEventID},
			wantInSummary: []string{deployCheckout, "crash-looping"},
		},
		{
			name: "rule order: pull failure outranks crash-loop, citations only from the winning rule",
			items: []evidence.Item{
				event(1, "BackOff", "Pod", "checkout-api-7d9f8-abcde", backoffMsg),
				event(2, "Failed", "Pod", "checkout-api-7d9f8-abcde", pullMsg),
				event(3, "Failed", "Pod", "checkout-api-7d9f8-fghij", pullMsg),
			},
			wantAction:    praxisv1alpha1.ActionRollbackRelease,
			wantTarget:    deployCheckout,
			wantCitations: []string{"ev/event-02", "ev/event-03"},
			wantInSummary: []string{fragPull},
		},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			inc, bundle := incident(), bundleOf(tc.items...)

			hyps, err := a.Analyze(ctx, inc, bundle)
			if err != nil {
				t.Fatal(err)
			}
			if len(hyps) != 1 {
				t.Fatalf("Analyze returned %d hypotheses, want exactly 1", len(hyps))
			}
			for _, frag := range tc.wantInSummary {
				if !strings.Contains(hyps[0].Summary, frag) {
					t.Errorf("summary %q does not contain %q", hyps[0].Summary, frag)
				}
			}
			var got []string
			for _, c := range hyps[0].Citations {
				got = append(got, string(c))
			}
			if !reflect.DeepEqual(got, tc.wantCitations) {
				t.Errorf("citations = %v, want %v", got, tc.wantCitations)
			}

			plan, noAction, err := a.Plan(ctx, inc, bundle, hyps)
			if err != nil {
				t.Fatal(err)
			}
			if noAction.Proposed || plan == nil {
				t.Fatalf("expected a plan, got noAction=%+v plan=%v", noAction, plan)
			}
			if len(plan.Actions) != 1 || plan.Actions[0].Type != tc.wantAction {
				t.Fatalf("actions = %+v, want one %s", plan.Actions, tc.wantAction)
			}
			target := plan.Actions[0].Target
			if target.Kind != "Deployment" || target.Name != tc.wantTarget || target.Namespace != "shop" {
				t.Errorf("target = %+v, want Deployment shop/%s", target, tc.wantTarget)
			}
			assertPlanShape(t, plan, inc, bundle)
		})
	}
}

// assertPlanShape mirrors the CRD constraints the API server would apply,
// so a schema-invalid fixed plan fails here before it fails a live run.
func assertPlanShape(t *testing.T, plan *praxisv1alpha1.RemediationPlanSpec,
	inc *praxisv1alpha1.Incident, bundle *evidence.Bundle,
) {
	t.Helper()
	if plan.IncidentRef.Name != inc.Name || plan.IncidentRef.UID != inc.UID {
		t.Errorf("incidentRef = %+v does not pin the incident", plan.IncidentRef)
	}
	wantHash, err := bundle.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if plan.EvidenceBundleHash != wantHash {
		t.Errorf("evidenceBundleHash = %q, want the bundle's own %q", plan.EvidenceBundleHash, wantHash)
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(plan.EvidenceBundleHash) {
		t.Errorf("evidenceBundleHash %q violates the CRD pattern", plan.EvidenceBundleHash)
	}
	if len(plan.Hypothesis.Citations) < 1 {
		t.Error("hypothesis has no citations; the CRD requires at least one")
	}
	if len(plan.Hypothesis.Summary) == 0 || len(plan.Hypothesis.Summary) > 1024 {
		t.Errorf("summary length %d outside the CRD's 1..1024", len(plan.Hypothesis.Summary))
	}
	if plan.Verification.Predicate == "" || plan.Verification.Window.Duration <= 0 {
		t.Errorf("verification %+v is incomplete", plan.Verification)
	}
	if plan.Verification.OnFailure == praxisv1alpha1.FailureActionRollback &&
		plan.Rollback.Strategy == praxisv1alpha1.RollbackNone {
		t.Error("onFailure Rollback with strategy None violates the plan CEL rule")
	}
}

func TestNoActionPaths(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		bundle     *evidence.Bundle
		wantReason string
	}{
		{"empty bundle proposes no action", bundleOf(), noActionNoEvents},
		{
			"unrecognized events propose no action",
			bundleOf(
				event(1, "FailedScheduling", "Pod", "storefront-66b9c-11111", "0/1 nodes available"),
				// Right reason and message, wrong involved kind: the rules
				// key on (reason, kind) pairs, not reason alone.
				event(3, "Failed", "Deployment", deployCheckout, pullMsg),
				// BackOff pulling does not carry "restarting", so the
				// crash-loop rule must not claim it (the pull rule keys on
				// reason Failed, not BackOff).
				event(2, "BackOff", "Pod", "checkout-api-7d9f8-abcde", pullBackoff),
			),
			noActionNoMatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			hyps, err := a.Analyze(ctx, incident(), tc.bundle)
			if err != nil {
				t.Fatal(err)
			}
			if len(hyps) != 0 {
				t.Fatalf("Analyze returned %d hypotheses, want none", len(hyps))
			}
			plan, noAction, err := a.Plan(ctx, incident(), tc.bundle, hyps)
			if err != nil {
				t.Fatal(err)
			}
			if plan != nil || !noAction.Proposed {
				t.Fatalf("want a no-action verdict, got plan=%v noAction=%+v", plan, noAction)
			}
			if noAction.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", noAction.Reason, tc.wantReason)
			}
		})
	}
}

// TestDeterminismAndInputImmutability: identical inputs must produce
// deeply-equal outputs, and the agent must not touch its inputs.
func TestDeterminismAndInputImmutability(t *testing.T) {
	ctx := context.Background()
	a := New()
	inc := incident()
	bundle := bundleOf(
		event(1, "Failed", "Pod", "checkout-api-7d9f8-abcde", pullMsg),
		event(2, "BackOff", "Pod", "checkout-api-7d9f8-abcde", backoffMsg),
	)
	incBefore := inc.DeepCopy()
	bundleBefore := bundleOf(
		event(1, "Failed", "Pod", "checkout-api-7d9f8-abcde", pullMsg),
		event(2, "BackOff", "Pod", "checkout-api-7d9f8-abcde", backoffMsg),
	)

	h1, err := a.Analyze(ctx, inc, bundle)
	if err != nil {
		t.Fatal(err)
	}
	p1, _, err := a.Plan(ctx, inc, bundle, h1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := a.Analyze(ctx, inc, bundle)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := a.Plan(ctx, inc, bundle, h2)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(h1, h2) || !reflect.DeepEqual(p1, p2) {
		t.Error("identical inputs produced different outputs; the baseline must be pure")
	}
	if !reflect.DeepEqual(inc, incBefore) {
		t.Error("Analyze/Plan mutated the incident")
	}
	if !reflect.DeepEqual(bundle, bundleBefore) {
		t.Error("Analyze/Plan mutated the bundle")
	}
}

func TestInputGuards(t *testing.T) {
	a := New()
	if _, err := a.Analyze(context.Background(), nil, bundleOf()); err == nil {
		t.Error("nil incident must be rejected")
	}
	if _, _, err := a.Plan(context.Background(), incident(), nil, nil); err == nil {
		t.Error("nil bundle must be rejected")
	}
}

func TestDeploymentFor(t *testing.T) {
	cases := map[string]string{
		"checkout-api-7d9f8-abcde":  "checkout-api",
		"storefront-66b9c-11111":    "storefront",
		"inventory-5c9d8b7f6-x2v9q": "inventory",
		"lonely":                    "lonely",
		"two-parts":                 "two-parts",
		"batch-analytics-abc-def":   "batch-analytics",
		"payment-provider-sim-a-b":  "payment-provider-sim",
	}
	for pod, want := range cases {
		if got := deploymentFor(pod); got != want {
			t.Errorf("deploymentFor(%q) = %q, want %q", pod, got, want)
		}
	}
}
