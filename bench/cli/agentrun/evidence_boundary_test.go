/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	llmagent "github.com/akansh23-cloud/praxis/internal/agents/llm"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
	"github.com/akansh23-cloud/praxis/internal/hash"
	"github.com/akansh23-cloud/praxis/internal/llm/llmtest"
)

// The safe evidence boundary of playbook Session 3.3 task 8, proven
// end-to-end through the benchmark's own path: a cluster with planted
// credentials in every channel the collector reads, raw log lines behind
// the Loki seam, the REAL collector, the REAL LLM agent over a scripted
// model, and the question "what did the model actually receive?" answered
// over the recorded prompts, byte for byte. Fixtures are the obviously
// fake shapes of internal/evidence/redact_test.go; assertions name them
// symbolically and never print a value or a prompt.

var planted = map[string]string{
	"aws-access-key": "AKIAIOSFODNN7EXAMPLE",
	"aws-secret-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	"url-password":   "hunter2-fake-pass",
	"env-value":      "literal-env-value-FAKE-9f8e7d",
	"pem-body":       "MIIEfakefakefakeFAKE0123456789fakefakefakeFAKEfakefakefakeFAKE1234",
	"jwt": base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"praxis-bench"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("fake-signature-FAKE")),
}

const ctrAPI = "api"

func fingerprint(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("len=%d sha256=%x…", len(v), sum[:4])
}

// fakeLoki serves raw lines for the shop namespace and records queries.
type fakeLoki struct {
	lines   []string
	queries []string
}

func (f *fakeLoki) QueryRange(_ context.Context, logql string, _, _ time.Time, _ int) ([]logs.Stream, error) {
	f.queries = append(f.queries, logql)
	return []logs.Stream{{
		Labels: map[string]string{"namespace": nsShop, "container": ctrAPI, "pod": "checkout-api-7d9c6f5b4-x2m8q"},
		Lines:  f.lines,
	}}, nil
}

// noiseLines are hundreds of distinct raw lines of one shape: at Loki they
// are raw, in the bundle they must be ONE template with one exemplar.
func noiseLines(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("I0912 18:00:%02d.%06d       1 logs_generator.go:76] %d GET /api/v1/namespaces/ns/pods/p%d %d", i%60, i*7919%1000000, i, i%9, 200+i%3))
	}
	return out
}

// adversarialCluster is a shop namespace with a credential in every
// channel: change-cause annotation, pod env, kubelet waiting message,
// Event message.
func adversarialCluster(t *testing.T, inc *praxisv1alpha1.Incident) client.Client {
	t.Helper()
	credURL := "postgres://shopadmin:" + planted["url-password"] + "@db.shop.svc:5432/orders"
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api", Namespace: nsShop, UID: "d1",
			Annotations: map[string]string{
				"kubernetes.io/change-cause": "kubectl set env deploy/checkout-api DATABASE_URL=" + credURL +
					" AWS_ACCESS_KEY_ID=" + planted["aws-access-key"] + " --record",
				praxisv1alpha1.AnnotationCommit: "4be1f2a9c31d",
			}},
		Spec: appsv1.DeploymentSpec{Replicas: ptr(int32(2))},
	}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "checkout-api-7d9c6f5b4", Namespace: nsShop, UID: "rs1"}}
	if err := controllerutil.SetControllerReference(deploy, rs, newScheme(t)); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api-7d9c6f5b4-x2m8q", Namespace: nsShop, UID: "p1"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: ctrAPI, Image: "registry.k8s.io/e2e-test-images/agnhost:2.53",
			Env: []corev1.EnvVar{
				{Name: "DB_PASSWORD", Value: planted["env-value"]},
				{Name: "AWS_SECRET_ACCESS_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "aws-creds-object"}, Key: "secret"}}},
			},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: ctrAPI, RestartCount: 3,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff",
				Message: "back-off 40s restarting failed container; last log: key " + planted["aws-secret-key"]}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
		}}},
	}
	if err := controllerutil.SetControllerReference(rs, pod, newScheme(t)); err != nil {
		t.Fatal(err)
	}
	event := warningEvent("unhealthy", "Unhealthy", pod.Name,
		"Liveness probe failed: curl -H 'Authorization: Bearer "+planted["jwt"]+"' returned 401", time.Now())
	return clusterWith(t, inc, deploy, rs, pod, event)
}

func ptr[T any](v T) *T { return &v }

const (
	scriptedHypotheses = `{"hypotheses":[{"summary":"checkout-api was OOMKilled after its memory limit was lowered","confidencePercent":85,"citations":["ev/podstatus-01","ev/gitcommit-01"]}]}`
	scriptedPlan       = `{"verdict":"plan","noActionReason":"","plan":{
	  "actions":[{"type":"PatchResourceLimits","target":{"kind":"Deployment","namespace":"shop","name":"checkout-api"},
	    "patchResourceLimits":{"container":"api","memory":{"from":"64Mi","to":"128Mi"}}}],
	  "verification":{"predicate":"kube_deployment_status_replicas_available{namespace=\"shop\",deployment=\"checkout-api\"} >= 2","window":"10m","onFailure":"Rollback"},
	  "rollback":{"strategy":"RestorePreviousSpec"}}}`
)

// TestLLMPathUsesTheRealRedactedEvidence is the carry-forward proof: the
// benchmark's LLM path consumes exactly what the Phase 3 pipeline
// produces, and nothing the pipeline removes reaches the model.
func TestLLMPathUsesTheRealRedactedEvidence(t *testing.T) {
	inc := benchIncident(time.Now().Add(-10 * time.Second))
	c := adversarialCluster(t, inc)
	raw := append(noiseLines(300),
		"export AWS_SECRET_ACCESS_KEY="+planted["aws-secret-key"],
		"-----BEGIN RSA PRIVATE KEY-----", planted["pem-body"], "-----END RSA PRIVATE KEY-----",
		"Authorization: Bearer "+planted["jwt"]+" rejected",
	)
	loki := &fakeLoki{lines: raw}
	collector := collectorOver(c)
	collector.Logs = loki
	model := llmtest.New(scriptedHypotheses, scriptedPlan)

	report, err := Respond(context.Background(), c, collector, llmagent.New(model), inc, discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rejection != nil || report.PlanName == "" {
		t.Fatalf("expected a persisted plan, got %+v", report)
	}

	// 1. The bundle is the real pipeline's: canonical, capped, hashed.
	if got := hash.SHA256Prefixed(mustCanonical(t, report.Bundle)); got != report.BundleHash {
		t.Error("the report's hash is not the canonical hash of the bundle the agent saw")
	}
	if len(report.Bundle.Items) > evidence.MaxItems || report.BundleBytes > evidence.MaxBundleBytes {
		t.Errorf("caps violated: %d items, %d bytes", len(report.Bundle.Items), report.BundleBytes)
	}
	if !strings.HasPrefix(loki.queries[0], `{namespace="shop"}`) || len(loki.queries) != 1 {
		t.Errorf("loki queries = %q", loki.queries)
	}

	seen := modelInputs(model)
	assertNoPlantedInModelInputs(t, seen)
	assertOnlyTemplatesReachedModel(t, seen, report, raw[:300])

	// 4. The persisted plan is bound to that bundle and carries the audit
	// annotations; the Incident carries no rejection.
	var plan praxisv1alpha1.RemediationPlan
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: nsShop, Name: report.PlanName}, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Spec.EvidenceBundleHash != report.BundleHash {
		t.Error("the persisted plan cites a different bundle than the model analyzed")
	}
	if !strings.HasPrefix(plan.Annotations[praxisv1alpha1.AnnotationPromptHash], "sha256:") ||
		plan.Annotations[praxisv1alpha1.AnnotationModel] != "fake/fake-model" {
		t.Errorf("plan annotations = %v", plan.Annotations)
	}
	if report.Usage == nil || report.Usage.Calls != 2 {
		t.Errorf("usage not recorded: %+v", report.Usage)
	}
}

func mustCanonical(t *testing.T, b *evidence.Bundle) []byte {
	t.Helper()
	raw, err := hash.CanonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// modelInputs concatenates everything the scripted model received.
func modelInputs(model *llmtest.Fake) string {
	var inputs strings.Builder
	for _, call := range model.Calls {
		inputs.WriteString(call.System)
		inputs.WriteString(call.User)
	}
	return inputs.String()
}

// assertNoPlantedInModelInputs: every planted credential is absent from
// the model's inputs, every expected redaction marker is present, and env
// variables travelled as names only.
func assertNoPlantedInModelInputs(t *testing.T, seen string) {
	t.Helper()
	for name, secret := range planted {
		if strings.Contains(seen, secret) {
			t.Errorf("planted %s (%s) reached the model", name, fingerprint(secret))
		}
	}
	for _, leak := range []string{"aws-creds-object", "shopadmin:"} {
		if strings.Contains(seen, leak) {
			t.Errorf("%q reached the model", leak)
		}
	}
	for _, marker := range []string{"«redacted:url-credentials»", "«redacted:aws-access-key»", "«redacted:jwt»", "«redacted:opaque-token»"} {
		if !strings.Contains(seen, marker) {
			t.Errorf("marker %s absent from the model input: the redaction path was not used", marker)
		}
	}
	if !strings.Contains(seen, `"container.api.env":"DB_PASSWORD,AWS_SECRET_ACCESS_KEY(secretKeyRef)"`) {
		t.Error("env names did not reach the model as names-only")
	}
}

// assertOnlyTemplatesReachedModel: raw log lines never reach the model —
// only LogTemplate items do, and of the distinct noise lines only one
// exemplar may appear.
func assertOnlyTemplatesReachedModel(t *testing.T, seen string, report *Report, noise []string) {
	t.Helper()
	exemplars := map[string]bool{}
	for _, it := range report.Bundle.Items {
		if it.Type == evidence.ItemTypeLogTemplate {
			exemplars[it.Data[evidence.LogDataExemplar]] = true
		}
	}
	if len(exemplars) == 0 {
		t.Fatal("no LogTemplate evidence reached the bundle")
	}
	crossed := 0
	for _, line := range noise {
		if !exemplars[line] && strings.Contains(seen, line) {
			crossed++
		}
	}
	if crossed > 0 {
		t.Errorf("%d raw noise lines reached the model", crossed)
	}
	if !strings.Contains(seen, `"type":"LogTemplate"`) || !strings.Contains(seen, fmt.Sprintf(`"count":"%d"`, len(noise))) {
		t.Errorf("the model did not receive the %d noise lines as one LogTemplate item", len(noise))
	}
}
