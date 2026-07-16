package openai

import (
	"time"

	oai "github.com/openai/openai-go"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// This file is the OpenAI translation layer. It contains ONLY pure functions
// that convert between ModelMesh's unified DTOs and the OpenAI SDK types. No
// network, no business logic, no state. Keeping mapping here isolates it from
// the provider's orchestration (openai.go) and from the rest of the system,
// which never sees an OpenAI SDK type.

// toChatParams translates a unified ChatRequest into OpenAI chat params. The
// model is assumed already resolved (non-empty) by the caller.
func toChatParams(req provider.ChatRequest, model string) oai.ChatCompletionNewParams {
	params := oai.ChatCompletionNewParams{
		Model:    oai.ChatModel(model),
		Messages: toChatMessages(req.Messages),
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = oai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = oai.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = oai.Float(*req.TopP)
	}
	if len(req.Stop) > 0 {
		params.Stop = oai.ChatCompletionNewParamsStopUnion{OfStringArray: req.Stop}
	}
	return params
}

// toChatMessages maps unified messages onto OpenAI's message param union using
// the SDK's role-specific constructors.
func toChatMessages(msgs []provider.ChatMessage) []oai.ChatCompletionMessageParamUnion {
	out := make([]oai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			out = append(out, oai.SystemMessage(m.Content))
		case provider.RoleAssistant:
			out = append(out, oai.AssistantMessage(m.Content))
		default: // RoleUser and any unexpected role default to a user turn
			out = append(out, oai.UserMessage(m.Content))
		}
	}
	return out
}

// fromChatCompletion normalizes an OpenAI ChatCompletion into a unified
// ChatResponse.
func fromChatCompletion(providerName string, resp *oai.ChatCompletion) provider.ChatResponse {
	choices := make([]provider.Choice, len(resp.Choices))
	for i, c := range resp.Choices {
		choices[i] = provider.Choice{
			Index: int(c.Index),
			Message: provider.ChatMessage{
				Role:    provider.RoleAssistant,
				Content: c.Message.Content,
			},
			FinishReason: mapFinishReason(c.FinishReason),
		}
	}

	return provider.ChatResponse{
		ID:       resp.ID,
		Model:    resp.Model,
		Provider: providerName,
		Choices:  choices,
		Usage: provider.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
		Created: time.Unix(resp.Created, 0).UTC(),
	}
}

// mapFinishReason normalizes OpenAI finish reasons onto the unified enum.
func mapFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "stop":
		return provider.FinishReasonStop
	case "length":
		return provider.FinishReasonLength
	case "content_filter":
		return provider.FinishReasonContentFilter
	case "tool_calls", "function_call":
		// Tool calling is out of scope for Phase 1; treat as a normal stop.
		return provider.FinishReasonStop
	default:
		return provider.FinishReasonStop
	}
}

// toEmbeddingParams translates a unified EmbeddingRequest into OpenAI params.
func toEmbeddingParams(req provider.EmbeddingRequest, model string) oai.EmbeddingNewParams {
	return oai.EmbeddingNewParams{
		Model: oai.EmbeddingModel(model),
		Input: oai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: req.Input},
	}
}

// fromEmbeddingResponse normalizes an OpenAI embeddings response into the unified
// DTO. OpenAI returns float64 vectors; ModelMesh stores float32 to halve memory
// with negligible precision impact for downstream similarity use.
func fromEmbeddingResponse(providerName string, resp *oai.CreateEmbeddingResponse) provider.EmbeddingResponse {
	data := make([]provider.Embedding, len(resp.Data))
	for i, e := range resp.Data {
		vec := make([]float32, len(e.Embedding))
		for j, f := range e.Embedding {
			vec[j] = float32(f)
		}
		data[i] = provider.Embedding{Index: int(e.Index), Vector: vec}
	}

	return provider.EmbeddingResponse{
		Model:    resp.Model,
		Provider: providerName,
		Data:     data,
		Usage: provider.Usage{
			PromptTokens: int(resp.Usage.PromptTokens),
			TotalTokens:  int(resp.Usage.TotalTokens),
		},
	}
}
