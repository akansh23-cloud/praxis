/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akansh23-cloud/praxis/internal/hash"
)

// -update rewrites testdata/golden_bundle.json from the current assembler
// output: go test ./internal/evidence -run TestAssembleGolden -update
var update = flag.Bool("update", false, "rewrite golden files")

var fixedCollectedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// Shared fixture vocabulary (also keeps goconst quiet without weakening
// the fixtures).
const (
	goldenPod     = "checkout-api-7d9c6f5b4-x2m8q"
	keyTemplate   = "template"
	tmplUseMemory = "use-memory"
	tmplSLOBurn   = "slo-burn"
	hugeItemKey   = "shop/huge"
	keyNamespace  = "namespace"
	goldenNS      = "shop"
	goldenPodKey  = goldenNS + "/" + goldenPod
	goldenDeploy  = "checkout-api"
	goldenCause   = "commit 4be1f2a: trim session-cache memory to cut per-pod cost"
	goldenCommit  = "4be1f2a9c31d"
	reasonBackOff = "BackOff"
	kindPodTest   = "Pod"
	goldenRS      = "checkout-api-7d9c6f5b4"
	otherNS       = "other"
	valFalse      = "false"
)

func testIncidentRef() IncidentRef {
	return IncidentRef{Name: "checkout-oomkill", UID: "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"}
}

// goldenCollected is the fixed fake collector output the golden-file and
// determinism tests are defined over: a plausible slice of the oomkill
// scenario, handed over deliberately unsorted.
func goldenCollected() []Collected {
	return []Collected{
		{Type: ItemTypeMetric, Source: SourcePrometheus, Key: tmplUseMemory, Data: map[string]string{
			keyTemplate: tmplUseMemory, "signal": "USE",
			"promql": `sum by (namespace, pod) (container_memory_working_set_bytes{namespace=~"^(?:shop)$",container!=""})`,
			"series": `{namespace="shop",pod="checkout-api-7d9c6f5b4-x2m8q"} 41943040`,
		}},
		{Type: ItemTypePodStatus, Source: SourceK8s, Key: goldenPodKey, Data: map[string]string{
			keyNamespace: goldenNS, "name": goldenPod, "phase": "Running",
			"ready": valFalse, "container.session-cache.lastTerminated": "OOMKilled:exit=137",
			"container.session-cache.restartCount": "4", "container.session-cache.state": "waiting:CrashLoopBackOff",
		}},
		{Type: ItemTypeGitCommit, Source: SourceK8s, Key: "shop/Deployment/checkout-api", Data: map[string]string{
			"workloadKind": kindDeployment, "workloadName": goldenDeploy, keyNamespace: goldenNS,
			"changeCause": goldenCause,
			"commit":      goldenCommit,
		}},
		{Type: ItemTypeEvent, Source: SourceK8s, Key: "shop/Pod/checkout-api-7d9c6f5b4-x2m8q/BackOff", Data: map[string]string{
			EventDataType: "Warning", EventDataReason: reasonBackOff,
			EventDataMessage:      "Back-off restarting failed container session-cache in pod checkout-api-7d9c6f5b4-x2m8q",
			EventDataInvolvedKind: kindPodTest, EventDataInvolvedName: goldenPod,
			EventDataInvolvedNamespace: goldenNS, EventDataCount: "6",
		}},
		{Type: ItemTypeMetric, Source: SourcePrometheus, Key: tmplSLOBurn, Data: map[string]string{
			keyTemplate: tmplSLOBurn, "signal": "SLO",
			"promql": `max by (namespace) (slo:error_budget_burn_rate{namespace=~"^(?:shop)$"})`,
			"error":  "query prometheus: connection refused",
		}},
		{Type: ItemTypeOwnerChain, Source: SourceK8s, Key: goldenPodKey, Data: map[string]string{
			keyNamespace: goldenNS, "pod": goldenPod,
			"chain":        "Pod/checkout-api-7d9c6f5b4-x2m8q -> ReplicaSet/checkout-api-7d9c6f5b4 -> Deployment/checkout-api",
			"workloadKind": kindDeployment, "workloadName": goldenDeploy,
		}},
		{Type: ItemTypeEvent, Source: SourceK8s, Key: "shop/Pod/checkout-api-7d9c6f5b4-x2m8q/Unhealthy", Data: map[string]string{
			EventDataType: "Warning", EventDataReason: "Unhealthy",
			EventDataMessage:      "Readiness probe failed: Get \"http://10.244.0.12:8080/ready\": dial tcp: connect: connection refused",
			EventDataInvolvedKind: kindPodTest, EventDataInvolvedName: goldenPod,
			EventDataInvolvedNamespace: goldenNS, EventDataCount: "2",
		}},
	}
}

func mustAssemble(t *testing.T, in []Collected) (*Bundle, []byte, string) {
	t.Helper()
	b, raw, h, err := Assemble(testIncidentRef(), fixedCollectedAt, in)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return b, raw, h
}

// TestAssembleGoldenByteIdentity is the Session 3.1 determinism proof:
// identical fixed inputs, assembled twice, produce byte-identical canonical
// bundles with the same sha256 — and those bytes match the committed golden
// file, so the canonical form cannot drift silently between sessions.
func TestAssembleGoldenByteIdentity(t *testing.T) {
	_, raw1, hash1 := mustAssemble(t, goldenCollected())
	_, raw2, hash2 := mustAssemble(t, goldenCollected())

	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("two assemblies of identical input differ:\n%s\n%s", raw1, raw2)
	}
	if hash1 != hash2 {
		t.Fatalf("two assemblies of identical input hash differently: %s vs %s", hash1, hash2)
	}
	if want := hash.SHA256Prefixed(raw1); hash1 != want {
		t.Fatalf("returned hash %s is not the sha256 of the returned bytes (%s)", hash1, want)
	}

	golden := filepath.Join("testdata", "golden_bundle.json")
	if *update {
		if err := os.WriteFile(golden, raw1, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden file (run with -update to create it): %v", err)
	}
	if !bytes.Equal(raw1, want) {
		t.Errorf("assembled bundle differs from golden file %s:\n got: %s\nwant: %s", golden, raw1, want)
	}
}

// TestAssembleShuffledInputSameBytes proves collector handover order is
// irrelevant: any permutation of the same items assembles to the same
// bytes, ids and hash.
func TestAssembleShuffledInputSameBytes(t *testing.T) {
	_, want, wantHash := mustAssemble(t, goldenCollected())

	permutations := map[string]func([]Collected) []Collected{
		"reversed": func(in []Collected) []Collected {
			slices.Reverse(in)
			return in
		},
		"rotated": func(in []Collected) []Collected {
			return append(in[3:], in[:3]...)
		},
		"swapped-neighbours": func(in []Collected) []Collected {
			for i := 0; i+1 < len(in); i += 2 {
				in[i], in[i+1] = in[i+1], in[i]
			}
			return in
		},
	}
	for name, permute := range permutations {
		t.Run(name, func(t *testing.T) {
			_, got, gotHash := mustAssemble(t, permute(goldenCollected()))
			if !bytes.Equal(got, want) {
				t.Errorf("permutation changed the canonical bytes:\n got: %s\nwant: %s", got, want)
			}
			if gotHash != wantHash {
				t.Errorf("permutation changed the hash: %s vs %s", gotHash, wantHash)
			}
		})
	}
}

// TestAssembleOrderingAndIDs pins the §6 ordering tuple and the post-sort
// id scheme: items sort by (type, source, natural key) and each type's ids
// count up from 01 in that final order.
func TestAssembleOrderingAndIDs(t *testing.T) {
	b, _, _ := mustAssemble(t, goldenCollected())

	wantIDs := []string{
		"ev/event-01",      // BackOff sorts before Unhealthy on natural key
		"ev/event-02",      // Unhealthy
		"ev/gitcommit-01",  // Event < GitCommit < Metric < OwnerChain < PodStatus lexically
		"ev/metric-01",     // slo-burn < use-memory on natural key
		"ev/metric-02",     // use-memory
		"ev/ownerchain-01", // the single owner chain
		"ev/podstatus-01",  // the single pod status
	}
	gotIDs := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		gotIDs = append(gotIDs, it.ID)
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("final id sequence:\n got %v\nwant %v", gotIDs, wantIDs)
	}
	if b.Items[3].Data[keyTemplate] != tmplSLOBurn || b.Items[4].Data[keyTemplate] != tmplUseMemory {
		t.Error("metric items are not in natural-key order")
	}
}

// evenly padded filler items for the cap tables.
func filler(typ ItemType, source string, n int, payloadBytes int) []Collected {
	out := make([]Collected, 0, n)
	for i := range n {
		out = append(out, Collected{
			Type:   typ,
			Source: source,
			Key:    fmt.Sprintf("ns/%s-%03d", strings.ToLower(string(typ)), i),
			Data: map[string]string{
				"name":    fmt.Sprintf("%s-%03d", strings.ToLower(string(typ)), i),
				"payload": strings.Repeat("x", payloadBytes),
			},
		})
	}
	return out
}

func countByType(b *Bundle) map[ItemType]int {
	got := map[ItemType]int{}
	for _, it := range b.Items {
		got[it.Type]++
	}
	return got
}

// TestAssembleCaps drives every §6 cap and the exact truncation priority
// (drop lowest retention rank first; within a rank, drop from the end of
// the sorted order) through one table.
func TestAssembleCaps(t *testing.T) {
	cases := []struct {
		name      string
		in        []Collected
		wantCount map[ItemType]int
	}{
		{
			// 70 events, one type: the count cap keeps the first 64 in
			// natural-key order and drops the last six.
			name:      "count cap within one type drops from the end",
			in:        filler(ItemTypeEvent, SourceK8s, 70, 16),
			wantCount: map[ItemType]int{ItemTypeEvent: 64},
		},
		{
			// 64 metrics + one of each lower-priority type: every drop
			// lands on the lowest-priority type present, walking the
			// documented ladder OwnerChain → LogTemplate → Event →
			// PodStatus before a single Metric would be touched.
			name: "count cap walks the priority ladder upward",
			in: slices.Concat(
				filler(ItemTypeMetric, SourcePrometheus, 64, 16),
				filler(ItemTypeOwnerChain, SourceK8s, 1, 16),
				filler(ItemTypeLogTemplate, SourceLoki, 1, 16),
				filler(ItemTypeEvent, SourceK8s, 1, 16),
				filler(ItemTypePodStatus, SourceK8s, 1, 16),
			),
			wantCount: map[ItemType]int{ItemTypeMetric: 64},
		},
		{
			// GitCommit and SyncState sit above Metric in retention, so
			// over-cap metrics are dropped before either of them.
			name: "gitcommit and syncstate outrank metric",
			in: slices.Concat(
				filler(ItemTypeMetric, SourcePrometheus, 64, 16),
				filler(ItemTypeGitCommit, SourceK8s, 1, 16),
				filler(ItemTypeSyncState, SourceArgoCD, 1, 16),
			),
			wantCount: map[ItemType]int{
				ItemTypeMetric:    62,
				ItemTypeGitCommit: 1,
				ItemTypeSyncState: 1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, raw, _ := mustAssemble(t, tc.in)
			if len(b.Items) > MaxItems {
				t.Errorf("bundle holds %d items, cap is %d", len(b.Items), MaxItems)
			}
			if len(raw) > MaxBundleBytes {
				t.Errorf("bundle serializes to %d bytes, cap is %d", len(raw), MaxBundleBytes)
			}
			if got := countByType(b); !mapsEqualTyped(got, tc.wantCount) {
				t.Errorf("per-type survivor counts:\n got %v\nwant %v", got, tc.wantCount)
			}
			assertIDsContiguous(t, b)
		})
	}
}

func mapsEqualTyped(a, b map[ItemType]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestAssembleCountCapKeepsSortedPrefix pins WHICH items survive: within
// the truncated type it is the first items in natural-key order.
func TestAssembleCountCapKeepsSortedPrefix(t *testing.T) {
	b, _, _ := mustAssemble(t, filler(ItemTypeEvent, SourceK8s, 70, 16))
	if got := len(b.Items); got != 64 {
		t.Fatalf("got %d items, want 64", got)
	}
	for i, it := range b.Items {
		wantName := fmt.Sprintf("event-%03d", i)
		if it.Data["name"] != wantName {
			t.Fatalf("survivor %d is %q, want %q — truncation did not drop from the end", i, it.Data["name"], wantName)
		}
		wantID := fmt.Sprintf("ev/event-%02d", i+1)
		if it.ID != wantID {
			t.Fatalf("survivor %d has id %q, want %q", i, it.ID, wantID)
		}
	}
}

// TestAssembleItemCap proves an oversized item is truncated to the 4 KiB
// canonical form deterministically, with the cut marked.
func TestAssembleItemCap(t *testing.T) {
	in := []Collected{{
		Type:   ItemTypeLogTemplate,
		Source: SourceLoki,
		Key:    hugeItemKey,
		Data: map[string]string{
			keyTemplate: strings.Repeat("a", 10*1024),
			"count":     "12",
		},
	}}
	b1, raw1, hash1 := mustAssemble(t, in)
	_, raw2, hash2 := mustAssemble(t, []Collected{{
		Type:   ItemTypeLogTemplate,
		Source: SourceLoki,
		Key:    hugeItemKey,
		Data: map[string]string{
			keyTemplate: strings.Repeat("a", 10*1024),
			"count":     "12",
		},
	}})

	if !bytes.Equal(raw1, raw2) || hash1 != hash2 {
		t.Error("item truncation is not deterministic across two assemblies")
	}
	it := b1.Items[0]
	itemRaw, err := hash.CanonicalJSON(it)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemRaw) > MaxItemBytes {
		t.Errorf("item still serializes to %d bytes, cap is %d", len(itemRaw), MaxItemBytes)
	}
	if !strings.HasSuffix(it.Data[keyTemplate], "…") {
		t.Error("truncated value does not carry the … marker")
	}
	if it.Data["count"] != "12" {
		t.Error("truncation touched a value it did not need to")
	}
}

// TestAssembleTotalCap proves the 128 KiB aggregate cap: enough near-4KiB
// items to overflow the bundle get dropped lowest-priority-first until the
// canonical bytes fit, and higher-priority items all survive.
func TestAssembleTotalCap(t *testing.T) {
	in := slices.Concat(
		filler(ItemTypeEvent, SourceK8s, 40, 3500),   // ~140 KiB of events alone
		filler(ItemTypeGitCommit, SourceK8s, 2, 100), // must all survive
		filler(ItemTypeMetric, SourcePrometheus, 3, 100),
	)
	b, raw, _ := mustAssemble(t, in)

	if len(raw) > MaxBundleBytes {
		t.Fatalf("bundle serializes to %d bytes, cap is %d", len(raw), MaxBundleBytes)
	}
	got := countByType(b)
	if got[ItemTypeGitCommit] != 2 || got[ItemTypeMetric] != 3 {
		t.Errorf("high-priority items were dropped before low: %v", got)
	}
	if got[ItemTypeEvent] >= 40 {
		t.Errorf("no event was dropped although the aggregate exceeds the cap: %v", got)
	}
	// The surviving events are the sorted prefix.
	for i, it := range b.Items {
		if it.Type != ItemTypeEvent {
			continue
		}
		if want := fmt.Sprintf("ev/event-%02d", i+1); it.ID != want {
			t.Fatalf("event ids not contiguous from 01: got %s at position %d", it.ID, i)
		}
	}
	assertIDsContiguous(t, b)
}

// assertIDsContiguous checks the post-truncation id law: per type, ids
// count 01,02,… with no gaps, in bundle order.
func assertIDsContiguous(t *testing.T, b *Bundle) {
	t.Helper()
	seq := map[ItemType]int{}
	for _, it := range b.Items {
		seq[it.Type]++
		if want := ItemID(it.Type, seq[it.Type]); it.ID != want {
			t.Errorf("item id %q, want %q — ids must be assigned contiguously after truncation", it.ID, want)
		}
	}
}

func TestAssembleRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		in   Collected
	}{
		{"unknown type", Collected{Type: ItemType("Rumor"), Source: SourceK8s, Key: "k", Data: map[string]string{}}},
		{"unknown source", Collected{Type: ItemTypeEvent, Source: "carrier-pigeon", Key: "k", Data: map[string]string{}}},
		{"empty natural key", Collected{Type: ItemTypeEvent, Source: SourceK8s, Key: "", Data: map[string]string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := Assemble(testIncidentRef(), fixedCollectedAt, []Collected{tc.in}); err == nil {
				t.Error("Assemble accepted malformed input; want error")
			}
		})
	}
}

// TestAssembleEmptyInput: a scope with nothing observable still yields a
// well-formed, hashable bundle with an empty (not null) items array.
func TestAssembleEmptyInput(t *testing.T) {
	b, raw, h := mustAssemble(t, nil)
	if b.Items == nil || len(b.Items) != 0 {
		t.Errorf("empty input must produce an empty items slice, got %#v", b.Items)
	}
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Errorf("canonical form must carry items:[], got %s", raw)
	}
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("hash %q is not prefixed", h)
	}
}

// TestAssembleDoesNotMutateInput: collectors may reuse their slices; the
// assembler must work on copies (truncation must not write back).
func TestAssembleDoesNotMutateInput(t *testing.T) {
	in := []Collected{{
		Type:   ItemTypeLogTemplate,
		Source: SourceLoki,
		Key:    hugeItemKey,
		Data:   map[string]string{keyTemplate: strings.Repeat("a", 10*1024)},
	}}
	mustAssemble(t, in)
	if len(in[0].Data[keyTemplate]) != 10*1024 {
		t.Error("Assemble mutated the caller's data map")
	}
}
