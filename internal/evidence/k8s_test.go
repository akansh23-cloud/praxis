/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// fakeReader is an in-memory Reader; a namespace mapped in errs fails
// every read, proving collector errors propagate instead of yielding a
// silently partial bundle.
type fakeReader struct {
	pods         map[string][]corev1.Pod
	events       map[string][]corev1.Event
	deployments  map[string][]appsv1.Deployment
	replicaSets  map[string][]appsv1.ReplicaSet
	statefulSets map[string][]appsv1.StatefulSet
	daemonSets   map[string][]appsv1.DaemonSet
	errs         map[string]error

	queried []string // namespaces seen, for scope assertions
}

func (f *fakeReader) fail(ns string) error { return f.errs[ns] }

func (f *fakeReader) ListPods(_ context.Context, ns string) ([]corev1.Pod, error) {
	f.queried = append(f.queried, ns)
	return f.pods[ns], f.fail(ns)
}

func (f *fakeReader) ListEvents(_ context.Context, ns string) ([]corev1.Event, error) {
	f.queried = append(f.queried, ns)
	return f.events[ns], f.fail(ns)
}

func (f *fakeReader) ListDeployments(_ context.Context, ns string) ([]appsv1.Deployment, error) {
	f.queried = append(f.queried, ns)
	return f.deployments[ns], f.fail(ns)
}

func (f *fakeReader) ListReplicaSets(_ context.Context, ns string) ([]appsv1.ReplicaSet, error) {
	f.queried = append(f.queried, ns)
	return f.replicaSets[ns], f.fail(ns)
}

func (f *fakeReader) ListStatefulSets(_ context.Context, ns string) ([]appsv1.StatefulSet, error) {
	f.queried = append(f.queried, ns)
	return f.statefulSets[ns], f.fail(ns)
}

func (f *fakeReader) ListDaemonSets(_ context.Context, ns string) ([]appsv1.DaemonSet, error) {
	f.queried = append(f.queried, ns)
	return f.daemonSets[ns], f.fail(ns)
}

func controllerRef(kind, name string) metav1.OwnerReference {
	yes := true
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: &yes}
}

func int32ptr(v int32) *int32 { return &v }

// appsDeployment builds a minimal shop Deployment carrying annotations.
func appsDeployment(name string, annotations map[string]string) appsv1.Deployment {
	return appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: goldenNS, Annotations: annotations,
	}}
}

// oomFixture is the shop namespace mid-oomkill: a crash-looping pod owned
// by a Deployment through a ReplicaSet, the deployment carrying the
// change annotations the fault stamped, plus one Warning and one Normal
// event.
func oomFixture() *fakeReader {
	return &fakeReader{
		pods: map[string][]corev1.Pod{goldenNS: {{
			ObjectMeta: metav1.ObjectMeta{
				Name: goldenPod, Namespace: goldenNS,
				OwnerReferences: []metav1.OwnerReference{controllerRef(kindReplicaSet, goldenRS)},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: ctrSessionCache, Image: "registry.k8s.io/e2e-test-images/agnhost:2.53",
					Ready: false, RestartCount: 4,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
						Reason: "CrashLoopBackOff", Message: "back-off 1m20s restarting failed container",
					}},
					LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						Reason: "OOMKilled", ExitCode: 137,
					}},
				}},
			},
		}}},
		events: map[string][]corev1.Event{goldenNS: {
			{
				ObjectMeta: metav1.ObjectMeta{Name: "backoff-ev", Namespace: goldenNS},
				Type:       corev1.EventTypeWarning, Reason: reasonBackOff,
				Message: "Back-off restarting failed container",
				InvolvedObject: corev1.ObjectReference{
					Kind: kindPodTest, Name: goldenPod, Namespace: goldenNS,
				},
				Count: 6,
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pulled-ev", Namespace: goldenNS},
				Type:       corev1.EventTypeNormal, Reason: "Pulled",
				Message:        "Successfully pulled image",
				InvolvedObject: corev1.ObjectReference{Kind: kindPodTest, Name: goldenPod, Namespace: goldenNS},
			},
		}},
		deployments: map[string][]appsv1.Deployment{goldenNS: {{
			ObjectMeta: metav1.ObjectMeta{
				Name: goldenDeploy, Namespace: goldenNS,
				Annotations: map[string]string{
					changeCauseAnnotation:            goldenCause,
					praxisv1alpha1.AnnotationCommit:  goldenCommit,
					"deployment.kubernetes.io/other": "unrelated",
				},
			},
			Spec:   appsv1.DeploymentSpec{Replicas: int32ptr(2)},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
		}}},
		replicaSets: map[string][]appsv1.ReplicaSet{goldenNS: {{
			ObjectMeta: metav1.ObjectMeta{
				Name: goldenRS, Namespace: goldenNS,
				OwnerReferences: []metav1.OwnerReference{controllerRef(kindDeployment, goldenDeploy)},
			},
		}}},
	}
}

func itemsOf(t *testing.T, items []Collected, typ ItemType) []Collected {
	t.Helper()
	var out []Collected
	for _, it := range items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}

func TestCollectKubernetesOOMScenario(t *testing.T) {
	items, err := CollectKubernetes(context.Background(), oomFixture(), []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}

	pod := itemsOf(t, items, ItemTypePodStatus)
	if len(pod) != 1 {
		t.Fatalf("got %d PodStatus items, want 1", len(pod))
	}
	wantPod := map[string]string{
		DataNamespace:                            goldenNS,
		DataName:                                 goldenPod,
		PodDataPhase:                             "Running",
		PodDataReady:                             valFalse,
		"container.session-cache.image":          "registry.k8s.io/e2e-test-images/agnhost:2.53",
		"container.session-cache.ready":          valFalse,
		"container.session-cache.restartCount":   "4",
		"container.session-cache.state":          stateCrashLoop,
		"container.session-cache.waitingMessage": "back-off 1m20s restarting failed container",
		"container.session-cache.lastTerminated": stateOOMKilled,
	}
	for k, want := range wantPod {
		if got := pod[0].Data[k]; got != want {
			t.Errorf("PodStatus[%q] = %q, want %q", k, got, want)
		}
	}
	if pod[0].Source != SourceK8s || pod[0].Key != goldenPodKey {
		t.Errorf("PodStatus source/key = %q/%q", pod[0].Source, pod[0].Key)
	}

	chains := itemsOf(t, items, ItemTypeOwnerChain)
	if len(chains) != 1 {
		t.Fatalf("got %d OwnerChain items, want 1", len(chains))
	}
	if got := chains[0].Data[OwnerDataChain]; got != goldenChain {
		t.Errorf("chain = %q, want %q", got, goldenChain)
	}
	if chains[0].Data[OwnerDataWorkloadKind] != kindDeployment ||
		chains[0].Data[OwnerDataWorkloadName] != goldenDeploy {
		t.Errorf("chain workload = %s/%s, want Deployment/checkout-api",
			chains[0].Data[OwnerDataWorkloadKind], chains[0].Data[OwnerDataWorkloadName])
	}
	if chains[0].Data[OwnerDataDesiredReplicas] != "2" || chains[0].Data[OwnerDataReadyReplicas] != "1" {
		t.Errorf("chain replicas = %s desired / %s ready, want 2/1",
			chains[0].Data[OwnerDataDesiredReplicas], chains[0].Data[OwnerDataReadyReplicas])
	}

	events := itemsOf(t, items, ItemTypeEvent)
	if len(events) != 1 {
		t.Fatalf("got %d Event items, want 1 (the Normal event must be skipped)", len(events))
	}
	if events[0].Data[EventDataReason] != reasonBackOff || events[0].Data[EventDataCount] != "6" {
		t.Errorf("event data = %v", events[0].Data)
	}

	commits := itemsOf(t, items, ItemTypeGitCommit)
	if len(commits) != 1 {
		t.Fatalf("got %d GitCommit items, want 1", len(commits))
	}
	if commits[0].Data[CommitDataCommit] != goldenCommit ||
		commits[0].Data[CommitDataChangeCause] != goldenCause {
		t.Errorf("commit data = %v", commits[0].Data)
	}
	if commits[0].Key != "shop/Deployment/checkout-api" {
		t.Errorf("commit key = %q", commits[0].Key)
	}
}

// TestCollectKubernetesInventsNoHistory: workloads without change
// annotations produce no GitCommit evidence at all.
func TestCollectKubernetesInventsNoHistory(t *testing.T) {
	r := &fakeReader{
		deployments: map[string][]appsv1.Deployment{goldenNS: {{
			ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: goldenNS,
				Annotations: map[string]string{"some.other/annotation": "x"}},
		}}},
		statefulSets: map[string][]appsv1.StatefulSet{goldenNS: {{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: goldenNS},
		}}},
	}
	items, err := CollectKubernetes(context.Background(), r, []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	if commits := itemsOf(t, items, ItemTypeGitCommit); len(commits) != 0 {
		t.Errorf("collector invented commit history: %v", commits)
	}
}

// TestCollectKubernetesPartialAnnotations: either annotation alone is
// enough for an item, and only the present keys appear.
func TestCollectKubernetesPartialAnnotations(t *testing.T) {
	r := &fakeReader{
		deployments: map[string][]appsv1.Deployment{goldenNS: {
			{ObjectMeta: metav1.ObjectMeta{Name: "cause-only", Namespace: goldenNS,
				Annotations: map[string]string{changeCauseAnnotation: "hotfix rollout"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "commit-only", Namespace: goldenNS,
				Annotations: map[string]string{praxisv1alpha1.AnnotationCommit: "abc123def456"}}},
		}},
	}
	items, err := CollectKubernetes(context.Background(), r, []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	commits := itemsOf(t, items, ItemTypeGitCommit)
	if len(commits) != 2 {
		t.Fatalf("got %d GitCommit items, want 2", len(commits))
	}
	for _, c := range commits {
		switch c.Data[CommitDataWorkloadName] {
		case "cause-only":
			if c.Data[CommitDataChangeCause] != "hotfix rollout" {
				t.Errorf("cause-only data = %v", c.Data)
			}
			if _, ok := c.Data[CommitDataCommit]; ok {
				t.Error("cause-only item carries a commit key it has no value for")
			}
		case "commit-only":
			if c.Data[CommitDataCommit] != "abc123def456" {
				t.Errorf("commit-only data = %v", c.Data)
			}
			if _, ok := c.Data[CommitDataChangeCause]; ok {
				t.Error("commit-only item carries a changeCause key it has no value for")
			}
		default:
			t.Errorf("unexpected GitCommit item %v", c.Data)
		}
	}
}

// TestCollectKubernetesScope: only the scope namespaces are ever queried,
// and objects outside them never become evidence.
func TestCollectKubernetesScope(t *testing.T) {
	r := oomFixture()
	r.pods[otherNS] = []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: "outsider", Namespace: otherNS},
	}}
	items, err := CollectKubernetes(context.Background(), r, []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Data[DataNamespace] == otherNS || it.Data[EventDataInvolvedNamespace] == otherNS {
			t.Errorf("evidence leaked from outside the scope: %v", it)
		}
	}
	for _, ns := range r.queried {
		if ns != goldenNS {
			t.Errorf("collector queried namespace %q outside the scope", ns)
		}
	}
}

func TestCollectKubernetesPropagatesErrors(t *testing.T) {
	r := oomFixture()
	r.errs = map[string]error{goldenNS: fmt.Errorf("apiserver on fire")}
	if _, err := CollectKubernetes(context.Background(), r, []string{goldenNS}); err == nil {
		t.Fatal("collector swallowed a read error; a partial picture must fail the collection")
	}
}

// TestCollectKubernetesBarePod: a pod without a controller yields status
// but no owner chain.
func TestCollectKubernetesBarePod(t *testing.T) {
	r := &fakeReader{pods: map[string][]corev1.Pod{goldenNS: {{
		ObjectMeta: metav1.ObjectMeta{Name: "loner", Namespace: goldenNS},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}}}}
	items, err := CollectKubernetes(context.Background(), r, []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(itemsOf(t, items, ItemTypePodStatus)); got != 1 {
		t.Errorf("got %d PodStatus items, want 1", got)
	}
	if got := len(itemsOf(t, items, ItemTypeOwnerChain)); got != 0 {
		t.Errorf("bare pod produced %d OwnerChain items, want 0", got)
	}
}

// TestCollectKubernetesFeedsAssembler: the collector's output assembles
// cleanly — every type/source/key it emits is inside the closed vocabulary
// the assembler enforces.
func TestCollectKubernetesFeedsAssembler(t *testing.T) {
	items, err := CollectKubernetes(context.Background(), oomFixture(), []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Assemble(testIncidentRef(), fixedCollectedAt, items); err != nil {
		t.Fatalf("assembler rejected collector output: %v", err)
	}
}

// TestCollectKubernetesEnvNamesOnly (LLD §6: env values dropped, names
// kept): a container's env list is represented by variable NAMES only —
// literal values never enter an item, valueFrom references are named by
// their source kind (never the referenced object or key), and no read of
// any referenced Secret can happen because the Reader has no such method.
func TestCollectKubernetesEnvNamesOnly(t *testing.T) {
	const plantedValue = "hunter2-literal-env-value-FAKE"
	r := &fakeReader{pods: map[string][]corev1.Pod{goldenNS: {{
		ObjectMeta: metav1.ObjectMeta{Name: goldenPod, Namespace: goldenNS},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name: "migrate",
				Env:  []corev1.EnvVar{{Name: "DB_URL", Value: "postgres://" + plantedValue + "@db/shop"}},
			}},
			Containers: []corev1.Container{{
				Name: "api",
				Env: []corev1.EnvVar{
					{Name: "DB_HOST", Value: "db.shop.svc"},
					{Name: "DB_PASSWORD", Value: plantedValue},
					{Name: "AWS_SECRET_ACCESS_KEY", ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "aws-creds-secret-object"},
							Key:                  "secret-key-name",
						}}},
					{Name: "LOG_LEVEL", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
							Key:                  "level",
						}}},
					{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
					{Name: "CPU_LIMIT", ValueFrom: &corev1.EnvVarSource{
						ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.cpu"}}},
				},
			}, {
				Name: "sidecar", // no env at all: no key invented
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}}}}
	items, err := CollectKubernetes(context.Background(), r, []string{goldenNS})
	if err != nil {
		t.Fatal(err)
	}
	pods := itemsOf(t, items, ItemTypePodStatus)
	if len(pods) != 1 {
		t.Fatalf("got %d PodStatus items, want 1", len(pods))
	}
	data := pods[0].Data
	if got, want := data["container.api.env"], "DB_HOST,DB_PASSWORD,AWS_SECRET_ACCESS_KEY(secretKeyRef),LOG_LEVEL(configMapKeyRef),POD_IP(fieldRef),CPU_LIMIT(resourceFieldRef)"; got != want {
		t.Errorf("container.api.env:\n got %q\nwant %q", got, want)
	}
	if got, want := data["container.migrate.env"], "DB_URL"; got != want {
		t.Errorf("init container env names = %q, want %q", got, want)
	}
	if _, ok := data["container.sidecar.env"]; ok {
		t.Error("a container without env got an env key invented")
	}
	// Values and referenced object/key names are absent from EVERY value
	// of EVERY item, not just the env key.
	for _, it := range items {
		for k, v := range it.Data {
			for _, forbidden := range []string{plantedValue, "db.shop.svc", "aws-creds-secret-object", "secret-key-name", "app-config", "status.podIP", "limits.cpu"} {
				if strings.Contains(v, forbidden) {
					t.Errorf("item %q field %q carries %q — env values and valueFrom targets must not enter evidence", it.Key, k, forbidden)
				}
			}
		}
	}
}
