/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package deploystack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akansh23-cloud/praxis/bench/cli/kube"
)

// Install installs (or upgrades to) the pinned stack, one release at a
// time. `helm upgrade --install --wait` makes it idempotent: a re-run over
// a healthy stack is a fast no-op, a re-run over a half-installed one
// completes it. Charts come straight from their repository URLs (--repo),
// so no local helm repo state is created or mutated.
func Install(ctx context.Context, tc *kube.Toolchain, deployDir string, progress func(format string, args ...any)) error {
	for _, ch := range Charts {
		values := filepath.Join(deployDir, ch.ValuesFile)
		if _, err := os.Stat(values); err != nil {
			return fmt.Errorf("values file for release %q: %s does not exist — bench/deploy/ and cli/deploystack/versions.go have drifted apart", ch.Release, values)
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
