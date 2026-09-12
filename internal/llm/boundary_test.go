/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package llm

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The import boundary of playbook Session 3.3 task 6 and LLD §2, at the
// source level for BOTH modules: only the analyzer-side seam may depend
// on model-facing code. Everything else — the manager, the controllers,
// the evidence pipeline, the validators, and every future executor-side
// package — must not import internal/llm or internal/agents. The same
// rule is enforced by golangci-lint's depguard in `make lint`; this test
// is the belt to that brace, and it names the offending file.

// guardedPrefixes are the import paths only the allowlist may name.
var guardedPrefixes = []string{
	"github.com/akansh23-cloud/praxis/internal/llm",
	"github.com/akansh23-cloud/praxis/internal/agents",
}

// allowedDirs (repo-relative) may import the guarded packages: the model
// boundary itself, the agents, and the benchmark's agent driver, which
// stands in for the analyzer until the Phase 5 split gives it a binary.
var allowedDirs = []string{
	"internal/llm/",
	"internal/agents/",
	"bench/cli/agentrun/",
	"bench/cli/runner/",
	"bench/cli/praxisbench/",
}

func TestOnlyTheAnalyzerSeamImportsModelCode(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checked, violations := 0, 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch {
			case rel == ".git" || rel == "bin" || strings.HasPrefix(rel, "bench/bin"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for _, allowed := range allowedDirs {
			if strings.HasPrefix(rel, allowed) {
				return nil
			}
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, guarded := range guardedPrefixes {
				if p == guarded || strings.HasPrefix(p, guarded+"/") {
					violations++
					t.Errorf("%s imports %q: only internal/llm, internal/agents and the benchmark's agent driver may depend on model-facing code", rel, p)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("only %d files checked; the walk is looking in the wrong place", checked)
	}
	t.Logf("%d Go files outside the seam checked, %d violations", checked, violations)
}

// The guard must be positive too: the allowlist really does cover the
// packages that legitimately depend on model code.
func TestAllowedDirsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range allowedDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Errorf("allowed dir %s does not exist: %v", dir, err)
		}
	}
}
