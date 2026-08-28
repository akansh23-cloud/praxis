/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package hash

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// These tables pin the canonicalization against RFC 8785 itself: the number
// vectors, the string-escaping example and the UTF-16 sorting example come
// from the RFC (§3.2.2, §3.2.3, Appendix A), so the implementation is
// checked against an external authority, not against its own output.

func TestCanonicalizeJSONNumberFormatting(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"zero", "0", "0"},
		{"negative zero collapses to zero", "-0", "0"},
		{"integer", "1", "1"},
		{"float with integral value equals the integer", "1.0", "1"},
		{"trailing fraction zeros dropped", "4.50", "4.5"},
		{"negative fraction", "-1.5", "-1.5"},
		{"small exponent becomes plain decimal", "2e-3", "0.002"},
		{"uppercase exponent normalised", "1E30", "1e+30"},
		{"shortest round-trip representation", "333333333.33333329", "333333333.3333333"},
		{"long fraction collapses to exponent form", "0.000000000000000000000000001", "1e-27"},
		{"boundary: 1e-6 stays decimal", "0.000001", "0.000001"},
		{"boundary: 1e-7 goes exponential", "1e-7", "1e-7"},
		{"go-diverges: 1e-5 stays decimal in ES", "0.00001", "0.00001"},
		{"go-diverges: negative 1e-5", "-0.00001", "-0.00001"},
		{"go-diverges: multi-digit mantissa below 1e-4", "3.4e-6", "0.0000034"},
		{"smallest subnormal double", "5e-324", "5e-324"},
		{"largest double", "1.7976931348623157e+308", "1.7976931348623157e+308"},
		{"2^53 exact", "9007199254740992", "9007199254740992"},
		{"boundary: 1e20 stays decimal", "100000000000000000000", "100000000000000000000"},
		{"boundary: 1e21 goes exponential", "1e+21", "1e+21"},
		// JCS treats every JSON number as an IEEE-754 double (RFC 8785
		// §3.2.2.3), so integers beyond 2^53 round to the nearest double.
		// Pinned here so the property is a documented fact, not a surprise;
		// no Praxis-hashed content carries integers of that size.
		{"beyond 2^53 rounds per JCS", "9007199254740993", "9007199254740992"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeJSON([]byte(tt.input))
			if err != nil {
				t.Fatalf("CanonicalizeJSON(%q) error = %v, want nil", tt.input, err)
			}
			if string(got) != tt.want {
				t.Errorf("CanonicalizeJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatES6NumberSpecials(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  string
	}{
		{"negative zero", math.Copysign(0, -1), "0"},
		{"smallest subnormal", 5e-324, "5e-324"},
		{"largest double", math.MaxFloat64, "1.7976931348623157e+308"},
		{"exponent just below decimal range", 1e-7, "1e-7"},
		{"exponent at top of decimal range", 1e20, "100000000000000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatES6Number(tt.input)
			if err != nil {
				t.Fatalf("formatES6Number(%v) error = %v, want nil", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("formatES6Number(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := formatES6Number(f); err == nil {
			t.Errorf("formatES6Number(%v) error = nil, want an error", f)
		}
	}
}

func TestCanonicalizeJSONRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty input", ""},
		{"truncated object", `{"a":`},
		{"trailing data after first value", `{} {}`},
		{"bare NaN is not JSON", "NaN"},
		{"number overflows a double", "1e400"},
		{"negative overflow", "-1e309"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := CanonicalizeJSON([]byte(tt.input)); err == nil {
				t.Errorf("CanonicalizeJSON(%q) = %q with nil error, want an error", tt.input, got)
			}
		})
	}
}

func TestCanonicalizeJSONKeyOrderIndependence(t *testing.T) {
	permutations := []string{
		`{"b":2,"a":1,"c":{"z":true,"y":null}}`,
		`{"c":{"y":null,"z":true},"a":1,"b":2}`,
		"{\n  \"a\": 1,\n  \"c\": {\"z\": true, \"y\": null},\n  \"b\": 2\n}",
	}
	const want = `{"a":1,"b":2,"c":{"y":null,"z":true}}`
	for _, input := range permutations {
		got, err := CanonicalizeJSON([]byte(input))
		if err != nil {
			t.Fatalf("CanonicalizeJSON(%q) error = %v, want nil", input, err)
		}
		if string(got) != want {
			t.Errorf("CanonicalizeJSON(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalJSONFromGoValues(t *testing.T) {
	type inner struct {
		// Declared deliberately out of alphabetical order: canonical output
		// must not depend on Go struct field order.
		Zeta  int    `json:"zeta"`
		Alpha string `json:"alpha"`
	}
	type outer struct {
		B    inner `json:"b"`
		A    []int `json:"a"`
		Skip *int  `json:"skip,omitempty"`
	}

	tests := []struct {
		name  string
		input any
		want  string
	}{
		{
			name:  "struct fields sorted, arrays preserved, omitempty dropped",
			input: outer{B: inner{Zeta: 1, Alpha: "x"}, A: []int{3, 1, 2}},
			want:  `{"a":[3,1,2],"b":{"alpha":"x","zeta":1}}`,
		},
		{
			name:  "map and struct with equal content produce equal bytes",
			input: map[string]any{"a": []int{3, 1, 2}, "b": map[string]any{"alpha": "x", "zeta": 1}},
			want:  `{"a":[3,1,2],"b":{"alpha":"x","zeta":1}}`,
		},
		{
			name:  "html-sensitive characters stay literal, not escaped",
			input: map[string]string{"s": "<>&"},
			want:  `{"s":"<>&"}`,
		},
		{name: "top-level string", input: "hi", want: `"hi"`},
		{name: "top-level bool", input: true, want: "true"},
		{name: "top-level nil", input: nil, want: "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalJSON(tt.input)
			if err != nil {
				t.Fatalf("CanonicalJSON(%#v) error = %v, want nil", tt.input, err)
			}
			if string(got) != tt.want {
				t.Errorf("CanonicalJSON(%#v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	if _, err := CanonicalJSON(math.NaN()); err == nil {
		t.Errorf("CanonicalJSON(NaN) error = nil, want an error")
	}
	if _, err := CanonicalJSON(make(chan int)); err == nil {
		t.Errorf("CanonicalJSON(chan) error = nil, want an error")
	}
}

func TestCanonicalJSONStringEscaping(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			// RFC 8785 §3.2.2.2's worked example, decoded into a Go string:
			// U+20AC $ U+000F LF A ' B " \ \ " /
			name:  "rfc 8785 escape example",
			input: "\u20ac$\x0f\nA'B\"\\\\\"/",
			want:  "\"\u20ac$\\u000f\\nA'B\\\"\\\\\\\\\\\"/\"",
		},
		{
			name:  "named short escapes",
			input: "\b\f\t\r",
			want:  "\"\\b\\f\\t\\r\"",
		},
		{
			name:  "other control characters as lowercase \\u00xx",
			input: "\x01\x1f",
			want:  "\"\\u0001\\u001f\"",
		},
		{
			name:  "DEL and C1 controls stay literal like JSON.stringify",
			input: "\x7f\u0080",
			want:  "\"\x7f\u0080\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalJSON(tt.input)
			if err != nil {
				t.Fatalf("CanonicalJSON(%q) error = %v, want nil", tt.input, err)
			}
			if string(got) != tt.want {
				t.Errorf("CanonicalJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalJSONSortsKeysByUTF16CodeUnits(t *testing.T) {
	// RFC 8785 §3.2.3's sorting example. The emoji (U+1F600, a surrogate
	// pair starting 0xd83d in UTF-16) must sort BEFORE U+FB33 even though
	// its code point and UTF-8 bytes are larger — the case where UTF-16
	// order and byte order disagree.
	input := map[string]string{
		"\u20ac":     "Euro Sign",
		"\r":         "Carriage Return",
		"\ufb33":     "Hebrew Letter Dalet With Dagesh",
		"1":          "One",
		"\U0001f600": "Emoji: Grinning Face",
		"\u0080":     "Control",
		"\u00f6":     "Latin Small Letter O With Diaeresis",
	}
	want := "{\"\\r\":\"Carriage Return\"," +
		"\"1\":\"One\"," +
		"\"\u0080\":\"Control\"," +
		"\"\u00f6\":\"Latin Small Letter O With Diaeresis\"," +
		"\"\u20ac\":\"Euro Sign\"," +
		"\"\U0001f600\":\"Emoji: Grinning Face\"," +
		"\"\ufb33\":\"Hebrew Letter Dalet With Dagesh\"}"

	got, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v, want nil", err)
	}
	if string(got) != want {
		t.Errorf("CanonicalJSON() = %q, want %q", got, want)
	}
}

func TestCanonicalizeJSONFullRFCExample(t *testing.T) {
	// RFC 8785 Appendix A: input document and its canonical form, verbatim.
	// The backquoted input carries the RFC's escape sequences literally,
	// exactly as they appear on the wire.
	input := `{
  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
  "literals": [null, true, false]
}`
	want := `{"literals":[null,true,false],` +
		`"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],` +
		`"string":"` + "\u20ac" + `$\u000f\nA'B\"\\\\\"/"}`

	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeJSON() error = %v, want nil", err)
	}
	if string(got) != want {
		t.Errorf("CanonicalizeJSON() = %q, want %q", got, want)
	}
}

func TestCanonicalizeJSONIsIdempotent(t *testing.T) {
	inputs := []string{
		`{"b": 2.0, "a": [1e-7, "x", {"k": null}], "s": "\u20ac"}`,
		`{"numbers":[333333333.33333329,1E30],"nested":{"z":true,"y":[[]]}}`,
	}
	for _, input := range inputs {
		once, err := CanonicalizeJSON([]byte(input))
		if err != nil {
			t.Fatalf("CanonicalizeJSON(%q) error = %v, want nil", input, err)
		}
		twice, err := CanonicalizeJSON(once)
		if err != nil {
			t.Fatalf("CanonicalizeJSON(canonical) error = %v, want nil", err)
		}
		if !bytes.Equal(once, twice) {
			t.Errorf("canonicalization is not idempotent: %q != %q", once, twice)
		}
	}
}

func TestCanonicalizeJSONHasNoInsignificantWhitespace(t *testing.T) {
	input := "{\n\t\"a\" : [ 1 , 2 ] ,\r\n \"b\" : { \"c\" : \"a string with spaces\" } }"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeJSON() error = %v, want nil", err)
	}
	want := `{"a":[1,2],"b":{"c":"a string with spaces"}}`
	if string(got) != want {
		t.Errorf("CanonicalizeJSON() = %q, want %q", got, want)
	}
	if strings.ContainsAny(strings.ReplaceAll(string(got), "a string with spaces", ""), " \t\n\r") {
		t.Errorf("canonical output contains whitespace outside string values: %q", got)
	}
}
