/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package validate

import (
	"context"
	"fmt"
	"strings"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/evidence"
)

// The real citation validator (playbook Session 3.3 task 4; FR-P3-04):
// deterministic set membership of every cited id in the exact bundle
// analyzed. Nothing about an id's SHAPE earns trust — "ev/podstatus-99"
// is refused exactly like "ev/nonsense-01" when the bundle holds no such
// item — and nothing is repaired: an unresolved citation fails the whole
// hypothesis rather than being dropped.
//
// The same function guards both ends of the pipeline: the LLM agent
// refuses to emit a plan whose hypotheses do not resolve, the benchmark
// harness re-checks whatever any agent returns, and the controller checks
// every persisted plan against the stored bundle.

// UnresolvedCitations returns the citations of h that name no item of the
// bundle, in citation order, each reported once. A nil bundle resolves
// nothing; a hypothesis with no citations has nothing to resolve and is
// reported as unresolved by the validator (a claim must cite evidence).
func UnresolvedCitations(h praxisv1alpha1.Hypothesis, bundle *evidence.Bundle) []praxisv1alpha1.EvidenceID {
	ids := map[praxisv1alpha1.EvidenceID]bool{}
	if bundle != nil {
		for i := range bundle.Items {
			ids[praxisv1alpha1.EvidenceID(bundle.Items[i].ID)] = true
		}
	}
	var unresolved []praxisv1alpha1.EvidenceID
	seen := map[praxisv1alpha1.EvidenceID]bool{}
	for _, c := range h.Citations {
		if ids[c] || seen[c] {
			continue
		}
		seen[c] = true
		unresolved = append(unresolved, c)
	}
	return unresolved
}

// BundleCitationValidator implements CitationValidator over a bundle.
type BundleCitationValidator struct{}

var _ CitationValidator = BundleCitationValidator{}

// Validate implements CitationValidator. It never returns an error: a
// missing bundle or an unresolved citation is a verdict on the plan, not
// machinery trouble.
func (BundleCitationValidator) Validate(
	_ context.Context, hypothesis praxisv1alpha1.Hypothesis, bundle *evidence.Bundle,
) (Verdict, error) {
	if bundle == nil {
		return Verdict{
			Passed:  false,
			Reason:  praxisv1alpha1.ReasonCitationInvalid,
			Message: "No persisted evidence bundle exists to resolve the hypothesis's citations against",
		}, nil
	}
	if len(hypothesis.Citations) == 0 {
		return Verdict{
			Passed:  false,
			Reason:  praxisv1alpha1.ReasonCitationInvalid,
			Message: "The hypothesis cites no evidence",
		}, nil
	}
	unresolved := UnresolvedCitations(hypothesis, bundle)
	if len(unresolved) > 0 {
		return Verdict{
			Passed:  false,
			Reason:  praxisv1alpha1.ReasonCitationInvalid,
			Message: FormatUnresolved(unresolved, len(hypothesis.Citations), len(bundle.Items)),
		}, nil
	}
	return Verdict{
		Passed:  true,
		Reason:  praxisv1alpha1.ReasonCitationsResolved,
		Message: fmt.Sprintf("All %d citation(s) resolve to items of the %d-item evidence bundle", len(hypothesis.Citations), len(bundle.Items)),
	}, nil
}

// FormatUnresolved renders the verdict message for unresolved citations:
// the ids that failed, verbatim, are exactly what a human needs to see.
func FormatUnresolved(unresolved []praxisv1alpha1.EvidenceID, cited, items int) string {
	names := make([]string, 0, len(unresolved))
	for _, id := range unresolved {
		names = append(names, string(id))
	}
	return fmt.Sprintf("%d of %d citation(s) do not resolve to any item of the %d-item evidence bundle: %s",
		len(unresolved), cited, items, strings.Join(names, ", "))
}
