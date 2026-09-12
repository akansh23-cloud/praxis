/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package validate

import (
	"context"
	"slices"
	"strings"
	"testing"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

func bundleWith(ids ...string) *evidence.Bundle {
	b := &evidence.Bundle{Version: evidence.SchemaVersion, Incident: evidence.IncidentRef{Name: "inc", UID: "u"}}
	for _, id := range ids {
		b.Items = append(b.Items, evidence.Item{ID: id, Type: evidence.ItemTypeEvent, Source: evidence.SourceK8s, Data: map[string]string{}})
	}
	return b
}

func hyp(citations ...string) praxisv1alpha1.Hypothesis {
	h := praxisv1alpha1.Hypothesis{Summary: "s", ConfidencePercent: 50}
	for _, c := range citations {
		h.Citations = append(h.Citations, praxisv1alpha1.EvidenceID(c))
	}
	return h
}

// TestBundleCitationValidator is the deterministic table behind
// Rejected/CitationInvalid: membership in the exact bundle, nothing else.
func TestBundleCitationValidator(t *testing.T) {
	cases := []struct {
		name       string
		hypothesis praxisv1alpha1.Hypothesis
		bundle     *evidence.Bundle
		wantPass   bool
		wantReason string
		wantInMsg  []string
	}{
		{
			name: "every citation resolves", hypothesis: hyp("ev/podstatus-01", "ev/event-02"),
			bundle:   bundleWith("ev/event-01", "ev/event-02", "ev/podstatus-01"),
			wantPass: true, wantReason: praxisv1alpha1.ReasonCitationsResolved, wantInMsg: []string{"All 2 citation(s)"},
		},
		{
			name: "one citation is missing", hypothesis: hyp("ev/podstatus-01", "ev/event-09"),
			bundle:   bundleWith("ev/event-01", "ev/podstatus-01"),
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"1 of 2", "ev/event-09"},
		},
		{
			name: "a well-shaped id that is not in the bundle is not evidence", hypothesis: hyp("ev/podstatus-99"),
			bundle:   bundleWith("ev/podstatus-01"),
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"ev/podstatus-99"},
		},
		{
			name: "duplicate citations of one real item resolve", hypothesis: hyp("ev/event-01", "ev/event-01"),
			bundle:   bundleWith("ev/event-01"),
			wantPass: true, wantReason: praxisv1alpha1.ReasonCitationsResolved,
		},
		{
			name: "duplicate missing citations are reported once", hypothesis: hyp("ev/metric-07", "ev/metric-07", "ev/event-01"),
			bundle:   bundleWith("ev/event-01"),
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"1 of 3", "ev/metric-07"},
		},
		{
			name: "no citations at all", hypothesis: hyp(),
			bundle:   bundleWith("ev/event-01"),
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"cites no evidence"},
		},
		{
			name: "no bundle", hypothesis: hyp("ev/event-01"),
			bundle:   nil,
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"No persisted evidence bundle"},
		},
		{
			name: "empty bundle", hypothesis: hyp("ev/event-01"),
			bundle:   bundleWith(),
			wantPass: false, wantReason: praxisv1alpha1.ReasonCitationInvalid, wantInMsg: []string{"0-item"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, err := BundleCitationValidator{}.Validate(context.Background(), tc.hypothesis, tc.bundle)
			if err != nil {
				t.Fatalf("Validate returned machinery error %v; a verdict was expected", err)
			}
			if verdict.Passed != tc.wantPass || verdict.Reason != tc.wantReason {
				t.Errorf("verdict = %+v, want passed=%v reason=%s", verdict, tc.wantPass, tc.wantReason)
			}
			for _, want := range tc.wantInMsg {
				if !strings.Contains(verdict.Message, want) {
					t.Errorf("message %q lacks %q", verdict.Message, want)
				}
			}
		})
	}
}

// TestUnresolvedCitationsIsOrderIndependent: permuting the bundle's items
// changes nothing — membership is a set question — while the report keeps
// citation order so messages are stable.
func TestUnresolvedCitationsIsOrderIndependent(t *testing.T) {
	h := hyp("ev/c", "ev/a", "ev/zz", "ev/b", "ev/yy")
	want := []praxisv1alpha1.EvidenceID{"ev/zz", "ev/yy"}
	b := bundleWith("ev/a", "ev/b", "ev/c")
	if got := UnresolvedCitations(h, b); !slices.Equal(got, want) {
		t.Errorf("unresolved = %v, want %v", got, want)
	}
	slices.Reverse(b.Items)
	if got := UnresolvedCitations(h, b); !slices.Equal(got, want) {
		t.Errorf("after permuting the bundle: unresolved = %v, want %v", got, want)
	}
	if got := UnresolvedCitations(h, nil); len(got) != 5 {
		t.Errorf("nil bundle: %d unresolved, want all 5", len(got))
	}
}
