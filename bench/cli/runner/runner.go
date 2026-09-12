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

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/akansh23-cloud/praxis/bench/cli/deploystack"
	"github.com/akansh23-cloud/praxis/bench/cli/kube"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
	"github.com/akansh23-cloud/praxis/bench/cli/scoring"
	"github.com/akansh23-cloud/praxis/internal/agents"
	llmagent "github.com/akansh23-cloud/praxis/internal/agents/llm"
	"github.com/akansh23-cloud/praxis/internal/agents/rulebased"
	"github.com/akansh23-cloud/praxis/internal/evidence"
	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
	"github.com/akansh23-cloud/praxis/internal/llm"
	"github.com/akansh23-cloud/praxis/internal/llm/providers"
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

	// AgentRuleBased is the intentionally dumb baseline of FR-P2-04,
	// driven in-process through the Agent seam (internal/agents).
	AgentRuleBased = "rulebased"

	// AgentLLM is the model-backed agent of Phase 3 (Session 3.3),
	// driven through the same seam over the same real evidence bundle.
	// Provider and model are configuration (--llm-provider, --llm-model);
	// the Anthropic credential is read from ANTHROPIC_API_KEY and never
	// from a flag.
	AgentLLM = "llm"

	// apiKeyEnv is the only place an Anthropic credential is read from.
	apiKeyEnv = "ANTHROPIC_API_KEY"

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

	// ResultsDir receives one JSONL file of scored run records per
	// invocation; empty means <bench>/results.
	ResultsDir string

	// PrometheusURL and LokiURL feed the real evidence collector's
	// telemetry seams, exactly like the manager's flags; empty means the
	// bundle honestly omits Metric / LogTemplate evidence.
	PrometheusURL string
	LokiURL       string

	// The LLM agent's configuration (--agent llm). Provider defaults to
	// anthropic, model to claude-opus-5; the credential comes from the
	// environment.
	LLMProvider string
	LLMModel    string
	LLMBaseURL  string
	LLMEffort   string
}

// agentFor maps the --agent flag to a seam implementation; nil means
// nothing responds (the Session 2.1/2.2 behavior, unchanged).
func agentFor(opts Options) (agents.Agent, error) {
	switch opts.Agent {
	case AgentNone:
		return nil, nil
	case AgentRuleBased:
		return rulebased.New(), nil
	case AgentLLM:
		modelClient, err := llmClientFor(opts)
		if err != nil {
			return nil, err
		}
		return llmagent.New(modelClient), nil
	default:
		return nil, fmt.Errorf("unknown agent %q: available agents are %q, %q and %q",
			opts.Agent, AgentNone, AgentRuleBased, AgentLLM)
	}
}

// llmClientFor builds the configured model client. The API key is read
// from the environment here and handed to the client's config — it never
// appears in a flag, a log line or a record.
func llmClientFor(opts Options) (llm.Client, error) {
	cfg := llm.Config{
		Provider: opts.LLMProvider,
		Model:    opts.LLMModel,
		BaseURL:  opts.LLMBaseURL,
		Effort:   opts.LLMEffort,
	}
	if cfg.Provider == "" {
		cfg.Provider = llm.ProviderAnthropic
	}
	if cfg.Model == "" && cfg.Provider == llm.ProviderAnthropic {
		cfg.Model = "claude-opus-5"
	}
	if cfg.Provider == llm.ProviderAnthropic {
		cfg.APIKey = os.Getenv(apiKeyEnv)
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("--agent llm with provider %s needs %s in the environment (it is never a flag)", cfg.Provider, apiKeyEnv)
		}
	}
	return providers.New(cfg)
}

// collectorFor builds the REAL evidence collector over the benchmark's
// client: the secretless Reader wraps it, so the collector cannot ask for
// a Secret however privileged the bench kubeconfig is; the telemetry
// seams come from flags, like the manager's.
func collectorFor(c client.Client, opts Options) (*evidence.Collector, error) {
	collector := &evidence.Collector{Reader: evidence.NewReader(c)}
	if opts.PrometheusURL != "" {
		qc, err := evidence.NewHTTPQueryClient(opts.PrometheusURL)
		if err != nil {
			return nil, err
		}
		collector.Prom = qc
	}
	if opts.LokiURL != "" {
		lc, err := logs.NewHTTPClient(opts.LokiURL)
		if err != nil {
			return nil, err
		}
		collector.Logs = lc
	}
	return collector, nil
}

// ScenarioAll is the --scenario value that runs every pack under
// bench/scenarios/, in name order.
const ScenarioAll = "all"

// Runner holds the resolved world one invocation operates in.
type Runner struct {
	opts     Options
	out      printer
	scns     []*scenario.Scenario
	scn      *scenario.Scenario // the scenario currently being run
	benchDir string
	repoRoot string
	tc       *kube.Toolchain
	c        client.Client
	timings  []timing

	agent       agents.Agent        // nil for --agent none
	collector   *evidence.Collector // the real pipeline, built once per run
	resultsFile *os.File
	resultsPath string
	records     []scoring.Record
}

// Run executes `praxisbench run`.
func Run(ctx context.Context, opts Options, w io.Writer) error {
	ag, err := agentFor(opts)
	if err != nil {
		return err
	}
	if opts.Runs < 1 {
		return fmt.Errorf("--runs is %d; must be at least 1", opts.Runs)
	}

	scnPaths, benchDir, repoRoot, err := locate(opts.Scenario)
	if err != nil {
		return err
	}
	scns := make([]*scenario.Scenario, 0, len(scnPaths))
	for _, path := range scnPaths {
		scn, err := scenario.Load(path)
		if err != nil {
			return err
		}
		scns = append(scns, scn)
	}
	tc, err := kube.NewToolchain(repoRoot, filepath.Join(benchDir, kubeconfigName))
	if err != nil {
		return err
	}

	r := &Runner{
		opts:     opts,
		out:      printer{w: w},
		scns:     scns,
		benchDir: benchDir,
		repoRoot: repoRoot,
		tc:       tc,
		agent:    ag,
	}
	if err := r.openResults(); err != nil {
		return err
	}
	defer r.closeResults()
	return r.run(ctx)
}

// openResults creates this invocation's JSONL record file up front, so a
// run that dies mid-suite still leaves the completed runs' records behind.
func (r *Runner) openResults() error {
	dir := r.opts.ResultsDir
	if dir == "" {
		dir = filepath.Join(r.benchDir, "results")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create results directory %s: %w", dir, err)
	}
	r.resultsPath = filepath.Join(dir,
		fmt.Sprintf("run-%s-%s.jsonl", time.Now().UTC().Format("20060102-150405"), r.opts.Agent))
	f, err := os.OpenFile(r.resultsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create results file: %w", err)
	}
	r.resultsFile = f
	return nil
}

func (r *Runner) closeResults() {
	if r.resultsFile != nil {
		if err := r.resultsFile.Close(); err != nil {
			r.out.f("    warning: closing %s: %v", r.resultsPath, err)
		}
	}
}

func (r *Runner) run(ctx context.Context) error {
	names := make([]string, len(r.scns))
	for i, scn := range r.scns {
		names[i] = scn.Name
	}
	r.out.f("==> praxisbench run: scenarios %v (runs=%d each, agent=%s, keep=%v)",
		names, r.opts.Runs, r.opts.Agent, r.opts.Keep)
	r.out.f("    cluster %q · kubeconfig %s", KindClusterName, r.tc.Kubeconfig)
	r.out.f("    run records → %s", r.resultsPath)

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
	if r.collector, err = collectorFor(c, r.opts); err != nil {
		return err
	}
	if r.agent != nil {
		if r.opts.PrometheusURL == "" {
			r.out.f("    no --prometheus-url: bundles will carry no Metric evidence")
		}
		if r.opts.LokiURL == "" {
			r.out.f("    no --loki-url: bundles will carry no LogTemplate evidence")
		}
	}

	outcomes := make([]outcome, 0, len(r.scns)*r.opts.Runs)
	for _, scn := range r.scns {
		r.scn = scn
		for i := range r.opts.Runs {
			oc, err := r.runOnce(ctx, i+1)
			if err != nil {
				return fmt.Errorf("scenario %s run %d/%d: %w", scn.Name, i+1, r.opts.Runs, err)
			}
			outcomes = append(outcomes, oc)
		}
	}

	r.printSummary(outcomes)
	if err := r.printReport(); err != nil {
		return err
	}
	r.out.f("")
	r.out.f("==> run records: %s (praxisbench report --input %s re-renders the table)", r.resultsPath, r.resultsPath)
	return nil
}

// printReport renders the aggregate score table for the records this
// invocation produced.
func (r *Runner) printReport() error {
	if len(r.records) == 0 {
		return nil
	}
	aggs, err := scoring.AggregateRecords(r.records)
	if err != nil {
		return err
	}
	r.out.f("")
	return scoring.RenderReport(r.out.w, aggs)
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

// locate resolves the --scenario argument — "all", a name under
// bench/scenarios/, or a path — into scenario paths, plus the bench module
// directory and the repository root the CRDs are installed from.
func locate(scenarioArg string) (scnPaths []string, benchDir, repoRoot string, err error) {
	switch scenarioArg {
	case ScenarioAll:
		if benchDir, err = benchDirFromCWD(); err != nil {
			return nil, "", "", err
		}
		if scnPaths, err = allScenarioPaths(benchDir); err != nil {
			return nil, "", "", err
		}
	default:
		var scnPath string
		if info, statErr := os.Stat(scenarioArg); statErr == nil {
			abs, absErr := filepath.Abs(scenarioArg)
			if absErr != nil {
				return nil, "", "", fmt.Errorf("resolve --scenario path %q: %w", scenarioArg, absErr)
			}
			scnPath = abs
			dir := abs
			if !info.IsDir() {
				dir = filepath.Dir(abs)
			}
			if benchDir, err = ascendToBenchDir(dir); err != nil {
				return nil, "", "", err
			}
		} else {
			if benchDir, err = benchDirFromCWD(); err != nil {
				return nil, "", "", err
			}
			scnPath = filepath.Join(benchDir, "scenarios", scenarioArg)
			if _, statErr := os.Stat(scnPath); statErr != nil {
				return nil, "", "", fmt.Errorf("no scenario named %q under %s — pass a name from bench/scenarios/, a path to a scenario.yaml, or %q",
					scenarioArg, filepath.Join(benchDir, "scenarios"), ScenarioAll)
			}
		}
		scnPaths = []string{scnPath}
	}

	repoRoot = filepath.Dir(benchDir)
	if _, statErr := os.Stat(filepath.Join(repoRoot, "config", "crd", "bases")); statErr != nil {
		return nil, "", "", fmt.Errorf("%s has no config/crd/bases — the benchmark installs the praxis CRDs from the repository checkout and cannot run without it", repoRoot)
	}
	return scnPaths, benchDir, repoRoot, nil
}

// allScenarioPaths lists every pack directory under bench/scenarios/ in
// name order. A pack literally named "all" would be unrunnable by name, so
// it is rejected rather than silently shadowed.
func allScenarioPaths(benchDir string) ([]string, error) {
	scenariosDir := filepath.Join(benchDir, "scenarios")
	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		return nil, fmt.Errorf("list scenarios under %s: %w", scenariosDir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == ScenarioAll {
			return nil, fmt.Errorf("%s is a scenario directory, but %q is reserved for running every pack — rename it", filepath.Join(scenariosDir, e.Name()), ScenarioAll)
		}
		paths = append(paths, filepath.Join(scenariosDir, e.Name()))
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no scenario directories under %s", scenariosDir)
	}
	return paths, nil
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

// ScenariosDir resolves bench/scenarios from the working directory — the
// default answer-key location for `praxisbench score`.
func ScenariosDir() (string, error) {
	benchDir, err := benchDirFromCWD()
	if err != nil {
		return "", err
	}
	return filepath.Join(benchDir, "scenarios"), nil
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
