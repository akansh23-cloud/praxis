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

// statusKey mirrors the field name chaos conditions use for their state.
const statusKey = "status"

func chaosObject(conditions ...map[string]any) map[string]any {
	anyConds := make([]any, len(conditions))
	for i, c := range conditions {
		anyConds[i] = any(c)
	}
	return map[string]any{statusKey: map[string]any{"conditions": anyConds}}
}

func condition(condType, condStatus string) map[string]any {
	return map[string]any{"type": condType, statusKey: condStatus}
}

func TestChaosAllInjected(t *testing.T) {
	cases := []struct {
		name       string
		obj        map[string]any
		want       bool
		wantStatus string
	}{
		{
			name:       "no status yet",
			obj:        map[string]any{},
			want:       false,
			wantStatus: "not reported",
		},
		{
			name:       "injected",
			obj:        chaosObject(condition(chaosCondAllInjected, chaosStatusTrue)),
			want:       true,
			wantStatus: "AllInjected=True",
		},
		{
			name:       "not yet injected",
			obj:        chaosObject(condition(chaosCondAllInjected, "False")),
			want:       false,
			wantStatus: "AllInjected=False",
		},
		{
			name:       "other conditions only",
			obj:        chaosObject(condition("Selected", chaosStatusTrue)),
			want:       false,
			wantStatus: "AllInjected not reported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, status := chaosAllInjected(tc.obj)
			if got != tc.want {
				t.Errorf("chaosAllInjected() = %v, want %v (status %q)", got, tc.want, status)
			}
			if !strings.Contains(status, tc.wantStatus) {
				t.Errorf("chaosAllInjected() status %q does not contain %q", status, tc.wantStatus)
			}
		})
	}
}

func TestClassifyConnect(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		failed     bool
		want       bool
		wantStatus string
	}{
		{
			name: "connect succeeded", output: "", failed: false,
			want: false, wantStatus: "still reaches",
		},
		{
			name: "partition timeout", output: "TIMEOUT\n", failed: true,
			want: true, wantStatus: "timed out",
		},
		{
			name: "refused is a different problem", output: "REFUSED\n", failed: true,
			want: false, wantStatus: "not with the partition's TIMEOUT signature",
		},
		{
			name: "dns failure is a different problem", output: "OTHER: lookup no-such-host failed\n", failed: true,
			want: false, wantStatus: "not with the partition's TIMEOUT signature",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, status := classifyConnect(tc.output, tc.failed)
			if got != tc.want {
				t.Errorf("classifyConnect(%q, %v) = %v, want %v", tc.output, tc.failed, got, tc.want)
			}
			if !strings.Contains(status, tc.wantStatus) {
				t.Errorf("classifyConnect() status %q does not contain %q", status, tc.wantStatus)
			}
		})
	}
}

func TestPodReady(t *testing.T) {
	ready := &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}}}
	notReady := &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionFalse},
	}}}
	noCondition := &corev1.Pod{}

	if !podReady(ready) {
		t.Error("podReady(Ready=True) = false")
	}
	if podReady(notReady) {
		t.Error("podReady(Ready=False) = true")
	}
	if podReady(noCondition) {
		t.Error("podReady(no conditions) = true")
	}
}
