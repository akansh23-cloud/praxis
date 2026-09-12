/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package providers

import (
	"testing"

	"github.com/akansh23-cloud/praxis/internal/llm"
)

func TestNewSelectsByConfig(t *testing.T) {
	a, err := New(llm.Config{Provider: llm.ProviderAnthropic, Model: "claude-opus-5", APIKey: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Provider().Name != llm.ProviderAnthropic {
		t.Errorf("provider = %+v", a.Provider())
	}
	o, err := New(llm.Config{Provider: llm.ProviderOllama, Model: "llama3.1"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Provider().Name != llm.ProviderOllama {
		t.Errorf("provider = %+v", o.Provider())
	}
	if _, err := New(llm.Config{Provider: "openai", Model: "x"}); err == nil {
		t.Error("an unknown provider must be refused")
	}
	if _, err := New(llm.Config{Provider: llm.ProviderAnthropic, Model: "claude-opus-5"}); err == nil {
		t.Error("anthropic without a key must be refused")
	}
}
