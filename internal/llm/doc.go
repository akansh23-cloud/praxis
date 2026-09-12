/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package llm defines the LLMClient seam of docs/02-LLD.md §5 and its
// provider implementations (anthropic/, ollama/, chosen by configuration in
// providers/).
//
// Single responsibility: exchange one fixed system instruction and one
// user message for a schema-constrained JSON completion, and account for
// the tokens and money it cost. Nothing here interprets the completion,
// holds a cluster credential, or decides what a response means.
//
// Provider neutrality is structural: this package exports no provider
// type, every implementation returns the same Usage and Provider values,
// and callers (internal/agents/llm) import only this package — never a
// provider — which internal/agents/boundary_test.go enforces. Credentials
// are process configuration (Config.APIKey from the environment); they are
// never logged, never placed in an error, and every provider error passes
// through Scrub so an echoed key cannot leak through a message.
//
// Import boundary (lint-enforced by the depguard rule in .golangci.yml and
// internal/llm/boundary_test.go): only internal/llm, internal/agents and
// the benchmark's agent driver may import this package.
//
// Specification: docs/02-LLD.md §5; docs/01-HLD.md §9. Implemented in
// playbook Session 3.3.
package llm
