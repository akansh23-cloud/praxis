/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"fmt"

	"github.com/akansh23-cloud/praxis/internal/hash"
)

// This file defines the evidence bundle DATA TYPES of docs/02-LLD.md §6 —
// the parameter type the Agent seam (internal/agents) is defined over.
// Collection lives in k8s.go/prometheus.go, canonical assembly with the
// §6 caps in assemble.go, and the composed entry point in collect.go
// (playbook Session 3.1). Log templating and scrubbing arrive with
// Session 3.2.

// SchemaVersion is the bundle schema version of LLD §6.
const SchemaVersion = "1"

// ItemType is the closed set of §6 evidence types. Closed like the action
// vocabulary (ADR-001): §17.3 diagnosis rules may only demand evidence of
// these types (ADR-005), so a new type is a design decision, not a string.
type ItemType string

const (
	// ItemTypePodStatus is a pod's phase, readiness and per-container
	// state (env variable names only).
	ItemTypePodStatus ItemType = "PodStatus"
	// ItemTypeEvent is a Warning Event in the incident's scope.
	ItemTypeEvent ItemType = "Event"
	// ItemTypeOwnerChain is a pod's owner chain up to its workload.
	ItemTypeOwnerChain ItemType = "OwnerChain"
	// ItemTypeMetric is one Prometheus query result from the code-owned
	// RED/USE/SLO-burn template set.
	ItemTypeMetric ItemType = "Metric"
	// ItemTypeLogTemplate is one Drain-style log template with its count,
	// contributing pods and exactly one exemplar (ADR-007); never a raw line.
	ItemTypeLogTemplate ItemType = "LogTemplate"
	// ItemTypeSyncState is desired-state divergence reported by a GitOps
	// controller (Phase 6).
	ItemTypeSyncState ItemType = "SyncState"
	// ItemTypeGitCommit is recent-change context read from workload
	// annotations (kubernetes.io/change-cause, praxis.dev/commit).
	ItemTypeGitCommit ItemType = "GitCommit"
)

// sourceTokens maps each §6 evidence type to the lowercase token its item
// ids carry (LLD §6 id scheme ev/<source>-<seq>). ADR-005 fixes these
// tokens as the closed set §17.3 evidence-id patterns are validated
// against; the bench scenario loader enforces the same list, so an id
// minted here is matchable by every well-formed answer key.
var sourceTokens = map[ItemType]string{
	ItemTypePodStatus:   "podstatus",
	ItemTypeEvent:       "event",
	ItemTypeOwnerChain:  "ownerchain",
	ItemTypeMetric:      "metric",
	ItemTypeLogTemplate: "logtemplate",
	ItemTypeSyncState:   "syncstate",
	ItemTypeGitCommit:   "gitcommit",
}

// ItemID renders the canonical §6 id for the seq-th item of a type,
// e.g. ItemID(ItemTypeEvent, 3) == "ev/event-03".
func ItemID(t ItemType, seq int) string {
	return fmt.Sprintf("ev/%s-%02d", sourceTokens[t], seq)
}

// IncidentRef names the incident a bundle was collected for.
type IncidentRef struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// Item is one piece of evidence. The data payload is a flat string map —
// a §6-conformant JSON object whose dotted keys express nesting (e.g.
// container.api.state) — kept flat deliberately: it keeps canonical
// serialization and the per-item size cap trivial, and it is the exact
// contract the Phase 2 bench and the frozen rule-based baseline already
// read.
type Item struct {
	// ID is the §6 evidence id, ev/<source>-<seq>, assigned by the
	// producer after sorting so identical state yields identical ids.
	ID string `json:"id"`

	Type ItemType `json:"type"`

	// Source names the system observed: k8s, prometheus, loki, argocd, flux.
	Source string `json:"source"`

	Data map[string]string `json:"data"`

	Redacted bool `json:"redacted"`
}

// Data keys for ItemTypeEvent items — the Phase 2 minimal payload contract
// shared by the bench gatherer (writer) and the rule-based baseline
// (reader). One definition here so the two cannot drift.
const (
	EventDataType              = "type"    // corev1.EventTypeNormal | Warning
	EventDataReason            = "reason"  // e.g. Failed, Unhealthy, BackOff
	EventDataMessage           = "message" // the kubelet's own words
	EventDataInvolvedKind      = "involvedKind"
	EventDataInvolvedName      = "involvedName"
	EventDataInvolvedNamespace = "involvedNamespace"
	EventDataCount             = "count"
)

// Bundle is the §6 evidence document: everything an Agent is allowed to
// know beyond the Incident itself. There is deliberately no cluster client
// anywhere near the Agent seam — if it is not in the bundle, the agent
// cannot see it.
type Bundle struct {
	Version     string      `json:"version"`
	Incident    IncidentRef `json:"incident"`
	CollectedAt string      `json:"collectedAt"` // RFC 3339
	Items       []Item      `json:"items"`
}

// Hash returns the §6 bundle hash — sha256 over the RFC 8785 canonical
// JSON of the bundle, in the "sha256:<hex>" form
// RemediationPlanSpec.EvidenceBundleHash requires.
func (b *Bundle) Hash() (string, error) {
	canonical, err := hash.CanonicalJSON(b)
	if err != nil {
		return "", fmt.Errorf("canonicalize evidence bundle: %w", err)
	}
	return hash.SHA256Prefixed(canonical), nil
}
