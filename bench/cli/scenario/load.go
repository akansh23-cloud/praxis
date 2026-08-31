/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// FileName is the canonical scenario file name inside a scenario directory.
const FileName = "scenario.yaml"

// Load reads and validates one scenario. path may be the scenario.yaml
// itself or the directory containing it. Every schema violation in the file
// is reported, not just the first one.
func Load(path string) (*Scenario, error) {
	file := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		file = filepath.Join(path, FileName)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}

	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("resolve scenario path %q: %w", file, err)
	}

	var s Scenario
	if err := yaml.UnmarshalStrict(raw, &s); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown field") {
			return nil, fmt.Errorf("parse %s: %v\nthe schema is docs/02-LLD.md §17; field names are case-sensitive", file, err)
		}
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	s.Dir = filepath.Dir(abs)

	if err := s.validate(file); err != nil {
		return nil, err
	}
	return &s, nil
}
