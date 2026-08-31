/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/agents"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

const (
	nsShop    = "shop"
	evEvent01 = "ev/event-01"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, appsv1.AddToScheme, praxisv1alpha1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func benchIncident(created time.Time) *praxisv1alpha1.Incident {
	return &praxisv1alpha1.Incident{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "bench-20260831-100000-r1",
			Namespace:         nsShop,
			UID:               "uid-inc-1",
			CreationTimestamp: metav1.Time{Time: created},
			Labels:            map[string]string{"praxis.dev/bench-scenario": "some-pack"},
		},
		Spec: praxisv1alpha1.IncidentSpec{
			Source:      praxisv1alpha1.IncidentSourceManual,
			Description: "synthetic incident",
			Severity:    "High",
			Scope:       praxisv1alpha1.IncidentScope{Namespaces: []string{nsShop}},
		},
	}
}

func warningEvent(name, reason, pod, message string, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: nsShop},
		Type:           corev1.EventTypeWarning,
		Reason:         reason,
		Message:        message,
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod, Namespace: nsShop},
		LastTimestamp:  metav1.Time{Time: at},
	}
}

// stubAgent lets each test script the seam's behavior.
type stubAgent struct {
	hyps     agents.Hypotheses
	plan     *praxisv1alpha1.RemediationPlanSpec
	noAction agents.NoAction
}

func (s *stubAgent) Analyze(context.Context, *praxisv1alpha1.Incident, *evidence.Bundle) (agents.Hypotheses, error) {
	return s.hyps, nil
}

func (s *stubAgent) Plan(context.Context, *praxisv1alpha1.Incident, *evidence.Bundle, agents.Hypotheses,
) (*praxisv1alpha1.RemediationPlanSpec, agents.NoAction, error) {
	return s.plan, s.noAction, nil
}

func validPlanSpec(inc *praxisv1alpha1.Incident, bundleHash string) *praxisv1alpha1.RemediationPlanSpec {
	return &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef:        praxisv1alpha1.IncidentRef{Name: inc.Name, UID: inc.UID},
		EvidenceBundleHash: bundleHash,
		Hypothesis: praxisv1alpha1.Hypothesis{
			Summary:           "stub hypothesis",
			ConfidencePercent: 50,
			Citations:         []praxisv1alpha1.EvidenceID{evEvent01},
		},
		Actions: []praxisv1alpha1.Action{{
			Type:   praxisv1alpha1.ActionRestartWorkload,
			Target: praxisv1alpha1.TargetRef{Kind: "Deployment", Namespace: nsShop, Name: "checkout-api"},
		}},
		Verification: praxisv1alpha1.VerificationSpec{
			Predicate: "vector(1)", Window: metav1.Duration{Duration: time.Minute},
			OnFailure: praxisv1alpha1.FailureActionEscalate,
		},
		Rollback: praxisv1alpha1.RollbackSpec{Strategy: praxisv1alpha1.RollbackNone},
	}
}

func discard(string, ...any) {}

func TestRespondPersistsPlan(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).
		WithObjects(inc, warningEvent("e1", "BackOff", "checkout-api-a-b", "Back-off restarting failed container", now)).
		Build()

	// The stub returns whatever hash the gatherer computed — mimic by
	// planning lazily is overkill; any well-formed hash string works for
	// the fake client (no CEL there), so pin one.
	ag := &stubAgent{
		hyps: agents.Hypotheses{{Summary: "s", ConfidencePercent: 1, Citations: []praxisv1alpha1.EvidenceID{evEvent01}}},
		plan: validPlanSpec(inc, "sha256:"+strings.Repeat("ab", 32)),
	}
	report, err := Respond(context.Background(), c, ag, inc, discard)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Responded() || report.PlanName == "" {
		t.Fatalf("expected a persisted plan, got %+v", report)
	}
	var plan praxisv1alpha1.RemediationPlan
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: report.PlanName}, &plan); err != nil {
		t.Fatalf("created plan not found: %v", err)
	}
	if plan.Spec.IncidentRef.Name != inc.Name {
		t.Errorf("plan.incidentRef = %+v", plan.Spec.IncidentRef)
	}
	if len(report.Bundle.Items) != 1 {
		t.Errorf("bundle has %d items, want 1", len(report.Bundle.Items))
	}
	if report.SanitizedIncident.Labels["praxis.dev/bench-scenario"] != "" {
		t.Error("agent saw the bench-scenario label; sanitization failed")
	}
	if inc.Labels["praxis.dev/bench-scenario"] == "" {
		t.Error("sanitization mutated the runner's own incident copy")
	}
}

func TestRespondAnnotatesNoAction(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(inc).Build()

	ag := &stubAgent{noAction: agents.NoAction{Proposed: true, Reason: "nothing to fix"}}
	report, err := Respond(context.Background(), c, ag, inc, discard)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Responded() || report.PlanName != "" {
		t.Fatalf("expected a no-action response, got %+v", report)
	}
	var cur praxisv1alpha1.Incident
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: inc.Name}, &cur); err != nil {
		t.Fatal(err)
	}
	if got := cur.Annotations[praxisv1alpha1.AnnotationNoActionProposed]; got != "nothing to fix" {
		t.Errorf("annotation = %q, want the agent's reason", got)
	}
}

func TestRespondRecordsSchemaRejection(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	reject := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*praxisv1alpha1.RemediationPlan); ok {
				return apierrors.NewInvalid(
					schema.GroupKind{Group: "praxis.dev", Kind: "RemediationPlan"},
					obj.GetName(), nil)
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(inc).WithInterceptorFuncs(reject).Build()

	ag := &stubAgent{plan: validPlanSpec(inc, "sha256:"+strings.Repeat("cd", 32))}
	report, err := Respond(context.Background(), c, ag, inc, discard)
	if err != nil {
		t.Fatalf("a schema rejection must be a recorded outcome, not an error; got %v", err)
	}
	if report.Responded() {
		t.Error("a rejected plan must not count as a response")
	}
	if report.PlanCreateError == "" || report.PlanSpec == nil {
		t.Errorf("rejection not recorded: %+v", report)
	}
}

func TestRespondEnforcesSeamContract(t *testing.T) {
	now := time.Now()
	cases := map[string]*stubAgent{
		"neither plan nor no-action": {},
		"both plan and no-action": {
			plan:     &praxisv1alpha1.RemediationPlanSpec{},
			noAction: agents.NoAction{Proposed: true, Reason: "but also this"},
		},
	}
	for name, ag := range cases {
		t.Run(name, func(t *testing.T) {
			inc := benchIncident(now.Add(-10 * time.Second))
			c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(inc).Build()
			if _, err := Respond(context.Background(), c, ag, inc, discard); err == nil ||
				!strings.Contains(err.Error(), "seam contract violation") {
				t.Errorf("want a seam contract violation error, got %v", err)
			}
		})
	}
}

func TestGatherBundleFiltersSortsAndCaps(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-30 * time.Second))
	objs := []client.Object{
		inc,
		// Normal events are never evidence for the Phase 2 gatherer.
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "normal", Namespace: nsShop},
			Type:           corev1.EventTypeNormal,
			Reason:         "Pulled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "a-b-c", Namespace: nsShop},
			LastTimestamp:  metav1.Time{Time: now},
		},
		// A stale warning outside the window must be dropped.
		warningEvent("stale", "BackOff", "old-pod-a-b", "ancient history", now.Add(-time.Hour)),
		// These two arrive in anti-alphabetical order; ids must follow the
		// sorted order, not list order.
		warningEvent("w2", "Unhealthy", "zzz-pod-a-b", "Readiness probe failed", now),
		warningEvent("w1", "Failed", "aaa-pod-a-b", "Failed to pull image", now),
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(objs...).Build()

	bundle, err := gatherBundle(context.Background(), c, inc, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Items) != 2 {
		t.Fatalf("bundle has %d items, want 2 (normal + stale filtered): %+v", len(bundle.Items), bundle.Items)
	}
	if bundle.Items[0].ID != "ev/event-01" || bundle.Items[0].Data[evidence.EventDataInvolvedName] != "aaa-pod-a-b" {
		t.Errorf("first item = %+v; ids must be assigned after content sorting", bundle.Items[0])
	}
	if bundle.Items[1].ID != "ev/event-02" || bundle.Items[1].Data[evidence.EventDataReason] != "Unhealthy" {
		t.Errorf("second item = %+v", bundle.Items[1])
	}
	if bundle.Incident.Name != inc.Name || bundle.Incident.UID != string(inc.UID) {
		t.Errorf("bundle.incident = %+v", bundle.Incident)
	}
}

func TestGatherBundleCapsAtSixtyFour(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-30 * time.Second))
	objs := make([]client.Object, 0, 71)
	objs = append(objs, inc)
	for i := range 70 {
		objs = append(objs, warningEvent(
			fmt.Sprintf("w%03d", i), "BackOff", fmt.Sprintf("pod%03d-a-b", i), "Back-off restarting", now))
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(objs...).Build()

	bundle, err := gatherBundle(context.Background(), c, inc, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Items) != maxEventItems {
		t.Errorf("bundle has %d items, want the §6 cap of %d", len(bundle.Items), maxEventItems)
	}
}

func TestSanitizeIncidentStripsBenchMetadata(t *testing.T) {
	inc := benchIncident(time.Now())
	inc.Annotations = map[string]string{
		"praxis.dev/bench-run": "3",
		"unrelated":            "kept",
	}
	s := SanitizeIncident(inc)
	if len(s.Labels) != 0 {
		t.Errorf("labels survived sanitization: %v", s.Labels)
	}
	if s.Annotations["unrelated"] != "kept" || len(s.Annotations) != 1 {
		t.Errorf("annotations after sanitization: %v", s.Annotations)
	}
	if inc.Labels["praxis.dev/bench-scenario"] == "" || inc.Annotations["praxis.dev/bench-run"] == "" {
		t.Error("SanitizeIncident mutated its input")
	}
}
