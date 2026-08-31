/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"regexp"
	"strings"
	"testing"
)

func sampleBundle(msg string) *Bundle {
	return &Bundle{
		Version:     SchemaVersion,
		Incident:    IncidentRef{Name: "bench-x", UID: "uid-1"},
		CollectedAt: "2026-08-31T10:00:00Z",
		Items: []Item{{
			ID:     ItemID(ItemTypeEvent, 1),
			Type:   ItemTypeEvent,
			Source: "k8s",
			Data: map[string]string{
				EventDataReason:  "Failed",
				EventDataMessage: msg,
			},
		}},
	}
}

func TestBundleHashDeterministic(t *testing.T) {
	a, b := sampleBundle("Failed to pull image"), sampleBundle("Failed to pull image")
	// Rebuild b's data map in reverse insertion order: canonical JSON must
	// erase the difference.
	b.Items[0].Data = map[string]string{
		EventDataMessage: "Failed to pull image",
		EventDataReason:  "Failed",
	}

	ha, err := a.Hash()
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("equal bundles hash differently: %s vs %s", ha, hb)
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(ha) {
		t.Errorf("hash %q is not in the sha256:<hex> form the RemediationPlan CRD requires", ha)
	}

	c := sampleBundle("a different message")
	hc, err := c.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if hc == ha {
		t.Error("different bundle content produced the same hash")
	}
}

// TestItemIDTokens pins the id scheme to the ADR-005 closed source-token
// set — the same list the bench scenario loader validates §17.3 patterns
// against. If these drift, answer keys demand evidence no producer mints.
func TestItemIDTokens(t *testing.T) {
	want := map[ItemType]string{
		ItemTypePodStatus:   "ev/podstatus-07",
		ItemTypeEvent:       "ev/event-07",
		ItemTypeOwnerChain:  "ev/ownerchain-07",
		ItemTypeMetric:      "ev/metric-07",
		ItemTypeLogTemplate: "ev/logtemplate-07",
		ItemTypeSyncState:   "ev/syncstate-07",
		ItemTypeGitCommit:   "ev/gitcommit-07",
	}
	if len(want) != len(sourceTokens) {
		t.Fatalf("token map has %d entries, test covers %d", len(sourceTokens), len(want))
	}
	for typ, id := range want {
		if got := ItemID(typ, 7); got != id {
			t.Errorf("ItemID(%s, 7) = %q, want %q", typ, got, id)
		}
	}
	// Ids must satisfy the CRD's EvidenceID pattern.
	pat := regexp.MustCompile(`^ev/[a-z0-9][a-z0-9-]*$`)
	for typ := range want {
		if id := ItemID(typ, 42); !pat.MatchString(id) {
			t.Errorf("ItemID(%s, 42) = %q violates the EvidenceID CRD pattern", typ, id)
		}
	}
	if id := ItemID(ItemTypeEvent, 5); !strings.HasPrefix(id, "ev/event-") {
		t.Errorf("unexpected id %q", id)
	}
}
