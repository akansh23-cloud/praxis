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
// makes that structural, per package:
//
//   - internal/agents and its implementations may import ONLY the seam
//     types: no cluster client, no controller machinery, no model SDK. An
//     agent that "reads the cluster" or "phones a model" cannot even
//     compile into the seam unnoticed.
//   - internal/evidence became the COLLECTOR in Session 3.1 (LLD §2:
//     collectors, assembly, hashing live there), so it additionally holds
//     the typed k8s API and exactly one controller-runtime path —
//     pkg/client, wrapped once into the secretless Reader whose closed
//     method set reader_test.go pins. Anything else from
//     controller-runtime (manager, cache, builder) stays out. Its logs
//     subpackage (Session 3.2: Loki client + templating) is held to the
//     same list and in practice imports only the standard library.
//
// (Benchmark internals live in the bench module, which the main module
// does not depend on at all — that direction is impossible by
// construction; this guards the rest.)
// agentSeamImports is the complete world of the agent packages.
var agentSeamImports = []string{
	"github.com/akansh23-cloud/praxis/api/v1alpha1",
	"github.com/akansh23-cloud/praxis/internal/agents",
	"github.com/akansh23-cloud/praxis/internal/evidence",
	"github.com/akansh23-cloud/praxis/internal/hash",
	"k8s.io/apimachinery/",
}

// evidenceImports adds the collector's few extras to the seam types.
var evidenceImports = append([]string{
	"k8s.io/api/",
	"sigs.k8s.io/controller-runtime/pkg/client",
	"sigs.k8s.io/yaml", // rbac_test.go parses the shipped roles
}, agentSeamImports...)

var seamImportRules = []struct {
	name    string
	dir     string
	allowed []string
}{
	{name: "internal/agents", dir: ".", allowed: agentSeamImports},
	{name: "internal/agents/rulebased", dir: "rulebased", allowed: agentSeamImports},
	{name: "internal/evidence", dir: "../evidence", allowed: evidenceImports},
	// The log templating subpackage (Session 3.2) is stdlib-only by
	// design: no Drain library, no HTTP framework, nothing that could
	// grow into a client — the same allowlist proves it.
	{name: "internal/evidence/logs", dir: "../evidence/logs", allowed: evidenceImports},
}

// forbiddenImportSubstrings name the classes of dependency whose absence
// is the point for EVERY seam package, so a violation fails with a
// message that says why.
const (
	whyNoClient = "agents must not hold a cluster client — evidence arrives only via the bundle"
	whyNoSDK    = "no model SDK before Session 3.3 (and never in the seam definition)"
)

var forbiddenImportSubstrings = map[string]string{
	"client-go": whyNoClient,
	"anthropic": whyNoSDK,
	"openai":    whyNoSDK,
	"ollama":    whyNoSDK,
	"/bench":    "the main module must never depend on the benchmark",
}

// agentOnlyForbidden applies to the agent packages on top of the shared
// list: the evidence collector legitimately wraps controller-runtime's
// client, agents never touch it in any form.
var agentOnlyForbidden = map[string]string{
	"controller-runtime": whyNoClient,
}

func TestSeamPackagesImportOnlyTheAllowlist(t *testing.T) {
	for _, rules := range seamImportRules {
		t.Run(rules.name, func(t *testing.T) {
			entries, err := os.ReadDir(rules.dir)
			if err != nil {
				t.Fatal(err)
			}
			agentPackage := strings.HasPrefix(rules.name, "internal/agents")
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
					continue
				}
				checkImports(t, filepath.Join(rules.dir, e.Name()), rules.allowed, agentPackage)
			}
		})
	}
}

func checkImports(t *testing.T, file string, allowedPrefixes []string, agentPackage bool) {
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
		if agentPackage {
			for substr, why := range agentOnlyForbidden {
				if strings.Contains(path, substr) {
					t.Errorf("%s imports %q: %s", file, path, why)
				}
			}
		}
		if isStdlib(path) {
			continue
		}
		allowed := false
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(path, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("%s imports %q, outside the seam allowlist %v",
				file, path, allowedPrefixes)
		}
	}
}

// isStdlib: standard library import paths have no dot in their first
// segment (net/http, context), module paths do (k8s.io/..., github.com/...).
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
