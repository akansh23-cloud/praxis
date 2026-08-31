/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/bench/cli/kube"
	"github.com/akansh23-cloud/praxis/bench/cli/scenario"
)

// responseKind classifies what (if anything) answered the Incident.
type responseKind string

const (
	// ResponsePlan means a RemediationPlan referencing the Incident appeared.
	ResponsePlan responseKind = "PlanProposed"
	// ResponseNoAction means the restraint annotation appeared on the Incident.
	ResponseNoAction responseKind = "NoActionProposed"
	// ResponseTimeout means the wait ended with neither — expected with --agent none.
	ResponseTimeout responseKind = "NoResponse"
)

// response is the observed answer to one filed Incident.
type response struct {
	Kind   responseKind
	Detail string
	Waited time.Duration
}

// outcome is the record of one run. The scorer session (2.3) turns these
// into JSONL; until then they are printed.
type outcome struct {
	Run      int
	Response response
}

// runOnce drives one namespace-scoped run: namespaces + topology → healthy
// → fault → Incident → await response → teardown (unless --keep). On a
// mid-run failure the namespaces are torn down best-effort so a broken run
// does not leak into the next.
func (r *Runner) runOnce(ctx context.Context, n int) (outcome, error) {
	nss := r.scn.Incident.ScopeNamespaces
	label := fmt.Sprintf("r%d:", n)
	oc := outcome{Run: n}
	needCleanup, success := false, false
	defer func() {
		if needCleanup && !success && !r.opts.Keep {
			tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
			defer cancel()
			if terr := r.teardown(tctx); terr != nil {
				r.out.f("    warning: teardown after the failure also failed: %v", terr)
			}
		}
	}()

	if err := r.phase(ctx, label+"namespaces+topology", func(ctx context.Context) error {
		for _, ns := range nss {
			if err := kube.EnsureNamespace(ctx, r.c, ns); err != nil {
				return err
			}
		}
		needCleanup = true
		r.out.f("    namespaces %v ready; applying %s", nss, r.scn.Topology.Kustomize)
		_, err := r.tc.Kubectl(ctx, "apply", "-k", r.scn.TopologyDir())
		return err
	}); err != nil {
		return oc, err
	}

	if err := r.phase(ctx, label+"topology-healthy", func(ctx context.Context) error {
		return r.waitTopologyHealthy(ctx, 5*time.Minute)
	}); err != nil {
		return oc, err
	}

	if err := r.phase(ctx, label+"inject-fault", func(ctx context.Context) error {
		return r.injectFault(ctx)
	}); err != nil {
		return oc, err
	}

	var inc *praxisv1alpha1.Incident
	if err := r.phase(ctx, label+"file-incident", func(ctx context.Context) error {
		var err error
		inc, err = r.fileIncident(ctx, n)
		return err
	}); err != nil {
		return oc, err
	}

	if err := r.phase(ctx, label+"await-response", func(ctx context.Context) error {
		var err error
		oc.Response, err = r.awaitResponse(ctx, inc)
		return err
	}); err != nil {
		return oc, err
	}

	if r.opts.Keep {
		r.out.f("    --keep: leaving namespaces %v and Incident %s/%s in place", nss, inc.Namespace, inc.Name)
		r.out.f("    inspect with: kubectl --kubeconfig %s -n %s get incidents,remediationplans,pods", r.tc.Kubeconfig, nss[0])
	} else if err := r.phase(ctx, label+"teardown", func(ctx context.Context) error {
		return r.teardown(ctx)
	}); err != nil {
		return oc, err
	}
	success = true
	return oc, nil
}

// waitTopologyHealthy blocks until every Deployment in the scope namespaces
// is fully rolled out and available.
func (r *Runner) waitTopologyHealthy(ctx context.Context, timeout time.Duration) error {
	nss := r.scn.Incident.ScopeNamespaces
	var lastNotReady []string
	total := 0
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		total = 0
		var notReady []string
		for _, ns := range nss {
			var list appsv1.DeploymentList
			if err := r.c.List(ctx, &list, client.InNamespace(ns)); err != nil {
				return false, fmt.Errorf("list Deployments in %q: %w", ns, err)
			}
			for i := range list.Items {
				d := &list.Items[i]
				total++
				if !deploymentReady(d) {
					notReady = append(notReady, fmt.Sprintf("%s/%s (%d/%d ready)", ns, d.Name, d.Status.ReadyReplicas, replicas(d)))
				}
			}
		}
		if total == 0 {
			return false, fmt.Errorf("no Deployments found in namespaces %v after applying the topology — does the overlay target the right namespace?", nss)
		}
		lastNotReady = notReady
		return len(notReady) == 0, nil
	})
	if err != nil {
		if len(lastNotReady) > 0 {
			return fmt.Errorf("topology not healthy after %s; still not ready: %s: %w", timeout, strings.Join(lastNotReady, ", "), err)
		}
		return err
	}
	r.out.f("    all %d Deployments ready in %v", total, nss)
	return nil
}

// injectFault applies the scenario's fault. Session 2.1 exercises Manifest
// (all the smoke scenario needs); Patch and ChaosMesh injection arrive with
// the scenario packs they serve.
func (r *Runner) injectFault(ctx context.Context) error {
	switch r.scn.Fault.Kind {
	case scenario.FaultManifest:
		r.out.f("    applying %s (%s) into namespace %q", r.scn.Fault.Ref, r.scn.Fault.Kind, r.scn.Incident.ScopeNamespaces[0])
		_, err := r.tc.Kubectl(ctx, "apply", "-n", r.scn.Incident.ScopeNamespaces[0], "-f", r.scn.FaultPath())
		return err
	default:
		return fmt.Errorf("fault kind %q is not implemented yet — the scenario-pack session (playbook 2.2) adds it alongside the packs that need it; smoke uses Manifest", r.scn.Fault.Kind)
	}
}

// fileIncident creates the synthetic Incident the scenario prescribes.
func (r *Runner) fileIncident(ctx context.Context, n int) (*praxisv1alpha1.Incident, error) {
	name := fmt.Sprintf("bench-%s-%s-r%d", r.scn.Name, time.Now().UTC().Format("20060102-150405"), n)
	inc := &praxisv1alpha1.Incident{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.scn.Incident.ScopeNamespaces[0],
			Labels:    map[string]string{"praxis.dev/bench-scenario": r.scn.Name},
		},
		Spec: praxisv1alpha1.IncidentSpec{
			// Manual: the harness files the incident the way an operator
			// would; nothing here pretends to be Alertmanager.
			Source:   praxisv1alpha1.IncidentSourceManual,
			Severity: r.scn.Incident.SeverityHint,
			Description: fmt.Sprintf("praxisbench scenario %q: synthetic incident filed by the benchmark harness after fault injection (%s %s)",
				r.scn.Name, r.scn.Fault.Kind, r.scn.Fault.Ref),
			Scope: praxisv1alpha1.IncidentScope{Namespaces: r.scn.Incident.ScopeNamespaces},
		},
	}
	if err := r.c.Create(ctx, inc); err != nil {
		return nil, fmt.Errorf("create Incident %s/%s: %w", inc.Namespace, inc.Name, err)
	}
	r.out.f("    filed Incident %s/%s (severity %s)", inc.Namespace, inc.Name, inc.Spec.Severity)
	return inc, nil
}

// awaitResponse polls until a RemediationPlan references the Incident, the
// restraint annotation appears, or the scenario timeout passes. The timeout
// is an observation, not an error.
func (r *Runner) awaitResponse(ctx context.Context, inc *praxisv1alpha1.Incident) (response, error) {
	deadline := r.scn.Timeout()
	r.out.f("    waiting up to %s for a RemediationPlan or the %s annotation on Incident %s/%s",
		deadline, praxisv1alpha1.AnnotationNoActionProposed, inc.Namespace, inc.Name)

	start := time.Now()
	lastProgress := start
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return response{}, ctx.Err()
		case <-ticker.C:
		}

		for _, ns := range r.scn.Incident.ScopeNamespaces {
			var plans praxisv1alpha1.RemediationPlanList
			if err := r.c.List(ctx, &plans, client.InNamespace(ns)); err != nil {
				return response{}, fmt.Errorf("list RemediationPlans in %q: %w", ns, err)
			}
			for i := range plans.Items {
				p := &plans.Items[i]
				if p.Spec.IncidentRef.Name != inc.Name {
					continue
				}
				if p.Spec.IncidentRef.UID != "" && p.Spec.IncidentRef.UID != inc.UID {
					continue
				}
				waited := time.Since(start)
				r.out.f("    RemediationPlan %s/%s appeared after %s", ns, p.Name, fmtDur(waited))
				return response{Kind: ResponsePlan, Detail: ns + "/" + p.Name, Waited: waited}, nil
			}
		}

		var cur praxisv1alpha1.Incident
		if err := r.c.Get(ctx, types.NamespacedName{Namespace: inc.Namespace, Name: inc.Name}, &cur); err != nil {
			return response{}, fmt.Errorf("refresh Incident %s/%s: %w", inc.Namespace, inc.Name, err)
		}
		if reason := cur.Annotations[praxisv1alpha1.AnnotationNoActionProposed]; reason != "" {
			waited := time.Since(start)
			r.out.f("    %s appeared after %s (reason: %s)", praxisv1alpha1.AnnotationNoActionProposed, fmtDur(waited), reason)
			return response{Kind: ResponseNoAction, Detail: reason, Waited: waited}, nil
		}

		if waited := time.Since(start); waited >= deadline {
			r.out.f("    timed out after %s: no RemediationPlan and no %s annotation.", fmtDur(waited), praxisv1alpha1.AnnotationNoActionProposed)
			if r.opts.Agent == AgentNone {
				r.out.f("    Expected with --agent none: nothing is wired to respond yet — the rule-based baseline arrives with the scorer session (playbook 2.3).")
			}
			return response{Kind: ResponseTimeout, Detail: "graceful timeout", Waited: waited}, nil
		}
		if time.Since(lastProgress) >= 30*time.Second {
			lastProgress = time.Now()
			r.out.f("    still waiting (%s of %s)…", fmtDur(time.Since(start)), deadline)
		}
	}
}

// teardown deletes the scope namespaces and waits until they are gone, so
// consecutive runs start from nothing.
func (r *Runner) teardown(ctx context.Context) error {
	nss := r.scn.Incident.ScopeNamespaces
	for _, ns := range nss {
		if err := kube.DeleteNamespace(ctx, r.c, ns); err != nil {
			return err
		}
	}
	for _, ns := range nss {
		if err := kube.WaitNamespaceGone(ctx, r.c, ns, 4*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

func replicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

func deploymentReady(d *appsv1.Deployment) bool {
	want := replicas(d)
	return d.Generation <= d.Status.ObservedGeneration &&
		d.Status.UpdatedReplicas == want &&
		d.Status.ReadyReplicas == want &&
		d.Status.AvailableReplicas == want
}
