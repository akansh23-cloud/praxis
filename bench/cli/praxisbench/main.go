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
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/akansh23-cloud/praxis/bench/cli/runner"
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
		`agent expected to respond; only "none" exists until the scorer session adds the rule-based baseline`)
	_ = cmd.MarkFlagRequired("scenario")
	return cmd
}

func newScoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "score",
		Short: "Score recorded runs against a scenario's ground truth (Session 2.3)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("score is not implemented yet: the scorer and its JSONL output arrive with " +
				"Session 2.3 (docs/03-CLAUDE-CODE-PLAYBOOK.md); until then `run` reports outcomes on stdout only")
		},
	}
}

func newReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Aggregate scores over N runs into a summary (Session 2.3)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("report is not implemented yet: aggregate reporting (mean/min/max over N runs) " +
				"arrives with Session 2.3 alongside the scorer")
		},
	}
}
