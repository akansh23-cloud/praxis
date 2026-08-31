/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// noisyDeployName matches the Deployment in scenarios/noisy-neighbour/fault.yaml.
	noisyDeployName = "batch-analytics"

	// noisyBurnFloorNanoCores is the CPU burn that proves the hog is
	// really starving the node: 0.5 cores, against ~1.3 measured for
	// `stress --cpus 2` — a 2.5× margin over the pass line.
	noisyBurnFloorNanoCores = 500_000_000
)

// noisyNeighbour observes the noisy-neighbour fault in two acts: the hog
// must be Running WITHOUT CPU limits (the misconfiguration itself, from the
// API), and it must be measurably burning CPU (the harm, from the kubelet's
// stats summary — the same cAdvisor numbers a metrics pipeline would see).
func noisyNeighbour(ctx context.Context, env Env) error {
	ns := env.Namespaces[0]
	if err := pollUntil(ctx, env, 2*time.Minute, "the batch-analytics hog running with no CPU limits",
		func(ctx context.Context) (bool, string, error) {
			var pods corev1.PodList
			if err := env.Client.List(ctx, &pods,
				client.InNamespace(ns), byApp(noisyDeployName)); err != nil {
				return false, "", fmt.Errorf("list %s pods: %w", noisyDeployName, err)
			}
			for i := range pods.Items {
				pod := &pods.Items[i]
				if pod.Status.Phase != corev1.PodRunning {
					continue
				}
				if limited, container := anyCPULimit(pod); limited {
					return false, "", fmt.Errorf(
						"hog pod %s container %q HAS a CPU limit — the fault manifest and the check have drifted apart", pod.Name, container)
				}
				env.Logf("    hog pod %s is Running, BestEffort (no resources stanza)", pod.Name)
				return true, "", nil
			}
			return false, fmt.Sprintf("%d %s pods, none Running yet", len(pods.Items), noisyDeployName), nil
		}); err != nil {
		return err
	}

	return pollUntil(ctx, env, 3*time.Minute,
		fmt.Sprintf("batch-analytics burning >%.1f CPU cores (kubelet stats summary)", float64(noisyBurnFloorNanoCores)/1e9),
		func(ctx context.Context) (bool, string, error) {
			var nodes corev1.NodeList
			if err := env.Client.List(ctx, &nodes); err != nil {
				return false, "", fmt.Errorf("list nodes: %w", err)
			}
			var total uint64
			for i := range nodes.Items {
				raw, err := env.Tools.Kubectl(ctx, "get", "--raw",
					"/api/v1/nodes/"+nodes.Items[i].Name+"/proxy/stats/summary")
				if err != nil {
					return false, "", fmt.Errorf("read kubelet stats summary for node %s: %w", nodes.Items[i].Name, err)
				}
				sum, err := sumPodCPUNanoCores([]byte(raw), ns, noisyDeployName+"-")
				if err != nil {
					return false, "", fmt.Errorf("parse stats summary for node %s: %w", nodes.Items[i].Name, err)
				}
				total += sum
			}
			if total >= noisyBurnFloorNanoCores {
				env.Logf("    batch-analytics burning %.2f cores", float64(total)/1e9)
				return true, "", nil
			}
			return false, fmt.Sprintf("batch-analytics at %.2f cores so far (cAdvisor window lags ~15s)", float64(total)/1e9), nil
		})
}

// anyCPULimit reports whether any container in the pod carries a CPU
// limit, naming the first that does.
func anyCPULimit(pod *corev1.Pod) (bool, string) {
	for _, c := range pod.Spec.Containers {
		if _, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
			return true, c.Name
		}
	}
	return false, ""
}

// statsSummary is the slice of the kubelet Summary API this check reads.
type statsSummary struct {
	Pods []struct {
		PodRef struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"podRef"`
		CPU struct {
			UsageNanoCores uint64 `json:"usageNanoCores"`
		} `json:"cpu"`
	} `json:"pods"`
}

// sumPodCPUNanoCores totals instantaneous CPU usage across the pods of one
// workload, identified by namespace and pod-name prefix (stats carry names,
// not labels).
func sumPodCPUNanoCores(raw []byte, namespace, namePrefix string) (uint64, error) {
	var summary statsSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return 0, fmt.Errorf("not a kubelet stats summary: %w", err)
	}
	var total uint64
	for _, pod := range summary.Pods {
		if pod.PodRef.Namespace == namespace && strings.HasPrefix(pod.PodRef.Name, namePrefix) {
			total += pod.CPU.UsageNanoCores
		}
	}
	return total, nil
}
