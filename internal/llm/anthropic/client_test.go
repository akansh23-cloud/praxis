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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akansh23-cloud/praxis/internal/llm"
)

// The credential every test plants. Obviously fake; the assertions prove
// it never appears in an error, whatever the provider echoes back.
const fakeKey = "sk-ant-api03-FAKE-FAKE-FAKE-0123456789"

const schemaJSON = `{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`

// messagesServer emulates POST /v1/messages: it records the request body
// and answers from the script keyed by the user text.
func messagesServer(t *testing.T, requests *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("x-api-key") != fakeKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key %s"}}`, r.Header.Get("x-api-key"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*requests = append(*requests, req)
		user := req["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		switch {
		case strings.Contains(user, "REFUSE"):
			_, _ = fmt.Fprint(w, `{"id":"msg_r","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"nope"},"usage":{"input_tokens":5,"output_tokens":0}}`)
		case strings.Contains(user, "TRUNCATE"):
			_, _ = fmt.Fprint(w, `{"id":"msg_t","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"{\"ok\":"}],"stop_reason":"max_tokens","usage":{"input_tokens":5,"output_tokens":99}}`)
		case strings.Contains(user, "NOTJSON"):
			_, _ = fmt.Fprint(w, `{"id":"msg_n","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"sure thing"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`)
		case strings.Contains(user, "ECHOKEY"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad request for key %s"}}`, fakeKey)
		default:
			_, _ = fmt.Fprint(w, `{"id":"msg_ok","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn","usage":{"input_tokens":120,"output_tokens":8,"cache_read_input_tokens":40,"cache_creation_input_tokens":0}}`)
		}
	}))
}

func newClient(t *testing.T, srv *httptest.Server, cfg llm.Config) *Client {
	t.Helper()
	cfg.Provider = llm.ProviderAnthropic
	cfg.BaseURL = srv.URL
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.APIKey == "" {
		cfg.APIKey = fakeKey
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCompleteStructuredRequestShape pins the wire contract: system in the
// system field, user text in the single user message, the JSON schema as
// output_config.format of type json_schema, no sampling parameters, the
// effort only when configured, and the usage priced from the table.
func TestCompleteStructuredRequestShape(t *testing.T) {
	var requests []map[string]any
	srv := messagesServer(t, &requests)
	defer srv.Close()
	c := newClient(t, srv, llm.Config{Effort: EffortHigh, MaxTokens: 4096})

	out, usage, err := c.CompleteStructured(context.Background(), "SYSTEM RULES", "user data", []byte(schemaJSON))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"ok":true}` {
		t.Errorf("completion = %s", out)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 8 || usage.CacheReadInputTokens != 40 {
		t.Errorf("usage = %+v", usage)
	}
	if !usage.CostKnown || usage.CostUSD <= 0 {
		t.Errorf("usage must be priced for a table model: %+v", usage)
	}
	wantCost := 120.0/1e6*5 + 8.0/1e6*25
	if diff := usage.CostUSD - wantCost; diff > 1e-12 || diff < -1e-12 {
		t.Errorf("cost = %v, want %v", usage.CostUSD, wantCost)
	}
	if c.Provider() != (llm.Provider{Name: llm.ProviderAnthropic, Model: DefaultModel}) {
		t.Errorf("provider = %+v", c.Provider())
	}

	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(requests))
	}
	req := requests[0]
	if req["model"] != DefaultModel || req["max_tokens"].(float64) != 4096 {
		t.Errorf("model/max_tokens = %v/%v", req["model"], req["max_tokens"])
	}
	system := req["system"].([]any)[0].(map[string]any)["text"]
	if system != "SYSTEM RULES" {
		t.Errorf("system = %v — instructions must travel in the system field", system)
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k", "tools", "tool_choice"} {
		if _, present := req[forbidden]; present {
			t.Errorf("request carries %q; no sampling or tool parameters may be sent", forbidden)
		}
	}
	outputConfig := req["output_config"].(map[string]any)
	if outputConfig["effort"] != EffortHigh {
		t.Errorf("effort = %v", outputConfig["effort"])
	}
	format := outputConfig["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Errorf("format type = %v", format["type"])
	}
	schema := format["schema"].(map[string]any)
	if schema["additionalProperties"] != false || schema["type"] != "object" {
		t.Errorf("schema not sent verbatim: %v", schema)
	}
}

func TestCompleteStructuredOmitsEffortByDefault(t *testing.T) {
	var requests []map[string]any
	srv := messagesServer(t, &requests)
	defer srv.Close()
	c := newClient(t, srv, llm.Config{})
	if _, _, err := c.CompleteStructured(context.Background(), "s", "u", []byte(schemaJSON)); err != nil {
		t.Fatal(err)
	}
	if _, present := requests[0]["output_config"].(map[string]any)["effort"]; present {
		t.Error("effort sent although unconfigured")
	}
	if requests[0]["max_tokens"].(float64) != DefaultMaxTokens {
		t.Errorf("max_tokens = %v, want the default %d", requests[0]["max_tokens"], DefaultMaxTokens)
	}
}

// TestCompleteStructuredFailures: every failure shape is a ProviderError
// with a classified kind, and the API key never appears in any of them —
// not even when the provider echoes it back.
func TestCompleteStructuredFailures(t *testing.T) {
	var requests []map[string]any
	srv := messagesServer(t, &requests)
	defer srv.Close()
	c := newClient(t, srv, llm.Config{})

	cases := []struct {
		user     string
		wantKind string
		wantHTTP int
	}{
		{"REFUSE", llm.KindRefusal, 0},
		{"TRUNCATE", llm.KindTruncated, 0},
		{"NOTJSON", llm.KindMalformed, 0},
		{"ECHOKEY", "invalid_request_error", 400},
	}
	for _, tc := range cases {
		t.Run(tc.user, func(t *testing.T) {
			_, _, err := c.CompleteStructured(context.Background(), "s", tc.user, []byte(schemaJSON))
			var perr *llm.ProviderError
			if !errors.As(err, &perr) {
				t.Fatalf("error %v is not a ProviderError", err)
			}
			if perr.Kind != tc.wantKind || perr.StatusCode != tc.wantHTTP {
				t.Errorf("kind/status = %s/%d, want %s/%d (%v)", perr.Kind, perr.StatusCode, tc.wantKind, tc.wantHTTP, err)
			}
			if strings.Contains(err.Error(), fakeKey) {
				t.Fatalf("the API key leaked into an error message")
			}
		})
	}

	// A wrong key: the provider's 401 comes back scrubbed too.
	wrong := newClient(t, srv, llm.Config{APIKey: "sk-ant-WRONG-KEY-000000"})
	_, _, err := wrong.CompleteStructured(context.Background(), "s", "u", []byte(schemaJSON))
	var perr *llm.ProviderError
	if !errors.As(err, &perr) || perr.StatusCode != 401 {
		t.Fatalf("want a 401 ProviderError, got %v", err)
	}
	if strings.Contains(err.Error(), "WRONG-KEY") {
		t.Fatal("the wrong API key leaked into the error message")
	}

	// Malformed schema never reaches the network.
	before := len(requests)
	if _, _, err := c.CompleteStructured(context.Background(), "s", "u", []byte("not json")); err == nil {
		t.Error("a non-JSON schema must fail")
	}
	if len(requests) != before {
		t.Error("a malformed schema was sent to the provider")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	cases := []llm.Config{
		{Provider: llm.ProviderOllama, Model: "m", APIKey: "k"},
		{Provider: llm.ProviderAnthropic, Model: "m"},
		{Provider: llm.ProviderAnthropic, Model: "", APIKey: "k"},
		{Provider: llm.ProviderAnthropic, Model: "m", APIKey: "k", Effort: "extreme"},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Errorf("case %d: New accepted an invalid config %+v", i, cfg)
		} else if strings.Contains(err.Error(), "k") && cfg.APIKey == "k" && strings.Contains(err.Error(), "APIKey") {
			t.Errorf("case %d: error text mentions the key", i)
		}
	}
	c, err := New(llm.Config{Provider: llm.ProviderAnthropic, Model: "unknown-model", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.pricing != (llm.Pricing{}) {
		t.Error("an unknown model must have unknown pricing, not a guessed one")
	}
	c, err = New(llm.Config{Provider: llm.ProviderAnthropic, Model: "unknown-model", APIKey: "k",
		Pricing: &llm.Pricing{InputUSDPerMTok: 1, OutputUSDPerMTok: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if c.pricing.InputUSDPerMTok != 1 {
		t.Error("configured pricing must override the table")
	}
}
