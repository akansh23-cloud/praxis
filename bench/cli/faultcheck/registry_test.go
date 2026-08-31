/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// shippedScenarioNames reads the pack directories under bench/scenarios/.
func shippedScenarioNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("read bench/scenarios: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("bench/scenarios is empty; at least the smoke pack must exist")
	}
	return names
}

// TestEveryShippedScenarioHasCheck fails the build the moment a pack lands
// without a fault-manifested check — the playbook Session 2.2 rule that
// every scenario must be mechanically verifiable without an agent.
func TestEveryShippedScenarioHasCheck(t *testing.T) {
	for _, name := range shippedScenarioNames(t) {
		if _, ok := Lookup(name); !ok {
			t.Errorf("scenario %q has no fault-manifested check registered in cli/faultcheck", name)
		}
	}
}

// TestEveryCheckHasShippedScenario catches the reverse drift: a check whose
// pack was renamed or removed.
func TestEveryCheckHasShippedScenario(t *testing.T) {
	shipped := shippedScenarioNames(t)
	for _, name := range Names() {
		if !slices.Contains(shipped, name) {
			t.Errorf("check %q is registered but bench/scenarios/%s does not exist", name, name)
		}
	}
}
