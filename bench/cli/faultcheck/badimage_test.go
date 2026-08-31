/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestContainerImagePullBroken(t *testing.T) {
	cases := []struct {
		name string
		cs   corev1.ContainerStatus
		want bool
	}{
		{name: "no state", cs: corev1.ContainerStatus{}, want: false},
		{name: "running", cs: corev1.ContainerStatus{State: running()}, want: false},
		{name: "first pull failure", cs: corev1.ContainerStatus{State: waiting("ErrImagePull")}, want: true},
		{name: "backing off retries", cs: corev1.ContainerStatus{State: waiting("ImagePullBackOff")}, want: true},
		{name: "waiting for another reason", cs: corev1.ContainerStatus{State: waiting("ContainerCreating")}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerImagePullBroken(tc.cs); got != tc.want {
				t.Errorf("containerImagePullBroken() = %v, want %v", got, tc.want)
			}
		})
	}
}
