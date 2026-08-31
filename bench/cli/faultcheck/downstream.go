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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// chaosExperimentName matches metadata.name in
	// scenarios/downstream-dep-restraint/fault.yaml.
	chaosExperimentName = "payment-provider-partition"

	// chaosCondAllInjected is Chaos Mesh's "every target carries the
	// fault" condition; chaosStatusTrue is its satisfied state.
	chaosCondAllInjected = "AllInjected"
	chaosStatusTrue      = "True"

	// connectTimeout caps each in-pod connection attempt. The partition
	// DROPs packets, so the expected failure mode is a timeout, and 5s is
	// far beyond any in-cluster round trip.
	connectTimeout = "--timeout=5s"
)

// networkChaosGVK identifies the Chaos Mesh experiment kind; read as
// unstructured so the benchmark does not vendor Chaos Mesh's Go API for
// one status condition.
var networkChaosGVK = schema.GroupVersionKind{
	Group: "chaos-mesh.org", Version: "v1alpha1", Kind: "NetworkChaos",
}

// downstreamDepRestraint observes the downstream-dep-restraint fault in
// four acts, matching the outage signature the pack promises:
//
//  1. every payment-provider-sim pod is still Ready — NOTHING in-cluster
//     looks broken;
//  2. Chaos Mesh reports the partition fully injected (AllInjected=True);
//  3. positive control: a connection from inside a checkout-api pod to a
//     storefront pod endpoint still succeeds, proving the probing
//     machinery and pod networking work — the failure in act 4 cannot be
//     an artifact of a broken probe;
//  4. the same connection attempt to the provider's pod endpoint times
//     out.
//
// Two vantage-point rules, both learned the hard way:
//   - the connection must originate INSIDE a shop pod (kubectl exec +
//     `agnhost connect`) — kubelet probes and port-forward tunnels enter
//     from the node and are not partitioned, which is exactly why act 1
//     stays green;
//   - it must target the POD endpoint, not the Service: kind's kube-proxy
//     masquerades DNAT'd ClusterIP traffic, so a service-path connection
//     arrives with the node as source and sidesteps the pod-scoped
//     partition. The sim's modelled "provider address" is its endpoint;
//     the control uses the same addressing so the comparison stays fair.
func downstreamDepRestraint(ctx context.Context, env Env) error {
	ns := env.Namespaces[0]

	if err := pollUntil(ctx, env, 2*time.Minute, "all payment-provider-sim pods Ready (nothing in-cluster broken)",
		func(ctx context.Context) (bool, string, error) {
			return providerPodsReady(ctx, env, ns)
		}); err != nil {
		return err
	}

	if err := pollUntil(ctx, env, 2*time.Minute, "the partition experiment reporting AllInjected=True",
		func(ctx context.Context) (bool, string, error) {
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(networkChaosGVK)
			if err := env.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: chaosExperimentName}, u); err != nil {
				return false, fmt.Sprintf("NetworkChaos %s not readable: %v", chaosExperimentName, err), nil
			}
			done, status := chaosAllInjected(u.Object)
			return done, status, nil
		}); err != nil {
		return err
	}

	if err := pollUntil(ctx, env, 2*time.Minute, "positive control: checkout-api still reaches a storefront pod",
		func(ctx context.Context) (bool, string, error) {
			ip, status := podIPByApp(ctx, env, ns, "storefront")
			if ip == "" {
				return false, status, nil
			}
			out, err := connectFromCheckout(ctx, env, ns, ip+":8080")
			if err != nil {
				return false, fmt.Sprintf("control connection to storefront pod %s failed: %s", ip, firstLine(out, err)), nil
			}
			return true, "", nil
		}); err != nil {
		return err
	}

	return pollUntil(ctx, env, 3*time.Minute, "a connection from checkout-api to the provider's pod timing out",
		func(ctx context.Context) (bool, string, error) {
			ip, status := podIPByApp(ctx, env, ns, "payment-provider-sim")
			if ip == "" {
				return false, status, nil
			}
			out, err := connectFromCheckout(ctx, env, ns, ip+":8080")
			broken, status := classifyConnect(out, err != nil)
			if broken {
				env.Logf("    connect to payment-provider-sim pod %s from checkout-api: TIMEOUT (packets dropped)", ip)
			}
			return broken, status, nil
		})
}

// connectFromCheckout runs `agnhost connect` inside a checkout-api pod —
// the only vantage point the partition applies to.
func connectFromCheckout(ctx context.Context, env Env, ns, hostPort string) (string, error) {
	return env.Tools.Kubectl(ctx, "exec", "-n", ns, "deploy/checkout-api", "-c", "checkout-api",
		"--", "/agnhost", "connect", hostPort, connectTimeout)
}

// podIPByApp returns the pod IP of a Running pod of the given workload,
// or ("", why-not).
func podIPByApp(ctx context.Context, env Env, ns, app string) (string, string) {
	var pods corev1.PodList
	if err := env.Client.List(ctx, &pods, client.InNamespace(ns), byApp(app)); err != nil {
		return "", fmt.Sprintf("list %s pods: %v", app, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
			return pod.Status.PodIP, ""
		}
	}
	return "", fmt.Sprintf("no Running %s pod with an IP yet", app)
}

// classifyConnect interprets an `agnhost connect` outcome. Only a TIMEOUT
// is the partition's signature (iptables DROP swallows packets); success
// means connectivity is intact, and any other failure (REFUSED, DNS) is
// a different problem the check must not mistake for the fault.
func classifyConnect(output string, failed bool) (broken bool, status string) {
	if !failed {
		return false, "checkout-api still reaches the provider"
	}
	if strings.Contains(output, "TIMEOUT") {
		return true, "connection timed out"
	}
	return false, "connection failed, but not with the partition's TIMEOUT signature: " + strings.TrimSpace(output)
}

// providerPodsReady requires at least one payment-provider-sim pod and all
// of them Ready.
func providerPodsReady(ctx context.Context, env Env, ns string) (bool, string, error) {
	var pods corev1.PodList
	if err := env.Client.List(ctx, &pods,
		client.InNamespace(ns), byApp("payment-provider-sim")); err != nil {
		return false, "", fmt.Errorf("list payment-provider-sim pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return false, "no payment-provider-sim pods found", nil
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podReady(pod) {
			return false, fmt.Sprintf("pod %s is not Ready", pod.Name), nil
		}
	}
	return true, "", nil
}

// podReady reads the pod's Ready condition.
func podReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// chaosAllInjected reads the AllInjected condition from a chaos
// experiment's unstructured status.
func chaosAllInjected(obj map[string]any) (bool, string) {
	conditions, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return false, "status.conditions not reported yet"
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == chaosCondAllInjected {
			return cond["status"] == chaosStatusTrue,
				fmt.Sprintf("%s=%v", chaosCondAllInjected, cond["status"])
		}
	}
	return false, "condition " + chaosCondAllInjected + " not reported yet"
}

// firstLine compresses a command's output (or its error) to one line of
// evidence.
func firstLine(out string, err error) string {
	if line := strings.TrimSpace(out); line != "" {
		line, _, _ = strings.Cut(line, "\n")
		return line
	}
	if err != nil {
		line, _, _ := strings.Cut(err.Error(), "\n")
		return line
	}
	return "(no output)"
}
