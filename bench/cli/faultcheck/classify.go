/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Pure pod/container classifiers, separated from the polling checks so the
// state-reading rules are unit-testable without a cluster.

// appLabel is the workload selector label every topology manifest sets.
const appLabel = "app"

// byApp selects a topology workload's pods by its app label.
func byApp(name string) client.MatchingLabels {
	return client.MatchingLabels{appLabel: name}
}

// containerOOMKilled reports whether a container status records an OOM
// kill — in its current state or, once the kubelet has restarted it into a
// crash-loop, in its last termination.
func containerOOMKilled(cs corev1.ContainerStatus) bool {
	if t := cs.State.Terminated; t != nil && t.Reason == "OOMKilled" {
		return true
	}
	if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
		return true
	}
	return false
}

// anyContainerStatus applies pred across a pod's regular and init container
// statuses and returns the first matching container name.
func anyContainerStatus(pod *corev1.Pod, pred func(corev1.ContainerStatus) bool) (string, bool) {
	for _, list := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses} {
		for _, cs := range list {
			if pred(cs) {
				return cs.Name, true
			}
		}
	}
	return "", false
}
