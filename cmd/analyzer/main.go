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
		"praxis-analyzer is not implemented yet: the analyzer is wired up in Phase 3.")
	fmt.Fprintln(os.Stderr,
		"Phase 0 ships the API types and repository scaffold only. See docs/00-MASTER-PLAN.md.")
	os.Exit(1)
}
