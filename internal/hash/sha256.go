/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package hash

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256Hex returns the lowercase hex encoding of the SHA-256 digest of
// data — the bare form the LLD composes into larger hashes (§8).
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256Prefixed returns the digest in the "sha256:<hex>" rendering the API
// stores and compares everywhere a hash is a field value (evidenceBundleHash,
// status.approval.boundTo).
func SHA256Prefixed(data []byte) string {
	return "sha256:" + SHA256Hex(data)
}
