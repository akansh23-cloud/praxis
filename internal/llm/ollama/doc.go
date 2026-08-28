/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package ollama implements LLMClient against a local Ollama endpoint, for
// regulated or air-gapped deployments.
//
// Single responsibility: provider-specific transport and request shaping for
// locally hosted models.
//
// Specification: docs/01-HLD.md §9. Implemented in Phase 3.

package ollama
