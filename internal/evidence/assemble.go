/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/akansh23-cloud/praxis/internal/hash"
)

// This file is the §6 bundle assembler: collectors hand over evidence in
// any order, and Assemble turns it into the one canonical, capped, hashed
// document that identical inputs always reproduce byte for byte.

// The normative §6 caps.
const (
	// MaxItems is the most evidence items one bundle may hold.
	MaxItems = 64

	// MaxItemBytes caps one item's canonical JSON serialization.
	MaxItemBytes = 4 * 1024

	// MaxBundleBytes caps the whole bundle's canonical JSON serialization.
	MaxBundleBytes = 128 * 1024
)

// The closed §6 source systems an item may name.
const (
	SourceK8s        = "k8s"
	SourcePrometheus = "prometheus"
	SourceLoki       = "loki"
	SourceArgoCD     = "argocd"
	SourceFlux       = "flux"
)

var validSources = map[string]bool{
	SourceK8s:        true,
	SourcePrometheus: true,
	SourceLoki:       true,
	SourceArgoCD:     true,
	SourceFlux:       true,
}

// Collected is one piece of evidence as a collector hands it over — an
// Item before it has earned an id. Key is the item's natural identity
// within its type (a pod's "namespace/name", a metric's template id): the
// third component of the §6 ordering tuple (type, source, natural key). It
// exists only to make the sort deterministic and is not serialized.
type Collected struct {
	Type   ItemType
	Source string
	Key    string
	Data   map[string]string
}

// retentionRank is the complete normative retention order of LLD §6 /
// ADR-006: truncation always drops from the lowest rank present, so
// OwnerChain is sacrificed first and GitCommit survives cap pressure
// longest — when the bundle must forget something, it forgets volume
// before it forgets causes. These ranks decide which items survive the
// caps and therefore the canonical bytes and hash of any over-cap
// bundle; changing them is a versioned contract change (ADR-006), never
// a quiet edit.
var retentionRank = map[ItemType]int{
	ItemTypeGitCommit:   7,
	ItemTypeSyncState:   6,
	ItemTypeMetric:      5,
	ItemTypePodStatus:   4,
	ItemTypeEvent:       3,
	ItemTypeLogTemplate: 2,
	ItemTypeOwnerChain:  1,
}

// workItem is a Collected plus the cached canonical form of its data,
// which serves as the final sort tie-break so even two items with an
// identical (type, source, key) tuple order deterministically, and
// whether the scrubber changed any of its values.
type workItem struct {
	Collected
	canonicalData string
	redacted      bool
}

// Assemble builds the canonical §6 bundle from collector output:
//
//  1. every item's data is copied, every value is scrubbed (redact.go —
//     the item is marked redacted when anything was replaced), and the
//     item is truncated to the 4 KiB item cap; scrubbing comes first so
//     a cut can never leave the visible half of a credential behind;
//  2. items sort by (type, source, natural key), canonical data bytes as
//     the final tie-break;
//  3. the 64-item and 128 KiB caps are enforced by dropping, one item at a
//     time, the last-sorted item of the lowest-retention type present;
//  4. ids ev/<source-token>-<seq> are assigned per type AFTER sorting and
//     truncation, so the ids in the final document are always contiguous;
//  5. the bundle is serialized once through internal/hash's canonical JSON
//     and hashed sha256 over exactly those bytes.
//
// Identical inputs (incident, collectedAt, items in any order) therefore
// produce byte-identical bundles with identical hashes.
func Assemble(incident IncidentRef, collectedAt time.Time, collected []Collected) (*Bundle, []byte, string, error) {
	work := make([]workItem, 0, len(collected))
	for i := range collected {
		c := collected[i]
		if _, ok := sourceTokens[c.Type]; !ok {
			return nil, nil, "", fmt.Errorf("evidence item %d has unknown type %q", i, c.Type)
		}
		if !validSources[c.Source] {
			return nil, nil, "", fmt.Errorf("evidence item %d has source %q outside the closed §6 set", i, c.Source)
		}
		if c.Key == "" {
			return nil, nil, "", fmt.Errorf("evidence item %d (%s) has no natural key", i, c.Type)
		}
		c.Data = maps.Clone(c.Data)
		if c.Data == nil {
			c.Data = map[string]string{}
		}
		redacted := false
		for k, v := range c.Data {
			if scrubbed, changed := Scrub(v); changed {
				c.Data[k] = scrubbed
				redacted = true
			}
		}
		if err := fitToItemCap(&c, redacted); err != nil {
			return nil, nil, "", fmt.Errorf("evidence item %d (%s): %w", i, c.Type, err)
		}
		canonical, err := hash.CanonicalJSON(c.Data)
		if err != nil {
			return nil, nil, "", fmt.Errorf("canonicalize data of evidence item %d (%s): %w", i, c.Type, err)
		}
		work = append(work, workItem{Collected: c, canonicalData: string(canonical), redacted: redacted})
	}

	slices.SortFunc(work, func(a, b workItem) int {
		return cmp.Or(
			cmp.Compare(string(a.Type), string(b.Type)),
			cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Key, b.Key),
			cmp.Compare(a.canonicalData, b.canonicalData),
		)
	})

	for len(work) > MaxItems {
		work = dropLowestPriority(work)
	}

	bundle := &Bundle{
		Version:     SchemaVersion,
		Incident:    incident,
		CollectedAt: collectedAt.UTC().Format(time.RFC3339),
	}
	raw, err := renderItems(bundle, work)
	if err != nil {
		return nil, nil, "", err
	}
	// Ids are re-assigned after every drop so the final document never
	// shows a gap; with at most 64 items per pass this converges fast.
	for len(raw) > MaxBundleBytes && len(work) > 0 {
		work = dropLowestPriority(work)
		if raw, err = renderItems(bundle, work); err != nil {
			return nil, nil, "", err
		}
	}
	return bundle, raw, hash.SHA256Prefixed(raw), nil
}

// dropLowestPriority removes one item: the LAST one, in sorted order, of
// the lowest-retention type present. That is the whole §6 truncation rule
// — both the item-count and the total-size cap apply it repeatedly.
func dropLowestPriority(work []workItem) []workItem {
	victim, victimRank := -1, 0
	for i, w := range work {
		if r := retentionRank[w.Type]; victim == -1 || r < victimRank ||
			(r == victimRank && i > victim) {
			victim, victimRank = i, r
		}
	}
	return slices.Delete(work, victim, victim+1)
}

// fitToItemCap truncates an item's data until its canonical serialization
// — measured with a same-width placeholder id, since real ids exist only
// after sorting — fits MaxItemBytes. Deterministic rule: repeatedly take
// the entry with the longest value in bytes (ties: smallest key), halve it
// on a rune boundary and mark the cut with "…"; entries too short to
// shrink are removed outright. Values dominate item size, so this
// converges; the marker keeps the cut visible to any reader.
func fitToItemCap(c *Collected, redacted bool) error {
	const marker = "…"
	for {
		probe := Item{ID: ItemID(c.Type, MaxItems), Type: c.Type, Source: c.Source, Data: c.Data, Redacted: redacted}
		raw, err := hash.CanonicalJSON(probe)
		if err != nil {
			return fmt.Errorf("canonicalize while fitting to item cap: %w", err)
		}
		if len(raw) <= MaxItemBytes {
			return nil
		}
		key := ""
		for k, v := range c.Data {
			if key == "" || len(v) > len(c.Data[key]) || (len(v) == len(c.Data[key]) && k < key) {
				key = k
			}
		}
		if key == "" {
			return fmt.Errorf("item exceeds %d bytes with no data left to truncate", MaxItemBytes)
		}
		v := c.Data[key]
		if len(v) < 8 {
			delete(c.Data, key)
			continue
		}
		half := []rune(v)
		c.Data[key] = string(half[:len(half)/2]) + marker
	}
}

// renderItems assigns per-type ids over the sorted work list, writes the
// items into the bundle and returns its canonical serialization.
func renderItems(bundle *Bundle, work []workItem) ([]byte, error) {
	items := make([]Item, 0, len(work))
	seq := map[ItemType]int{}
	for _, w := range work {
		seq[w.Type]++
		items = append(items, Item{
			ID:       ItemID(w.Type, seq[w.Type]),
			Type:     w.Type,
			Source:   w.Source,
			Data:     w.Data,
			Redacted: w.redacted,
		})
	}
	bundle.Items = items
	raw, err := hash.CanonicalJSON(bundle)
	if err != nil {
		return nil, fmt.Errorf("canonicalize evidence bundle: %w", err)
	}
	return raw, nil
}
