/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package runner drives one benchmark scenario end-to-end against the
// dedicated praxis-bench kind cluster (playbook Session 2.1 task 5):
// ensure cluster → install CRDs → install the pinned deploy stack → then,
// per run: create namespaces, apply topology, verify healthy, inject the
// fault, file the Incident, wait for a RemediationPlan or the
// praxis.dev/no-action-proposed annotation, tear down. A timeout while
// waiting is a recorded outcome, not a failure — with --agent none it is
// the expected one.
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/akansh23-cloud/praxis/bench/cli/deploystack"
	"github.com/akansh23-cloud/praxis/bench/cli/kube"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// KindClusterName is the benchmark's dedicated kind cluster. It is
	// deliberately distinct from the dev cluster ("praxis") and both e2e
	// clusters, matching the repository's one-cluster-per-purpose rule, so
	// a benchmark can never touch a cluster it does not own.
	KindClusterName = "praxis-bench"

	// AgentNone means nothing is wired to respond; the wait must end in a
	// graceful timeout (or a restraint annotation placed by hand).
	AgentNone = "none"

	// kubeconfigName is the benchmark-owned kubeconfig inside bench/,
	// covered by the repository's *.kubeconfig gitignore rule. Keeping it
	// out of ~/.kube/config means a run never switches the user's
	// current-context.
	kubeconfigName = ".praxis-bench.kubeconfig"
)

// Options is the `praxisbench run` flag surface.
type Options struct {
	Scenario string
	Runs     int
	Keep     bool
	Agent    string
}

// Runner holds the resolved world one invocation operates in.
type Runner struct {
	opts     Options
	out      printer
	scn      *scenario.Scenario
	benchDir string
	repoRoot string
	tc       *kube.Toolchain
	c        client.Client
	timings  []timing
}

// Run executes `praxisbench run`.
func Run(ctx context.Context, opts Options, w io.Writer) error {
	if opts.Agent != AgentNone {
		return fmt.Errorf("unknown agent %q: only %q exists yet — the rule-based baseline arrives with the scorer session (playbook 2.3)", opts.Agent, AgentNone)
	}
	if opts.Runs < 1 {
		return fmt.Errorf("--runs is %d; must be at least 1", opts.Runs)
	}

	scnPath, benchDir, repoRoot, err := locate(opts.Scenario)
	if err != nil {
		return err
	}
	scn, err := scenario.Load(scnPath)
	if err != nil {
		return err
	}
	tc, err := kube.NewToolchain(repoRoot, filepath.Join(benchDir, kubeconfigName))
	if err != nil {
		return err
	}

	r := &Runner{
		opts:     opts,
		out:      printer{w: w},
		scn:      scn,
		benchDir: benchDir,
		repoRoot: repoRoot,
		tc:       tc,
	}
	return r.run(ctx)
}

func (r *Runner) run(ctx context.Context) error {
	r.out.f("==> praxisbench run: scenario %q (runs=%d, agent=%s, keep=%v)",
		r.scn.Name, r.opts.Runs, r.opts.Agent, r.opts.Keep)
	r.out.f("    cluster %q · kubeconfig %s", KindClusterName, r.tc.Kubeconfig)

	if err := r.phase(ctx, "ensure-cluster", func(ctx context.Context) error {
		created, err := r.tc.EnsureKindCluster(ctx, KindClusterName, deploystack.KindNodeImage)
		if err != nil {
			return err
		}
		if created {
			r.out.f("    created kind cluster %q (%s)", KindClusterName, deploystack.KindNodeImage)
		} else {
			r.out.f("    reusing kind cluster %q", KindClusterName)
		}
		return nil
	}); err != nil {
		return err
	}

	if err := r.phase(ctx, "install-crds", func(ctx context.Context) error {
		crdDir := filepath.Join(r.repoRoot, "config", "crd", "bases")
		if _, err := r.tc.Kubectl(ctx, "apply", "--server-side", "-f", crdDir); err != nil {
			return err
		}
		_, err := r.tc.Kubectl(ctx, "wait", "--for", "condition=Established", "--timeout=60s", "-f", crdDir)
		return err
	}); err != nil {
		return err
	}

	if err := r.phase(ctx, "deploy-stack", func(ctx context.Context) error {
		return deploystack.Install(ctx, r.tc, filepath.Join(r.benchDir, "deploy"), r.out.f)
	}); err != nil {
		return err
	}

	c, err := r.tc.Client()
	if err != nil {
		return err
	}
	r.c = c

	outcomes := make([]outcome, 0, r.opts.Runs)
	for i := range r.opts.Runs {
		oc, err := r.runOnce(ctx, i+1)
		if err != nil {
			return fmt.Errorf("run %d/%d: %w", i+1, r.opts.Runs, err)
		}
		outcomes = append(outcomes, oc)
	}

	r.printSummary(outcomes)
	return nil
}

// phase times one named pipeline step and prints its banner.
func (r *Runner) phase(ctx context.Context, name string, f func(context.Context) error) error {
	r.out.f("==> %s", name)
	start := time.Now()
	err := f(ctx)
	r.timings = append(r.timings, timing{name: name, d: time.Since(start)})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// locate resolves the --scenario argument (a name under bench/scenarios/
// or a path) plus the bench module directory and the repository root the
// CRDs are installed from.
func locate(scenarioArg string) (scnPath, benchDir, repoRoot string, err error) {
	if info, statErr := os.Stat(scenarioArg); statErr == nil {
		abs, absErr := filepath.Abs(scenarioArg)
		if absErr != nil {
			return "", "", "", fmt.Errorf("resolve --scenario path %q: %w", scenarioArg, absErr)
		}
		scnPath = abs
		dir := abs
		if !info.IsDir() {
			dir = filepath.Dir(abs)
		}
		if benchDir, err = ascendToBenchDir(dir); err != nil {
			return "", "", "", err
		}
	} else {
		if benchDir, err = benchDirFromCWD(); err != nil {
			return "", "", "", err
		}
		scnPath = filepath.Join(benchDir, "scenarios", scenarioArg)
		if _, statErr := os.Stat(scnPath); statErr != nil {
			return "", "", "", fmt.Errorf("no scenario named %q under %s — pass a name from bench/scenarios/ or a path to a scenario.yaml",
				scenarioArg, filepath.Join(benchDir, "scenarios"))
		}
	}

	repoRoot = filepath.Dir(benchDir)
	if _, statErr := os.Stat(filepath.Join(repoRoot, "config", "crd", "bases")); statErr != nil {
		return "", "", "", fmt.Errorf("%s has no config/crd/bases — the benchmark installs the praxis CRDs from the repository checkout and cannot run without it", repoRoot)
	}
	return scnPath, benchDir, repoRoot, nil
}

// ascendToBenchDir walks up from start until it finds the bench module.
func ascendToBenchDir(start string) (string, error) {
	for dir := start; ; {
		if isBenchDir(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot locate the bench module above %s (looked for a directory holding both go.mod and scenarios/)", start)
		}
		dir = parent
	}
}

// benchDirFromCWD finds the bench module from the working directory, which
// may be the repo root, bench/ itself, or anywhere below either.
func benchDirFromCWD() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	for dir := cwd; ; {
		if isBenchDir(dir) {
			return dir, nil
		}
		if child := filepath.Join(dir, "bench"); isBenchDir(child) {
			return child, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("cannot locate bench/scenarios from %s — run inside the praxis repository or pass --scenario <path/to/scenario.yaml>", cwd)
}

func isBenchDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "scenarios"))
	return err == nil && info.IsDir()
}
