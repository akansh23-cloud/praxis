/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package llmtest is the scripted fake behind every LLM-agent test: it
// records exactly what a caller sent (system, user, schema) and answers
// from a script, so tests can prove both what the model would have seen
// and how the caller treats what comes back.
package llmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/akansh23-cloud/praxis/internal/llm"
)

// Call is one recorded CompleteStructured invocation.
type Call struct {
	System string
	User   string
	Schema []byte
}

// Response is one scripted answer.
type Response struct {
	JSON  string
	Usage llm.Usage
	Err   error
}

// Fake implements llm.Client from a script. Responses are consumed in
// order; when the script is exhausted, Handler (if set) answers, else the
// call fails — a test that provokes more calls than it scripted is a
// test that found a retry loop.
type Fake struct {
	mu        sync.Mutex
	Calls     []Call
	Responses []Response
	Handler   func(call Call) (json.RawMessage, llm.Usage, error)
	Info      llm.Provider
}

// New returns a fake that answers the given JSON documents in order.
func New(responses ...string) *Fake {
	f := &Fake{Info: llm.Provider{Name: "fake", Model: "fake-model"}}
	for _, r := range responses {
		f.Responses = append(f.Responses, Response{JSON: r, Usage: llm.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.001, CostKnown: true}})
	}
	return f
}

var _ llm.Client = (*Fake)(nil)

func (f *Fake) CompleteStructured(_ context.Context, system, user string, jsonSchema []byte) (json.RawMessage, llm.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := Call{System: system, User: user, Schema: append([]byte(nil), jsonSchema...)}
	f.Calls = append(f.Calls, call)
	if len(f.Responses) > 0 {
		r := f.Responses[0]
		f.Responses = f.Responses[1:]
		if r.Err != nil {
			return nil, r.Usage, r.Err
		}
		return json.RawMessage(r.JSON), r.Usage, nil
	}
	if f.Handler != nil {
		return f.Handler(call)
	}
	return nil, llm.Usage{}, fmt.Errorf("llmtest: unscripted call %d (system %d bytes, user %d bytes)", len(f.Calls), len(system), len(user))
}

// Provider implements llm.Client.
func (f *Fake) Provider() llm.Provider { return f.Info }

// CallCount is the number of completions requested so far.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}
