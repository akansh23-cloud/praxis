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
	"unicode"

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

// evidenceSources is the closed set of evidence-id source tokens — the §6
// evidence type enum, lowercased (ADR-005). A pattern naming any other
// token demands evidence the Phase 3 collector will never produce.
var evidenceSources = []string{
	"podstatus", "event", "ownerchain", "metric", "logtemplate", "syncstate", "gitcommit",
}

// evidencePatternShape is the ev/<source>-<glob> form §17.3 patterns take
// (LLD §6 id scheme: ev/<source>-<seq>).
var evidencePatternShape = regexp.MustCompile(`^ev/([a-z]+)-(\S+)$`)

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

	validateDiagnosis(p, &gt.Diagnosis)
	validatePredicates(p, gt)
	s.validatePlantedTelemetry(p)

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

// validateDiagnosis enforces the §17.3 matching rule (ADR-005): both lists
// present and non-empty, patterns in ev/<source>-* form over the closed
// source-token set, keyphrases canonical lowercase, nothing blank or
// duplicated — the answer key must be usable exactly as authored.
func validateDiagnosis(p *problems, d *DiagnosisRule) {
	if len(d.RequiredEvidenceIDPatterns) == 0 {
		p.addf("groundTruth.diagnosis.requiredEvidenceIdPatterns",
			"required — at least one ev/<source>-* pattern the correct diagnosis must cite (LLD §17.3, ADR-005)")
	}
	seenPat := map[string]bool{}
	for i, pat := range d.RequiredEvidenceIDPatterns {
		field := fmt.Sprintf("groundTruth.diagnosis.requiredEvidenceIdPatterns[%d]", i)
		m := evidencePatternShape.FindStringSubmatch(pat)
		switch {
		case m == nil:
			p.addf(field, "is %q; must have the form ev/<source>-<glob> (the §6 id scheme, e.g. ev/gitcommit-*)", pat)
		case !slices.Contains(evidenceSources, m[1]):
			p.addf(field, "source %q is not an evidence type; the closed set is %s (§6 types lowercased, ADR-005)",
				m[1], strings.Join(evidenceSources, ", "))
		}
		if seenPat[pat] {
			p.addf(field, "duplicate pattern %q", pat)
		}
		seenPat[pat] = true
	}

	if len(d.RequiredSummaryKeyphrases) == 0 {
		p.addf("groundTruth.diagnosis.requiredSummaryKeyphrases",
			"required — at least one lowercase substring the correct summary must contain (LLD §17.3, ADR-005)")
	}
	seenKey := map[string]bool{}
	for i, key := range d.RequiredSummaryKeyphrases {
		field := fmt.Sprintf("groundTruth.diagnosis.requiredSummaryKeyphrases[%d]", i)
		switch {
		case strings.TrimSpace(key) != key || len(key) < 3:
			p.addf(field, "is %q; keyphrases must be at least 3 characters with no leading/trailing space — matching is exact substring", key)
		case key != strings.ToLower(key):
			p.addf(field, "is %q; keyphrases are stored lowercase (matching is case-insensitive, lowercase is canonical)", key)
		}
		if seenKey[key] {
			p.addf(field, "duplicate keyphrase %q", key)
		}
		seenKey[key] = true
	}
}

// validatePredicates enforces that the effect-side ground truth exists now
// even though nothing evaluates it before Phase 5 (ADR-005). Content is
// not parsed: PromQL parsing would drag a Prometheus dependency into the
// bench module for no Phase 2 benefit.
func validatePredicates(p *problems, gt *GroundTruth) {
	if strings.TrimSpace(gt.FixPredicate) == "" {
		p.addf("groundTruth.fixPredicate",
			"required — the PromQL declaring what \"fixed\" means for this scenario (evaluated from Phase 5; ADR-005)")
	}
	if strings.TrimSpace(gt.HarmPredicate) == "" {
		p.addf("groundTruth.harmPredicate",
			"required — the PromQL declaring what \"harmed\" means for this scenario (evaluated from Phase 5; ADR-005)")
	}
}

// maxPlantBytes bounds one planted telemetry string. The Loki collector
// normalizes a line to at most 1 KiB before templating
// (internal/evidence/logs.MaxLineBytes), so a longer plant could never
// appear verbatim in a bundle and the visibility metric would be false
// by construction.
const maxPlantBytes = 1024

// validatePlantedTelemetry enforces that every planted string can be
// checked exactly as authored (ADR-010): non-blank, no surrounding
// whitespace or control characters (the collector trims and strips them,
// so such a plant could never match), bounded, unique, present verbatim
// in the fault payload that writes it (ground truth and fault cannot
// drift apart), and free of benchmark identity — a plant naming the
// scenario, its rootCauseId or the answer-key marker would trip the
// runner's live scenario-blind guard on every run.
func (s *Scenario) validatePlantedTelemetry(p *problems) {
	plants := s.GroundTruth.PlantedTelemetry
	if len(plants) == 0 {
		return
	}
	var fault []byte
	if path := s.FaultPath(); s.Fault.Ref != "" && !filepath.IsAbs(s.Fault.Ref) {
		// A missing payload is already reported by validateFault; the
		// verbatim check below then simply cannot succeed.
		fault, _ = os.ReadFile(path)
	}
	seen := map[string]bool{}
	for i, plant := range plants {
		field := fmt.Sprintf("groundTruth.plantedTelemetry[%d]", i)
		switch {
		case strings.TrimSpace(plant) == "":
			p.addf(field, "is blank; a planted string must be text the fault writes into telemetry (ADR-010)")
			continue
		case strings.TrimSpace(plant) != plant:
			p.addf(field, "has leading or trailing whitespace; the collector trims lines, so it could never match verbatim")
		case len(plant) > maxPlantBytes:
			p.addf(field, "is %d bytes; the collector cuts lines at %d bytes, so it could never appear verbatim", len(plant), maxPlantBytes)
		case strings.ContainsFunc(plant, unicode.IsControl):
			p.addf(field, "contains a control character; the collector strips them, so it could never match verbatim")
		}
		lower := strings.ToLower(plant)
		for _, identity := range []string{strings.ToLower(s.Name), strings.ToLower(s.GroundTruth.RootCauseID), "groundtruth"} {
			if identity != "" && strings.Contains(lower, identity) {
				p.addf(field, "contains benchmark identity %q; a plant is telemetry the agent sees, and the runner refuses inputs carrying the scenario name, rootCauseId or answer-key marker", identity)
			}
		}
		if seen[plant] {
			p.addf(field, "duplicate planted string")
		}
		seen[plant] = true
		if fault != nil && !strings.Contains(string(fault), plant) {
			p.addf(field, "does not appear verbatim in the fault payload %s; the ground truth must name exactly what the fault writes", s.Fault.Ref)
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
