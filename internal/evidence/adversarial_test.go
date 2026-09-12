/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
	"github.com/akansh23-cloud/praxis/internal/hash"
)

// The adversarial matrix of playbook Session 3.2 task 3, driven through
// the REAL composition — Collector.Collect over the secretless Reader, the
// Prometheus seam and the Loki seam — with every required credential
// class planted in every telemetry channel the collector is allowed to
// read:
//
//	channel                      planted
//	----------------------------------------------------------------
//	Deployment change-cause      URL credentials, AWS access key, PASSWORD= assignment
//	Pod container env            literal value, Secret-backed valueFrom
//	Pod waiting message          AWS secret key (opaque token)
//	Event message                Bearer JWT, PEM block
//	Prometheus error             URL credentials
//	Loki template + exemplar     Bearer JWT (constant across lines)
//	Loki template + exemplar     AWS access key (constant), varying ids
//	Loki lines                   PEM block spread over several lines
//	Loki lines                   DATABASE_URL= with credentials
//	Loki lines                   AWS_SECRET_ACCESS_KEY= assignment
//
// The assertion is over the COMPLETE canonical bundle bytes — the exact
// bytes the ConfigMap stores and the hash covers — not over chosen
// fields. Failure messages name fixtures symbolically; no assertion
// helper prints a planted value or the bundle.

const plantedPod = "checkout-api-7d9c6f5b4-plant"

func adversarialIncident() *praxisv1alpha1.Incident {
	ref := testIncidentRef()
	return &praxisv1alpha1.Incident{
		ObjectMeta: metav1.ObjectMeta{Name: ref.Name, UID: types.UID(ref.UID)},
		Spec: praxisv1alpha1.IncidentSpec{
			Scope: praxisv1alpha1.IncidentScope{Namespaces: []string{goldenNS}},
		},
	}
}

// adversarialReader is oomFixture plus strictly ADDED objects carrying
// the planted credentials, so every clean item of the oom fixture must
// reappear untouched next to the redacted ones.
func adversarialReader() *fakeReader {
	r := oomFixture()
	r.pods[goldenNS] = append(r.pods[goldenNS], corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: plantedPod, Namespace: goldenNS},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: ctrSessionCache,
			Env: []corev1.EnvVar{
				{Name: "DB_PASSWORD", Value: plantedAssignment},
				{Name: "AWS_SECRET_ACCESS_KEY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "aws-creds"}, Key: "secret",
					}}},
			},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: ctrSessionCache,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CrashLoopBackOff",
					Message: "back-off 40s restarting failed container; last log: key " + plantedAWSSecret,
				}},
			}},
		},
	})
	r.events[goldenNS] = append(r.events[goldenNS], corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "unhealthy-ev", Namespace: goldenNS},
		Type:       corev1.EventTypeWarning, Reason: "Unhealthy",
		Message:        "Liveness probe failed: curl -H 'Authorization: Bearer " + plantedJWT + "' returned 401; client cert:\n" + plantedPEM,
		InvolvedObject: corev1.ObjectReference{Kind: kindPodTest, Name: plantedPod, Namespace: goldenNS},
		Count:          2,
	})
	r.deployments[goldenNS] = append(r.deployments[goldenNS], appsDeployment("planted-deploy", map[string]string{
		changeCauseAnnotation: "kubectl set env deploy/planted-deploy DATABASE_URL=" + plantedURL +
			" AWS_ACCESS_KEY_ID=" + plantedAWSKey + " PASSWORD=" + plantedAssignment + " --record",
		praxisv1alpha1.AnnotationCommit: goldenCommit,
	}))
	return r
}

// adversarialProm is promFixture with the SLO query's error carrying a
// credentialed Prometheus URL — the one clean item this changes.
func adversarialProm() *fakeProm {
	f := promFixture()
	f.errors[instantiated("slo-error-budget-burn")] = fmt.Errorf(
		"Get \"http://%s:%s@prometheus.monitoring:9090/api/v1/query\": connection refused", plantedURLUser, plantedURLPass)
	return f
}

// cleanLogs is the log fixture without any planted line.
func cleanLogs() *fakeLogs {
	return &fakeLogs{streams: map[string][]logs.Stream{shopQuery: {
		stream(ctrNoise, podCheckout1, noiseLines(0, 50)...),
		stream(ctrSessionCache, podCheckout1, "fatal error: runtime: out of memory"),
	}}}
}

// adversarialLogs adds the planted lines to cleanLogs as a separate
// container's stream: repeated lines make the credential part of the
// TEMPLATE (not just the exemplar), a varying suffix keeps a constant
// key in a template with wildcards, and a PEM block arrives the way
// promtail delivers one — one line per row.
func adversarialLogs() *fakeLogs {
	f := cleanLogs()
	lines := make([]string, 0, 16)
	for i := range 3 {
		lines = append(lines,
			"Authorization: Bearer "+plantedJWT+" rejected",
			fmt.Sprintf("loaded aws key %s for request %d", plantedAWSKey, i),
			"DATABASE_URL="+plantedURL,
			"export AWS_SECRET_ACCESS_KEY="+plantedAWSSecret,
		)
	}
	lines = append(lines, "-----BEGIN RSA PRIVATE KEY-----", plantedPEMBody, plantedPEMBody+"AB", "-----END RSA PRIVATE KEY-----")
	f.streams[shopQuery] = append(f.streams[shopQuery], stream(ctrCheckout, plantedPod, lines...))
	return f
}

func collectWith(t *testing.T, r Reader, p QueryClient, l logs.Client) *CollectionResult {
	t.Helper()
	c := &Collector{Reader: r, Prom: p, Logs: l, Now: func() time.Time { return fixedCollectedAt }}
	res, err := c.Collect(context.Background(), adversarialIncident())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return res
}

// TestAdversarialBundleHoldsNoPlantedCredential is the Session 3.2
// definition of done at unit level: after the real composition, no
// planted credential survives anywhere in the complete canonical bytes,
// every expected marker is present, the touched items are flagged, the
// clean items are byte-identical to a clean collection, the bytes are
// stable across collections and pinned by a golden file, and the hash is
// the sha256 of exactly those bytes.
func TestAdversarialBundleHoldsNoPlantedCredential(t *testing.T) {
	res := collectWith(t, adversarialReader(), adversarialProm(), adversarialLogs())
	raw := string(res.Raw)

	assertPlantedAbsent(t, "complete canonical bundle bytes", raw)
	for _, it := range res.Bundle.Items {
		for k, v := range it.Data {
			assertPlantedAbsent(t, "item "+it.ID+" field "+k, v)
		}
	}
	assertMarkersPresent(t, raw)
	assertChannelsRedacted(t, res.Bundle.Items)

	// Legitimate change context survives next to the scrubbed value.
	if !strings.Contains(raw, `"commit":"`+goldenCommit+`"`) {
		t.Error("the GitCommit sha was lost while scrubbing its annotation neighbour")
	}
	// Env NAMES survive, the literal value and the Secret reference do not.
	if !strings.Contains(raw, `"container.session-cache.env":"DB_PASSWORD,AWS_SECRET_ACCESS_KEY(secretKeyRef)"`) {
		t.Error("env variable names are not represented as names-only")
	}
	if strings.Contains(raw, "aws-creds") {
		t.Error("the referenced Secret's name leaked into the bundle")
	}

	// The note reports exactly the flagged count; the flag is exact.
	flagged := redactedCount(res.Bundle)
	if flagged == 0 {
		t.Fatal("no item flagged redacted")
	}
	if wantNote := fmt.Sprintf("%d item(s) redacted", flagged); !containsNote(res.Notes, wantNote) {
		t.Errorf("notes %q lack %q", res.Notes, wantNote)
	}
	if !containsNote(res.Notes, "loki: ") {
		t.Errorf("notes %q lack the loki summary", res.Notes)
	}

	// Bundle-level idempotence: scrubbing the assembled values again
	// changes nothing.
	for _, it := range res.Bundle.Items {
		for k, v := range it.Data {
			if again, changed := Scrub(v); changed || again != v {
				t.Errorf("item %s field %q is not a scrub fixpoint", it.ID, k)
			}
		}
	}
	assertAdversarialStable(t, res)
}

// assertMarkersPresent: every class's marker appears in the bytes (the
// planted bearer tokens are JWTs and report as such, so bearer-token is
// covered by TestScrubClasses instead).
func assertMarkersPresent(t *testing.T, raw string) {
	t.Helper()
	for _, kind := range []string{
		RedactedURLCredentials, RedactedAWSAccessKey, RedactedJWT, RedactedPEMBlock,
		RedactedOpaqueToken, RedactedCredentialAssignment,
	} {
		if !strings.Contains(raw, RedactionMarker(kind)) {
			t.Errorf("marker %s missing from the bundle", RedactionMarker(kind))
		}
	}
}

// assertChannelsRedacted: each planted channel produced an item of the
// expected type whose field carries the expected marker, flagged redacted.
func assertChannelsRedacted(t *testing.T, items []Item) {
	t.Helper()
	channels := []struct {
		name   string
		typ    ItemType
		field  string
		marker string
	}{
		{"annotation change-cause: url", ItemTypeGitCommit, CommitDataChangeCause, RedactionMarker(RedactedURLCredentials)},
		{"annotation change-cause: aws key", ItemTypeGitCommit, CommitDataChangeCause, RedactionMarker(RedactedAWSAccessKey)},
		{"annotation change-cause: assignment", ItemTypeGitCommit, CommitDataChangeCause, RedactionMarker(RedactedCredentialAssignment)},
		{"pod waiting message: opaque secret", ItemTypePodStatus, "container.session-cache.waitingMessage", RedactionMarker(RedactedOpaqueToken)},
		{"event message: jwt", ItemTypeEvent, EventDataMessage, RedactionMarker(RedactedJWT)},
		{"event message: pem", ItemTypeEvent, EventDataMessage, markerPEM},
		{"prometheus error: url", ItemTypeMetric, MetricDataError, RedactionMarker(RedactedURLCredentials)},
		{"log template: jwt", ItemTypeLogTemplate, LogDataTemplate, RedactionMarker(RedactedJWT)},
		{"log exemplar: jwt", ItemTypeLogTemplate, LogDataExemplar, RedactionMarker(RedactedJWT)},
		{"log template: aws key with wildcard", ItemTypeLogTemplate, LogDataTemplate, RedactionMarker(RedactedAWSAccessKey) + " for request <*>"},
		{"log exemplar: url", ItemTypeLogTemplate, LogDataExemplar, "DATABASE_URL=postgres://" + RedactionMarker(RedactedURLCredentials) + "@"},
		{"log exemplar: pem body line", ItemTypeLogTemplate, LogDataExemplar, RedactionMarker(RedactedOpaqueToken)},
		{"log exemplar: pem header", ItemTypeLogTemplate, LogDataExemplar, markerPEM},
	}
	for _, ch := range channels {
		found := false
		for _, it := range items {
			if it.Type == ch.typ && strings.Contains(it.Data[ch.field], ch.marker) {
				found = true
				if !it.Redacted {
					t.Errorf("channel %q: item %s carries a marker but is not flagged redacted", ch.name, it.ID)
				}
			}
		}
		if !found {
			t.Errorf("channel %q: no %s item has %q in field %q", ch.name, ch.typ, ch.marker, ch.field)
		}
	}
}

// assertAdversarialStable: a second collection of the same fixtures is
// byte-identical, the hash is the sha256 of the bytes, the golden file
// pins them (and itself holds no planted value), and the caps hold.
func assertAdversarialStable(t *testing.T, res *CollectionResult) {
	t.Helper()
	again := collectWith(t, adversarialReader(), adversarialProm(), adversarialLogs())
	if !bytes.Equal(again.Raw, res.Raw) || again.Hash != res.Hash {
		t.Error("two collections of identical adversarial fixtures differ")
	}
	if want := hash.SHA256Prefixed(res.Raw); res.Hash != want {
		t.Errorf("hash %s is not the sha256 of the bytes (%s)", res.Hash, want)
	}
	golden := filepath.Join("testdata", "adversarial_bundle.json")
	if *update {
		if err := os.WriteFile(golden, res.Raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden file (run with -update to create it): %v", err)
	}
	assertPlantedAbsent(t, "committed golden file "+golden, string(want))
	if !bytes.Equal(res.Raw, want) {
		t.Errorf("adversarial bundle drifted from %s: got %d bytes %s, golden %d bytes %s",
			golden, len(res.Raw), res.Hash, len(want), hash.SHA256Prefixed(want))
	}
	if len(res.Bundle.Items) > MaxItems || len(res.Raw) > MaxBundleBytes {
		t.Errorf("caps violated: %d items, %d bytes", len(res.Bundle.Items), len(res.Raw))
	}
}

// TestAdversarialLeavesCleanItemsUntouched: every item of a clean
// collection reappears, byte for byte and unflagged, in the adversarial
// collection — scrubbing touches only what it must. (The SLO metric is
// the one item the adversarial Prometheus fixture deliberately changes.)
func TestAdversarialLeavesCleanItemsUntouched(t *testing.T) {
	clean := collectWith(t, oomFixture(), promFixture(), cleanLogs())
	dirty := collectWith(t, adversarialReader(), adversarialProm(), adversarialLogs())

	for _, want := range clean.Bundle.Items {
		if want.Redacted {
			t.Errorf("clean collection flagged %s redacted", want.ID)
		}
		if want.Data[MetricDataTemplate] == "slo-error-budget-burn" {
			continue
		}
		found := false
		for _, got := range dirty.Bundle.Items {
			if got.Type == want.Type && got.Source == want.Source && reflect.DeepEqual(got.Data, want.Data) {
				found = true
				if got.Redacted {
					t.Errorf("clean item %s (%s) is flagged redacted in the adversarial bundle", want.ID, want.Type)
				}
			}
		}
		if !found {
			t.Errorf("clean item %s (%s) is missing or altered in the adversarial bundle", want.ID, want.Type)
		}
	}
	assertPlantedAbsent(t, "clean bundle bytes", string(clean.Raw))
	if strings.Contains(string(clean.Raw), "«redacted:") {
		t.Error("a clean collection carries a redaction marker")
	}
}

func containsNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}
