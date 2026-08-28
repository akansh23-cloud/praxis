/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package hash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// CanonicalJSON marshals v with encoding/json (honouring struct tags and
// custom marshalers) and returns the RFC 8785 canonical form of the result.
// Equal logical content yields equal bytes regardless of struct field order,
// map iteration order, or how numbers were spelled.
func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling to JSON: %w", err)
	}
	return CanonicalizeJSON(raw)
}

// CanonicalizeJSON re-encodes a single JSON document into its RFC 8785
// canonical form: object keys sorted by UTF-16 code units, no insignificant
// whitespace, minimal string escaping, numbers rendered as ECMAScript
// renders IEEE-754 doubles.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the first JSON value")
	}
	return appendCanonical(nil, v)
}

func appendCanonical(buf []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(buf, "null"...), nil
	case bool:
		return strconv.AppendBool(buf, x), nil
	case string:
		return appendCanonicalString(buf, x), nil
	case json.Number:
		return appendCanonicalNumber(buf, x)
	case []any:
		buf = append(buf, '[')
		for i, elem := range x {
			if i > 0 {
				buf = append(buf, ',')
			}
			var err error
			if buf, err = appendCanonical(buf, elem); err != nil {
				return nil, err
			}
		}
		return append(buf, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, compareUTF16)
		buf = append(buf, '{')
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCanonicalString(buf, k)
			buf = append(buf, ':')
			var err error
			if buf, err = appendCanonical(buf, x[k]); err != nil {
				return nil, err
			}
		}
		return append(buf, '}'), nil
	default:
		// Unreachable: json.Decoder produces only the types above.
		return nil, fmt.Errorf("cannot canonicalize value of type %T", v)
	}
}

// compareUTF16 orders strings by their UTF-16 code units, the RFC 8785
// §3.2.3 property-sorting rule. It differs from Go's native byte order only
// for supplementary characters: their surrogate pairs (0xd800–0xdfff) sort
// below BMP characters in 0xe000–0xffff.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// appendCanonicalString serializes s the way ECMAScript's JSON.stringify
// does (RFC 8785 §3.2.2.2): the two mandatory escapes and the C0 controls
// only, with the named short forms where they exist and lowercase \u00xx
// otherwise. Everything else — including HTML-sensitive characters and
// non-ASCII — is emitted literally as UTF-8.
func appendCanonicalString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for _, r := range s {
		switch r {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\b':
			buf = append(buf, '\\', 'b')
		case '\f':
			buf = append(buf, '\\', 'f')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if r < 0x20 {
				buf = fmt.Appendf(buf, `\u%04x`, r)
			} else {
				buf = utf8.AppendRune(buf, r)
			}
		}
	}
	return append(buf, '"')
}

func appendCanonicalNumber(buf []byte, n json.Number) ([]byte, error) {
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return nil, fmt.Errorf("number %q does not fit an IEEE-754 double: %w", n, err)
	}
	text, err := formatES6Number(f)
	if err != nil {
		return nil, err
	}
	return append(buf, text...), nil
}

// formatES6Number renders f exactly as ECMAScript's Number::toString(10)
// (RFC 8785 §3.2.2.3): shortest round-trip digits, plain decimal notation
// while the decimal point position n satisfies -6 < n ≤ 21, exponent
// notation with an unpadded sign-carrying exponent outside that range, and
// "0" for negative zero. Go's own 'g' verb draws the decimal/exponent line
// elsewhere, so the choice is made here from the ES rules directly.
func formatES6Number(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("value %v is not representable in JSON", f)
	}
	if f == 0 {
		// Covers negative zero, which ECMAScript renders as "0".
		return "0", nil
	}

	// The 'e' form is uniform ("d.dddde±xx"), so the shortest digits and
	// the decimal exponent can be read off without notation heuristics.
	text := strconv.FormatFloat(f, 'e', -1, 64)
	eIdx := strings.IndexByte(text, 'e')
	mantissa := text[:eIdx]
	exp, err := strconv.Atoi(text[eIdx+1:])
	if err != nil {
		return "", fmt.Errorf("parsing exponent of %q: %w", text, err)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	digits := strings.Replace(mantissa, ".", "", 1)

	// ECMAScript Number::toString(10) with k shortest digits and decimal
	// point position n, i.e. value = 0.<digits> × 10^n.
	n, k := exp+1, len(digits)
	switch {
	case n >= k && n <= 21:
		return sign + digits + strings.Repeat("0", n-k), nil
	case n > 0 && n <= 21:
		return sign + digits[:n] + "." + digits[n:], nil
	case n > -6 && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits, nil
	}

	es6Exp := n - 1
	expSign := "+"
	if es6Exp < 0 {
		expSign, es6Exp = "-", -es6Exp
	}
	if k > 1 {
		return sign + digits[:1] + "." + digits[1:] + "e" + expSign + strconv.Itoa(es6Exp), nil
	}
	return sign + digits + "e" + expSign + strconv.Itoa(es6Exp), nil
}
