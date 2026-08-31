/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// eventReasonUnhealthy is the kubelet's reason for failed-probe events.
const eventReasonUnhealthy = "Unhealthy"

// containerRunningNotReady reports a container that is alive but failing
// readiness — the exact signature of a probe misconfiguration, as opposed
// to a crash (terminated) or an unstartable pod (waiting).
func containerRunningNotReady(cs corev1.ContainerStatus) bool {
	return cs.State.Running != nil && !cs.Ready
}

// readinessWrongPort observes the readiness-wrong-port fault: a storefront
// pod is Running but not Ready, and the kubelet has said why — an
// Unhealthy event recording the failed readiness probe. Requiring the
// event pins the diagnosis to the probe itself, not to some other way of
// being unready.
func readinessWrongPort(ctx context.Context, env Env) error {
	ns := env.Namespaces[0]
	return pollUntil(ctx, env, 3*time.Minute, "a running storefront pod failing its readiness probe",
		func(ctx context.Context) (bool, string, error) {
			var pods corev1.PodList
			if err := env.Client.List(ctx, &pods,
				client.InNamespace(ns), byApp("storefront")); err != nil {
				return false, "", fmt.Errorf("list storefront pods: %w", err)
			}
			var stuck []string
			for i := range pods.Items {
				pod := &pods.Items[i]
				if _, ok := anyContainerStatus(pod, containerRunningNotReady); ok {
					stuck = append(stuck, pod.Name)
				}
			}
			if len(stuck) == 0 {
				return false, fmt.Sprintf("%d storefront pods, all Ready so far", len(pods.Items)), nil
			}

			var events corev1.EventList
			if err := env.Client.List(ctx, &events, client.InNamespace(ns)); err != nil {
				return false, "", fmt.Errorf("list events: %w", err)
			}
			for _, pod := range stuck {
				if ev, ok := readinessFailedEvent(events.Items, pod); ok {
					env.Logf("    pod %s is Running but not Ready; kubelet: %s", pod, strings.TrimSpace(ev))
					return true, "", nil
				}
			}
			return false, fmt.Sprintf("pods %v not Ready but no readiness-probe-failed event yet", stuck), nil
		})
}

// readinessFailedEvent finds a kubelet Unhealthy event for the pod that
// names a failed readiness probe, returning its message.
func readinessFailedEvent(events []corev1.Event, podName string) (string, bool) {
	for i := range events {
		ev := &events[i]
		if ev.Reason == eventReasonUnhealthy && ev.InvolvedObject.Name == podName &&
			strings.Contains(ev.Message, "Readiness probe failed") {
			return ev.Message, true
		}
	}
	return "", false
}
