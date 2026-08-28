/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package llm defines the LLMClient interface and its provider
// implementations (anthropic/, ollama/).
//
// Single responsibility: exchange structured prompts for schema-constrained
// completions. It holds no cluster credentials and makes no decisions about
// what the response means.
//
// Import boundary (lint-enforced): nothing under internal/executor, verify,
// rollback, risk, policy, simulate, approve or audit may import this package.
//
// Specification: docs/02-LLD.md §5. Implemented in Phase 3.

package llm
