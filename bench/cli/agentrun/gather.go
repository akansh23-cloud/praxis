/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package agentrun

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// This is the Phase 2 stand-in for evidence collection: recent Kubernetes
// WARNING events from the incident's scope namespaces, and nothing else —
// exactly the observations FR-P2-04's rule-based baseline is defined
// over, and observations any production analyzer would legitimately have.
// No metrics, no logs, no object specs: the real bounded collector of LLD
// §6 is Phase 3's (Session 3.1) and replaces this wholesale.
//
// Only status/event FIELDS are copied into items — never object labels or
// annotations, where benchmark bookkeeping (and, in real clusters,
// operator noise) lives.

const (
	// eventWindow is how far before the Incident's creation an event's
	// last activity may lie and still count as evidence. It generously
	// covers the injection-to-filing gap of every pack while bounding how
	// much unrelated history the bundle drags in.
	eventWindow = 2 * time.Minute

	// maxEventItems is the §6 bundle item cap.
	maxEventItems = 64

	sourceK8s = "k8s"
)

// gatherBundle lists warning events in the scope namespaces and freezes
// them into a §6-shaped bundle. Items are sorted by content before ids
// are assigned, so the same observed state yields the same ids.
func gatherBundle(ctx context.Context, c client.Client,
	inc *praxisv1alpha1.Incident, now time.Time,
) (*evidence.Bundle, error) {
	cutoff := inc.CreationTimestamp.Add(-eventWindow)

	var picked []*corev1.Event
	for _, ns := range inc.Spec.Scope.Namespaces {
		var list corev1.EventList
		if err := c.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil, fmt.Errorf("list events in %q: %w", ns, err)
		}
		for i := range list.Items {
			ev := &list.Items[i]
			if ev.Type != corev1.EventTypeWarning || lastActivity(ev).Before(cutoff) {
				continue
			}
			picked = append(picked, ev)
		}
	}

	slices.SortFunc(picked, func(a, b *corev1.Event) int {
		return cmp.Or(
			cmp.Compare(a.InvolvedObject.Namespace, b.InvolvedObject.Namespace),
			cmp.Compare(a.InvolvedObject.Kind, b.InvolvedObject.Kind),
			cmp.Compare(a.InvolvedObject.Name, b.InvolvedObject.Name),
			cmp.Compare(a.Reason, b.Reason),
			cmp.Compare(a.Message, b.Message),
		)
	})
	if len(picked) > maxEventItems {
		picked = picked[:maxEventItems]
	}

	items := make([]evidence.Item, 0, len(picked))
	for i, ev := range picked {
		items = append(items, evidence.Item{
			ID:     evidence.ItemID(evidence.ItemTypeEvent, i+1),
			Type:   evidence.ItemTypeEvent,
			Source: sourceK8s,
			Data: map[string]string{
				evidence.EventDataType:              ev.Type,
				evidence.EventDataReason:            ev.Reason,
				evidence.EventDataMessage:           ev.Message,
				evidence.EventDataInvolvedKind:      ev.InvolvedObject.Kind,
				evidence.EventDataInvolvedName:      ev.InvolvedObject.Name,
				evidence.EventDataInvolvedNamespace: ev.InvolvedObject.Namespace,
				evidence.EventDataCount:             strconv.Itoa(int(eventCount(ev))),
			},
		})
	}

	return &evidence.Bundle{
		Version:     evidence.SchemaVersion,
		Incident:    evidence.IncidentRef{Name: inc.Name, UID: string(inc.UID)},
		CollectedAt: now.UTC().Format(time.RFC3339),
		Items:       items,
	}, nil
}

// lastActivity is the most recent instant an Event object claims to have
// fired, across the legacy and the events.k8s.io-style fields.
func lastActivity(ev *corev1.Event) time.Time {
	t := ev.CreationTimestamp.Time
	for _, cand := range []time.Time{
		ev.FirstTimestamp.Time, ev.LastTimestamp.Time, ev.EventTime.Time,
	} {
		if cand.After(t) {
			t = cand
		}
	}
	if ev.Series != nil && ev.Series.LastObservedTime.After(t) {
		t = ev.Series.LastObservedTime.Time
	}
	return t
}

func eventCount(ev *corev1.Event) int32 {
	if ev.Series != nil {
		return ev.Series.Count
	}
	if ev.Count > 0 {
		return ev.Count
	}
	return 1
}
