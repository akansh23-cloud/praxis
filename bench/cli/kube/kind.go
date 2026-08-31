/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// EnsureKindCluster makes the named kind cluster exist with the pinned node
// image and the toolchain's kubeconfig pointing at it. Idempotent: an
// existing cluster is reused (its kubeconfig re-exported, so a lost file
// regenerates), matching the root Makefile's exact-name-match discipline so
// "praxis" and "praxis-bench" can never be mistaken for one another.
func (t *Toolchain) EnsureKindCluster(ctx context.Context, name, nodeImage string) (created bool, err error) {
	out, err := t.runKind(ctx, "get", "clusters")
	if err != nil {
		return false, fmt.Errorf("list kind clusters: %w", err)
	}
	if slices.Contains(strings.Fields(out), name) {
		if _, err := t.runKind(ctx, "export", "kubeconfig", "--name", name, "--kubeconfig", t.Kubeconfig); err != nil {
			return false, fmt.Errorf("export kubeconfig for existing cluster %q: %w", name, err)
		}
		return false, nil
	}
	if _, err := t.runKind(ctx, "create", "cluster",
		"--name", name,
		"--image", nodeImage,
		"--kubeconfig", t.Kubeconfig,
		"--wait", "120s"); err != nil {
		return false, fmt.Errorf("create kind cluster %q: %w", name, err)
	}
	return true, nil
}
