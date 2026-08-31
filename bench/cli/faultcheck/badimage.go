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

// containerImagePullBroken reports whether a container cannot pull its
// image. ErrImagePull and ImagePullBackOff alternate as the kubelet
// retries; both mean the same fault, so both count.
func containerImagePullBroken(cs corev1.ContainerStatus) bool {
	w := cs.State.Waiting
	return w != nil && (w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff")
}

// badImageTag observes the bad-image-tag fault: after the release patch,
// the rollout's new checkout-api pod must be stuck failing to pull its
// image. The old ReplicaSet keeps serving (maxUnavailable 0); the wedged
// rollout is the fault, not an outage.
func badImageTag(ctx context.Context, env Env) error {
	return pollUntil(ctx, env, 3*time.Minute, "a checkout-api pod stuck in ErrImagePull/ImagePullBackOff",
		func(ctx context.Context) (bool, string, error) {
			var pods corev1.PodList
			if err := env.Client.List(ctx, &pods,
				client.InNamespace(env.Namespaces[0]),
				byApp("checkout-api")); err != nil {
				return false, "", fmt.Errorf("list checkout-api pods: %w", err)
			}
			for i := range pods.Items {
				pod := &pods.Items[i]
				if name, ok := anyContainerStatus(pod, containerImagePullBroken); ok {
					env.Logf("    pod %s container %q cannot pull its image", pod.Name, name)
					return true, "", nil
				}
			}
			return false, fmt.Sprintf("%d checkout-api pods, none failing an image pull yet", len(pods.Items)), nil
		})
}
