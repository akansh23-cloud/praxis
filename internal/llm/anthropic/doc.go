/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package anthropic implements LLMClient against the Anthropic Messages API.
//
// Single responsibility: provider-specific transport, request shaping and
// usage accounting for Anthropic models. Provider choice is configuration;
// no caller depends on this package directly.
//
// Specification: docs/01-HLD.md §9. Implemented in Phase 3.

package anthropic
