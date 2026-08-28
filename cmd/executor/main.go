/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr,
		"praxis-executor is not implemented yet: the executor is wired up across Phases 2-5.")
	fmt.Fprintln(os.Stderr,
		"Phase 0 ships the API types and repository scaffold only. See docs/00-MASTER-PLAN.md.")
	os.Exit(1)
}
