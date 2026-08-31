/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// Field paths and message fragments asserted by three or more cases.
const (
	wantRequired        = "required"
	fieldKustomize      = "topology.kustomize"
	fieldScopeNamespace = "incident.scopeNamespaces"
)

// baseYAML is a fully valid scenario. Reject cases below mutate exactly one
// aspect of it, so each test failure names precisely one rule.
const baseYAML = `name: demo
topology:
  kustomize: ../../topology/overlays/demo
fault:
  kind: Patch
  ref: fault.yaml
  notes: a patch is the simplest reliable mechanism for this fault
incident:
  severityHint: High
  scopeNamespaces: [shop]
groundTruth:
  rootCauseId: memory-limit-lowered
  diagnosis:
    requiredEvidenceIdPatterns: [ev/gitcommit-*, ev/podstatus-*]
    requiredSummaryKeyphrases: [checkout-api, memory limit]
  acceptableActions:
    - type: PatchResourceLimits
      target: {kind: Deployment, name: checkout-api}
  forbiddenActions:
    - type: ScaleWorkload
  restraintExpected: false
  fixPredicate: 'up{job="demo"} == 1'
  harmPredicate: 'up{job="demo"} == 0'
timeoutMinutes: 12
`

// writeFixture lays out a temporary bench-like tree:
//
//	<root>/scenarios/demo/{scenario.yaml,fault.yaml}
//	<root>/topology/overlays/demo/kustomization.yaml
//	<root>/topology/overlays/no-kustomization/
//
// and returns the scenario directory.
func writeFixture(t *testing.T, scenarioYAML string) string {
	t.Helper()
	root := t.TempDir()
	scnDir := filepath.Join(root, "scenarios", "demo")
	overlay := filepath.Join(root, "topology", "overlays", "demo")
	for _, dir := range []string{scnDir, overlay, filepath.Join(root, "topology", "overlays", "no-kustomization")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(scnDir, FileName):              scenarioYAML,
		filepath.Join(scnDir, "fault.yaml"):          "# fault payload\n",
		filepath.Join(overlay, "kustomization.yaml"): "resources: []\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return scnDir
}

// mutate replaces old with new in baseYAML and fails the test if old is not
// present — so a refactor of baseYAML cannot silently neuter a case.
func mutate(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(baseYAML, old) {
		t.Fatalf("test bug: baseYAML does not contain %q", old)
	}
	return strings.Replace(baseYAML, old, new, 1)
}

func TestLoadValid(t *testing.T) {
	scnDir := writeFixture(t, baseYAML)

	for name, path := range map[string]string{
		"file path": filepath.Join(scnDir, FileName),
		"dir path":  scnDir,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%q) failed: %v", path, err)
			}
			if s.Name != "demo" {
				t.Errorf("Name = %q, want demo", s.Name)
			}
			if s.Dir != scnDir {
				t.Errorf("Dir = %q, want %q", s.Dir, scnDir)
			}
			if s.Fault.Kind != FaultPatch {
				t.Errorf("Fault.Kind = %q, want Patch", s.Fault.Kind)
			}
			if s.Incident.SeverityHint != praxisv1alpha1.Severity("High") {
				t.Errorf("SeverityHint = %q, want High", s.Incident.SeverityHint)
			}
			if got := s.Timeout(); got != 12*time.Minute {
				t.Errorf("Timeout() = %v, want 12m", got)
			}
			if _, err := os.Stat(s.TopologyDir()); err != nil {
				t.Errorf("TopologyDir() %q does not resolve: %v", s.TopologyDir(), err)
			}
			if _, err := os.Stat(s.FaultPath()); err != nil {
				t.Errorf("FaultPath() %q does not resolve: %v", s.FaultPath(), err)
			}
			if len(s.GroundTruth.AcceptableActions) != 1 ||
				s.GroundTruth.AcceptableActions[0].Type != praxisv1alpha1.ActionPatchResourceLimits ||
				s.GroundTruth.AcceptableActions[0].Target == nil ||
				s.GroundTruth.AcceptableActions[0].Target.Name != "checkout-api" {
				t.Errorf("AcceptableActions = %+v, want one PatchResourceLimits on checkout-api", s.GroundTruth.AcceptableActions)
			}
			if len(s.GroundTruth.ForbiddenActions) != 1 || s.GroundTruth.ForbiddenActions[0].Target != nil {
				t.Errorf("ForbiddenActions = %+v, want one bare ScaleWorkload", s.GroundTruth.ForbiddenActions)
			}
			diag := s.GroundTruth.Diagnosis
			if len(diag.RequiredEvidenceIDPatterns) != 2 || diag.RequiredEvidenceIDPatterns[0] != "ev/gitcommit-*" {
				t.Errorf("Diagnosis.RequiredEvidenceIDPatterns = %v, want [ev/gitcommit-* ev/podstatus-*]", diag.RequiredEvidenceIDPatterns)
			}
			if len(diag.RequiredSummaryKeyphrases) != 2 || diag.RequiredSummaryKeyphrases[1] != "memory limit" {
				t.Errorf("Diagnosis.RequiredSummaryKeyphrases = %v, want [checkout-api, memory limit]", diag.RequiredSummaryKeyphrases)
			}
			if s.GroundTruth.FixPredicate == "" || s.GroundTruth.HarmPredicate == "" {
				t.Errorf("predicates = (%q, %q), want both populated", s.GroundTruth.FixPredicate, s.GroundTruth.HarmPredicate)
			}
		})
	}
}

// TestShippedScenariosLoad walks the real packs under bench/scenarios/ so a
// pack that drifts from the schema fails `make test` before it fails a run.
func TestShippedScenariosLoad(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("read bench/scenarios: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("bench/scenarios is empty; at least the smoke pack must exist")
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			s, err := Load(filepath.Join("..", "..", "scenarios", e.Name()))
			if err != nil {
				t.Fatalf("shipped scenario does not validate: %v", err)
			}
			if s.Name != e.Name() {
				t.Errorf("Name = %q, want %q", s.Name, e.Name())
			}
			// Every shipped pack must carry its full §17.3 answer key and
			// effect-side ground truth (ADR-005) — the validator enforces
			// this, and these assertions keep the proof explicit even if
			// the validator regresses.
			if len(s.GroundTruth.Diagnosis.RequiredEvidenceIDPatterns) == 0 {
				t.Error("shipped pack has no requiredEvidenceIdPatterns (§17.3)")
			}
			if len(s.GroundTruth.Diagnosis.RequiredSummaryKeyphrases) == 0 {
				t.Error("shipped pack has no requiredSummaryKeyphrases (§17.3)")
			}
			if strings.TrimSpace(s.GroundTruth.FixPredicate) == "" {
				t.Error("shipped pack has no fixPredicate (ADR-005)")
			}
			if strings.TrimSpace(s.GroundTruth.HarmPredicate) == "" {
				t.Error("shipped pack has no harmPredicate (ADR-005)")
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name string
		yaml func(t *testing.T) string
		want []string // substrings the error must contain
	}{
		{
			name: "unknown top-level field",
			yaml: func(t *testing.T) string { return baseYAML + "surprise: true\n" },
			want: []string{"unknown field", "surprise", "§17"},
		},
		{
			name: "unknown nested field",
			yaml: func(t *testing.T) string {
				return mutate(t, "  notes: a patch is the simplest reliable mechanism for this fault",
					"  notes: ok\n  mechanism: kubectl")
			},
			want: []string{"unknown field", "mechanism"},
		},
		{
			name: "name missing",
			yaml: func(t *testing.T) string { return mutate(t, "name: demo", `name: ""`) },
			want: []string{"name:", wantRequired},
		},
		{
			name: "name does not match directory",
			yaml: func(t *testing.T) string { return mutate(t, "name: demo", "name: other") },
			want: []string{"name:", `"other"`, "directory", `"demo"`},
		},
		{
			name: "fault kind outside the closed set",
			yaml: func(t *testing.T) string { return mutate(t, "kind: Patch", "kind: KubectlDelete") },
			want: []string{"fault.kind", "KubectlDelete", "Manifest", "Patch", "ChaosMesh"},
		},
		{
			name: "fault notes empty",
			yaml: func(t *testing.T) string {
				return mutate(t, "  notes: a patch is the simplest reliable mechanism for this fault", `  notes: ""`)
			},
			want: []string{"fault.notes", "why"},
		},
		{
			name: "fault ref does not exist",
			yaml: func(t *testing.T) string { return mutate(t, "ref: fault.yaml", "ref: missing.yaml") },
			want: []string{"fault.ref", "missing.yaml", "does not exist"},
		},
		{
			name: "fault ref absolute",
			yaml: func(t *testing.T) string { return mutate(t, "ref: fault.yaml", "ref: /etc/fault.yaml") },
			want: []string{"fault.ref", "relative"},
		},
		{
			name: "topology dir does not exist",
			yaml: func(t *testing.T) string {
				return mutate(t, "kustomize: ../../topology/overlays/demo", "kustomize: ../../topology/overlays/nope")
			},
			want: []string{fieldKustomize, "does not exist"},
		},
		{
			name: "topology dir lacks kustomization.yaml",
			yaml: func(t *testing.T) string {
				return mutate(t, "kustomize: ../../topology/overlays/demo",
					"kustomize: ../../topology/overlays/no-kustomization")
			},
			want: []string{fieldKustomize, "kustomization.yaml"},
		},
		{
			name: "topology path absolute",
			yaml: func(t *testing.T) string {
				return mutate(t, "kustomize: ../../topology/overlays/demo", "kustomize: /topology/overlays/demo")
			},
			want: []string{fieldKustomize, "relative"},
		},
		{
			name: "severity hint outside the enum",
			yaml: func(t *testing.T) string { return mutate(t, "severityHint: High", "severityHint: Sev1") },
			want: []string{"incident.severityHint", "Sev1", "Critical", "Low"},
		},
		{
			name: "scope namespaces empty",
			yaml: func(t *testing.T) string { return mutate(t, "scopeNamespaces: [shop]", "scopeNamespaces: []") },
			want: []string{fieldScopeNamespace, "at least one"},
		},
		{
			name: "scope namespaces over the Incident CRD cap",
			yaml: func(t *testing.T) string {
				return mutate(t, "scopeNamespaces: [shop]",
					"scopeNamespaces: [n1, n2, n3, n4, n5, n6, n7, n8, n9, n10, n11]")
			},
			want: []string{fieldScopeNamespace, "10"},
		},
		{
			name: "scope namespace not a DNS-1123 label",
			yaml: func(t *testing.T) string { return mutate(t, "scopeNamespaces: [shop]", "scopeNamespaces: [Shop]") },
			want: []string{"incident.scopeNamespaces[0]", "Shop"},
		},
		{
			name: "scope namespaces duplicated",
			yaml: func(t *testing.T) string {
				return mutate(t, "scopeNamespaces: [shop]", "scopeNamespaces: [shop, shop]")
			},
			want: []string{fieldScopeNamespace, "duplicate"},
		},
		{
			name: "root cause id missing",
			yaml: func(t *testing.T) string { return mutate(t, "rootCauseId: memory-limit-lowered", `rootCauseId: ""`) },
			want: []string{"groundTruth.rootCauseId", wantRequired},
		},
		{
			name: "root cause id not an id",
			yaml: func(t *testing.T) string {
				return mutate(t, "rootCauseId: memory-limit-lowered", "rootCauseId: Memory Limit")
			},
			want: []string{"groundTruth.rootCauseId", "lowercase"},
		},
		{
			name: "action type outside the closed vocabulary",
			yaml: func(t *testing.T) string {
				return mutate(t, "type: PatchResourceLimits", "type: DeletePod")
			},
			want: []string{"groundTruth.acceptableActions[0].type", "DeletePod", "RestartWorkload"},
		},
		{
			name: "action target without a name",
			yaml: func(t *testing.T) string {
				return mutate(t, "target: {kind: Deployment, name: checkout-api}", "target: {kind: Deployment}")
			},
			want: []string{"groundTruth.acceptableActions[0].target.name", wantRequired},
		},
		{
			name: "action target kind outside the target vocabulary",
			yaml: func(t *testing.T) string {
				return mutate(t, "target: {kind: Deployment, name: checkout-api}",
					"target: {kind: ReplicaSet, name: checkout-api}")
			},
			want: []string{"groundTruth.acceptableActions[0].target.kind", "ReplicaSet", "Deployment"},
		},
		{
			name: "diagnosis rule missing",
			yaml: func(t *testing.T) string {
				return mutate(t, `  diagnosis:
    requiredEvidenceIdPatterns: [ev/gitcommit-*, ev/podstatus-*]
    requiredSummaryKeyphrases: [checkout-api, memory limit]
`, "")
			},
			want: []string{"requiredEvidenceIdPatterns", "requiredSummaryKeyphrases", wantRequired, "§17.3"},
		},
		{
			name: "evidence pattern set empty",
			yaml: func(t *testing.T) string {
				return mutate(t, "requiredEvidenceIdPatterns: [ev/gitcommit-*, ev/podstatus-*]",
					"requiredEvidenceIdPatterns: []")
			},
			want: []string{"requiredEvidenceIdPatterns", "at least one"},
		},
		{
			name: "evidence pattern without the ev/ scheme",
			yaml: func(t *testing.T) string {
				return mutate(t, "ev/gitcommit-*", "gitcommit-*")
			},
			want: []string{"requiredEvidenceIdPatterns[0]", "ev/<source>-<glob>"},
		},
		{
			name: "evidence source outside the closed set",
			yaml: func(t *testing.T) string {
				return mutate(t, "ev/gitcommit-*", "ev/gitlab-*")
			},
			want: []string{"requiredEvidenceIdPatterns[0]", `"gitlab"`, "podstatus", "gitcommit", "ADR-005"},
		},
		{
			name: "evidence pattern duplicated",
			yaml: func(t *testing.T) string {
				return mutate(t, "[ev/gitcommit-*, ev/podstatus-*]", "[ev/gitcommit-*, ev/gitcommit-*]")
			},
			want: []string{"requiredEvidenceIdPatterns[1]", "duplicate"},
		},
		{
			name: "keyphrase set empty",
			yaml: func(t *testing.T) string {
				return mutate(t, "requiredSummaryKeyphrases: [checkout-api, memory limit]",
					"requiredSummaryKeyphrases: []")
			},
			want: []string{"requiredSummaryKeyphrases", "at least one"},
		},
		{
			name: "keyphrase blank",
			yaml: func(t *testing.T) string {
				return mutate(t, "[checkout-api, memory limit]", `[checkout-api, "  "]`)
			},
			want: []string{"requiredSummaryKeyphrases[1]", "3 characters"},
		},
		{
			name: "keyphrase too short to mean anything",
			yaml: func(t *testing.T) string {
				return mutate(t, "[checkout-api, memory limit]", "[checkout-api, ok]")
			},
			want: []string{"requiredSummaryKeyphrases[1]", "3 characters"},
		},
		{
			name: "keyphrase not canonical lowercase",
			yaml: func(t *testing.T) string {
				return mutate(t, "[checkout-api, memory limit]", "[checkout-api, Memory Limit]")
			},
			want: []string{"requiredSummaryKeyphrases[1]", "lowercase"},
		},
		{
			name: "keyphrase duplicated",
			yaml: func(t *testing.T) string {
				return mutate(t, "[checkout-api, memory limit]", "[checkout-api, checkout-api]")
			},
			want: []string{"requiredSummaryKeyphrases[1]", "duplicate"},
		},
		{
			name: "fix predicate missing",
			yaml: func(t *testing.T) string {
				return mutate(t, "  fixPredicate: 'up{job=\"demo\"} == 1'\n", "")
			},
			want: []string{"groundTruth.fixPredicate", wantRequired, "ADR-005"},
		},
		{
			name: "harm predicate blank",
			yaml: func(t *testing.T) string {
				return mutate(t, `harmPredicate: 'up{job="demo"} == 0'`, `harmPredicate: '   '`)
			},
			want: []string{"groundTruth.harmPredicate", wantRequired},
		},
		{
			name: "restraint contradicted by acceptable actions",
			yaml: func(t *testing.T) string {
				return mutate(t, "restraintExpected: false", "restraintExpected: true")
			},
			want: []string{"restraintExpected", "no plan"},
		},
		{
			name: "no acceptable actions without restraint",
			yaml: func(t *testing.T) string {
				return mutate(t, `  acceptableActions:
    - type: PatchResourceLimits
      target: {kind: Deployment, name: checkout-api}`, "  acceptableActions: []")
			},
			want: []string{"acceptableActions", "restraintExpected"},
		},
		{
			name: "action both acceptable and forbidden",
			yaml: func(t *testing.T) string {
				return mutate(t, "- type: ScaleWorkload", "- type: PatchResourceLimits")
			},
			want: []string{"forbiddenActions[0]", "acceptableActions", "contradict"},
		},
		{
			name: "timeout zero",
			yaml: func(t *testing.T) string { return mutate(t, "timeoutMinutes: 12", "timeoutMinutes: 0") },
			want: []string{"timeoutMinutes", "at least 1"},
		},
		{
			name: "timeout missing",
			yaml: func(t *testing.T) string { return mutate(t, "timeoutMinutes: 12\n", "") },
			want: []string{"timeoutMinutes", "at least 1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scnDir := writeFixture(t, tc.yaml(t))
			_, err := Load(scnDir)
			if err == nil {
				t.Fatal("Load succeeded, want a validation error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q:\n%v", want, err)
				}
			}
		})
	}
}
