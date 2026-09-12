/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reader is the ONLY view of the cluster the evidence path has, and it is
// the type-level half of the no-secrets invariant (AGENTS.md; LLD §6, §16):
//
//   - There is no Secrets method. There is also no generic Get/List taking
//     an arbitrary object, so the interface cannot be steered toward one.
//     Code that asks this Reader for a Secret does not compile.
//   - Every method is a namespaced read. There are no write methods, so the
//     analyzer path cannot mutate the cluster through its evidence client.
//
// The method set is pinned by TestReaderMethodSetIsClosed: adding any
// method — a Secrets getter included — fails that test until the addition
// is made deliberately, in the open. The RBAC half of the invariant (no
// secrets rules in config/rbac) is asserted by TestRBACGrantsNoSecretAccess.
//
// The set mirrors the analyzer read matrix of LLD §16 that Session 3.1
// actually consumes: pods, events, and the apps workloads whose owner
// chains and change annotations become evidence.
type Reader interface {
	ListPods(ctx context.Context, namespace string) ([]corev1.Pod, error)
	ListEvents(ctx context.Context, namespace string) ([]corev1.Event, error)
	ListDeployments(ctx context.Context, namespace string) ([]appsv1.Deployment, error)
	ListReplicaSets(ctx context.Context, namespace string) ([]appsv1.ReplicaSet, error)
	ListStatefulSets(ctx context.Context, namespace string) ([]appsv1.StatefulSet, error)
	ListDaemonSets(ctx context.Context, namespace string) ([]appsv1.DaemonSet, error)
}

// NewReader wraps a controller-runtime reader into the narrow evidence
// Reader. The wrapped reader is held in an unexported field of an
// unexported type, so nothing outside this package can reach around the
// interface to the full-powered client underneath.
func NewReader(c client.Reader) Reader {
	return &clusterReader{c: c}
}

// clusterReader is the production Reader: thin, namespaced list calls and
// nothing else. It deliberately does NOT embed the wrapped client — an
// embedded client.Reader would re-export the generic Get/List surface this
// type exists to remove.
type clusterReader struct {
	c client.Reader
}

func (r *clusterReader) ListPods(ctx context.Context, namespace string) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list pods in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *clusterReader) ListEvents(ctx context.Context, namespace string) ([]corev1.Event, error) {
	var list corev1.EventList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list events in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *clusterReader) ListDeployments(ctx context.Context, namespace string) ([]appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list deployments in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *clusterReader) ListReplicaSets(ctx context.Context, namespace string) ([]appsv1.ReplicaSet, error) {
	var list appsv1.ReplicaSetList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list replicasets in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *clusterReader) ListStatefulSets(ctx context.Context, namespace string) ([]appsv1.StatefulSet, error) {
	var list appsv1.StatefulSetList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list statefulsets in %q: %w", namespace, err)
	}
	return list.Items, nil
}

func (r *clusterReader) ListDaemonSets(ctx context.Context, namespace string) ([]appsv1.DaemonSet, error) {
	var list appsv1.DaemonSetList
	if err := r.c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list daemonsets in %q: %w", namespace, err)
	}
	return list.Items, nil
}
