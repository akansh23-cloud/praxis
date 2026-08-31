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
	"k8s.io/apimachinery/pkg/types"
)

// smokeMarkerName matches metadata.name in scenarios/smoke/fault.yaml.
const smokeMarkerName = "praxis-bench-fault-marker"

// smokeInert verifies the smoke scenario's deliberately inert fault: the
// marker ConfigMap exists and nothing else is claimed. The pack breaks no
// workload, so the only honest mechanical observation is that the injection
// path really placed the marker.
func smokeInert(ctx context.Context, env Env) error {
	return pollUntil(ctx, env, 30*time.Second, "the smoke fault marker ConfigMap",
		func(ctx context.Context) (bool, string, error) {
			var cm corev1.ConfigMap
			key := types.NamespacedName{Namespace: env.Namespaces[0], Name: smokeMarkerName}
			if err := env.Client.Get(ctx, key, &cm); err != nil {
				return false, fmt.Sprintf("ConfigMap %s/%s not readable: %v", key.Namespace, key.Name, err), nil
			}
			if cm.Data["scenario"] != "smoke" {
				return false, fmt.Sprintf("marker exists but data.scenario is %q, want smoke", cm.Data["scenario"]), nil
			}
			return true, "", nil
		})
}
