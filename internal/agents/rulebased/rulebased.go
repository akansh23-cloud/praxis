/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package rulebased

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/agents"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// Agent is the rule-based baseline. It is stateless and pure: identical
// (incident, bundle) inputs always produce identical output.
type Agent struct{}

// New returns the baseline agent.
func New() *Agent { return &Agent{} }

// maxCitations bounds how many matching events one hypothesis cites; the
// CRD allows 64, the baseline keeps its evidence list short and readable.
const maxCitations = 16

// maxMessageInSummary bounds how much of a kubelet message is echoed into
// the hypothesis summary (CRD cap: 1024 for the whole summary).
const maxMessageInSummary = 200

// verb names what a rule proposes.
type verb int

const (
	verbRollback verb = iota
	verbRestart
)

// rule is one deterministic (Event reason, involved kind) pattern. Rules
// are evaluated strictly in table order; the first rule any event matches
// decides the whole response. The optional message substring
// disambiguates kubelet reasons that cover several conditions (BackOff is
// both "restarting failed container" and "pulling image").
type rule struct {
	name            string
	reason          string
	involvedKind    string
	messageContains string // lowercase; empty matches any message
	action          verb
	confidence      int32
	// summary renders the fixed hypothesis text. It is a template over
	// what the rule actually saw — never over anything it could not.
	summary func(pod, deployment, message string) string
}

// kubelet event vocabulary the rules key on.
const (
	kindPod         = "Pod"
	reasonFailed    = "Failed"
	reasonUnhealthy = "Unhealthy"
	reasonBackOff   = "BackOff"
)

// rules is the entire intelligence of this agent. Three failure patterns
// and their reflex responses, transcribed from the laziest page of a
// runbook. That poverty is deliberate — see the package documentation.
var rules = []rule{
	{
		name: "image-pull-failure", reason: reasonFailed, involvedKind: kindPod,
		messageContains: "pull",
		action:          verbRollback, confidence: 50,
		summary: func(pod, deployment, message string) string {
			return fmt.Sprintf("image pull failure on pod %s of deployment %s (%s); rolling back the release", pod, deployment, message)
		},
	},
	{
		name: "readiness-probe-failure", reason: reasonUnhealthy, involvedKind: kindPod,
		messageContains: "readiness probe failed",
		action:          verbRollback, confidence: 50,
		summary: func(pod, deployment, message string) string {
			return fmt.Sprintf("pod %s of deployment %s is failing its readiness probe (%s); rolling back the release", pod, deployment, message)
		},
	},
	{
		name: "crashloop-backoff", reason: reasonBackOff, involvedKind: kindPod,
		messageContains: "restarting",
		action:          verbRestart, confidence: 40,
		summary: func(pod, deployment, message string) string {
			return fmt.Sprintf("pod %s of deployment %s is crash-looping (%s); restarting the workload", pod, deployment, message)
		},
	},
}

// The two no-action patterns. An empty rule table hit is as deterministic
// as a rule hit, and it exercises the restraint path
// (praxis.dev/no-action-proposed) as FR-P2-04's floor must.
const (
	noActionNoEvents = "rule-based baseline: no warning events observed in the incident scope; proposing no action"
	noActionNoMatch  = "rule-based baseline: observed warning events match no known failure pattern; proposing no action"
)

// match is what one firing rule saw: the rule, the first matching event,
// and the ids of every matching event (the citations).
type match struct {
	rule       *rule
	pod        string
	namespace  string
	message    string
	deployment string
	citations  []praxisv1alpha1.EvidenceID
}

// Analyze scans the bundle's Event items against the rule table and
// returns at most one hypothesis — this agent has exactly one idea at a
// time, so top-3 buys it nothing over top-1.
func (a *Agent) Analyze(_ context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle) (agents.Hypotheses, error) {
	if err := checkInputs(incident, bundle); err != nil {
		return nil, err
	}
	m := evaluate(bundle)
	if m == nil {
		return nil, nil
	}
	return agents.Hypotheses{a.hypothesis(m)}, nil
}

// Plan maps the matched rule to its fixed action, or proposes no action
// when nothing matched. The spec is fully deterministic: same bundle, same
// plan, byte for byte (modulo the bundle's own hash).
func (a *Agent) Plan(_ context.Context, incident *praxisv1alpha1.Incident, bundle *evidence.Bundle,
	hypotheses agents.Hypotheses,
) (*praxisv1alpha1.RemediationPlanSpec, agents.NoAction, error) {
	if err := checkInputs(incident, bundle); err != nil {
		return nil, agents.NoAction{}, err
	}
	m := evaluate(bundle)
	if m == nil {
		reason := noActionNoMatch
		if !hasEventItems(bundle) {
			reason = noActionNoEvents
		}
		return nil, agents.NoAction{Proposed: true, Reason: reason}, nil
	}

	bundleHash, err := bundle.Hash()
	if err != nil {
		return nil, agents.NoAction{}, err
	}
	hypothesis := a.hypothesis(m)
	if len(hypotheses) > 0 {
		// Trust the analysis the caller carried over; it is this agent's
		// own (identical) output on honest call sequences.
		hypothesis = hypotheses[0]
	}

	action := praxisv1alpha1.Action{
		Type: praxisv1alpha1.ActionRollbackRelease,
		Target: praxisv1alpha1.TargetRef{
			Kind:      "Deployment",
			Namespace: m.namespace,
			Name:      m.deployment,
		},
	}
	if m.rule.action == verbRestart {
		action.Type = praxisv1alpha1.ActionRestartWorkload
	}

	return &praxisv1alpha1.RemediationPlanSpec{
		IncidentRef: praxisv1alpha1.IncidentRef{
			Name: incident.Name,
			UID:  incident.UID,
		},
		EvidenceBundleHash: bundleHash,
		Hypothesis:         hypothesis,
		Actions:            []praxisv1alpha1.Action{action},
		Verification: praxisv1alpha1.VerificationSpec{
			// One fixed template: "the deployment I touched has an
			// available replica". Deliberately weak — a real agent should
			// propose a predicate tied to the actual failure.
			Predicate: fmt.Sprintf(
				`kube_deployment_status_replicas_available{namespace=%q,deployment=%q} >= 1`,
				m.namespace, m.deployment),
			Window:    metav1.Duration{Duration: 2 * time.Minute},
			OnFailure: praxisv1alpha1.FailureActionEscalate,
		},
		Rollback: praxisv1alpha1.RollbackSpec{Strategy: praxisv1alpha1.RollbackNone},
	}, agents.NoAction{}, nil
}

func checkInputs(incident *praxisv1alpha1.Incident, bundle *evidence.Bundle) error {
	if incident == nil {
		return fmt.Errorf("rulebased: incident is required")
	}
	if bundle == nil {
		return fmt.Errorf("rulebased: evidence bundle is required (it may be empty, not absent)")
	}
	return nil
}

// evaluate applies the rule table: for each rule in order, scan the
// bundle's items in order; the first rule with at least one matching event
// wins and every event matching it becomes a citation.
func evaluate(bundle *evidence.Bundle) *match {
	for i := range rules {
		r := &rules[i]
		var m *match
		for j := range bundle.Items {
			item := &bundle.Items[j]
			if !ruleMatches(r, item) {
				continue
			}
			if m == nil {
				pod := item.Data[evidence.EventDataInvolvedName]
				m = &match{
					rule:       r,
					pod:        pod,
					namespace:  item.Data[evidence.EventDataInvolvedNamespace],
					message:    item.Data[evidence.EventDataMessage],
					deployment: deploymentFor(pod),
				}
			}
			if len(m.citations) < maxCitations {
				m.citations = append(m.citations, praxisv1alpha1.EvidenceID(item.ID))
			}
		}
		if m != nil {
			return m
		}
	}
	return nil
}

func ruleMatches(r *rule, item *evidence.Item) bool {
	if item.Type != evidence.ItemTypeEvent {
		return false
	}
	return item.Data[evidence.EventDataReason] == r.reason &&
		item.Data[evidence.EventDataInvolvedKind] == r.involvedKind &&
		(r.messageContains == "" ||
			strings.Contains(strings.ToLower(item.Data[evidence.EventDataMessage]), r.messageContains))
}

func hasEventItems(bundle *evidence.Bundle) bool {
	for i := range bundle.Items {
		if bundle.Items[i].Type == evidence.ItemTypeEvent {
			return true
		}
	}
	return false
}

func (a *Agent) hypothesis(m *match) praxisv1alpha1.Hypothesis {
	msg := m.message
	if len(msg) > maxMessageInSummary {
		cut := maxMessageInSummary
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg = msg[:cut] + "…"
	}
	return praxisv1alpha1.Hypothesis{
		Summary:           m.rule.summary(m.pod, m.deployment, msg),
		ConfidencePercent: m.rule.confidence,
		Citations:         m.citations,
	}
}

// deploymentFor guesses the Deployment behind a pod by dropping the two
// generated suffixes of the `<deployment>-<replicaset-hash>-<random>`
// convention. String surgery instead of owner-chain evidence is part of
// the documented dumbness: a pod named any other way yields a wrong
// target, and the benchmark scores it accordingly.
func deploymentFor(podName string) string {
	parts := strings.Split(podName, "-")
	if len(parts) <= 2 {
		return podName
	}
	return strings.Join(parts[:len(parts)-2], "-")
}
