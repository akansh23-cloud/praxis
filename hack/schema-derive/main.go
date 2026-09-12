/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// schema-derive derives the planner's JSON Schemas from the RemediationPlan
// CRD (playbook Session 3.3 task 2) and writes them into internal/planschema,
// where they are embedded. The CRD is the single source of truth: this tool
// is the only way those files change, and -check fails when they are stale.
//
//	go run ./hack/schema-derive          # regenerate (make schema-derive)
//	go run ./hack/schema-derive -check   # verify, exit 1 if stale (make schema-check)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akansh23-cloud/praxis/internal/planschema"
)

func main() {
	crd := flag.String("crd", filepath.Join("config", "crd", "bases", "praxis.dev_remediationplans.yaml"),
		"path to the RemediationPlan CRD manifest (the single source of truth)")
	out := flag.String("out", filepath.Join("internal", "planschema"), "directory the derived schemas are written to")
	check := flag.Bool("check", false, "verify the derived files are current instead of writing them")
	flag.Parse()

	raw, err := os.ReadFile(*crd)
	if err != nil {
		fail("read CRD: %v", err)
	}
	artifacts, err := planschema.Derive(raw)
	if err != nil {
		fail("derive: %v", err)
	}
	files := map[string][]byte{
		"remediationplanspec.openapi.json": artifacts.SpecOpenAPI,
		"planner.schema.json":              artifacts.Planner,
		"hypotheses.schema.json":           artifacts.Hypotheses,
	}
	stale := 0
	for name, want := range files {
		path := filepath.Join(*out, name)
		if *check {
			have, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(have, want) {
				fmt.Fprintf(os.Stderr, "schema-derive: %s is stale or missing — run `make schema-derive`\n", path)
				stale++
			}
			continue
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			fail("write %s: %v", path, err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, len(want))
	}
	if stale > 0 {
		os.Exit(1)
	}
	if *check {
		fmt.Printf("schema-derive: %d derived files match %s\n", len(files), *crd)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "schema-derive: "+format+"\n", args...)
	os.Exit(1)
}
