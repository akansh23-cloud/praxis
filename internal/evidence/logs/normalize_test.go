/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestNormalize pins line hygiene: terminal control sequences and control
// characters are data-free noise that would otherwise become template
// tokens, so they are removed before templating; lines are trimmed and
// bounded. Nothing here interprets a line — a control sequence is dropped,
// never acted upon.
func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain line untouched", lineStarted, lineStarted},
		{"ansi colour stripped", "\x1b[31merror\x1b[0m: dial failed", "error: dial failed"},
		{"ansi cursor sequence stripped", "\x1b[2K\x1b[1Gprogress 50%", "progress 50%"},
		{"tab becomes a space", "key\tvalue", "key value"},
		{"control characters removed", "\x00\x07bell\x08 rung\x7f", "bell rung"},
		{"surrounding whitespace trimmed", "   padded   \r\n", "padded"},
		{"empty stays empty", "", ""},
		{"invalid utf8 replaced", "bad\xffbyte", "bad�byte"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeBoundsLength: an overlong line is cut at MaxLineBytes on a
// rune boundary with the cut marked, so a template or exemplar can never
// exceed the bound and a multi-byte rune is never split.
func TestNormalizeBoundsLength(t *testing.T) {
	long := strings.Repeat("é", MaxLineBytes) // 2 bytes per rune: 2 KiB
	got := Normalize(long)
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a rune")
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated line does not carry the … marker: %q", got[len(got)-8:])
	}
	if n := len(got); n > MaxLineBytes+len("…") {
		t.Errorf("normalized line is %d bytes, bound is %d plus the marker", n, MaxLineBytes)
	}
	exact := strings.Repeat("a", MaxLineBytes)
	if got := Normalize(exact); got != exact {
		t.Error("a line exactly at the bound must not be truncated")
	}
}
