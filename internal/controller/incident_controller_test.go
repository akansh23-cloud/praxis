/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/hash"
)

// Session 3.1 gives the incident controller its real job (LLD §4.1, §6):
// Detected → Collecting → Analyzed with a genuinely persisted, hashed
// evidence bundle, and Remediating only after Analyzed. These specs drive
// the machine step by step through envtest — real API server, real
// status subresource, real ConfigMap — with the production collector
// reading through the secretless evidence.Reader.

const (
	evDeployName = "checkout-api"
	evPauseImage = "registry.k8s.io/pause:3.10"
)

// failingReader satisfies evidence.Reader and fails every read: the
// collection-failure specs inject it to prove failures stay visible and
// the phase never advances past them.
type failingReader struct{}

var errReaderDown = fmt.Errorf("injected reader failure")

func (failingReader) ListPods(context.Context, string) ([]corev1.Pod, error) {
	return nil, errReaderDown
}

func (failingReader) ListEvents(context.Context, string) ([]corev1.Event, error) {
	return nil, errReaderDown
}

func (failingReader) ListDeployments(context.Context, string) ([]appsv1.Deployment, error) {
	return nil, errReaderDown
}

func (failingReader) ListReplicaSets(context.Context, string) ([]appsv1.ReplicaSet, error) {
	return nil, errReaderDown
}

func (failingReader) ListStatefulSets(context.Context, string) ([]appsv1.StatefulSet, error) {
	return nil, errReaderDown
}

func (failingReader) ListDaemonSets(context.Context, string) ([]appsv1.DaemonSet, error) {
	return nil, errReaderDown
}

var _ = Describe("Incident evidence machine", func() {
	var reconciler *IncidentReconciler

	ctx := context.Background()

	newReconciler := func(c client.Client) *IncidentReconciler {
		return &IncidentReconciler{
			Client:    c,
			Scheme:    k8sClient.Scheme(),
			APIReader: k8sClient,
			Collector: &evidence.Collector{Reader: evidence.NewReader(k8sClient)},
		}
	}

	BeforeEach(func() {
		reconciler = newReconciler(k8sClient)
	})

	reconcileIncident := func(r *IncidentReconciler, incident *praxisv1alpha1.Incident) (ctrl.Result, error) {
		GinkgoHelper()
		result, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: incident.Namespace, Name: incident.Name},
		})
		Expect(k8sClient.Get(ctx,
			types.NamespacedName{Namespace: incident.Namespace, Name: incident.Name}, incident)).To(Succeed())
		return result, err
	}

	mustReconcile := func(r *IncidentReconciler, incident *praxisv1alpha1.Incident) ctrl.Result {
		GinkgoHelper()
		result, err := reconcileIncident(r, incident)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	evidenceCondition := func(incident *praxisv1alpha1.Incident) *metav1.Condition {
		return meta.FindStatusCondition(incident.Status.Conditions, praxisv1alpha1.ConditionEvidenceCollected)
	}

	// scopedNamespace creates a dedicated namespace so content assertions
	// cannot be polluted by fixtures other specs leave in "default".
	scopedNamespace := func() string {
		GinkgoHelper()
		ns := uniqueName("evns")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
		return ns
	}

	newScopedIncident := func(ns string) *praxisv1alpha1.Incident {
		incident := newTestIncident(uniqueName("ev-inc"))
		incident.Spec.Scope.Namespaces = []string{ns}
		return incident
	}

	It("walks Detected → Collecting → Analyzed with a real persisted bundle", func() {
		ns := scopedNamespace()

		// A deployment carrying the change annotations, its replicaset, a
		// crash-looping pod and a warning event — the oomkill shape.
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: evDeployName, Namespace: ns,
				Annotations: map[string]string{
					"kubernetes.io/change-cause":    "commit 4be1f2a: trim session-cache memory",
					praxisv1alpha1.AnnotationCommit: "4be1f2a9c31d",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": evDeployName}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": evDeployName}},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name: fixtureContainerName, Image: evPauseImage,
					}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, deploy)).To(Succeed())

		rs := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Name: "checkout-api-7d9c6f5b4", Namespace: ns},
			Spec: appsv1.ReplicaSetSpec{
				Selector: deploy.Spec.Selector,
				Template: deploy.Spec.Template,
			},
		}
		Expect(controllerutil.SetControllerReference(deploy, rs, k8sClient.Scheme())).To(Succeed())
		Expect(k8sClient.Create(ctx, rs)).To(Succeed())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "checkout-api-7d9c6f5b4-x2m8q", Namespace: ns},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: fixtureContainerName, Image: evPauseImage,
			}}},
		}
		Expect(controllerutil.SetControllerReference(rs, pod, k8sClient.Scheme())).To(Succeed())
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		pod.Status = corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: fixtureContainerName, Image: evPauseImage, RestartCount: 4,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason: "OOMKilled", ExitCode: 137,
				}},
			}},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		Expect(k8sClient.Create(ctx, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "backoff-ev", Namespace: ns},
			Type:       corev1.EventTypeWarning, Reason: "BackOff",
			Message:        "Back-off restarting failed container",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: ns},
		})).To(Succeed())

		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		By("step 1: Detected → Collecting, announced on the condition")
		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseCollecting))
		cond := evidenceCondition(incident)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(praxisv1alpha1.ReasonCollectionInProgress))
		Expect(incident.Status.EvidenceBundleHash).To(BeEmpty())

		By("step 2: Collecting → Analyzed only with ref, hash and ConfigMap all real")
		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseAnalyzed))
		wantRef := evidence.BundleConfigMapName(incident.UID)
		Expect(incident.Status.EvidenceBundleRef).To(Equal(wantRef))
		Expect(incident.Status.EvidenceBundleHash).To(MatchRegexp(`^sha256:[a-f0-9]{64}$`))
		cond = evidenceCondition(incident)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(praxisv1alpha1.ReasonEvidenceStored))
		Expect(cond.Message).To(ContainSubstring("prometheus not configured"))

		By("the ConfigMap holds exactly the canonical bytes the hash covers")
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: incident.Namespace, Name: wantRef}, cm)).To(Succeed())
		raw, ok := cm.Data[evidence.BundleConfigMapKey]
		Expect(ok).To(BeTrue())
		Expect(hash.SHA256Prefixed([]byte(raw))).To(Equal(incident.Status.EvidenceBundleHash))
		Expect(metav1.IsControlledBy(cm, incident)).To(BeTrue())

		By("the bundle carries the expected evidence under closed-vocabulary ids")
		for _, id := range []string{"ev/podstatus-01", "ev/ownerchain-01", "ev/gitcommit-01", "ev/event-01"} {
			Expect(raw).To(ContainSubstring(fmt.Sprintf("%q", id)))
		}
		Expect(raw).To(ContainSubstring("OOMKilled"))
		Expect(raw).To(ContainSubstring("4be1f2a9c31d"))

		By("a settled incident performs zero further status writes and never re-collects")
		counting := &statusWriteCountingClient{Client: k8sClient}
		countingReconciler := newReconciler(counting)
		cmVersion := cm.ResourceVersion
		incVersion := incident.ResourceVersion
		for range 3 {
			mustReconcile(countingReconciler, incident)
		}
		Expect(counting.statusWrites).To(BeZero())
		Expect(incident.ResourceVersion).To(Equal(incVersion))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: incident.Namespace, Name: wantRef}, cm)).To(Succeed())
		Expect(cm.ResourceVersion).To(Equal(cmVersion))
	})

	It("honours a presupplied hash without collecting, visibly", func() {
		incident := newTestIncident(uniqueName("presup-inc"))
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		incident.Status.EvidenceBundleHash = evidenceHash
		Expect(k8sClient.Status().Update(ctx, incident)).To(Succeed())

		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseAnalyzed))
		Expect(incident.Status.EvidenceBundleHash).To(Equal(evidenceHash), "the presupplied hash must not be overwritten")
		Expect(incident.Status.EvidenceBundleRef).To(BeEmpty(), "no ConfigMap was persisted, so no ref may claim one")
		cond := evidenceCondition(incident)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(praxisv1alpha1.ReasonEvidencePresupplied))
	})

	It("keeps Collecting and surfaces CollectionFailed when collection fails, then recovers", func() {
		ns := scopedNamespace()
		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		broken := newReconciler(k8sClient)
		broken.Collector = &evidence.Collector{Reader: failingReader{}}

		mustReconcile(broken, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseCollecting))

		_, err := reconcileIncident(broken, incident)
		Expect(err).To(MatchError(ContainSubstring("injected reader failure")))
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseCollecting),
			"a failed collection must never look Analyzed")
		Expect(incident.Status.EvidenceBundleHash).To(BeEmpty())
		cond := evidenceCondition(incident)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(praxisv1alpha1.ReasonCollectionFailed))
		Expect(cond.Message).To(ContainSubstring("injected reader failure"))

		By("the next reconcile with a healthy reader completes the collection")
		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseAnalyzed))
		Expect(evidenceCondition(incident).Reason).To(Equal(praxisv1alpha1.ReasonEvidenceStored))
	})

	It("adopts an already-persisted bundle instead of re-collecting", func() {
		ns := scopedNamespace()
		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())
		mustReconcile(reconciler, incident) // → Collecting

		storedBytes := `{"collectedAt":"2026-09-01T12:00:00Z","incident":{},"items":[],"version":"1"}`
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      evidence.BundleConfigMapName(incident.UID),
				Namespace: incident.Namespace,
			},
			Data: map[string]string{evidence.BundleConfigMapKey: storedBytes},
		}
		Expect(controllerutil.SetControllerReference(incident, cm, k8sClient.Scheme())).To(Succeed())
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseAnalyzed))
		Expect(incident.Status.EvidenceBundleHash).To(Equal(hash.SHA256Prefixed([]byte(storedBytes))),
			"status must carry the hash of the bytes actually stored")

		stored := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, stored)).To(Succeed())
		Expect(stored.Data[evidence.BundleConfigMapKey]).To(Equal(storedBytes), "adoption must not rewrite the bundle")
		Expect(evidenceCondition(incident).Message).To(ContainSubstring("Adopted"))
	})

	It("refuses a bundle ConfigMap owned by someone else", func() {
		ns := scopedNamespace()
		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())
		mustReconcile(reconciler, incident) // → Collecting

		squatter := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      evidence.BundleConfigMapName(incident.UID),
				Namespace: incident.Namespace,
			},
			Data: map[string]string{evidence.BundleConfigMapKey: "{}"},
		}
		Expect(k8sClient.Create(ctx, squatter)).To(Succeed())

		_, err := reconcileIncident(reconciler, incident)
		Expect(err).To(MatchError(ContainSubstring("not owned by this incident")))
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseCollecting))
		Expect(incident.Status.EvidenceBundleHash).To(BeEmpty())
	})

	It("reaches Remediating only after Analyzed, per the §4.1 order", func() {
		ns := scopedNamespace()
		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		// The plan references the incident from the very start.
		plan := newTestPlan(uniqueName("early-plan"), incident.Name, evidenceHash)
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseCollecting),
			"a referencing plan must not short-circuit collection")

		mustReconcile(reconciler, incident)
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseRemediating),
			"once analyzed, the existing reference lifts the incident to Remediating")
		Expect(incident.Status.EvidenceBundleHash).NotTo(BeEmpty())
	})

	It("ignores plans pinned to a different Incident incarnation (UID)", func() {
		ns := scopedNamespace()
		incident := newScopedIncident(ns)
		Expect(k8sClient.Create(ctx, incident)).To(Succeed())

		plan := newTestPlan(uniqueName("stale-plan"), incident.Name, evidenceHash)
		plan.Spec.IncidentRef.UID = types.UID("11111111-2222-3333-4444-555555555555")
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		mustReconcile(reconciler, incident) // → Collecting
		mustReconcile(reconciler, incident) // → Analyzed, not Remediating
		Expect(incident.Status.Phase).To(Equal(praxisv1alpha1.IncidentPhaseAnalyzed))
	})

	It("names the bundle ConfigMap praxis-ev-<uid8>", func() {
		Expect(evidence.BundleConfigMapName(types.UID("0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"))).
			To(Equal("praxis-ev-0a1b2c3d"))
		Expect(strings.HasPrefix(evidence.BundleConfigMapName("short"), "praxis-ev-")).To(BeTrue())
	})
})
