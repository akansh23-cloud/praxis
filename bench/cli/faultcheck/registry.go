/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package faultcheck

// registry maps scenario name → fault-manifested check. One entry per
// shipped pack under bench/scenarios/ — the registry tests enforce the
// correspondence in both directions, so a pack cannot land without its
// check and a check cannot outlive its pack.
var registry = map[string]Check{
	"smoke": smokeInert,
}
