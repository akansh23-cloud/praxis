/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// Client builds a typed client (core, apps, praxis.dev) for the benchmark
// kubeconfig.
func (t *Toolchain) Client() (client.Client, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", t.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load benchmark kubeconfig %s: %w", t.Kubeconfig, err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, appsv1.AddToScheme, praxisv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return nil, fmt.Errorf("build scheme: %w", err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}
	return c, nil
}

// EnsureNamespace creates the namespace if it does not exist.
func EnsureNamespace(ctx context.Context, c client.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %q: %w", name, err)
	}
	return nil
}

// DeleteNamespace deletes the namespace, tolerating its absence.
func DeleteNamespace(ctx context.Context, c client.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %q: %w", name, err)
	}
	return nil
}

// WaitNamespaceGone blocks until the namespace has finished terminating.
func WaitNamespaceGone(ctx context.Context, c client.Client, name string, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			var ns corev1.Namespace
			err := c.Get(ctx, types.NamespacedName{Name: name}, &ns)
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, nil //nolint:nilerr // transient errors: keep polling until the timeout rules
		})
	if err != nil {
		return fmt.Errorf("namespace %q still terminating after %s: %w", name, timeout, err)
	}
	return nil
}
