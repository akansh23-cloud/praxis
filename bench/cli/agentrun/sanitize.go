/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"strings"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// benchMetaPrefix is the metadata namespace the benchmark owns on cluster
// objects (e.g. the praxis.dev/bench-scenario label the runner puts on
// Incidents for humans inspecting a kept run). Everything under it is
// benchmark bookkeeping — never observable input for an agent, because
// Session 2.2 showed the scenario name doubles as an answer-key index.
const benchMetaPrefix = "praxis.dev/bench"

// SanitizeIncident returns the copy of an Incident an Agent is allowed to
// see: the spec verbatim (the runner keeps it scenario-neutral by
// construction), with benchmark-owned labels/annotations and the
// managedFields noise removed. The original is never modified.
func SanitizeIncident(inc *praxisv1alpha1.Incident) *praxisv1alpha1.Incident {
	s := inc.DeepCopy()
	s.ManagedFields = nil
	stripBenchMeta(s.Labels)
	stripBenchMeta(s.Annotations)
	return s
}

func stripBenchMeta(m map[string]string) {
	for k := range m {
		if strings.HasPrefix(k, benchMetaPrefix) {
			delete(m, k)
		}
	}
}
