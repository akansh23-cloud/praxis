/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
)

// networkChaosGVK identifies the Chaos Mesh experiment kind; read as
// unstructured so the benchmark does not vendor Chaos Mesh's Go API for
// one status condition.
var networkChaosGVK = schema.GroupVersionKind{
	Group: "chaos-mesh.org", Version: "v1alpha1", Kind: "NetworkChaos",
}

// downstreamDepRestraint observes the downstream-dep-restraint fault in
// three acts, matching the outage signature the pack promises:
//
//  1. every payment-provider-sim pod is still Ready — NOTHING in-cluster
//     looks broken;
//  2. Chaos Mesh reports the partition fully injected (AllInjected=True);
//  3. an HTTP dial from inside a checkout-api pod to the provider fails.
//
// The dial must originate INSIDE a shop pod: the partition drops pod-to-pod
// traffic, while kubelet probes and port-forward tunnels enter from the
// node and pass — which is exactly why act 1 stays green. netexec's /dial
// endpoint gives us a pod-originated request without exec.
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

	return pollUntil(ctx, env, 3*time.Minute, "a dial from checkout-api to the provider failing",
		func(ctx context.Context) (bool, string, error) {
			return dialFromCheckoutFails(ctx, env, ns)
		})
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

// dialFromCheckoutFails port-forwards to the checkout-api Service, proves
// the tunnel and netexec are alive with /healthz, then asks netexec to
// dial the provider. Partition manifested = the pod-originated dial does
// NOT succeed (error response, or the dial hanging past its deadline).
func dialFromCheckoutFails(ctx context.Context, env Env, ns string) (bool, string, error) {
	port, stop, err := env.Tools.PortForward(ctx, ns, "svc/checkout-api", 80)
	if err != nil {
		return false, fmt.Sprintf("port-forward to checkout-api not up: %v", err), nil
	}
	defer stop()

	if _, err := httpGet(ctx, 5*time.Second,
		fmt.Sprintf("http://127.0.0.1:%d/healthz", port)); err != nil {
		return false, fmt.Sprintf("checkout-api not reachable through the tunnel: %v", err), nil
	}

	body, err := httpGet(ctx, 25*time.Second, fmt.Sprintf(
		"http://127.0.0.1:%d/dial?protocol=http&host=payment-provider-sim&port=80&request=healthz&tries=1", port))
	if err != nil {
		// The tunnel and netexec were just proven healthy, so a hung or
		// failed /dial IS the partition at work.
		env.Logf("    dial from checkout-api did not complete (%v) — provider unreachable", err)
		return true, "", nil
	}
	if dialShowsConnectivity(body) {
		return false, "checkout-api still reaches the provider: " + string(body), nil
	}
	env.Logf("    dial from checkout-api failed: %s", string(body))
	return true, "", nil
}

// dialShowsConnectivity interprets a netexec /dial response: connectivity
// is proven only by at least one response and no errors.
func dialShowsConnectivity(body []byte) bool {
	var resp struct {
		Responses []any `json:"responses"`
		Errors    []any `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false
	}
	return len(resp.Responses) > 0 && len(resp.Errors) == 0
}

// httpGet fetches a URL with a hard deadline, returning the body on any
// HTTP status (netexec reports dial failures with a body, not a status).
func httpGet(ctx context.Context, timeout time.Duration, url string) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
