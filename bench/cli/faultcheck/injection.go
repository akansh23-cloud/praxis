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

const (
	// injectionApp and injectionContainer match the checkout-api sidecar
	// scenarios/prompt-injection/fault.yaml adds.
	injectionApp       = "checkout-api"
	injectionContainer = "feedback-ingest"
)

// promptInjection observes the prompt-injection fault (ADR-010) in two
// acts: the base fault first — the OOMKill oomkillAfterCommit proves,
// unchanged — then the injection itself: every planted string present at
// least once in the feedback-ingest sidecar's log, read through the
// Kubernetes API. Whether the lines then reach Loki and a bundle is the
// runner's telemetry gate and the scorer's visibility metric; this check
// proves only that the fault wrote what the pack says it writes.
func promptInjection(ctx context.Context, env Env) error {
	if err := oomkillAfterCommit(ctx, env); err != nil {
		return err
	}
	return plantsWritten(ctx, env, injectionApp, injectionContainer)
}

// plantsWritten polls until one pod of the app carries the container and
// that container's log holds every planted string.
func plantsWritten(ctx context.Context, env Env, app, container string) error {
	if len(env.PlantedTelemetry) == 0 {
		return fmt.Errorf("the %s check needs the pack's plantedTelemetry, but the scenario plants nothing — pack and check have drifted apart", app)
	}
	ns := env.Namespaces[0]
	what := fmt.Sprintf("every planted line (%d) in the %s log of a %s pod", len(env.PlantedTelemetry), container, app)
	return pollUntil(ctx, env, 2*time.Minute, what,
		func(ctx context.Context) (bool, string, error) {
			var pods corev1.PodList
			if err := env.Client.List(ctx, &pods, client.InNamespace(ns), byApp(app)); err != nil {
				return false, "", fmt.Errorf("list %s pods: %w", app, err)
			}
			carrying := 0
			for i := range pods.Items {
				pod := &pods.Items[i]
				if !HasContainer(pod, container) {
					continue
				}
				carrying++
				out, err := env.Tools.Kubectl(ctx, "logs", "-n", ns, pod.Name, "-c", container, "--tail=200")
				if err != nil {
					// The sidecar has not started yet; the next poll asks again.
					continue
				}
				missing := MissingPlants(out, env.PlantedTelemetry)
				if len(missing) == 0 {
					env.Logf("    pod %s container %q has written every planted line", pod.Name, container)
					return true, "", nil
				}
				return false, fmt.Sprintf("pod %s: %d of %d planted lines not written yet", pod.Name, len(missing), len(env.PlantedTelemetry)), nil
			}
			return false, fmt.Sprintf("%d %s pods, %d carrying a %s container", len(pods.Items), app, carrying, container), nil
		})
}

// HasContainer reports whether the pod's spec declares the container.
func HasContainer(pod *corev1.Pod, name string) bool {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return true
		}
	}
	return false
}

// MissingPlants returns, in the pack's order, the planted strings that do
// not occur verbatim in text. It is exact substring matching — the same
// rule the runner applies to bundle values, so the two observations agree.
func MissingPlants(text string, plants []string) []string {
	var missing []string
	for _, plant := range plants {
		if !strings.Contains(text, plant) {
			missing = append(missing, plant)
		}
	}
	return missing
}
