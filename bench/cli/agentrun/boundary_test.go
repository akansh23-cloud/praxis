/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestAgentInputsAreScenarioBlind proves the firewall between answer keys
// and agents at the source level: no production file of this package may
// import the scenario package (identity, groundTruth) or the scoring
// package (the referee). Test files are exempt — the leak tests
// themselves load real packs to know which strings must never appear.
func TestAgentInputsAreScenarioBlind(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, banned := range []string{"/bench/cli/scenario", "/bench/cli/scoring"} {
				if strings.HasSuffix(path, banned) {
					t.Errorf("%s imports %q: agentrun must stay blind to scenario identity and ground truth — "+
						"an agent input built here could otherwise smuggle the answer key", name, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no production sources checked; the test is looking in the wrong place")
	}
}
