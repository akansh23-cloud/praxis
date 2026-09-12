/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package ollama

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

const testModel = "llama3.1"

const schemaJSON = `{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`

func chatServer(t *testing.T, requests *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*requests = append(*requests, req)
		user := req["messages"].([]any)[1].(map[string]any)["content"].(string)
		switch {
		case strings.Contains(user, "LENGTH"):
			_, _ = fmt.Fprint(w, `{"model":"m","message":{"role":"assistant","content":"{\"ok\":"},"done":true,"done_reason":"length","prompt_eval_count":7,"eval_count":50}`)
		case strings.Contains(user, "ERROR"):
			_, _ = fmt.Fprint(w, `{"error":"model 'm' not found"}`)
		case strings.Contains(user, "HTTP500"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "boom")
		case strings.Contains(user, "NOTJSON"):
			_, _ = fmt.Fprint(w, `{"model":"m","message":{"role":"assistant","content":"hello"},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":1}`)
		default:
			_, _ = fmt.Fprint(w, `{"model":"m","message":{"role":"assistant","content":" {\"ok\":true} "},"done":true,"done_reason":"stop","prompt_eval_count":70,"eval_count":9}`)
		}
	}))
}

func TestCompleteStructuredRequestShape(t *testing.T) {
	var requests []map[string]any
	srv := chatServer(t, &requests)
	defer srv.Close()
	c, err := New(llm.Config{Provider: llm.ProviderOllama, Model: testModel, BaseURL: srv.URL + "/", MaxTokens: 2048})
	if err != nil {
		t.Fatal(err)
	}
	out, usage, err := c.CompleteStructured(context.Background(), "SYSTEM RULES", "user data", []byte(schemaJSON))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"ok":true}` {
		t.Errorf("completion = %s", out)
	}
	if usage.InputTokens != 70 || usage.OutputTokens != 9 || usage.CostKnown {
		t.Errorf("usage = %+v (a local model without configured pricing is unpriced)", usage)
	}
	req := requests[0]
	msgs := req["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "SYSTEM RULES" {
		t.Errorf("system message = %v", msgs[0])
	}
	if req["stream"] != false || req["model"] != testModel {
		t.Errorf("stream/model = %v/%v", req["stream"], req["model"])
	}
	opts := req["options"].(map[string]any)
	if opts["temperature"].(float64) != 0 || opts["seed"].(float64) != seed || opts["num_predict"].(float64) != 2048 {
		t.Errorf("options = %v — temperature 0 and a fixed seed are the determinism contract", opts)
	}
	format := req["format"].(map[string]any)
	if format["type"] != "object" || format["additionalProperties"] != false {
		t.Errorf("format must carry the schema verbatim: %v", format)
	}
	if c.Provider() != (llm.Provider{Name: llm.ProviderOllama, Model: testModel}) {
		t.Errorf("provider = %+v", c.Provider())
	}
}

func TestCompleteStructuredFailures(t *testing.T) {
	var requests []map[string]any
	srv := chatServer(t, &requests)
	defer srv.Close()
	c, err := New(llm.Config{Provider: llm.ProviderOllama, Model: "m", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		user     string
		wantKind string
	}{
		{"LENGTH", llm.KindTruncated},
		{"ERROR", llm.KindProviderError},
		{"HTTP500", llm.KindHTTPError},
		{"NOTJSON", llm.KindMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.user, func(t *testing.T) {
			_, _, err := c.CompleteStructured(context.Background(), "s", tc.user, []byte(schemaJSON))
			var perr *llm.ProviderError
			if !errors.As(err, &perr) || perr.Kind != tc.wantKind {
				t.Errorf("error = %v, want kind %s", err, tc.wantKind)
			}
		})
	}
	if _, _, err := c.CompleteStructured(context.Background(), "s", "u", []byte("nope")); err == nil {
		t.Error("an invalid schema must fail before the request")
	}
	down, _ := New(llm.Config{Provider: llm.ProviderOllama, Model: "m", BaseURL: "http://127.0.0.1:1"})
	if _, _, err := down.CompleteStructured(context.Background(), "s", "u", []byte(schemaJSON)); err == nil {
		t.Error("an unreachable Ollama must fail with a transport error")
	}
}

func TestNewDefaultsAndRejections(t *testing.T) {
	c, err := New(llm.Config{Provider: llm.ProviderOllama, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if c.base != DefaultBaseURL {
		t.Errorf("base = %q, want the default", c.base)
	}
	for _, cfg := range []llm.Config{
		{Provider: llm.ProviderAnthropic, Model: "m"},
		{Provider: llm.ProviderOllama, Model: ""},
		{Provider: llm.ProviderOllama, Model: "m", BaseURL: "not a url"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New accepted %+v", cfg)
		}
	}
}
