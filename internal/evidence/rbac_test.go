/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// TestRBACGrantsNoSecretAccess is the manifest half of the structural
// no-secrets invariant (LLD §16: analyzer secrets — "none, asserted by
// test"). It walks every role shipped under config/rbac and rejects any
// rule that could reach Secrets: a literal secrets resource, or a core /
// wildcard apiGroup with a wildcard resource. Together with the Reader's
// closed method set this makes secret access impossible twice over — no
// client method to ask with, no permission had one existed.
func TestRBACGrantsNoSecretAccess(t *testing.T) {
	rbacDir := filepath.Join("..", "..", "config", "rbac")
	entries, err := os.ReadDir(rbacDir)
	if err != nil {
		t.Fatalf("reading %s: %v", rbacDir, err)
	}

	type role struct {
		Kind  string              `json:"kind"`
		Rules []rbacv1.PolicyRule `json:"rules"`
	}

	rolesSeen := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(rbacDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for doc := range strings.SplitSeq(string(raw), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var r role
			if err := yaml.Unmarshal([]byte(doc), &r); err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			if !strings.Contains(r.Kind, "Role") {
				continue
			}
			rolesSeen++
			for _, rule := range r.Rules {
				coversCore := false
				for _, g := range rule.APIGroups {
					if g == "" || g == "*" {
						coversCore = true
					}
				}
				for _, res := range rule.Resources {
					if strings.HasPrefix(res, "secrets") {
						t.Errorf("%s (%s) grants access to %q — the evidence invariant forbids any secrets rule",
							path, r.Kind, res)
					}
					if res == "*" && coversCore {
						t.Errorf("%s (%s) grants a wildcard core resource rule — that includes secrets",
							path, r.Kind)
					}
				}
			}
		}
	}
	if rolesSeen == 0 {
		t.Fatal("no Role/ClusterRole manifests found under config/rbac — the assertion checked nothing")
	}
}
