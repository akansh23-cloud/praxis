/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package planschema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

const (
	validHash      = "sha256:9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f"
	kindDeploy     = "Deployment"
	nsShop         = "shop"
	checkoutName   = "checkout-api"
	fieldCitations = "citations"
)

// validSpec is a plan admission accepts: the sample plan's shape.
func validSpec() *praxisv1alpha1.RemediationPlanSpec {
	return &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef:        praxisv1alpha1.IncidentRef{Name: "checkout-oomkill"},
		EvidenceBundleHash: validHash,
		Hypothesis: praxisv1alpha1.Hypothesis{
			Summary:           "checkout-api OOMKilled after the memory limit was lowered",
			ConfidencePercent: 86,
			Citations:         []praxisv1alpha1.EvidenceID{"ev/podstatus-01", "ev/gitcommit-01"},
		},
		Actions: []praxisv1alpha1.Action{{
			Type:   praxisv1alpha1.ActionPatchResourceLimits,
			Target: praxisv1alpha1.TargetRef{Kind: kindDeploy, Namespace: nsShop, Name: checkoutName},
			PatchResourceLimits: &praxisv1alpha1.PatchResourceLimitsParams{
				Container: "session-cache",
				Memory:    &praxisv1alpha1.QuantityChange{From: resource.MustParse("16Mi"), To: resource.MustParse("96Mi")},
			},
		}},
		Verification: praxisv1alpha1.VerificationSpec{
			Predicate: `kube_deployment_status_replicas_available{namespace="shop",deployment="checkout-api"} >= 2`,
			Window:    metav1.Duration{Duration: 10 * time.Minute},
			OnFailure: praxisv1alpha1.FailureActionRollback,
		},
		Rollback: praxisv1alpha1.RollbackSpec{Strategy: praxisv1alpha1.RollbackRestorePreviousSpec},
	}
}

// TestValidateSpecRoundTrip is the playbook's round-trip proof: a valid
// RemediationPlanSpec passes the derived validator; an out-of-vocabulary
// verb fails; and every other admission rule the CRD carries — CEL
// discriminated unions, the rollback rule, bounds, patterns — fails the
// same way the API server would refuse it.
func TestValidateSpecRoundTrip(t *testing.T) {
	ctx := context.Background()
	if err := ValidateSpec(ctx, validSpec()); err != nil {
		t.Fatalf("a valid spec was refused: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(s *praxisv1alpha1.RemediationPlanSpec)
		wantErr string
	}{
		{"out-of-vocabulary verb", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0].Type = "DeleteNamespace"
			s.Actions[0].PatchResourceLimits = nil
		}, "actions[0].type in body should be one of [RestartWorkload ScaleWorkload RollbackRelease PatchResourceLimits CordonNode]"},
		{"params without their verb (CEL union)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0].Type = praxisv1alpha1.ActionRestartWorkload
		}, "patchResourceLimits params must be set iff type is PatchResourceLimits"},
		{"scale without params (CEL union)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0].Type = praxisv1alpha1.ActionScaleWorkload
			s.Actions[0].PatchResourceLimits = nil
		}, "scaleWorkload params must be set iff type is ScaleWorkload"},
		{"cordon a deployment (CEL)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0] = praxisv1alpha1.Action{Type: praxisv1alpha1.ActionCordonNode,
				Target: praxisv1alpha1.TargetRef{Kind: kindDeploy, Namespace: nsShop, Name: checkoutName}}
		}, "CordonNode must target a Node"},
		{"node target with namespace (CEL)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0] = praxisv1alpha1.Action{Type: praxisv1alpha1.ActionCordonNode,
				Target: praxisv1alpha1.TargetRef{Kind: "Node", Namespace: nsShop, Name: "worker-1"}}
		}, "forbidden for Node"},
		{"rollback on failure without a strategy (CEL)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Rollback.Strategy = praxisv1alpha1.RollbackNone
		}, "onFailure Rollback requires a rollback strategy"},
		{"no actions", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions = nil
		}, fieldActions},
		{"six actions", func(s *praxisv1alpha1.RemediationPlanSpec) {
			for range 5 {
				s.Actions = append(s.Actions, s.Actions[0])
			}
		}, fieldActions},
		{"confidence out of range", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Hypothesis.ConfidencePercent = 101
		}, "confidencePercent"},
		{"malformed citation", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Hypothesis.Citations = []praxisv1alpha1.EvidenceID{"EV/Nope"}
		}, fieldCitations},
		{"no citations", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Hypothesis.Citations = nil
		}, fieldCitations},
		{"malformed hash", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.EvidenceBundleHash = "md5:abc"
		}, "evidenceBundleHash"},
		{"empty summary", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Hypothesis.Summary = ""
		}, "summary"},
		{"unknown target kind", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0].Target.Kind = "Pod"
		}, "target.kind in body should be one of [Deployment StatefulSet DaemonSet Node]"},
		{"limits patch with neither memory nor cpu (CEL)", func(s *praxisv1alpha1.RemediationPlanSpec) {
			s.Actions[0].PatchResourceLimits.Memory = nil
		}, "at least one of memory or cpu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSpec()
			tc.mutate(s)
			err := ValidateSpec(ctx, s)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want a ValidationError, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}

	// Determinism: the same violations render identically, twice.
	s := validSpec()
	s.Actions[0].Type = "DeleteNamespace"
	s.Hypothesis.ConfidencePercent = 300
	a, b := ValidateSpec(ctx, s), ValidateSpec(ctx, s)
	if a == nil || b == nil || a.Error() != b.Error() {
		t.Errorf("validation errors are not deterministic:\n%v\n%v", a, b)
	}
}

// TestDerivedSchemasAreInSyncWithTheCRD: re-deriving from the CRD the
// repository ships must reproduce the embedded bytes exactly, so a CRD
// change without `make schema-derive` fails here.
func TestDerivedSchemasAreInSyncWithTheCRD(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "praxis.dev_remediationplans.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Derive(raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2][]byte{
		"remediationplanspec.openapi.json": {got.SpecOpenAPI, specOpenAPI},
		"planner.schema.json":              {got.Planner, plannerSchema},
		"hypotheses.schema.json":           {got.Hypotheses, hypothesesSchema},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Errorf("%s is out of sync with the CRD — run `make schema-derive`", name)
		}
	}
	// Byte-stable: deriving twice yields identical artifacts.
	again, err := Derive(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Planner, got.Planner) || !bytes.Equal(again.Hypotheses, got.Hypotheses) {
		t.Error("derivation is not byte-stable")
	}
}

// TestModelSchemasAreStructuredOutputSafe walks both model-facing schemas:
// every object closes additionalProperties, no unsupported keyword
// survives, the action verb enum is exactly the closed vocabulary, the
// code-owned spec fields are absent from the planner projection, and the
// hypothesis shape is the CRD's.
func TestModelSchemasAreStructuredOutputSafe(t *testing.T) {
	for name, raw := range map[string][]byte{"planner": plannerSchema, fieldHypotheses: hypothesesSchema} {
		t.Run(name, func(t *testing.T) {
			var node map[string]any
			if err := json.Unmarshal(raw, &node); err != nil {
				t.Fatal(err)
			}
			walk(t, name, node)
		})
	}

	var planner map[string]any
	if err := json.Unmarshal(plannerSchema, &planner); err != nil {
		t.Fatal(err)
	}
	verdict := planner[kwProperties].(map[string]any)["verdict"].(map[string]any)["enum"].([]any)
	if !slices.Equal(toStrings(verdict), []string{fieldPlan, "no-action"}) {
		t.Errorf("verdict enum = %v", verdict)
	}
	planAlts := planner[kwProperties].(map[string]any)[fieldPlan].(map[string]any)[kwAnyOf].([]any)
	plan := planAlts[0].(map[string]any)
	if planAlts[1].(map[string]any)[kwType] != "null" {
		t.Error("plan must be nullable for the no-action verdict")
	}
	props := plan[kwProperties].(map[string]any)
	for _, owned := range []string{"incidentRef", "evidenceBundleHash", "hypothesis"} {
		if _, present := props[owned]; present {
			t.Errorf("planner schema exposes code-owned field %q to the model", owned)
		}
	}
	if !slices.Equal(toStrings(plan[kwRequired].([]any)), []string{fieldActions, "verification", "rollback"}) {
		t.Errorf("plan required = %v", plan[kwRequired])
	}
	verbs := props[fieldActions].(map[string]any)[kwItems].(map[string]any)[kwProperties].(map[string]any)[kwType].(map[string]any)["enum"].([]any)
	if !slices.Equal(toStrings(verbs), []string{"RestartWorkload", "ScaleWorkload", "RollbackRelease", "PatchResourceLimits", "CordonNode"}) {
		t.Errorf("action verb enum = %v — must be exactly the closed vocabulary", verbs)
	}
	quantity := props[fieldActions].(map[string]any)[kwItems].(map[string]any)[kwProperties].(map[string]any)["patchResourceLimits"].(map[string]any)[kwProperties].(map[string]any)["memory"].(map[string]any)[kwProperties].(map[string]any)["from"].(map[string]any)
	if quantity[kwType] != kwString {
		t.Errorf("a Kubernetes quantity must project to a string, got %v", quantity)
	}

	var hyps map[string]any
	if err := json.Unmarshal(hypothesesSchema, &hyps); err != nil {
		t.Fatal(err)
	}
	item := hyps[kwProperties].(map[string]any)[fieldHypotheses].(map[string]any)[kwItems].(map[string]any)
	if !slices.Equal(toStrings(item[kwRequired].([]any)), []string{fieldCitations, "confidencePercent", "summary"}) {
		t.Errorf("hypothesis required = %v", item[kwRequired])
	}
}

func walk(t *testing.T, path string, node map[string]any) {
	t.Helper()
	for k := range node {
		if unsupportedKeywords[k] || strings.HasPrefix(k, "x-kubernetes") {
			t.Errorf("%s carries unsupported keyword %q", path, k)
		}
	}
	if node[kwType] == kwObject && node[kwAdditionalProperties] != false {
		t.Errorf("%s is an object without additionalProperties:false", path)
	}
	if props, ok := node[kwProperties].(map[string]any); ok {
		for name, child := range props {
			walk(t, path+"."+name, child.(map[string]any))
		}
	}
	if items, ok := node[kwItems].(map[string]any); ok {
		walk(t, path+"[]", items)
	}
	for _, key := range []string{kwAnyOf, "oneOf", "allOf"} {
		if alts, ok := node[key].([]any); ok {
			for i, alt := range alts {
				walk(t, path+"."+key+string(rune('0'+i)), alt.(map[string]any))
			}
		}
	}
}

func toStrings(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, v.(string))
	}
	return out
}

func TestDeriveRejectsForeignCRDs(t *testing.T) {
	if _, err := Derive([]byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  names:\n    kind: Other\n")); err == nil {
		t.Error("a CRD for another kind must be refused")
	}
	if _, err := Derive([]byte(": not yaml")); err == nil {
		t.Error("garbage must be refused")
	}
}
