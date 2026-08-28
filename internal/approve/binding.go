/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package approve

import (
	"fmt"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
	"github.com/akansh23-cloud/praxis/internal/hash"
)

// BoundTo computes the approval binding hash in the Phase-1 interim form of
// LLD §8:
//
//	"sha256:" + hex(sha256( bundleHash ‖ "\n" ‖ sha256hex(canonicalJSON(spec)) ))
//
// An approval names this value or it is void: change the evidence or the
// plan and the hash changes, so a yes given to one plan can never be
// replayed against another. The full LLD §8 form appends the diff-bytes
// hash and the policy bundle revision once those artifacts exist
// (Session 4.3); the LLD already records that upgrade.
//
// Both the controller entering AwaitingApproval and the approval handler
// call exactly this function — binding and verification cannot drift apart.
func BoundTo(evidenceBundleHash string, spec *praxisv1alpha1.RemediationPlanSpec) (string, error) {
	canonical, err := hash.CanonicalJSON(spec)
	if err != nil {
		return "", fmt.Errorf("canonicalizing plan spec: %w", err)
	}
	payload := evidenceBundleHash + "\n" + hash.SHA256Hex(canonical)
	return hash.SHA256Prefixed([]byte(payload)), nil
}
