/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package approve

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/hash"
)

const goldenBundleHash = "sha256:9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f"

// goldenCanonicalSpec is the hand-written canonical JSON of goldenSpec():
// keys sorted at every level, arrays in declaration order, quantities and
// durations in their marshaled string forms. Pinning the exact bytes here
// means a change to canonicalization, to the spec types' JSON tags, or to
// how Kubernetes marshals Quantity/Duration shows up as a diff in THIS
// file — and silently changing any of them would invalidate every
// outstanding approval, so they must never change silently.
const goldenCanonicalSpec = `{"actions":[{"patchResourceLimits":{"container":"api",` +
	`"memory":{"from":"256Mi","to":"512Mi"}},"target":{"kind":"Deployment",` +
	`"name":"checkout-api","namespace":"shop"},"type":"PatchResourceLimits"}],` +
	`"evidenceBundleHash":"` + goldenBundleHash + `",` +
	`"hypothesis":{"citations":["ev/pod-status-04","ev/event-11"],` +
	`"confidencePercent":86,"summary":"checkout-api OOMKilled after memory limit lowered"},` +
	`"incidentRef":{"name":"checkout-oomkill"},` +
	`"rollback":{"strategy":"RestorePreviousSpec"},` +
	`"verification":{"onFailure":"Rollback","predicate":"up == 1","window":"10m0s"}}`

// goldenBoundTo was computed OUTSIDE this codebase (sha256sum over the
// canonical bytes above, then over "<bundleHash>\n<innerHex>"), so the test
// checks the implementation against an external authority, not against its
// own output. Inner spec hash:
// 0314b2a4deba3985b937627ad678d59ac76cc352fbb809ceb9581fe333110f86.
const goldenBoundTo = "sha256:4fc75451e611bcdc8774b63421a1eedbd05562bf39e121af80e31b049e3a344e"

func goldenSpec() *praxisv1alpha1.RemediationPlanSpec {
	return &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef:        praxisv1alpha1.IncidentRef{Name: "checkout-oomkill"},
		EvidenceBundleHash: goldenBundleHash,
		Hypothesis: praxisv1alpha1.Hypothesis{
			Summary:           "checkout-api OOMKilled after memory limit lowered",
			ConfidencePercent: 86,
			Citations:         []praxisv1alpha1.EvidenceID{"ev/pod-status-04", "ev/event-11"},
		},
		Actions: []praxisv1alpha1.Action{{
			Type: praxisv1alpha1.ActionPatchResourceLimits,
			Target: praxisv1alpha1.TargetRef{
				Kind:      "Deployment",
				Namespace: "shop",
				Name:      "checkout-api",
			},
			PatchResourceLimits: &praxisv1alpha1.PatchResourceLimitsParams{
				Container: "api",
				Memory: &praxisv1alpha1.QuantityChange{
					From: resource.MustParse("256Mi"),
					To:   resource.MustParse("512Mi"),
				},
			},
		}},
		Verification: praxisv1alpha1.VerificationSpec{
			Predicate: "up == 1",
			Window:    metav1.Duration{Duration: 10 * time.Minute},
			OnFailure: praxisv1alpha1.FailureActionRollback,
		},
		Rollback: praxisv1alpha1.RollbackSpec{
			Strategy: praxisv1alpha1.RollbackRestorePreviousSpec,
		},
	}
}

func TestBoundToGoldenVector(t *testing.T) {
	spec := goldenSpec()

	canonical, err := hash.CanonicalJSON(spec)
	if err != nil {
		t.Fatalf("CanonicalJSON(spec) error = %v, want nil", err)
	}
	if string(canonical) != goldenCanonicalSpec {
		t.Errorf("CanonicalJSON(spec) = %q, want %q", canonical, goldenCanonicalSpec)
	}

	got, err := BoundTo(goldenBundleHash, spec)
	if err != nil {
		t.Fatalf("BoundTo() error = %v, want nil", err)
	}
	if got != goldenBoundTo {
		t.Errorf("BoundTo() = %q, want %q", got, goldenBoundTo)
	}
}

func TestBoundToIsDeterministic(t *testing.T) {
	first, err := BoundTo(goldenBundleHash, goldenSpec())
	if err != nil {
		t.Fatalf("BoundTo() error = %v, want nil", err)
	}
	second, err := BoundTo(goldenBundleHash, goldenSpec())
	if err != nil {
		t.Fatalf("BoundTo() error = %v, want nil", err)
	}
	if first != second {
		t.Errorf("BoundTo() is not deterministic: %q != %q", first, second)
	}
}

func TestBoundToChangesWhenAnyInputChanges(t *testing.T) {
	tests := []struct {
		name   string
		bundle string
		mutate func(*praxisv1alpha1.RemediationPlanSpec)
	}{
		{
			name:   "different evidence bundle hash",
			bundle: "sha256:abababababababababababababababababababababababababababababababab",
			mutate: func(*praxisv1alpha1.RemediationPlanSpec) {},
		},
		{
			name:   "different hypothesis summary",
			bundle: goldenBundleHash,
			mutate: func(s *praxisv1alpha1.RemediationPlanSpec) {
				s.Hypothesis.Summary = "a subtly different story"
			},
		},
		{
			name:   "different action target",
			bundle: goldenBundleHash,
			mutate: func(s *praxisv1alpha1.RemediationPlanSpec) {
				s.Actions[0].Target.Name = "payments-api"
			},
		},
		{
			name:   "different memory quantity",
			bundle: goldenBundleHash,
			mutate: func(s *praxisv1alpha1.RemediationPlanSpec) {
				s.Actions[0].PatchResourceLimits.Memory.To = resource.MustParse("1Gi")
			},
		},
		{
			name:   "different verification window",
			bundle: goldenBundleHash,
			mutate: func(s *praxisv1alpha1.RemediationPlanSpec) {
				s.Verification.Window = metav1.Duration{Duration: time.Minute}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := goldenSpec()
			tt.mutate(spec)
			got, err := BoundTo(tt.bundle, spec)
			if err != nil {
				t.Fatalf("BoundTo() error = %v, want nil", err)
			}
			if got == goldenBoundTo {
				t.Errorf("BoundTo() = golden hash %q despite changed input; the binding must cover it", got)
			}
		})
	}
}
