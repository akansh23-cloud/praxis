/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"regexp"
	"strings"
)

// The credential scrubber of LLD §6 (FR-P3-02), applied by the assembler
// to EVERY data value of EVERY item before canonical bytes exist — log
// templates and exemplars, but equally Event messages, change-cause
// annotations, metric output and error strings, because a credential can
// ride in on any allowed telemetry channel (ADR-008).
//
// This is defense in depth over telemetry the collector is allowed to
// read. The primary guarantee is structural and lives elsewhere: Secrets
// are unreachable by construction (reader.go, rbac_test.go). Regexes
// cannot recognise every secret — a random API key with dashes in it, a
// password that looks like a word — and nothing here claims they can; see
// ADR-008 for the known false-negative and false-positive classes.
//
// Properties every rule keeps:
//
//   - replacement is removal: the marker names the KIND of what was found
//     and carries nothing derived from the value — no hash, no prefix, no
//     length — so a marker can never be reversed into the secret;
//   - idempotent and deterministic: rules run in a fixed order, no marker
//     matches any rule (every value class excludes « and »), so
//     Scrub(Scrub(x)) == Scrub(x);
//   - the original value never appears in a log line or an error.

// RedactionMarker renders the LLD §6 replacement for one credential kind.
func RedactionMarker(kind string) string {
	return "«redacted:" + kind + "»"
}

// The kinds this scrubber emits.
const (
	RedactedPEMBlock             = "pem-block"
	RedactedURLCredentials       = "url-credentials"
	RedactedAWSAccessKey         = "aws-access-key"
	RedactedJWT                  = "jwt"
	RedactedBearerToken          = "bearer-token"
	RedactedOpaqueToken          = "opaque-token"
	RedactedCredentialAssignment = "credential-assignment"
)

var (
	// pemBlock: BEGIN … END of any PEM label; a BEGIN with no END in the
	// same value (a truncated dump) redacts everything after it, which is
	// the safe reading of an unterminated key.
	pemBlock = regexp.MustCompile(`-----BEGIN [A-Z ]+-----(?:[\s\S]*?-----END [A-Z ]+-----|[\s\S]*)`)

	// urlCredentials: the user:password@ authority of any URL. The user
	// name goes too — it is often an access-key id.
	urlCredentials = regexp.MustCompile(`://[^/\s:@«»"']+:[^/\s@«»"']+@`)

	// awsAccessKey: the documented AWS access-key-id prefixes plus 16
	// upper-case alphanumerics.
	awsAccessKey = regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}\b`)

	// jwt: three base64url segments, the first always starting with eyJ
	// (a JSON object header).
	jwt = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)

	// bearer: "Bearer <token>" in any case; the token must be 16+ token
	// characters AND carry a digit, so "Bearer authentication" in prose
	// is not a token.
	bearer = regexp.MustCompile(`(?i)\b(bearer)(\s+)([A-Za-z0-9\-._~+/]{16,}=*)`)

	// opaqueToken: a run of 40+ base64 characters. Only runs that mix
	// upper case, lower case and digits count (hex digests, DNS names and
	// pod names never do), and — unless the run IS the whole value — at
	// most two slashes, so URL paths are not mistaken for base64.
	opaqueToken = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)

	// credentialAssignment: KEY=value where the key ends in a credential
	// word; the key survives, the value (quoted or bare) does not.
	credentialAssignment = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|access[_-]?key)=(?:"[^"«»]*"|'[^'«»]*'|[^\s"'«»,;]+)`)
)

// scrubRules run in this fixed order; earlier rules claim their text
// first, which is what makes the kind a JWT behind "Bearer" reports
// deterministic.
var scrubRules = []func(string) string{
	func(s string) string { return pemBlock.ReplaceAllString(s, RedactionMarker(RedactedPEMBlock)) },
	func(s string) string {
		return urlCredentials.ReplaceAllString(s, "://"+RedactionMarker(RedactedURLCredentials)+"@")
	},
	func(s string) string { return awsAccessKey.ReplaceAllString(s, RedactionMarker(RedactedAWSAccessKey)) },
	func(s string) string { return jwt.ReplaceAllString(s, RedactionMarker(RedactedJWT)) },
	scrubBearer,
	scrubOpaque,
	func(s string) string {
		return credentialAssignment.ReplaceAllString(s, "${1}="+RedactionMarker(RedactedCredentialAssignment))
	},
}

// Scrub returns text with every credential-shaped substring replaced by
// its «redacted:<kind>» marker, and whether anything was replaced.
func Scrub(text string) (string, bool) {
	out := text
	for _, rule := range scrubRules {
		out = rule(out)
	}
	return out, out != text
}

func scrubBearer(s string) string {
	return bearer.ReplaceAllStringFunc(s, func(m string) string {
		sub := bearer.FindStringSubmatch(m)
		if !hasDigit(sub[3]) {
			return m
		}
		return sub[1] + sub[2] + RedactionMarker(RedactedBearerToken)
	})
}

func scrubOpaque(s string) string {
	locs := opaqueToken.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	trimmed := strings.TrimSpace(s)
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		run := s[loc[0]:loc[1]]
		whole := run == trimmed
		if !looksOpaque(run) || (!whole && strings.Count(run, "/") > 2) {
			continue
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(RedactionMarker(RedactedOpaqueToken))
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// looksOpaque: upper case, lower case and a digit all present.
func looksOpaque(run string) bool {
	return hasDigit(run) &&
		strings.ContainsAny(run, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(run, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, "0123456789")
}
