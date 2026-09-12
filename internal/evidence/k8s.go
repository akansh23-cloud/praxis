/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// The Kubernetes collector: deterministic, read-only gathering of the LLD
// §6 evidence the incident's scope holds — pod status, Warning events,
// owner chains, and the annotation-based recent-change context. The scope
// namespaces are the only namespaces ever queried; everything reaches the
// cluster through the secretless Reader, so this path cannot see a Secret
// and cannot write.
//
// Determinism note: items are handed to Assemble unordered — the
// assembler's (type, source, natural key) sort is the single place order
// is decided — but every value written here derives only from the objects
// read, never from wall clocks or iteration order.

// changeCauseAnnotation is the upstream convention `kubectl` and most
// deploy tooling stamp on a changed workload; read together with
// praxis.dev/commit into GitCommit items.
const changeCauseAnnotation = "kubernetes.io/change-cause"

// Data keys shared by the Kubernetes evidence payloads.
const (
	DataNamespace = "namespace"
	DataName      = "name"
)

// PodStatus data keys.
const (
	PodDataPhase  = "phase"
	PodDataReason = "reason"
	PodDataReady  = "ready"
)

// OwnerChain data keys.
const (
	OwnerDataPod             = "pod"
	OwnerDataChain           = "chain"
	OwnerDataWorkloadKind    = "workloadKind"
	OwnerDataWorkloadName    = "workloadName"
	OwnerDataDesiredReplicas = "workloadDesiredReplicas"
	OwnerDataReadyReplicas   = "workloadReadyReplicas"
)

// Workload kinds the owner-chain walk and replica summary know.
const (
	kindDeployment  = "Deployment"
	kindReplicaSet  = "ReplicaSet"
	kindStatefulSet = "StatefulSet"
	kindDaemonSet   = "DaemonSet"
)

// GitCommit data keys.
const (
	CommitDataWorkloadKind = "workloadKind"
	CommitDataWorkloadName = "workloadName"
	CommitDataChangeCause  = "changeCause"
	CommitDataCommit       = "commit"
)

// CollectKubernetes gathers the k8s evidence for the given scope
// namespaces. Any read error fails the whole collection — a partial
// cluster picture is worse than a retried one, and the controller
// surfaces the failure on the incident's EvidenceCollected condition.
func CollectKubernetes(ctx context.Context, r Reader, namespaces []string) ([]Collected, error) {
	var out []Collected
	for _, ns := range namespaces {
		items, err := collectNamespace(ctx, r, ns)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// nsWorkloads indexes one namespace's workload objects for owner-chain
// resolution and change-annotation reads.
type nsWorkloads struct {
	replicaSets  map[string]*appsv1.ReplicaSet
	deployments  map[string]*appsv1.Deployment
	statefulSets map[string]*appsv1.StatefulSet
	daemonSets   map[string]*appsv1.DaemonSet
}

func collectNamespace(ctx context.Context, r Reader, ns string) ([]Collected, error) {
	w, workloadItems, err := loadWorkloads(ctx, r, ns)
	if err != nil {
		return nil, err
	}
	out := workloadItems

	pods, err := r.ListPods(ctx, ns)
	if err != nil {
		return nil, err
	}
	for i := range pods {
		pod := &pods[i]
		out = append(out, podStatusItem(pod))
		if chain, ok := ownerChainItem(pod, w); ok {
			out = append(out, chain)
		}
	}

	events, err := r.ListEvents(ctx, ns)
	if err != nil {
		return nil, err
	}
	for i := range events {
		ev := &events[i]
		if ev.Type != corev1.EventTypeWarning {
			continue
		}
		out = append(out, eventItem(ns, ev))
	}
	return out, nil
}

// loadWorkloads reads the namespace's apps objects once, returning both
// the owner-chain index and the GitCommit items their change annotations
// yield. A workload without change annotations yields nothing: the
// recent-change context is read, never invented.
func loadWorkloads(ctx context.Context, r Reader, ns string) (*nsWorkloads, []Collected, error) {
	w := &nsWorkloads{
		replicaSets:  map[string]*appsv1.ReplicaSet{},
		deployments:  map[string]*appsv1.Deployment{},
		statefulSets: map[string]*appsv1.StatefulSet{},
		daemonSets:   map[string]*appsv1.DaemonSet{},
	}
	var items []Collected

	deployments, err := r.ListDeployments(ctx, ns)
	if err != nil {
		return nil, nil, err
	}
	for i := range deployments {
		d := &deployments[i]
		w.deployments[d.Name] = d
		items = appendCommitItem(items, ns, kindDeployment, d.Name, d.Annotations)
	}

	replicaSets, err := r.ListReplicaSets(ctx, ns)
	if err != nil {
		return nil, nil, err
	}
	for i := range replicaSets {
		w.replicaSets[replicaSets[i].Name] = &replicaSets[i]
	}

	statefulSets, err := r.ListStatefulSets(ctx, ns)
	if err != nil {
		return nil, nil, err
	}
	for i := range statefulSets {
		s := &statefulSets[i]
		w.statefulSets[s.Name] = s
		items = appendCommitItem(items, ns, kindStatefulSet, s.Name, s.Annotations)
	}

	daemonSets, err := r.ListDaemonSets(ctx, ns)
	if err != nil {
		return nil, nil, err
	}
	for i := range daemonSets {
		d := &daemonSets[i]
		w.daemonSets[d.Name] = d
		items = appendCommitItem(items, ns, kindDaemonSet, d.Name, d.Annotations)
	}
	return w, items, nil
}

func appendCommitItem(items []Collected, ns, kind, name string, annotations map[string]string) []Collected {
	cause, commit := annotations[changeCauseAnnotation], annotations[praxisv1alpha1.AnnotationCommit]
	if cause == "" && commit == "" {
		return items
	}
	data := map[string]string{
		DataNamespace:          ns,
		CommitDataWorkloadKind: kind,
		CommitDataWorkloadName: name,
	}
	if cause != "" {
		data[CommitDataChangeCause] = cause
	}
	if commit != "" {
		data[CommitDataCommit] = commit
	}
	return append(items, Collected{
		Type:   ItemTypeGitCommit,
		Source: SourceK8s,
		Key:    fmt.Sprintf("%s/%s/%s", ns, kind, name),
		Data:   data,
	})
}

func podStatusItem(pod *corev1.Pod) Collected {
	data := map[string]string{
		DataNamespace: pod.Namespace,
		DataName:      pod.Name,
		PodDataPhase:  string(pod.Status.Phase),
		PodDataReady:  strconv.FormatBool(isPodReady(pod)),
	}
	if pod.Status.Reason != "" {
		data[PodDataReason] = pod.Status.Reason
	}
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		prefix := "container." + cs.Name + "."
		data[prefix+"image"] = cs.Image
		data[prefix+"ready"] = strconv.FormatBool(cs.Ready)
		data[prefix+"restartCount"] = strconv.Itoa(int(cs.RestartCount))
		data[prefix+"state"] = renderContainerState(&cs.State)
		if wait := cs.State.Waiting; wait != nil && wait.Message != "" {
			data[prefix+"waitingMessage"] = wait.Message
		}
		if term := cs.LastTerminationState.Terminated; term != nil {
			data[prefix+"lastTerminated"] = fmt.Sprintf("%s:exit=%d", term.Reason, term.ExitCode)
		}
	}
	for _, list := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range list {
			if names := envNames(list[i].Env); names != "" {
				data["container."+list[i].Name+".env"] = names
			}
		}
	}
	return Collected{
		Type:   ItemTypePodStatus,
		Source: SourceK8s,
		Key:    pod.Namespace + "/" + pod.Name,
		Data:   data,
	}
}

// envNames renders a container's environment as variable NAMES only (LLD
// §6: env values dropped, names kept). A literal value is never read; a
// valueFrom reference is named by its source kind alone — not the
// referenced Secret, ConfigMap or key — and is never resolved: the Reader
// has no method that could fetch what it points at.
func envNames(env []corev1.EnvVar) string {
	names := make([]string, 0, len(env))
	for i := range env {
		e := &env[i]
		name := e.Name
		if from := e.ValueFrom; from != nil {
			switch {
			case from.SecretKeyRef != nil:
				name += "(secretKeyRef)"
			case from.ConfigMapKeyRef != nil:
				name += "(configMapKeyRef)"
			case from.FieldRef != nil:
				name += "(fieldRef)"
			case from.ResourceFieldRef != nil:
				name += "(resourceFieldRef)"
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ",")
}

func renderContainerState(state *corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting:" + state.Waiting.Reason
	case state.Terminated != nil:
		return fmt.Sprintf("terminated:%s:exit=%d", state.Terminated.Reason, state.Terminated.ExitCode)
	}
	return "unknown"
}

func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ownerChainItem walks the pod's controller references through the
// namespace's workload index: Pod → ReplicaSet → Deployment, or Pod
// straight to a StatefulSet/DaemonSet. Pods without a controller (bare
// pods) yield no chain.
func ownerChainItem(pod *corev1.Pod, w *nsWorkloads) (Collected, bool) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return Collected{}, false
	}

	hops := []string{"Pod/" + pod.Name, owner.Kind + "/" + owner.Name}
	topKind, topName := owner.Kind, owner.Name
	if owner.Kind == kindReplicaSet {
		if rs, ok := w.replicaSets[owner.Name]; ok {
			if rsOwner := metav1.GetControllerOf(rs); rsOwner != nil {
				hops = append(hops, rsOwner.Kind+"/"+rsOwner.Name)
				topKind, topName = rsOwner.Kind, rsOwner.Name
			}
		}
	}

	data := map[string]string{
		DataNamespace:         pod.Namespace,
		OwnerDataPod:          pod.Name,
		OwnerDataChain:        strings.Join(hops, " -> "),
		OwnerDataWorkloadKind: topKind,
		OwnerDataWorkloadName: topName,
	}
	if desired, ready, ok := workloadReplicas(w, topKind, topName); ok {
		data[OwnerDataDesiredReplicas] = strconv.Itoa(int(desired))
		data[OwnerDataReadyReplicas] = strconv.Itoa(int(ready))
	}
	return Collected{
		Type:   ItemTypeOwnerChain,
		Source: SourceK8s,
		Key:    pod.Namespace + "/" + pod.Name,
		Data:   data,
	}, true
}

func workloadReplicas(w *nsWorkloads, kind, name string) (desired, ready int32, ok bool) {
	switch kind {
	case kindDeployment:
		if d, found := w.deployments[name]; found {
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			return desired, d.Status.ReadyReplicas, true
		}
	case kindStatefulSet:
		if s, found := w.statefulSets[name]; found {
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			return desired, s.Status.ReadyReplicas, true
		}
	case kindDaemonSet:
		if d, found := w.daemonSets[name]; found {
			return d.Status.DesiredNumberScheduled, d.Status.NumberReady, true
		}
	}
	return 0, 0, false
}

func eventItem(ns string, ev *corev1.Event) Collected {
	involvedNS := orDefault(ev.InvolvedObject.Namespace, ns)
	return Collected{
		Type:   ItemTypeEvent,
		Source: SourceK8s,
		Key: fmt.Sprintf("%s/%s/%s/%s",
			involvedNS, ev.InvolvedObject.Kind, ev.InvolvedObject.Name, ev.Reason),
		Data: map[string]string{
			EventDataType:              ev.Type,
			EventDataReason:            ev.Reason,
			EventDataMessage:           ev.Message,
			EventDataInvolvedKind:      ev.InvolvedObject.Kind,
			EventDataInvolvedName:      ev.InvolvedObject.Name,
			EventDataInvolvedNamespace: involvedNS,
			EventDataCount:             strconv.Itoa(eventCount(ev)),
		},
	}
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// eventCount mirrors the counting the Phase 2 stand-in gatherer used:
// series-aware, never zero.
func eventCount(ev *corev1.Event) int {
	if ev.Series != nil {
		return int(ev.Series.Count)
	}
	if ev.Count > 0 {
		return int(ev.Count)
	}
	return 1
}
