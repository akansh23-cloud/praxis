/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package deploystack

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akansh23-cloud/praxis/bench/cli/kube"
)

// Install installs (or upgrades to) the pinned stack, one release at a
// time. A release already deployed at the pinned chart version is left
// alone WITHOUT touching the network — `helm upgrade` re-downloads the
// chart from its repository every time, so skipping healthy releases is
// what makes warm re-runs both fast and immune to registry hiccups.
// Everything else goes through `helm upgrade --install --wait`, which
// completes half-installed releases and upgrades version drift. Charts
// come straight from their repository URLs (--repo), so no local helm
// repo state is created or mutated.
func Install(ctx context.Context, tc *kube.Toolchain, deployDir string, progress func(format string, args ...any)) error {
	for _, ch := range Charts {
		values := filepath.Join(deployDir, ch.ValuesFile)
		if _, err := os.Stat(values); err != nil {
			return fmt.Errorf("values file for release %q: %s does not exist — bench/deploy/ and cli/deploystack/versions.go have drifted apart", ch.Release, values)
		}
		if deployed, err := releaseDeployed(ctx, tc, ch); err == nil && deployed {
			progress("    release %s already deployed at %s-%s — skipping", ch.Release, ch.Name, ch.Version)
			continue
		}
		progress("    helm upgrade --install %s %s@%s → namespace %s", ch.Release, ch.Name, ch.Version, ch.Namespace)
		if _, err := tc.Helm(ctx, "upgrade", "--install", ch.Release, ch.Name,
			"--repo", ch.Repo,
			"--version", ch.Version,
			"--namespace", ch.Namespace,
			"--create-namespace",
			"--values", values,
			"--wait",
			"--timeout", "10m"); err != nil {
			return fmt.Errorf("install %s: %w", ch.Release, err)
		}
	}
	return nil
}

// helmRelease is the slice of `helm list -o json` this check reads.
type helmRelease struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Chart  string `json:"chart"`
}

// releaseDeployed reports whether the release is already deployed at
// exactly the pinned chart version. Any error means "don't know" and the
// caller falls through to the real install.
func releaseDeployed(ctx context.Context, tc *kube.Toolchain, ch Chart) (bool, error) {
	out, err := tc.Helm(ctx, "list", "--namespace", ch.Namespace,
		"--filter", "^"+ch.Release+"$", "--output", "json")
	if err != nil {
		return false, err
	}
	var releases []helmRelease
	if err := json.Unmarshal([]byte(out), &releases); err != nil {
		return false, fmt.Errorf("parse helm list output: %w", err)
	}
	want := fmt.Sprintf("%s-%s", ch.Name, ch.Version)
	for _, rel := range releases {
		if rel.Name == ch.Release && rel.Status == "deployed" && rel.Chart == want {
			return true, nil
		}
	}
	return false, nil
}
