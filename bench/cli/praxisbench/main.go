/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// praxisbench is the Praxis benchmark CLI (docs/02-LLD.md §17). It exists
// before any AI does, deliberately: the harness that grades agents is built
// first so no model ever grades its own homework. Nothing in this module may
// import LLM code or depend on a model SDK — Phase 2 rule, AGENTS.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/akansh23-cloud/praxis/bench/cli/runner"
	"github.com/akansh23-cloud/praxis/bench/cli/scoring"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "praxisbench:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "praxisbench",
		Short: "Run, score and report Praxis benchmark scenarios",
		Long: "praxisbench drives benchmark scenarios against a local kind cluster:\n" +
			"deploy the shop topology, inject a fault, file an Incident, wait for a\n" +
			"RemediationPlan (or the praxis.dev/no-action-proposed annotation), score\n" +
			"the response, tear down. The scenario schema is docs/02-LLD.md §17.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd(), newScoreCmd(), newReportCmd())
	return root
}

func newRunCmd() *cobra.Command {
	var opts runner.Options
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a scenario end-to-end against the praxis-bench kind cluster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runner.Run(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.Scenario, "scenario", "",
		"scenario to run: a name under bench/scenarios/, a path to a scenario.yaml, or \"all\" for every pack (required)")
	cmd.Flags().IntVar(&opts.Runs, "runs", 1, "number of times to run the scenario")
	cmd.Flags().BoolVar(&opts.Keep, "keep", false,
		"keep the scenario namespaces and Incident after the run instead of tearing them down")
	cmd.Flags().StringVar(&opts.Agent, "agent", runner.AgentNone,
		`agent driven through the seam: "none" (nothing responds; the wait times out gracefully), "rulebased" (the intentionally dumb baseline) or "llm" (the model-backed agent; needs ANTHROPIC_API_KEY for provider anthropic)`)
	cmd.Flags().StringVar(&opts.ResultsDir, "results-dir", "",
		"directory for this invocation's scored JSONL run records (default bench/results/)")
	cmd.Flags().StringVar(&opts.PrometheusURL, "prometheus-url", "",
		"Prometheus base URL for the evidence collector's Metric items (e.g. a port-forward of svc/prometheus-server in monitoring); empty omits them")
	cmd.Flags().StringVar(&opts.LokiURL, "loki-url", "",
		"Loki base URL for the evidence collector's LogTemplate items (e.g. a port-forward of svc/loki in monitoring); empty omits them")
	cmd.Flags().StringVar(&opts.LLMProvider, "llm-provider", "anthropic", `LLM provider for --agent llm: "anthropic" or "ollama"`)
	cmd.Flags().StringVar(&opts.LLMModel, "llm-model", "", "LLM model id for --agent llm (default claude-opus-5 for anthropic; required for ollama)")
	cmd.Flags().StringVar(&opts.LLMBaseURL, "llm-base-url", "", "LLM endpoint override (a proxy, or a local Ollama on a non-default port)")
	cmd.Flags().StringVar(&opts.LLMEffort, "llm-effort", "", "Anthropic output effort for --agent llm: low, medium, high, xhigh or max (default: the provider's)")
	_ = cmd.MarkFlagRequired("scenario")
	return cmd
}

func newScoreCmd() *cobra.Command {
	var input, output, scenariosDir string
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Re-score recorded runs against the scenario answer keys",
		Long: "score re-referees existing run records: every record's metrics are recomputed\n" +
			"from its raw observations (hypotheses, plan, response) against the groundTruth\n" +
			"of the named scenarios — the path for re-scoring after an answer-key fix.\n" +
			"`run` already scores as it records, so this is only needed to re-referee.",
		RunE: func(_ *cobra.Command, _ []string) error {
			records, err := scoring.ReadRecords(input)
			if err != nil {
				return err
			}
			if scenariosDir == "" {
				if scenariosDir, err = runner.ScenariosDir(); err != nil {
					return err
				}
			}
			if err := scoring.Rescore(records, scenariosDir); err != nil {
				return err
			}
			out := os.Stdout
			if output != "-" {
				f, err := os.Create(output)
				if err != nil {
					return fmt.Errorf("create %s: %w", output, err)
				}
				defer f.Close() //nolint:errcheck // flushed by the loop's checked writes
				out = f
			}
			for i := range records {
				if err := scoring.AppendRecord(out, &records[i]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "run-record JSONL file, or a directory of them (required)")
	cmd.Flags().StringVar(&output, "output", "-", `where to write the re-scored JSONL ("-" for stdout)`)
	cmd.Flags().StringVar(&scenariosDir, "scenarios-dir", "", "answer-key location (default bench/scenarios/)")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func newReportCmd() *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Aggregate scored run records into the mean/min/max table",
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := scoring.ReadRecords(input)
			if err != nil {
				return err
			}
			aggs, err := scoring.AggregateRecords(records)
			if err != nil {
				return err
			}
			return scoring.RenderReport(cmd.OutOrStdout(), aggs)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "run-record JSONL file, or a directory of them (required)")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}
