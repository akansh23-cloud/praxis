/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

const (
	testNamespace = "default"

	// evidenceHash is a well-formed hash no persisted bundle hashes to:
	// the fixtures that need a hash WITHOUT a bundle (mismatch cases, the
	// presupplied-hash walk) use it. It matches the sample plan's
	// placeholder value.
	evidenceHash = "sha256:9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f9c1f"

	// fixtureCitation is the one id the fixture bundle holds; every
	// fixture plan cites it, and only it, so citations resolve exactly
	// when the incident carries the persisted fixture bundle.
	fixtureCitation = "ev/podstatus-01"

	// differentEvidenceHash is any other well-formed hash, for mismatch cases.
	differentEvidenceHash = "sha256:abababababababababababababababababababababababababababababababab"

	// Target fixtures shared by the phase-machine and admission tables.
	targetKindDeployment = "Deployment"
	targetKindNode       = "Node"
	fixtureWorkloadName  = "fixture-workload"
	fixtureContainerName = "api"
	fixtureNodeName      = "worker-1"
)

// nameSeq feeds uniqueName so fixtures from different specs sharing the
// default namespace can never collide or reference each other.
var nameSeq atomic.Int64

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nameSeq.Add(1))
}

func newTestIncident(name string) *praxisv1alpha1.Incident {
	return &praxisv1alpha1.Incident{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: praxisv1alpha1.IncidentSpec{
			Source:      praxisv1alpha1.IncidentSourceManual,
			Description: "envtest fixture incident",
			Severity:    praxisv1alpha1.Severity("High"),
			Scope: praxisv1alpha1.IncidentScope{
				Namespaces: []string{testNamespace},
			},
		},
	}
}

// newTestPlan builds a CEL-valid single-action plan referencing the named
// incident. Every fixture passes admission; what the controller then does
// with it is what the specs assert.
func newTestPlan(name, incidentName, bundleHash string) *praxisv1alpha1.RemediationPlan {
	return &praxisv1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: praxisv1alpha1.RemediationPlanSpec{
			IncidentRef:        praxisv1alpha1.IncidentRef{Name: incidentName},
			EvidenceBundleHash: bundleHash,
			Hypothesis: praxisv1alpha1.Hypothesis{
				Summary:           "Fixture workload needs a controlled restart",
				ConfidencePercent: 80,
				Citations:         []praxisv1alpha1.EvidenceID{fixtureCitation},
			},
			Actions: []praxisv1alpha1.Action{{
				Type: praxisv1alpha1.ActionRestartWorkload,
				Target: praxisv1alpha1.TargetRef{
					Kind:      targetKindDeployment,
					Namespace: testNamespace,
					Name:      fixtureWorkloadName,
				},
			}},
			Verification: praxisv1alpha1.VerificationSpec{
				Predicate: "up == 1",
				Window:    metav1.Duration{Duration: 5 * time.Minute},
				OnFailure: praxisv1alpha1.FailureActionEscalate,
			},
			Rollback: praxisv1alpha1.RollbackSpec{
				Strategy: praxisv1alpha1.RollbackNone,
			},
		},
	}
}

// fixtureCollectedAt fixes the fixture bundle's timestamp so its bytes and
// hash never move between runs.
var fixtureCollectedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// fixtureBundle renders the deterministic bundle envtest incidents carry
// through the real assembler: one PodStatus item, so fixtureCitation
// resolves and nothing else does.
func fixtureBundle(incident *praxisv1alpha1.Incident) (raw []byte, bundleHash string, err error) {
	ref := evidence.IncidentRef{Name: incident.Name, UID: string(incident.UID)}
	_, raw, bundleHash, err = evidence.Assemble(ref, fixtureCollectedAt, []evidence.Collected{{
		Type: evidence.ItemTypePodStatus, Source: evidence.SourceK8s, Key: testNamespace + "/fixture-pod",
		Data: map[string]string{
			evidence.DataNamespace: testNamespace, evidence.DataName: "fixture-pod",
			evidence.PodDataPhase: "Running", evidence.PodDataReady: "false",
			"container.api.lastTerminated": "OOMKilled:exit=137",
		},
	}})
	return raw, bundleHash, err
}

// statusWriteCountingClient wraps a real client and counts every write that
// goes through the status subresource. The no-op specs hand it to a
// reconciler and assert the count stays at zero — "settled objects are
// strict no-ops" as a measured fact, not an intention.
type statusWriteCountingClient struct {
	client.Client
	statusWrites int64
}

func (c *statusWriteCountingClient) Status() client.SubResourceWriter {
	return &countingSubResourceWriter{SubResourceWriter: c.Client.Status(), counter: &c.statusWrites}
}

type countingSubResourceWriter struct {
	client.SubResourceWriter
	counter *int64
}

func (w *countingSubResourceWriter) Create(
	ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption,
) error {
	atomic.AddInt64(w.counter, 1)
	return w.SubResourceWriter.Create(ctx, obj, subResource, opts...)
}

func (w *countingSubResourceWriter) Update(
	ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption,
) error {
	atomic.AddInt64(w.counter, 1)
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *countingSubResourceWriter) Patch(
	ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
) error {
	atomic.AddInt64(w.counter, 1)
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}
