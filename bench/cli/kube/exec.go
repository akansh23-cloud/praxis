/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package kube is the runner's hands: it locates the external tools a
// benchmark run drives (kind, kubectl, helm), pins every call to the
// benchmark's own kubeconfig so the user's clusters and current-context are
// never touched, and provides the typed client and namespace helpers the
// pipeline uses directly.
package kube

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Toolchain resolves and runs the external binaries. All kubectl and helm
// calls carry --kubeconfig, so nothing here can ever act on the user's
// current context.
type Toolchain struct {
	// Kubeconfig is the benchmark-owned kubeconfig file; kind writes it,
	// everything else reads it.
	Kubeconfig string

	kind    string
	kubectl string
	helm    string
}

// NewToolchain locates kind, kubectl and helm, with actionable errors
// naming how each missing tool is installed. helm prefers the repo-pinned
// binary in <repoRoot>/bin (installed by `make -C bench helm`) over PATH.
func NewToolchain(repoRoot, kubeconfig string) (*Toolchain, error) {
	t := &Toolchain{Kubeconfig: kubeconfig}

	var err error
	if t.kind, err = exec.LookPath("kind"); err != nil {
		return nil, fmt.Errorf("kind not found on PATH — install kind ≥0.20 (docs/DEVELOPMENT.md §1)")
	}
	if t.kubectl, err = exec.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found on PATH — install kubectl ≥1.28 (docs/DEVELOPMENT.md §1)")
	}

	pinned := filepath.Join(repoRoot, "bin", "helm")
	if info, statErr := os.Stat(pinned); statErr == nil && !info.IsDir() {
		t.helm = pinned
	} else if t.helm, err = exec.LookPath("helm"); err != nil {
		return nil, fmt.Errorf("helm not found — run `make -C bench helm` to install the pinned version into ./bin")
	}
	return t, nil
}

// HelmPath reports which helm binary was resolved, for the run header.
func (t *Toolchain) HelmPath() string { return t.helm }

// Kubectl runs kubectl against the benchmark kubeconfig.
func (t *Toolchain) Kubectl(ctx context.Context, args ...string) (string, error) {
	return t.run(ctx, t.kubectl, append([]string{"--kubeconfig", t.Kubeconfig}, args...)...)
}

// Helm runs helm against the benchmark kubeconfig.
func (t *Toolchain) Helm(ctx context.Context, args ...string) (string, error) {
	return t.run(ctx, t.helm, append([]string{"--kubeconfig", t.Kubeconfig}, args...)...)
}

// runKind runs kind; subcommands that touch a kubeconfig receive the flag
// explicitly from their caller.
func (t *Toolchain) runKind(ctx context.Context, args ...string) (string, error) {
	return t.run(ctx, t.kind, args...)
}

// run executes one command, capturing combined output. On failure the
// error carries the command line and the output tail, because "exit status
// 1" alone has never helped anyone.
func (t *Toolchain) run(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("%s %s: %w\n%s",
			filepath.Base(bin), strings.Join(args, " "), err, tail(buf.String(), 4000))
	}
	return buf.String(), nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
