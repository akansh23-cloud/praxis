/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package llm

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"anthropic ok", Config{Provider: ProviderAnthropic, Model: "claude-opus-5"}, ""},
		{"ollama ok", Config{Provider: ProviderOllama, Model: "llama3.1"}, ""},
		{"no provider", Config{Model: "x"}, "provider is required"},
		{"unknown provider", Config{Provider: "openai", Model: "x"}, "unknown provider"},
		{"no model", Config{Provider: ProviderAnthropic, Model: " "}, "model is required"},
		{"negative tokens", Config{Provider: ProviderAnthropic, Model: "m", MaxTokens: -1}, "must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPricingAndUsage(t *testing.T) {
	p := Pricing{InputUSDPerMTok: 5, OutputUSDPerMTok: 25}
	usd, known := p.Cost(1_000_000, 200_000)
	if !known || usd != 10 {
		t.Errorf("Cost = %v known=%v, want 10 USD known", usd, known)
	}
	if _, known := (Pricing{}).Cost(10, 10); known {
		t.Error("a zero pricing must read as unknown, not free")
	}

	a := Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 1, CostKnown: true}
	b := Usage{InputTokens: 1, OutputTokens: 1, CacheReadInputTokens: 3}
	sum := a.Add(b)
	if sum.InputTokens != 11 || sum.OutputTokens != 6 || sum.CacheReadInputTokens != 3 || sum.CostUSD != 1 {
		t.Errorf("Add = %+v", sum)
	}
	if sum.CostKnown {
		t.Error("a sum including an unpriced call must not claim a known cost")
	}
}

func TestScrubRemovesTheCredential(t *testing.T) {
	const key = "sk-ant-api03-FAKE-not-a-real-key-0000"
	got := Scrub("authentication_error: invalid x-api-key "+key+" rejected\x1b[0m", key)
	if strings.Contains(got, key) {
		t.Fatal("the credential survived scrubbing")
	}
	if !strings.Contains(got, "«redacted:api-key»") {
		t.Errorf("marker missing: %q", got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Error("control character survived")
	}
	long := Scrub(strings.Repeat("x", 2*MaxErrorMessage), "")
	if len(long) > MaxErrorMessage+len("…") {
		t.Errorf("message not bounded: %d bytes", len(long))
	}
}

func TestProviderErrorAndString(t *testing.T) {
	e := &ProviderError{Provider: ProviderAnthropic, StatusCode: 429, Kind: "rate_limit_error", Message: "slow down"}
	if got := e.Error(); got != "anthropic: rate_limit_error (HTTP 429): slow down" {
		t.Errorf("Error() = %q", got)
	}
	e2 := &ProviderError{Provider: ProviderOllama, Kind: KindTransport, Message: "connection refused"}
	if got := e2.Error(); got != "ollama: transport: connection refused" {
		t.Errorf("Error() = %q", got)
	}
	if got := (Provider{Name: ProviderOllama, Model: "llama3.1"}).String(); got != "ollama/llama3.1" {
		t.Errorf("String() = %q", got)
	}
}
