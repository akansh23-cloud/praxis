/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package providers selects an llm.Client implementation from
// configuration. It is the only place both provider packages are named,
// so a caller that wants "the configured model" imports this and the
// interface package, never a provider.
package providers

import (
	"fmt"

	"github.com/akansh23-cloud/praxis/internal/llm"
	"github.com/akansh23-cloud/praxis/internal/llm/anthropic"
	"github.com/akansh23-cloud/praxis/internal/llm/ollama"
)

// New builds the client the config names.
func New(cfg llm.Config) (llm.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Provider {
	case llm.ProviderAnthropic:
		return anthropic.New(cfg)
	case llm.ProviderOllama:
		return ollama.New(cfg)
	default:
		return nil, fmt.Errorf("llm: no implementation for provider %q", cfg.Provider)
	}
}
