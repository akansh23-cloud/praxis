/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package faultcheck holds the per-scenario "fault manifested" checks
// (playbook Session 2.2 task 3): after the runner injects a fault, the
// matching check observes the expected broken state through the harness's
// own probes — pod statuses, events, PDB math, kubelet stats, in-cluster
// dials — with no agent involved. A scenario without a registered check
// cannot run; the checks are harness logic, not scenario YAML, because they
// encode Kubernetes mechanics (OOMKill reasons, server-side-apply rollouts,
// iptables partitions) that no 30-line pack should have to restate.
package faultcheck

import (
	"context"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/akansh23-cloud/praxis/bench/cli/kube"
)

// Env is the world a check observes: the same client and toolchain the
// runner drives the cluster with, scoped to the scenario's namespaces.
type Env struct {
	Client     client.Client
	Tools      *kube.Toolchain
	Namespaces []string
	Logf       func(format string, args ...any)

	// PlantedTelemetry is the pack's groundTruth.plantedTelemetry
	// (ADR-010): the strings a planting pack's fault writes into a pod
	// log, so its check can prove they were written. Empty for every
	// other pack.
	PlantedTelemetry []string
}

// A Check blocks until the scenario's fault is observably manifested, and
// returns an error if it is not within the check's own deadline. Checks
// must be pure observers: nothing here may mutate the cluster.
type Check func(ctx context.Context, env Env) error

// Lookup returns the check registered for a scenario name.
func Lookup(name string) (Check, bool) {
	c, ok := registry[name]
	return c, ok
}

// Names lists every registered scenario name, sorted, for the registry
// completeness tests.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// pollUntil evaluates cond every interval until it reports done, ctx is
// cancelled, or the timeout passes. cond returns (done, status, err): err
// aborts immediately, status describes what is still missing and is carried
// into the timeout error so a failed check names exactly what never
// happened.
func pollUntil(ctx context.Context, env Env, timeout time.Duration, what string,
	cond func(ctx context.Context) (done bool, status string, err error),
) error {
	env.Logf("    waiting up to %s for %s", timeout, what)
	lastStatus := "condition never evaluated"
	start := time.Now()
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			done, status, err := cond(ctx)
			if err != nil {
				return false, err
			}
			lastStatus = status
			return done, nil
		})
	if err != nil {
		return fmt.Errorf("%s: not observed within %s (last state: %s): %w", what, timeout, lastStatus, err)
	}
	env.Logf("    observed after %s: %s", time.Since(start).Round(time.Second), what)
	return nil
}
