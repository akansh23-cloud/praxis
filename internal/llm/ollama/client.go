/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akansh23-cloud/praxis/internal/llm"
)

// A locally hosted model through Ollama's /api/chat endpoint, for the
// regulated and air-gapped deployments of docs/01-HLD.md §9. Structured
// output uses Ollama's `format` parameter, which takes the JSON schema
// itself; sampling is pinned (temperature 0, fixed seed) because Ollama
// honours it, so identical prompts on identical weights reproduce.

// DefaultBaseURL is where a local Ollama listens.
const DefaultBaseURL = "http://127.0.0.1:11434"

// DefaultTimeout bounds one request; local inference can be slow.
const DefaultTimeout = 10 * time.Minute

// seed pins sampling; any fixed value works, this one is documented.
const seed = 42

// maxResponseBytes bounds how much of a response is read.
const maxResponseBytes = 8 << 20

// Client implements llm.Client against Ollama.
type Client struct {
	base    string
	model   string
	hc      *http.Client
	pricing llm.Pricing
	numCtx  int64
}

// New builds a client from configuration; no credential is involved.
func New(cfg llm.Config) (*Client, error) {
	if cfg.Provider != llm.ProviderOllama {
		return nil, fmt.Errorf("ollama: config names provider %q", cfg.Provider)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("ollama: base URL %q is not an absolute URL", base)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	pricing := llm.Pricing{}
	if cfg.Pricing != nil {
		pricing = *cfg.Pricing
	}
	return &Client{
		base:    strings.TrimRight(base, "/"),
		model:   cfg.Model,
		hc:      &http.Client{Timeout: timeout},
		pricing: pricing,
		numCtx:  cfg.MaxTokens,
	}, nil
}

// Provider implements llm.Client.
func (c *Client) Provider() llm.Provider {
	return llm.Provider{Name: llm.ProviderOllama, Model: c.model}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []chatMessage   `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   json.RawMessage `json:"format"`
	Options  map[string]any  `json:"options"`
}

type chatResponse struct {
	Model           string      `json:"model"`
	Message         chatMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int64       `json:"prompt_eval_count"`
	EvalCount       int64       `json:"eval_count"`
	Error           string      `json:"error"`
}

// CompleteStructured implements llm.Client.
func (c *Client) CompleteStructured(ctx context.Context, system, user string, jsonSchema []byte) (json.RawMessage, llm.Usage, error) {
	if !json.Valid(jsonSchema) {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindMalformed, Message: "the output schema is not valid JSON"}
	}
	options := map[string]any{"temperature": 0, "seed": seed}
	if c.numCtx > 0 {
		options["num_predict"] = c.numCtx
	}
	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:  false,
		Format:  json.RawMessage(jsonSchema),
		Options: options,
	})
	if err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindMalformed, Message: llm.Scrub(err.Error(), "")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindTransport, Message: llm.Scrub(err.Error(), "")}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindTransport, Message: llm.Scrub(err.Error(), "")}
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindTransport, Message: llm.Scrub(err.Error(), "")}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, StatusCode: resp.StatusCode,
			Kind: llm.KindHTTPError, Message: llm.Scrub(string(raw), "")}
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindMalformed, Message: "unparseable /api/chat response: " + llm.Scrub(err.Error(), "")}
	}
	if cr.Error != "" {
		return nil, llm.Usage{}, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindProviderError, Message: llm.Scrub(cr.Error, "")}
	}
	usage := llm.Usage{InputTokens: cr.PromptEvalCount, OutputTokens: cr.EvalCount}
	usage.CostUSD, usage.CostKnown = c.pricing.Cost(usage.InputTokens, usage.OutputTokens)
	if cr.DoneReason == "length" {
		return nil, usage, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindTruncated, Message: "the completion hit the token limit before the JSON document closed"}
	}
	out := strings.TrimSpace(cr.Message.Content)
	if !json.Valid([]byte(out)) {
		return nil, usage, &llm.ProviderError{Provider: llm.ProviderOllama, Kind: llm.KindMalformed,
			Message: fmt.Sprintf("the completion is not a JSON document (%d bytes)", len(out))}
	}
	return json.RawMessage(out), usage, nil
}
