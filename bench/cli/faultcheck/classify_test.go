/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func terminated(reason string) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason}}
}

func waiting(reason string) corev1.ContainerState {
	return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
}

func running() corev1.ContainerState {
	return corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
}

func TestContainerOOMKilled(t *testing.T) {
	cases := []struct {
		name string
		cs   corev1.ContainerStatus
		want bool
	}{
		{name: "fresh status, no states", cs: corev1.ContainerStatus{}, want: false},
		{name: "running healthily", cs: corev1.ContainerStatus{State: running()}, want: false},
		{name: "terminated OOMKilled", cs: corev1.ContainerStatus{State: terminated("OOMKilled")}, want: true},
		{name: "terminated for another reason", cs: corev1.ContainerStatus{State: terminated("Error")}, want: false},
		{
			name: "crash-looping after an OOM kill",
			cs:   corev1.ContainerStatus{State: waiting("CrashLoopBackOff"), LastTerminationState: terminated("OOMKilled")},
			want: true,
		},
		{
			name: "crash-looping after a plain error",
			cs:   corev1.ContainerStatus{State: waiting("CrashLoopBackOff"), LastTerminationState: terminated("Error")},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerOOMKilled(tc.cs); got != tc.want {
				t.Errorf("containerOOMKilled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnyContainerStatus(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "init", State: terminated("Completed")},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "web", State: running()},
				{Name: "cache", State: terminated("OOMKilled")},
			},
		},
	}

	name, ok := anyContainerStatus(pod, containerOOMKilled)
	if !ok || name != "cache" {
		t.Errorf("anyContainerStatus(OOMKilled) = (%q, %v), want (cache, true)", name, ok)
	}

	_, ok = anyContainerStatus(pod, func(corev1.ContainerStatus) bool { return false })
	if ok {
		t.Error("anyContainerStatus(never-match) reported a match")
	}
}
