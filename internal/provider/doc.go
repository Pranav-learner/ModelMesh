// Package provider defines the core abstractions of the ModelMesh Provider
// Layer: the provider-independent request/response models, the LLMProvider
// contract that every concrete provider (OpenAI, Anthropic, ...) will
// implement, and the Registry and Manager used to look providers up at
// runtime.
//
// # Design intent
//
// This package deliberately contains no provider-specific knowledge. The DTOs
// (ChatRequest, ChatResponse, EmbeddingRequest, ...) are the single normalized
// vocabulary the rest of the system speaks; a concrete adapter's job is to
// translate between these types and its provider's native API. Nothing outside
// an adapter should ever see a provider-native shape.
//
// # Layering
//
// The Provider Layer sits at the bottom of the request pipeline. It only knows
// how to *execute* a call against a named provider and *normalize* the result.
// It does not decide which provider to use (that is the future Routing Engine),
// does not guard calls (the future Circuit Breaker), and does not cache
// (the future Cache System). Keeping those concerns out of this package is what
// lets each later phase plug in without rewriting the foundation.
//
// # Extension points
//
//   - New providers: implement LLMProvider and Register the instance. No other
//     package changes.
//   - Richer decisions: the Manager exposes lookups only; routing/scoring will
//     wrap it, not modify it.
//   - Fault injection / testing: the mock subpackage provides a configurable
//     LLMProvider used by unit and (future) integration tests.
package provider
