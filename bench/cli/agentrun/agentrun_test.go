/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"context"
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

// stubAgent lets each test script the seam's behavior. Its plan binds
// itself to whatever bundle it was handed, the way an honest agent must.
type stubAgent struct {
	hyps       agents.Hypotheses
	analyzeErr error
	planErr    error
	plan       *praxisv1alpha1.RemediationPlanSpec
	planHash   string // overrides the bundle hash when set (dishonest agent)
	noAction   agents.NoAction
	seen       *evidence.Bundle
}

func (s *stubAgent) Analyze(_ context.Context, _ *praxisv1alpha1.Incident, b *evidence.Bundle) (agents.Hypotheses, error) {
	s.seen = b
	return s.hyps, s.analyzeErr
}

func (s *stubAgent) Plan(_ context.Context, _ *praxisv1alpha1.Incident, b *evidence.Bundle, _ agents.Hypotheses,
) (*praxisv1alpha1.RemediationPlanSpec, agents.NoAction, error) {
	if s.planErr != nil {
		return nil, agents.NoAction{}, s.planErr
	}
	if s.plan != nil {
		if s.planHash != "" {
			s.plan.EvidenceBundleHash = s.planHash
		} else {
			h, _ := b.Hash()
			s.plan.EvidenceBundleHash = h
		}
	}
	return s.plan, s.noAction, nil
}

func validPlanSpec(inc *praxisv1alpha1.Incident) *praxisv1alpha1.RemediationPlanSpec {
	return &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef: praxisv1alpha1.IncidentRef{Name: inc.Name, UID: inc.UID},
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

// collectorOver is the REAL collector over a fake cluster: the secretless
// Reader wraps the client, exactly as the runner builds it.
func collectorOver(c client.Client) *evidence.Collector {
	return &evidence.Collector{Reader: evidence.NewReader(c), Now: func() time.Time {
		return time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	}}
}

func clusterWith(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(objs...).Build()
}

func hypsCiting(ids ...string) agents.Hypotheses {
	h := praxisv1alpha1.Hypothesis{Summary: "s", ConfidencePercent: 1}
	for _, id := range ids {
		h.Citations = append(h.Citations, praxisv1alpha1.EvidenceID(id))
	}
	return agents.Hypotheses{h}
}

func TestRespondPersistsPlanBoundToTheCollectedBundle(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	c := clusterWith(t, inc, warningEvent("e1", "BackOff", "checkout-api-a-b", "Back-off restarting failed container", now))

	ag := &stubAgent{hyps: hypsCiting(evEvent01), plan: validPlanSpec(inc)}
	report, err := Respond(context.Background(), c, collectorOver(c), ag, inc, discard)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Responded() || report.PlanName == "" || report.Rejection != nil {
		t.Fatalf("expected a persisted plan, got %+v", report)
	}
	var plan praxisv1alpha1.RemediationPlan
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: report.PlanName}, &plan); err != nil {
		t.Fatalf("created plan not found: %v", err)
	}
	if plan.Spec.IncidentRef.Name != inc.Name || plan.Spec.EvidenceBundleHash != report.BundleHash {
		t.Errorf("plan not bound to the collected bundle: ref=%+v hash=%s want %s", plan.Spec.IncidentRef, plan.Spec.EvidenceBundleHash, report.BundleHash)
	}
	// The bundle is the real collector's: one Warning event, real id,
	// canonical hash consistent with the seam's Bundle.Hash.
	if len(report.Bundle.Items) != 1 || report.Bundle.Items[0].ID != evEvent01 || report.Bundle.Items[0].Type != evidence.ItemTypeEvent {
		t.Errorf("bundle = %+v", report.Bundle.Items)
	}
	if h, _ := report.Bundle.Hash(); h != report.BundleHash || report.BundleBytes == 0 {
		t.Error("report hash/bytes disagree with the bundle")
	}
	if ag.seen != report.Bundle {
		t.Error("the agent did not receive the very bundle the collector assembled")
	}
	if report.SanitizedIncident.Labels["praxis.dev/bench-scenario"] != "" {
		t.Error("agent saw the bench-scenario label; sanitization failed")
	}
	if inc.Labels["praxis.dev/bench-scenario"] == "" {
		t.Error("sanitization mutated the runner's own incident copy")
	}
}

func TestRespondAnnotatesNoAction(t *testing.T) {
	inc := benchIncident(time.Now())
	c := clusterWith(t, inc)
	ag := &stubAgent{noAction: agents.NoAction{Proposed: true, Reason: "nothing to fix"}}
	report, err := Respond(context.Background(), c, collectorOver(c), ag, inc, discard)
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
	if _, rejected := cur.Annotations[praxisv1alpha1.AnnotationAnalysisRejected]; rejected {
		t.Error("a no-action verdict must not read as a rejection")
	}
}

func TestRespondRecordsSchemaRejection(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	reject := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*praxisv1alpha1.RemediationPlan); ok {
				return apierrors.NewInvalid(schema.GroupKind{Group: "praxis.dev", Kind: "RemediationPlan"}, obj.GetName(), nil)
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).
		WithObjects(inc, warningEvent("e1", "BackOff", "checkout-api-a-b", "Back-off restarting failed container", now)).
		WithInterceptorFuncs(reject).Build()

	ag := &stubAgent{hyps: hypsCiting(evEvent01), plan: validPlanSpec(inc)}
	report, err := Respond(context.Background(), c, collectorOver(c), ag, inc, discard)
	if err != nil {
		t.Fatalf("a schema rejection must be a recorded outcome, not an error; got %v", err)
	}
	if report.Responded() || report.PlanCreateError == "" || report.PlanSpec == nil {
		t.Errorf("rejection not recorded: %+v", report)
	}
}

func TestRespondEnforcesSeamContract(t *testing.T) {
	now := time.Now()
	cases := map[string]*stubAgent{
		"neither plan nor no-action": {hyps: hypsCiting(evEvent01)},
		"both plan and no-action": {hyps: hypsCiting(evEvent01),
			plan: &praxisv1alpha1.RemediationPlanSpec{}, noAction: agents.NoAction{Proposed: true, Reason: "but also this"}},
		"plan bound to another bundle": {hyps: hypsCiting(evEvent01),
			plan: validPlanSpec(benchIncident(now)), planHash: "sha256:" + strings.Repeat("ab", 32)},
	}
	for name, ag := range cases {
		t.Run(name, func(t *testing.T) {
			inc := benchIncident(now.Add(-10 * time.Second))
			c := clusterWith(t, inc, warningEvent("e1", "BackOff", "p-a-b", "Back-off restarting", now))
			if _, err := Respond(context.Background(), c, collectorOver(c), ag, inc, discard); err == nil ||
				!strings.Contains(err.Error(), "seam contract violation") {
				t.Errorf("want a seam contract violation error, got %v", err)
			}
		})
	}
	inc := benchIncident(now)
	if _, err := Respond(context.Background(), clusterWith(t, inc), nil, &stubAgent{}, inc, discard); err == nil {
		t.Error("Respond without a collector must refuse: the benchmark has no evidence path of its own")
	}
}

// TestRespondRefusesUnresolvedCitationsFromAnyAgent: the harness re-checks
// every citation itself, so an agent that returns a hypothesis citing an
// id the bundle does not hold — whatever the agent claims — produces no
// plan, an AnalysisRejected record, and the Incident annotation.
func TestRespondRefusesUnresolvedCitationsFromAnyAgent(t *testing.T) {
	now := time.Now()
	inc := benchIncident(now.Add(-10 * time.Second))
	c := clusterWith(t, inc, warningEvent("e1", "BackOff", "checkout-api-a-b", "Back-off restarting failed container", now))

	ag := &stubAgent{hyps: hypsCiting(evEvent01, "ev/event-09"), plan: validPlanSpec(inc)}
	report, err := Respond(context.Background(), c, collectorOver(c), ag, inc, discard)
	if err != nil {
		t.Fatalf("a refused analysis is a recorded outcome, not an error: %v", err)
	}
	if report.Rejection == nil || report.Rejection.Reason != praxisv1alpha1.ReasonCitationInvalid {
		t.Fatalf("rejection = %+v", report.Rejection)
	}
	if !strings.Contains(report.Rejection.Detail, "ev/event-09") || report.Responded() || report.PlanName != "" {
		t.Errorf("report = %+v", report)
	}
	if len(report.Hypotheses) != 0 || len(report.RefusedHypotheses) != 1 {
		t.Error("refused hypotheses must not be reported as valid ones")
	}
	var plans praxisv1alpha1.RemediationPlanList
	if err := c.List(context.Background(), &plans, client.InNamespace(nsShop)); err != nil || len(plans.Items) != 0 {
		t.Errorf("a plan was created for a refused analysis: %d plans, err %v", len(plans.Items), err)
	}
	var cur praxisv1alpha1.Incident
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: inc.Name}, &cur); err != nil {
		t.Fatal(err)
	}
	if got := cur.Annotations[praxisv1alpha1.AnnotationAnalysisRejected]; !strings.HasPrefix(got, "CitationInvalid: ") || !strings.Contains(got, "ev/event-09") {
		t.Errorf("annotation = %q", got)
	}
}

// TestRespondRecordsAgentRefusals: an agent's own first-class refusals
// land as the same outcome, with their reason.
func TestRespondRecordsAgentRefusals(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		agent      *stubAgent
		wantReason string
	}{
		"citation error from Analyze": {
			agent:      &stubAgent{analyzeErr: &agents.CitationError{Unresolved: []praxisv1alpha1.EvidenceID{"ev/metric-07"}}},
			wantReason: praxisv1alpha1.ReasonCitationInvalid,
		},
		"schema error from Plan": {
			agent:      &stubAgent{hyps: hypsCiting(evEvent01), planErr: &agents.SchemaError{Attempts: 2, Violations: []string{"actions[0].type"}}},
			wantReason: praxisv1alpha1.ReasonSchemaInvalid,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inc := benchIncident(now.Add(-10 * time.Second))
			c := clusterWith(t, inc, warningEvent("e1", "BackOff", "p-a-b", "Back-off restarting", now))
			report, err := Respond(context.Background(), c, collectorOver(c), tc.agent, inc, discard)
			if err != nil {
				t.Fatalf("refusals are outcomes, not errors: %v", err)
			}
			if report.Rejection == nil || report.Rejection.Reason != tc.wantReason || report.Responded() {
				t.Errorf("report = %+v", report)
			}
			var cur praxisv1alpha1.Incident
			_ = c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: inc.Name}, &cur)
			if !strings.HasPrefix(cur.Annotations[praxisv1alpha1.AnnotationAnalysisRejected], tc.wantReason+": ") {
				t.Errorf("annotation = %q", cur.Annotations[praxisv1alpha1.AnnotationAnalysisRejected])
			}
		})
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
