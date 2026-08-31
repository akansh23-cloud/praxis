/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// oomkillAfterCommit observes the oomkill-after-commit fault: after the
// patch lowers the session-cache memory limit, the rollout's new
// checkout-api pod must record an OOMKilled container. The old ReplicaSet
// keeps serving (maxUnavailable 0), so the signal is the kill itself, not
// an outage — exactly the crash-loop-after-deploy shape the pack models.
func oomkillAfterCommit(ctx context.Context, env Env) error {
	return pollUntil(ctx, env, 4*time.Minute, "an OOMKilled container in a checkout-api pod",
		func(ctx context.Context) (bool, string, error) {
			var pods corev1.PodList
			if err := env.Client.List(ctx, &pods,
				client.InNamespace(env.Namespaces[0]),
				byApp("checkout-api")); err != nil {
				return false, "", fmt.Errorf("list checkout-api pods: %w", err)
			}
			for i := range pods.Items {
				pod := &pods.Items[i]
				if name, ok := anyContainerStatus(pod, containerOOMKilled); ok {
					env.Logf("    pod %s container %q was OOMKilled", pod.Name, name)
					return true, "", nil
				}
			}
			return false, fmt.Sprintf("%d checkout-api pods, none OOMKilled yet", len(pods.Items)), nil
		})
}
