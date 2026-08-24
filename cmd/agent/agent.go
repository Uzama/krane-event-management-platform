package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// maxAgentTurns bounds the tool-call loop. The agent is read-only and
// small (3-4 tools); a well-behaved model answers in a handful of turns.
// A model stuck calling tools past this is a bug, not patience -- Run
// returns an error rather than looping forever.
const maxAgentTurns = 8

// ModelClient is the one method Run needs from an LLM -- kept narrow so
// tests can script it and the real Anthropic client (anthropic.go) is a
// thin adapter, not the thing under test.
type ModelClient interface {
	CreateMessage(ctx context.Context, req ModelRequest) (ModelResponse, error)
}

type ModelRequest struct {
	System   string
	Messages []Message
	Tools    []ToolDef
}

type Message struct {
	Role    string // "user" or "assistant"
	Content []ContentBlock
}

// ContentBlock is a union of the block shapes the loop needs: text,
// tool_use (from the model), and tool_result (from us, back to the
// model). Unused fields are zero for a given Type.
type ContentBlock struct {
	Type string // "text" | "tool_use" | "tool_result"

	Text string // text

	ID    string          // tool_use: the call id
	Name  string          // tool_use: the tool name
	Input json.RawMessage // tool_use: the arguments

	ToolUseID string // tool_result: which call this answers
	Content   string // tool_result: the result text
	IsError   bool   // tool_result: true if the tool failed
}

type ModelResponse struct {
	StopReason string // "tool_use" | "end_turn" | ...
	Content    []ContentBlock
}

type RunDeps struct {
	Model      ModelClient
	API        api
	Logger     *slog.Logger
	OnToolCall func(fields map[string]any) // optional, for tests/observability
}

// Run drives one scenario to completion: send the prompt, execute any
// tool_use blocks the model asks for against API (bound to the caller's
// own bearer token -- see client.go), feed the results back, repeat until
// the model stops asking for tools. It never retries a failed tool call
// itself; a 403/404/etc. is reported to the model exactly once, as a
// tool_result with is_error=true, and the model decides what to say.
func Run(ctx context.Context, deps RunDeps, systemPrompt, userPrompt string) (string, error) {
	messages := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: userPrompt}}},
	}

	for turn := 0; turn < maxAgentTurns; turn++ {
		resp, err := deps.Model.CreateMessage(ctx, ModelRequest{
			System:   systemPrompt,
			Messages: messages,
			Tools:    ToolDefinitions(),
		})
		if err != nil {
			return "", fmt.Errorf("model turn %d: %w", turn, err)
		}

		messages = append(messages, Message{Role: "assistant", Content: resp.Content})

		if resp.StopReason != "tool_use" {
			return finalText(resp.Content), nil
		}

		var toolResults []ContentBlock
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}

			result, isErr, err := Dispatch(ctx, deps.API, block.Name, block.Input)
			if err != nil {
				return "", fmt.Errorf("dispatching tool %q: %w", block.Name, err)
			}

			if deps.Logger != nil {
				deps.Logger.Info("tool_call", "turn", turn, "tool", block.Name, "args", string(block.Input), "is_error", isErr)
			}
			if deps.OnToolCall != nil {
				deps.OnToolCall(map[string]any{"turn": turn, "tool": block.Name, "args": string(block.Input), "is_error": isErr})
			}

			toolResults = append(toolResults, ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
				IsError:   isErr,
			})
		}

		messages = append(messages, Message{Role: "user", Content: toolResults})
	}

	return "", fmt.Errorf("exceeded %d turns without a final answer", maxAgentTurns)
}

func finalText(blocks []ContentBlock) string {
	for _, b := range blocks {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}
