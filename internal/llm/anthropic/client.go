/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/akansh23-cloud/praxis/internal/llm"
)

// The Anthropic Messages API through the official Go SDK, shaped for
// exactly one job: a schema-constrained completion of one system
// instruction plus one user message.
//
// Determinism: the current model generation accepts no sampling
// parameters (temperature, top_p, top_k are rejected with HTTP 400), so
// none are sent — the request carries only the model, the token bound,
// the messages, the JSON schema and, optionally, the effort level. There
// is no fallback routing to another model: the model an incident was
// analyzed by is audit metadata (the prompt hash records it), so it must
// be exactly the configured one.
//
// Credentials: the API key lives in the SDK client's request option and
// nowhere else; every error passes through llm.Scrub before it leaves.

// DefaultModel is the model the benchmark defaults to when none is
// configured; every other place a model is named is configuration.
const DefaultModel = "claude-opus-5"

// The output_config.effort levels the API accepts.
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortXHigh  = "xhigh"
	EffortMax    = "max"
)

// DefaultMaxTokens bounds a completion when the config leaves it unset —
// generous enough for a plan, small enough that a runaway answer cannot
// run up a bill.
const DefaultMaxTokens = 16000

// DefaultTimeout bounds one request.
const DefaultTimeout = 5 * time.Minute

// PricingSource names where defaultPricing was read from and when, so a
// published USD figure can cite its basis. The table was taken from the
// 2026-06-24 snapshot of the Claude API reference and re-verified against
// the live pricing page on 2026-09-12 (playbook Session 3.4): identical
// for every listed model. Re-verify before publishing numbers for a
// model; update the table and this text together.
const PricingSource = "Claude API pricing page (platform.claude.com/docs/en/about-claude/pricing), first-party list prices; snapshot 2026-06-24, re-verified live 2026-09-12"

// defaultPricing is the first-party list price table (USD per million
// tokens) for uncached input and output, as published in the Claude API
// reference (see PricingSource for the snapshot and verification dates).
// Prices change;
// llm.Config.Pricing overrides this table, and a model absent from it is
// accounted as "cost unknown", never as free. The table prices exactly the
// two token classes a Praxis request consumes: no request here sets
// cache_control, so cache reads and writes — billed at their own rates —
// should never appear; if a response reports them anyway, the call is
// accounted as cost unknown rather than priced at the wrong rate.
var defaultPricing = map[string]llm.Pricing{
	DefaultModel:        {InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
	"claude-opus-4-8":   {InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
	"claude-opus-4-7":   {InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
	"claude-opus-4-6":   {InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
	"claude-sonnet-5":   {InputUSDPerMTok: 2, OutputUSDPerMTok: 10},
	"claude-sonnet-4-6": {InputUSDPerMTok: 3, OutputUSDPerMTok: 15},
	"claude-haiku-4-5":  {InputUSDPerMTok: 1, OutputUSDPerMTok: 5},
	"claude-fable-5-1":  {InputUSDPerMTok: 10, OutputUSDPerMTok: 50},
}

// Client implements llm.Client against the Anthropic Messages API.
type Client struct {
	api       sdk.Client
	model     string
	effort    string
	maxTokens int64
	pricing   llm.Pricing
	apiKey    string
}

// New builds a client from configuration. The API key is required and is
// never read from anywhere but the config.
func New(cfg llm.Config) (*Client, error) {
	if cfg.Provider != llm.ProviderAnthropic {
		return nil, fmt.Errorf("anthropic: config names provider %q", cfg.Provider)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.APIKey == "" {
		return nil, errors.New("anthropic: an API key is required (set ANTHROPIC_API_KEY in the process environment)")
	}
	switch cfg.Effort {
	case "", EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
	default:
		return nil, fmt.Errorf("anthropic: unknown effort %q (want low, medium, high, xhigh or max)", cfg.Effort)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(2),
		option.WithRequestTimeout(timeout),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	pricing, ok := defaultPricing[cfg.Model]
	if cfg.Pricing != nil {
		pricing, ok = *cfg.Pricing, true
	}
	if !ok {
		pricing = llm.Pricing{}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	return &Client{
		api:       sdk.NewClient(opts...),
		model:     cfg.Model,
		effort:    cfg.Effort,
		maxTokens: maxTokens,
		pricing:   pricing,
		apiKey:    cfg.APIKey,
	}, nil
}

// Provider implements llm.Client.
func (c *Client) Provider() llm.Provider {
	return llm.Provider{Name: llm.ProviderAnthropic, Model: c.model}
}

// CompleteStructured implements llm.Client.
func (c *Client) CompleteStructured(ctx context.Context, system, user string, jsonSchema []byte) (json.RawMessage, llm.Usage, error) {
	var schema map[string]any
	if err := json.Unmarshal(jsonSchema, &schema); err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderAnthropic, Kind: llm.KindMalformed,
			Message: "the output schema is not a JSON object: " + llm.Scrub(err.Error(), c.apiKey)}
	}
	params := sdk.MessageNewParams{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    []sdk.TextBlockParam{{Text: system}},
		Messages:  []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(user))},
		OutputConfig: sdk.OutputConfigParam{
			Format: sdk.JSONOutputFormatParam{Schema: schema},
		},
	}
	if c.effort != "" {
		params.OutputConfig.Effort = sdk.OutputConfigEffort(c.effort)
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return nil, llm.Usage{}, c.wrap(err)
	}
	usage := llm.Usage{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
	}
	usage.CostUSD, usage.CostKnown = c.pricing.Cost(usage.InputTokens, usage.OutputTokens)
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		// Token classes the table does not price: honest "unknown" beats a
		// number computed at the wrong rate.
		usage.CostKnown = false
	}

	switch resp.StopReason {
	case sdk.StopReasonRefusal:
		return nil, usage, &llm.ProviderError{Provider: llm.ProviderAnthropic, Kind: llm.KindRefusal,
			Message: llm.Scrub("the model declined the request (category "+string(resp.StopDetails.Category)+")", c.apiKey)}
	case sdk.StopReasonMaxTokens:
		return nil, usage, &llm.ProviderError{Provider: llm.ProviderAnthropic, Kind: llm.KindTruncated,
			Message: fmt.Sprintf("the completion hit max_tokens (%d) before the JSON document closed", c.maxTokens)}
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(sdk.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	out := strings.TrimSpace(text.String())
	if !json.Valid([]byte(out)) {
		return nil, usage, &llm.ProviderError{Provider: llm.ProviderAnthropic, Kind: llm.KindMalformed,
			Message: fmt.Sprintf("the completion is not a JSON document (%d bytes, stop_reason %s)", len(out), resp.StopReason)}
	}
	return json.RawMessage(out), usage, nil
}

// wrap classifies an SDK error into the one shape callers see, with the
// credential scrubbed from whatever text the provider or a proxy sent.
func (c *Client) wrap(err error) error {
	var apierr *sdk.Error
	if errors.As(err, &apierr) {
		return &llm.ProviderError{
			Provider:   llm.ProviderAnthropic,
			StatusCode: apierr.StatusCode,
			Kind:       string(apierr.Type()),
			Message:    llm.Scrub(apierr.Error(), c.apiKey),
		}
	}
	return &llm.ProviderError{Provider: llm.ProviderAnthropic, Kind: llm.KindTransport, Message: llm.Scrub(err.Error(), c.apiKey)}
}
