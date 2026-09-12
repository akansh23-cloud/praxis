/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Client is the LLMClient seam of LLD §5: one structured completion per
// call. system carries the fixed, trusted instructions; user carries the
// delimited DATA (evidence, incident) the instructions operate on; the
// completion must conform to jsonSchema, a JSON Schema document.
//
// Implementations must be deterministic where the provider allows it
// (Anthropic's current models accept no sampling parameters at all;
// Ollama is pinned to temperature 0 and a fixed seed), must never place
// caller text anywhere but the message it was given for, and must never
// surface a credential.
type Client interface {
	CompleteStructured(ctx context.Context, system, user string, jsonSchema []byte) (json.RawMessage, Usage, error)

	// Provider names the provider and the model configured for this
	// client — audit metadata, recorded next to the prompt hash.
	Provider() Provider
}

// Provider identifies who answered.
type Provider struct {
	// Name is the provider key: ProviderAnthropic or ProviderOllama.
	Name string
	// Model is the configured model id, verbatim.
	Model string
}

// String renders provider/model for annotations and records.
func (p Provider) String() string { return p.Name + "/" + p.Model }

// The provider keys Config.Provider accepts.
const (
	ProviderAnthropic = "anthropic"
	ProviderOllama    = "ollama"
)

// Usage is the per-call token and cost accounting every provider returns
// (LLD §15: praxis_llm_tokens_total, praxis_llm_cost_usd_total). Cost is
// derived from the configured pricing; CostKnown is false when no price
// is known for the model, so an unknown price reads as "unpriced" rather
// than "free".
type Usage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	CostUSD                  float64
	CostKnown                bool
}

// Add sums two usages; CostKnown holds only if both sides were priced.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:              u.InputTokens + o.InputTokens,
		OutputTokens:             u.OutputTokens + o.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens + o.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens + o.CacheCreationInputTokens,
		CostUSD:                  u.CostUSD + o.CostUSD,
		CostKnown:                u.CostKnown && o.CostKnown,
	}
}

// Pricing is a model's list price in USD per million tokens.
type Pricing struct {
	InputUSDPerMTok  float64
	OutputUSDPerMTok float64
}

// Cost prices a call. A zero Pricing is "unknown", not "free".
func (p Pricing) Cost(inputTokens, outputTokens int64) (usd float64, known bool) {
	if p.InputUSDPerMTok == 0 && p.OutputUSDPerMTok == 0 {
		return 0, false
	}
	return float64(inputTokens)/1e6*p.InputUSDPerMTok + float64(outputTokens)/1e6*p.OutputUSDPerMTok, true
}

// Config selects and configures a provider. It is process configuration
// — flags and environment — never anything read from the cluster.
type Config struct {
	// Provider is ProviderAnthropic or ProviderOllama.
	Provider string

	// Model is the model id, required. Business logic never names a
	// model; it comes from here.
	Model string

	// BaseURL overrides the provider endpoint (tests, proxies, a local
	// Ollama on a non-default port). Empty means the provider default.
	BaseURL string

	// APIKey is the provider credential (Anthropic). It is read from the
	// environment by the caller and travels only inside this struct and
	// the request header. Ollama needs none.
	APIKey string

	// MaxTokens bounds one completion; zero means the provider default.
	MaxTokens int64

	// Effort is the Anthropic output_config.effort level (low, medium,
	// high, xhigh, max); empty leaves the provider default. Ignored by
	// Ollama.
	Effort string

	// Pricing overrides the provider's built-in price table for Model;
	// nil means "use the table, or unknown".
	Pricing *Pricing

	// Timeout bounds one request; zero means the provider default.
	Timeout time.Duration
}

// Validate checks the provider-independent part of a Config.
func (c Config) Validate() error {
	switch c.Provider {
	case ProviderAnthropic, ProviderOllama:
	case "":
		return fmt.Errorf("llm: provider is required (%s or %s)", ProviderAnthropic, ProviderOllama)
	default:
		return fmt.Errorf("llm: unknown provider %q (want %s or %s)", c.Provider, ProviderAnthropic, ProviderOllama)
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("llm: model is required for provider %s", c.Provider)
	}
	if c.MaxTokens < 0 {
		return fmt.Errorf("llm: max tokens must not be negative")
	}
	return nil
}

// The provider-independent error kinds; a provider's own error type
// string (e.g. "rate_limit_error") is passed through as the kind when the
// provider classified the failure itself.
const (
	KindRefusal       = "refusal"        // the model declined the request
	KindTruncated     = "truncated"      // the completion hit the token bound before the JSON closed
	KindMalformed     = "malformed"      // the completion, or the schema, is not the JSON expected
	KindTransport     = "transport"      // the request never got a response
	KindHTTPError     = "http_error"     // a non-2xx response without a classified provider error
	KindProviderError = "provider_error" // the provider reported an error in its own shape
)

// ProviderError is the one error shape a provider surfaces for a failed
// call: what failed, classified, with a bounded message — never the
// request, never a header, never a credential.
type ProviderError struct {
	Provider   string
	StatusCode int    // HTTP status when one exists, else 0
	Kind       string // one of the Kind* constants, or the provider's own error type
	Message    string
}

func (e *ProviderError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: %s (HTTP %d): %s", e.Provider, e.Kind, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Provider, e.Kind, e.Message)
}

// MaxErrorMessage bounds how much provider text an error carries.
const MaxErrorMessage = 300

// Scrub prepares provider text for an error: the credential, if a proxy
// or provider echoed it, is replaced by a marker, and the text is bounded.
// Nothing derived from the secret survives.
func Scrub(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "«redacted:api-key»")
	}
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	if len(text) > MaxErrorMessage {
		text = text[:MaxErrorMessage] + "…"
	}
	return text
}
