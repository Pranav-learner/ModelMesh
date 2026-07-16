package anthropic

import (
	"strings"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// This file is the Anthropic translation layer: pure functions converting
// between ModelMesh's unified DTOs and the Anthropic SDK types. No network, no
// state. No Anthropic SDK type escapes this package.

// defaultMaxTokens is used when a ChatRequest does not specify MaxTokens, because
// the Anthropic Messages API requires max_tokens to be set.
const defaultMaxTokens = 1024

// toMessageParams translates a unified ChatRequest into Anthropic message params.
// The model is assumed already resolved (non-empty) by the caller.
//
// Anthropic differs structurally from OpenAI in two ways handled here:
//   - System prompts are not a message role; they are a top-level field. System
//     messages are therefore extracted out of the message list.
//   - max_tokens is required, so a default is applied when unset.
func toMessageParams(req provider.ChatRequest, model string) ant.MessageNewParams {
	system, turns := splitSystem(req.Messages)

	params := ant.MessageNewParams{
		Model:     ant.Model(model),
		Messages:  turns,
		MaxTokens: int64(defaultMaxTokens),
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = int64(req.MaxTokens)
	}
	if len(system) > 0 {
		params.System = system
	}
	if req.Temperature != nil {
		params.Temperature = ant.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = ant.Float(*req.TopP)
	}
	if len(req.Stop) > 0 {
		params.StopSequences = req.Stop
	}
	return params
}

// splitSystem separates system messages (which become the top-level System
// field) from user/assistant conversational turns.
func splitSystem(msgs []provider.ChatMessage) ([]ant.TextBlockParam, []ant.MessageParam) {
	var system []ant.TextBlockParam
	turns := make([]ant.MessageParam, 0, len(msgs))

	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			system = append(system, ant.TextBlockParam{Text: m.Content})
		case provider.RoleAssistant:
			turns = append(turns, ant.NewAssistantMessage(ant.NewTextBlock(m.Content)))
		default: // RoleUser and any unexpected role
			turns = append(turns, ant.NewUserMessage(ant.NewTextBlock(m.Content)))
		}
	}
	return system, turns
}

// fromMessage normalizes an Anthropic Message into a unified ChatResponse.
//
// Anthropic returns content as an array of typed blocks; text blocks are
// concatenated into a single assistant message. Anthropic does not return a
// creation timestamp, so the caller-supplied now() is used.
func fromMessage(providerName string, msg *ant.Message, now time.Time) provider.ChatResponse {
	var content strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}

	promptTokens := int(msg.Usage.InputTokens)
	completionTokens := int(msg.Usage.OutputTokens)

	return provider.ChatResponse{
		ID:       msg.ID,
		Model:    string(msg.Model),
		Provider: providerName,
		Choices: []provider.Choice{
			{
				Index: 0,
				Message: provider.ChatMessage{
					Role:    provider.RoleAssistant,
					Content: content.String(),
				},
				FinishReason: mapStopReason(string(msg.StopReason)),
			},
		},
		Usage: provider.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		Created: now,
	}
}

// mapStopReason normalizes Anthropic stop reasons onto the unified enum.
func mapStopReason(reason string) provider.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return provider.FinishReasonStop
	case "max_tokens":
		return provider.FinishReasonLength
	case "tool_use":
		// Tool use is out of scope for Phase 1; treat as a normal stop.
		return provider.FinishReasonStop
	default:
		return provider.FinishReasonStop
	}
}
