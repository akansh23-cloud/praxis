/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// summaryJSON mimics the kubelet Summary API shape: two hog pods, a victim,
// and a pod in another namespace that must not be counted.
const summaryJSON = `{
  "node": {"nodeName": "praxis-bench-control-plane"},
  "pods": [
    {"podRef": {"name": "batch-analytics-abc-1", "namespace": "shop"}, "cpu": {"usageNanoCores": 900000000}},
    {"podRef": {"name": "batch-analytics-abc-2", "namespace": "shop"}, "cpu": {"usageNanoCores": 400000000}},
    {"podRef": {"name": "inventory-xyz", "namespace": "shop"}, "cpu": {"usageNanoCores": 3000000}},
    {"podRef": {"name": "batch-analytics-ghost", "namespace": "monitoring"}, "cpu": {"usageNanoCores": 700000000}}
  ]
}`

func TestSumPodCPUNanoCores(t *testing.T) {
	got, err := sumPodCPUNanoCores([]byte(summaryJSON), "shop", "batch-analytics-")
	if err != nil {
		t.Fatalf("sumPodCPUNanoCores failed: %v", err)
	}
	if want := uint64(1_300_000_000); got != want {
		t.Errorf("sumPodCPUNanoCores() = %d, want %d (hog pods only, victim and other namespaces excluded)", got, want)
	}

	if got, err := sumPodCPUNanoCores([]byte(summaryJSON), "shop", "storefront-"); err != nil || got != 0 {
		t.Errorf("sumPodCPUNanoCores(no matching pods) = (%d, %v), want (0, nil)", got, err)
	}

	if _, err := sumPodCPUNanoCores([]byte("kubelet said no"), "shop", "batch-analytics-"); err == nil {
		t.Error("sumPodCPUNanoCores accepted non-JSON input")
	}
}

// hogName is the unlimited container the anyCPULimit cases build around.
const hogName = "hog"

func TestAnyCPULimit(t *testing.T) {
	limited := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
	}
	memOnly := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
	}

	cases := []struct {
		name       string
		containers []corev1.Container
		want       bool
		wantName   string
	}{
		{name: "no resources stanza", containers: []corev1.Container{{Name: hogName}}, want: false},
		{name: "memory limit only", containers: []corev1.Container{{Name: hogName, Resources: memOnly}}, want: false},
		{name: "cpu limited", containers: []corev1.Container{{Name: "api", Resources: limited}}, want: true, wantName: "api"},
		{
			name:       "one of two limited",
			containers: []corev1.Container{{Name: hogName}, {Name: "sidecar", Resources: limited}},
			want:       true, wantName: "sidecar",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: tc.containers}}
			got, name := anyCPULimit(pod)
			if got != tc.want || name != tc.wantName {
				t.Errorf("anyCPULimit() = (%v, %q), want (%v, %q)", got, name, tc.want, tc.wantName)
			}
		})
	}
}
