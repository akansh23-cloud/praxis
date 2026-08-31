/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agents

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent seam's world is (Incident, Bundle) and nothing else. This test
// makes that structural: every package on the seam — the interface, the
// rule-based baseline, and the evidence types they are defined over — may
// import ONLY the prefixes below. No cluster client, no controller
// machinery, no model SDK can appear without failing this test, so an
// agent that "reads the cluster" or "phones a model" cannot even compile
// into the seam unnoticed. (Benchmark internals live in the bench module,
// which the main module does not depend on at all — that direction is
// impossible by construction; this guards the rest.)
var allowedImportPrefixes = []string{
	"github.com/akansh23-cloud/praxis/api/v1alpha1",
	"github.com/akansh23-cloud/praxis/internal/agents",
	"github.com/akansh23-cloud/praxis/internal/evidence",
	"github.com/akansh23-cloud/praxis/internal/hash",
	"k8s.io/apimachinery/",
}

// forbiddenImportSubstrings name the classes of dependency whose absence
// is the point, so a violation fails with a message that says why.
var forbiddenImportSubstrings = map[string]string{
	"client-go":          "agents must not hold a cluster client — evidence arrives only via the bundle",
	"controller-runtime": "agents must not hold a cluster client — evidence arrives only via the bundle",
	"anthropic":          "no model SDK before Phase 3 (and never in the seam definition)",
	"openai":             "no model SDK before Phase 3 (and never in the seam definition)",
	"ollama":             "no model SDK before Phase 3 (and never in the seam definition)",
	"/bench":             "the main module must never depend on the benchmark",
}

func TestSeamPackagesImportOnlyTheAllowlist(t *testing.T) {
	dirs := map[string]string{
		"internal/agents":           ".",
		"internal/agents/rulebased": "rulebased",
		"internal/evidence":         "../evidence",
	}
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
					continue
				}
				checkImports(t, filepath.Join(dir, e.Name()))
			}
		})
	}
}

func checkImports(t *testing.T, file string) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for substr, why := range forbiddenImportSubstrings {
			if strings.Contains(path, substr) {
				t.Errorf("%s imports %q: %s", file, path, why)
			}
		}
		if isStdlib(path) {
			continue
		}
		allowed := false
		for _, prefix := range allowedImportPrefixes {
			if strings.HasPrefix(path, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("%s imports %q, outside the seam allowlist %v — the agent seam's world is (Incident, Bundle) only",
				file, path, allowedImportPrefixes)
		}
	}
}

// isStdlib: standard library import paths have no dot in their first
// segment (net/http, context), module paths do (k8s.io/..., github.com/...).
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
