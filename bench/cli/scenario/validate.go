/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// actionVocabulary is the closed action set. Kept in sync by hand with the
// ActionType enum in api/v1alpha1 (ADR-001): a scenario naming a verb the
// API server would reject must be rejected here first, at authoring time.
var actionVocabulary = []praxisv1alpha1.ActionType{
	praxisv1alpha1.ActionRestartWorkload,
	praxisv1alpha1.ActionScaleWorkload,
	praxisv1alpha1.ActionRollbackRelease,
	praxisv1alpha1.ActionPatchResourceLimits,
	praxisv1alpha1.ActionCordonNode,
}

// targetKinds mirrors the TargetRef kind enum in api/v1alpha1.
var targetKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "Node"}

// severities mirrors the Severity enum in api/v1alpha1 (which declares the
// marker but no constants).
var severities = []praxisv1alpha1.Severity{"Critical", "High", "Medium", "Low"}

// rootCauseIDPattern is the id shape §17.3 matching keys use.
var rootCauseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// problems accumulates violations so one Load reports every mistake in the
// file, not just the first.
type problems struct {
	list []string
}

func (p *problems) addf(field, format string, args ...any) {
	p.list = append(p.list, field+": "+fmt.Sprintf(format, args...))
}

func (s *Scenario) validate(file string) error {
	var p problems
	s.validateName(&p)
	s.validateTopology(&p)
	s.validateFault(&p)
	s.validateIncident(&p)
	s.validateGroundTruth(&p)
	if s.TimeoutMinutes < 1 {
		p.addf("timeoutMinutes", "is %d; must be at least 1 — it bounds the wait for a plan (LLD §17)",
			s.TimeoutMinutes)
	}
	if len(p.list) == 0 {
		return nil
	}
	return fmt.Errorf("invalid scenario %s:\n  - %s", file, strings.Join(p.list, "\n  - "))
}

func (s *Scenario) validateName(p *problems) {
	if s.Name == "" {
		p.addf("name", "required — the scenario must state its identity")
		return
	}
	if msgs := validation.IsDNS1123Label(s.Name); len(msgs) > 0 {
		p.addf("name", "%q is not usable in object names: %s", s.Name, strings.Join(msgs, "; "))
		return
	}
	if dir := filepath.Base(s.Dir); s.Name != dir {
		p.addf("name", "is %q but the scenario lives in directory %q; they must match so --scenario %s resolves to this file",
			s.Name, dir, s.Name)
	}
}

func (s *Scenario) validateTopology(p *problems) {
	kz := s.Topology.Kustomize
	switch {
	case kz == "":
		p.addf("topology.kustomize", "required — the overlay that builds the workloads this scenario breaks")
	case filepath.IsAbs(kz):
		p.addf("topology.kustomize", "is %q; must be relative to the scenario directory so the pack is portable", kz)
	default:
		dir := s.TopologyDir()
		if info, err := os.Stat(dir); err != nil {
			p.addf("topology.kustomize", "%q does not exist (resolved to %s)", kz, dir)
		} else if !info.IsDir() {
			p.addf("topology.kustomize", "%q resolves to a file (%s); it must be a kustomize overlay directory", kz, dir)
		} else if _, err := os.Stat(filepath.Join(dir, "kustomization.yaml")); err != nil {
			p.addf("topology.kustomize", "%q has no kustomization.yaml — it must be a kustomize overlay directory", kz)
		}
	}
}

func (s *Scenario) validateFault(p *problems) {
	if !slices.Contains(faultKinds, s.Fault.Kind) {
		p.addf("fault.kind", "is %q; must be one of %s", s.Fault.Kind, join(faultKinds))
	}
	switch ref := s.Fault.Ref; {
	case ref == "":
		p.addf("fault.ref", "required — the fault payload file")
	case filepath.IsAbs(ref):
		p.addf("fault.ref", "is %q; must be relative to the scenario directory so the pack is portable", ref)
	default:
		path := s.FaultPath()
		if info, err := os.Stat(path); err != nil {
			p.addf("fault.ref", "%q does not exist (resolved to %s)", ref, path)
		} else if info.IsDir() {
			p.addf("fault.ref", "%q resolves to a directory (%s); it must be a file", ref, path)
		}
	}
	if strings.TrimSpace(s.Fault.Notes) == "" {
		p.addf("fault.notes", "required — record why this mechanism was chosen over the alternatives (LLD §17)")
	}
}

func (s *Scenario) validateIncident(p *problems) {
	if !slices.Contains(severities, s.Incident.SeverityHint) {
		p.addf("incident.severityHint", "is %q; must be one of %s", s.Incident.SeverityHint, join(severities))
	}
	nss := s.Incident.ScopeNamespaces
	switch {
	case len(nss) == 0:
		p.addf("incident.scopeNamespaces", "must list at least one namespace — it is both the Incident scope and what the runner creates and tears down")
	case len(nss) > 10:
		p.addf("incident.scopeNamespaces", "has %d entries; the Incident CRD caps scope at 10 namespaces", len(nss))
	}
	seen := map[string]bool{}
	for i, ns := range nss {
		if msgs := validation.IsDNS1123Label(ns); len(msgs) > 0 {
			p.addf(fmt.Sprintf("incident.scopeNamespaces[%d]", i), "%q is not a valid namespace name: %s", ns, strings.Join(msgs, "; "))
		}
		if seen[ns] {
			p.addf("incident.scopeNamespaces", "duplicate namespace %q", ns)
		}
		seen[ns] = true
	}
}

func (s *Scenario) validateGroundTruth(p *problems) {
	gt := &s.GroundTruth
	switch {
	case gt.RootCauseID == "":
		p.addf("groundTruth.rootCauseId", "required — the §17.3 diagnosis matching key")
	case !rootCauseIDPattern.MatchString(gt.RootCauseID):
		p.addf("groundTruth.rootCauseId", "is %q; must be a lowercase id matching %s", gt.RootCauseID, rootCauseIDPattern)
	}

	validateActionRefs(p, "groundTruth.acceptableActions", gt.AcceptableActions)
	validateActionRefs(p, "groundTruth.forbiddenActions", gt.ForbiddenActions)

	switch {
	case gt.RestraintExpected && len(gt.AcceptableActions) > 0:
		p.addf("groundTruth.restraintExpected",
			"is true but acceptableActions is not empty — a restraint scenario's only correct answer is no plan (the praxis.dev/no-action-proposed annotation on the Incident)")
	case !gt.RestraintExpected && len(gt.AcceptableActions) == 0:
		p.addf("groundTruth.acceptableActions",
			"is empty while restraintExpected is false — name at least one acceptable action, or declare the scenario a restraint scenario")
	}

	for i, f := range gt.ForbiddenActions {
		for _, a := range gt.AcceptableActions {
			if f.Type != a.Type {
				continue
			}
			// A bare entry covers every target of its type, so it clashes
			// with any entry of the same type on the other side.
			if f.Target == nil || a.Target == nil || *f.Target == *a.Target {
				p.addf(fmt.Sprintf("groundTruth.forbiddenActions[%d]", i),
					"%s contradicts acceptableActions — the same action cannot be both", describe(f))
			}
		}
	}
}

func validateActionRefs(p *problems, field string, refs []ActionRef) {
	for i, ref := range refs {
		if !slices.Contains(actionVocabulary, ref.Type) {
			p.addf(fmt.Sprintf("%s[%d].type", field, i),
				"is %q; the closed vocabulary is %s (ADR-001)", ref.Type, join(actionVocabulary))
		}
		if ref.Target == nil {
			continue
		}
		if !slices.Contains(targetKinds, ref.Target.Kind) {
			p.addf(fmt.Sprintf("%s[%d].target.kind", field, i),
				"is %q; must be one of %s", ref.Target.Kind, join(targetKinds))
		}
		if ref.Target.Name == "" {
			p.addf(fmt.Sprintf("%s[%d].target.name", field, i), "required when a target is given")
		}
	}
}

func describe(ref ActionRef) string {
	if ref.Target == nil {
		return string(ref.Type)
	}
	return fmt.Sprintf("%s on %s/%s", ref.Type, ref.Target.Kind, ref.Target.Name)
}

func join[T ~string](vals []T) string {
	strs := make([]string, len(vals))
	for i, v := range vals {
		strs[i] = string(v)
	}
	return strings.Join(strs, ", ")
}
