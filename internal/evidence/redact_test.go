/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Planted credentials. Every value is obviously fake — the AWS examples
// are the ones AWS's own documentation uses, the JWT is built here from
// a throwaway header/payload/signature — but each has the exact shape
// the scrubber must catch. They are the secret fixtures of the whole
// package: the adversarial tests plant these same strings in every
// telemetry channel and assert their absence from complete bundle bytes.
const (
	plantedAWSKey     = "AKIAIOSFODNN7EXAMPLE"
	plantedAWSSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	plantedBearer     = "fake-bearer-token-0123456789abcdefFAKE"
	plantedURLUser    = "shopadmin"
	plantedURLPass    = "hunter2-fake-pass"
	plantedPEMBody    = "MIIEfakefakefakeFAKE0123456789fakefakefakeFAKEfakefakefakeFAKE1234"
	plantedAssignment = "s3cr3t-assignment-value-FAKE"
)

var plantedJWT = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
	base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"praxis-test","iat":1757699140}`)) + "." +
	base64.RawURLEncoding.EncodeToString([]byte("fake-signature-bytes-FAKE"))

var plantedURL = "postgres://" + plantedURLUser + ":" + plantedURLPass + "@db.shop.svc:5432/orders"

var plantedPEM = "-----BEGIN RSA PRIVATE KEY-----\n" + plantedPEMBody + "\n" + plantedPEMBody + "\n-----END RSA PRIVATE KEY-----"

// plantedSecrets are the exact substrings that must never survive
// scrubbing, keyed by the symbolic name failure messages use — the
// values themselves are never printed by any assertion helper.
var plantedSecrets = map[string]string{
	"aws-access-key":     plantedAWSKey,
	"aws-secret-key":     plantedAWSSecret,
	"bearer-token":       plantedBearer,
	"jwt":                plantedJWT,
	"url-password":       plantedURLPass,
	"url-user-pass":      plantedURLUser + ":" + plantedURLPass,
	"pem-body":           plantedPEMBody,
	"assignment-value":   plantedAssignment,
	"aws-key-first-half": plantedAWSKey[:10],
}

// fingerprint identifies a planted value in a failure message without
// reproducing it.
func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("len=%d sha256=%x…", len(value), sum[:4])
}

// assertPlantedAbsent fails, naming the fixture symbolically, if any
// planted secret survives anywhere in blob. It never prints blob or the
// secret.
func assertPlantedAbsent(t *testing.T, where, blob string) {
	t.Helper()
	for name, secret := range plantedSecrets {
		if strings.Contains(blob, secret) {
			t.Errorf("planted %s (%s) survives in %s", name, fingerprint(secret), where)
		}
	}
}

var markerPattern = regexp.MustCompile(`«redacted:[a-z-]+»`)

var markerPEM = RedactionMarker(RedactedPEMBlock)

// TestScrubClasses is the class table: every required LLD §6 class plus
// the two defense-in-depth classes, each replaced by its marker with the
// surrounding text intact.
func TestScrubClasses(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "aws access key",
			in:   "configured client with key " + plantedAWSKey + " in region eu-west-1",
			want: "configured client with key «redacted:aws-access-key» in region eu-west-1",
		},
		{
			name: "aws access key inside assignment",
			in:   "AWS_ACCESS_KEY_ID=" + plantedAWSKey,
			want: "AWS_ACCESS_KEY_ID=«redacted:aws-access-key»",
		},
		{
			name: "bearer token header",
			in:   "Authorization: Bearer " + plantedBearer + " rejected",
			want: "Authorization: Bearer «redacted:bearer-token» rejected",
		},
		{
			name: "bearer is case-insensitive",
			in:   "authorization: bearer " + plantedBearer,
			want: "authorization: bearer «redacted:bearer-token»",
		},
		{
			name: "jwt anywhere",
			in:   "session token " + plantedJWT + " expired",
			want: "session token «redacted:jwt» expired",
		},
		{
			name: "jwt behind bearer keeps its kind",
			in:   "Authorization: Bearer " + plantedJWT,
			want: "Authorization: Bearer «redacted:jwt»",
		},
		{
			name: "url credentials",
			in:   "dial " + plantedURL + ": connection refused",
			want: "dial postgres://«redacted:url-credentials»@db.shop.svc:5432/orders: connection refused",
		},
		{
			name: "url without credentials untouched",
			in:   "GET http://checkout-api.shop.svc/healthz",
			want: "GET http://checkout-api.shop.svc/healthz",
		},
		{
			name: "pem block with newlines",
			in:   "loaded key:\n" + plantedPEM + "\nok",
			want: "loaded key:\n«redacted:pem-block»\nok",
		},
		{
			name: "pem block with escaped newlines on one line",
			in:   `key="-----BEGIN EC PRIVATE KEY-----\n` + plantedPEMBody + `\n-----END EC PRIVATE KEY-----"`,
			want: `key="«redacted:pem-block»"`,
		},
		{
			name: "unterminated pem block redacts to the end",
			in:   "-----BEGIN PRIVATE KEY----- " + plantedPEMBody + " (truncated",
			want: markerPEM,
		},
		{
			name: "credential assignment keeps the key",
			in:   "kubectl set env deploy/checkout-api DB_PASSWORD=" + plantedAssignment + " --record",
			want: "kubectl set env deploy/checkout-api DB_PASSWORD=«redacted:credential-assignment» --record",
		},
		{
			name: "quoted credential assignment",
			in:   `api_key="` + plantedAssignment + `" token='` + plantedAssignment + `'`,
			want: `api_key=«redacted:credential-assignment» token=«redacted:credential-assignment»`,
		},
		{
			name: "long opaque mixed-case token",
			in:   "secret material " + plantedAWSSecret + " leaked",
			want: "secret material «redacted:opaque-token» leaked",
		},
		{
			name: "long lowercase hex is not opaque",
			in:   "image sha256:1f2e3d4c5b6a79887766554433221100ffeeddccbbaa99887766554433221100",
			want: "image sha256:1f2e3d4c5b6a79887766554433221100ffeeddccbbaa99887766554433221100",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := Scrub(tc.in)
			if got != tc.want {
				t.Errorf("Scrub(%s):\n got %q\nwant %q", tc.name, got, tc.want)
			}
			if changed != (tc.in != tc.want) {
				t.Errorf("changed = %v, want %v", changed, tc.in != tc.want)
			}
			assertPlantedAbsent(t, "scrubbed text of case "+tc.name, got)
		})
	}
}

// TestScrubMultipleCredentialsInOneInput: several classes in one string
// are all caught, in one pass, each with its own marker.
func TestScrubMultipleCredentialsInOneInput(t *testing.T) {
	in := "export AWS_ACCESS_KEY_ID=" + plantedAWSKey + " AWS_SECRET_ACCESS_KEY=" + plantedAWSSecret +
		" DATABASE_URL=" + plantedURL + " && curl -H 'Authorization: Bearer " + plantedJWT + "'" +
		" && echo '" + plantedPEM + "' > key.pem && PASSWORD=" + plantedAssignment
	got, changed := Scrub(in)
	if !changed {
		t.Fatal("nothing was scrubbed")
	}
	assertPlantedAbsent(t, "multi-credential input", got)
	kinds := markerPattern.FindAllString(got, -1)
	for _, want := range []string{
		"«redacted:aws-access-key»", "«redacted:opaque-token»", "«redacted:url-credentials»",
		"«redacted:jwt»", markerPEM, "«redacted:credential-assignment»",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("marker %s missing; markers present: %v", want, kinds)
		}
	}
	// The harmless structure around the credentials survives.
	for _, keep := range []string{"export AWS_ACCESS_KEY_ID=", "DATABASE_URL=postgres://", "@db.shop.svc:5432/orders", "> key.pem", "PASSWORD="} {
		if !strings.Contains(got, keep) {
			t.Errorf("scrubbing destroyed harmless context %q", keep)
		}
	}
}

// cleanCorpus is realistic telemetry with no credential in it: kubelet
// messages, change-cause annotations, PromQL, rendered series, sidecar
// lines, commit shas, digests. It must pass through byte-identical.
var cleanCorpus = []string{
	"Back-off restarting failed container session-cache in pod checkout-api-7d9c6f5b4-x2m8q_shop(0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9)",
	"Readiness probe failed: Get \"http://10.244.0.12:8080/ready\": dial tcp 10.244.0.12:8080: connect: connection refused",
	"Failed to pull image \"registry.k8s.io/e2e-test-images/agnhost:2.53-does-not-exist\": rpc error: code = NotFound",
	"Error: couldn't find key api-token in Secret shop/checkout-creds",
	"secret \"checkout-creds\" not found",
	"commit 4be1f2a: trim session-cache memory to cut per-pod cost",
	"4be1f2a9c31d",
	"a91c4e7b2d90",
	`sum by (namespace, pod) (container_memory_working_set_bytes{namespace=~"^(?:shop)$",container!=""})`,
	`{namespace="shop",pod="checkout-api-7d9c6f5b4-x2m8q"} 41943040`,
	"I0912 17:45:17.904552       1 logs_generator.go:76] 0 PUT /api/v1/namespaces/ns/pods/hnd 206",
	goldenChain,
	"registry.k8s.io/e2e-test-images/agnhost@sha256:1f2e3d4c5b6a79887766554433221100ffeeddccbbaa99887766554433221100",
	"kubectl set env deployment/checkout-api LOG_FORMAT=json TOKEN_TTL_SECONDS=300 --record",
	"level=info ts=2026-09-12T17:45:40.66184183Z caller=table_manager.go:271 msg=\"query readiness setup completed\"",
	"tokens=3 secrets mounted: 2, basic authentication enabled for 4 users",
	"Bearer authentication is required",
	stateOOMKilled,
	stateCrashLoop,
	"https://github.com/akansh23-cloud/praxis/commit/4be1f2a9c31d",
	"",
}

// TestScrubLeavesCleanContentUnchanged: no false positive on the corpus;
// the returned changed flag is false for every line.
func TestScrubLeavesCleanContentUnchanged(t *testing.T) {
	for _, line := range cleanCorpus {
		got, changed := Scrub(line)
		if got != line || changed {
			t.Errorf("clean input was altered:\n  in %q\n out %q (changed=%v)", line, got, changed)
		}
	}
}

// TestScrubIsIdempotentAndDeterministic: scrub(scrub(x)) == scrub(x),
// twice, for every fixture and every clean line — the marker text itself
// never matches a rule, so a redacted value cannot be re-redacted or
// regrow.
func TestScrubIsIdempotentAndDeterministic(t *testing.T) {
	inputs := slicesConcatStrings(cleanCorpus,
		"Bearer "+plantedBearer, plantedJWT, plantedURL, plantedPEM, plantedAWSKey, plantedAWSSecret,
		"PASSWORD="+plantedAssignment,
		"export AWS_ACCESS_KEY_ID="+plantedAWSKey+" DATABASE_URL="+plantedURL+" Bearer "+plantedJWT+" "+plantedPEM,
	)
	for _, in := range inputs {
		once, _ := Scrub(in)
		twice, changedAgain := Scrub(once)
		if twice != once || changedAgain {
			t.Errorf("not idempotent: scrub(scrub(x)) differs from scrub(x) (changed=%v) for input %s", changedAgain, fingerprint(in))
		}
		again, _ := Scrub(in)
		if again != once {
			t.Errorf("not deterministic across runs for input %s", fingerprint(in))
		}
	}
}

func slicesConcatStrings(base []string, more ...string) []string {
	out := make([]string, 0, len(base)+len(more))
	out = append(out, base...)
	return append(out, more...)
}

// TestScrubNeverEncodesTheSecret: the replacement is removal — the
// marker carries the kind and nothing derived from the value, so two
// different secrets of one class scrub to the identical marker.
func TestScrubNeverEncodesTheSecret(t *testing.T) {
	a, _ := Scrub("key " + plantedAWSKey)
	b, _ := Scrub("key AKIAZZZZZZZZZZZZFAKE")
	if a != b {
		t.Errorf("markers differ by secret value: %q vs %q — the replacement must not encode the original", a, b)
	}
	if !markerPattern.MatchString(a) {
		t.Errorf("marker shape drifted: %q", a)
	}
}
