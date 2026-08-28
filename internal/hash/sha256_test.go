/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package hash

import (
	"regexp"
	"testing"
)

// Vectors are the published FIPS 180-4 SHA-256 test values, so the helpers
// are checked against an external authority.

func TestSHA256Hex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:  "abc",
			input: "abc",
			want:  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
		{
			name:  "two-block message",
			input: "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
			want:  "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SHA256Hex([]byte(tt.input)); got != tt.want {
				t.Errorf("SHA256Hex(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSHA256Prefixed(t *testing.T) {
	got := SHA256Prefixed([]byte("abc"))
	want := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("SHA256Prefixed(\"abc\") = %q, want %q", got, want)
	}

	// The rendering must satisfy the CRD pattern for hash-valued fields
	// (api/v1alpha1: ^sha256:[a-f0-9]{64}$), or a computed hash could not
	// even be stored on an object.
	pattern := regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	if !pattern.MatchString(got) {
		t.Errorf("SHA256Prefixed output %q does not match the API hash pattern", got)
	}
}
