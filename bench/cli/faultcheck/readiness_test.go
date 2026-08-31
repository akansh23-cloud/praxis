/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestContainerRunningNotReady(t *testing.T) {
	cases := []struct {
		name string
		cs   corev1.ContainerStatus
		want bool
	}{
		{name: "running and ready", cs: corev1.ContainerStatus{State: running(), Ready: true}, want: false},
		{name: "running, failing readiness", cs: corev1.ContainerStatus{State: running(), Ready: false}, want: true},
		{name: "not ready because crashed", cs: corev1.ContainerStatus{State: terminated("Error"), Ready: false}, want: false},
		{name: "not ready because waiting", cs: corev1.ContainerStatus{State: waiting("ContainerCreating"), Ready: false}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerRunningNotReady(tc.cs); got != tc.want {
				t.Errorf("containerRunningNotReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadinessFailedEvent(t *testing.T) {
	events := []corev1.Event{
		{Reason: "Scheduled", Message: "assigned shop/storefront-a"},
		{Reason: eventReasonUnhealthy, Message: "Liveness probe failed: no route",
			InvolvedObject: corev1.ObjectReference{Name: "storefront-a"}},
		{Reason: eventReasonUnhealthy, Message: "Readiness probe failed: connect: connection refused",
			InvolvedObject: corev1.ObjectReference{Name: "storefront-a"}},
	}

	if msg, ok := readinessFailedEvent(events, "storefront-a"); !ok || !strings.Contains(msg, "Readiness probe failed") {
		t.Errorf("readinessFailedEvent(storefront-a) = (%q, %v), want the readiness message", msg, ok)
	}
	if _, ok := readinessFailedEvent(events, "storefront-b"); ok {
		t.Error("readinessFailedEvent matched an event for a different pod")
	}
	liveOnly := events[:2]
	if _, ok := readinessFailedEvent(liveOnly, "storefront-a"); ok {
		t.Error("readinessFailedEvent matched a liveness-only event set")
	}
}
