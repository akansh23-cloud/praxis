/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// Collector composes the Session 3.1 collectors into the one call the
// incident controller makes. It holds the secretless Reader and, when the
// cluster has one, a Prometheus query client — and nothing else: no
// writer, no generic client, no configuration a model could reach.
type Collector struct {
	// Reader is the only Kubernetes view (required).
	Reader Reader

	// Prom is the Prometheus seam; nil means no Prometheus is configured
	// and the bundle honestly carries no Metric items.
	Prom QueryClient

	// Now stamps collectedAt; nil means time.Now. Tests fix it to prove
	// byte-identity end to end.
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

	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ref := IncidentRef{Name: incident.Name, UID: string(incident.UID)}
	bundle, raw, bundleHash, err := Assemble(ref, now(), items)
	if err != nil {
		return nil, err
	}
	return &CollectionResult{Bundle: bundle, Raw: raw, Hash: bundleHash, Notes: notes}, nil
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
