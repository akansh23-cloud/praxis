/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
)

// Collector composes the collectors into the one call the incident
// controller makes. It holds the secretless Reader and, when the cluster
// has them, a Prometheus query client and a Loki log client — and nothing
// else: no writer, no generic client, no configuration a model could
// reach. Both telemetry endpoints arrive as process configuration
// (cmd/main.go flags); nothing here can fetch a credential for them from
// the cluster, because nothing here can fetch a Secret at all.
type Collector struct {
	// Reader is the only Kubernetes view (required).
	Reader Reader

	// Prom is the Prometheus seam; nil means no Prometheus is configured
	// and the bundle honestly carries no Metric items.
	Prom QueryClient

	// Logs is the Loki seam; nil means no Loki is configured and the
	// bundle honestly carries no LogTemplate items.
	Logs logs.Client

	// Now stamps collectedAt and ends the log window; nil means time.Now.
	// Tests fix it to prove byte-identity end to end.
	Now func() time.Time
}

// CollectionResult is one finished collection: the bundle, the exact
// canonical bytes to persist, their hash, and any honest omissions for
// the condition message.
type CollectionResult struct {
	Bundle *Bundle
	Raw    []byte
	Hash   string
	Notes  []string
}

// Collect gathers, assembles and hashes the evidence for one incident.
// Kubernetes read errors fail the collection (the controller retries with
// backoff); Prometheus-level failures are recorded inside Metric items
// per template rather than failing the bundle, because a down Prometheus
// must not blind the k8s half of the diagnosis.
func (c *Collector) Collect(ctx context.Context, incident *praxisv1alpha1.Incident) (*CollectionResult, error) {
	namespaces := incident.Spec.Scope.Namespaces
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	collectedAt := now()

	items, err := CollectKubernetes(ctx, c.Reader, namespaces)
	if err != nil {
		return nil, err
	}

	var notes []string
	if c.Prom == nil {
		notes = append(notes, "prometheus not configured; Metric evidence omitted")
	} else {
		items = append(items, CollectPrometheus(ctx, c.Prom, namespaces)...)
	}
	if c.Logs == nil {
		notes = append(notes, "loki not configured; LogTemplate evidence omitted")
	} else {
		logItems, logNotes := CollectLogs(ctx, c.Logs, namespaces, collectedAt)
		items = append(items, logItems...)
		notes = append(notes, logNotes...)
	}

	ref := IncidentRef{Name: incident.Name, UID: string(incident.UID)}
	bundle, raw, bundleHash, err := Assemble(ref, collectedAt, items)
	if err != nil {
		return nil, err
	}
	if n := redactedCount(bundle); n > 0 {
		notes = append(notes, fmt.Sprintf("%d item(s) redacted", n))
	}
	return &CollectionResult{Bundle: bundle, Raw: raw, Hash: bundleHash, Notes: notes}, nil
}

func redactedCount(b *Bundle) int {
	n := 0
	for i := range b.Items {
		if b.Items[i].Redacted {
			n++
		}
	}
	return n
}

// BundleConfigMapKey is the single data key the bundle ConfigMap holds:
// the canonical JSON bytes, exactly the bytes the hash covers.
const BundleConfigMapKey = "bundle.json"

// BundleConfigMapName renders the §6 storage name praxis-ev-<incident-uid8>.
func BundleConfigMapName(uid types.UID) string {
	u := string(uid)
	if len(u) > 8 {
		u = u[:8]
	}
	return "praxis-ev-" + u
}
