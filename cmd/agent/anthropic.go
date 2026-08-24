package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AnthropicClient is a minimal net/http adapter to the Messages API's
// tool-use loop -- CLAUDE.md prefers the standard library over a
// dependency, and the official Go SDK's dependency tree (AWS/GCP/gRPC/
// OTel, for Bedrock/Vertex/MCP support this agent doesn't use) was judged
// out of proportion to what a handful of JSON fields need. It implements
// ModelClient (agent.go), nothing more.
type AnthropicClient struct {
	apiKey  string
	model   string
	http    *http.Client
	baseURL string
}

const anthropicAPIVersion = "2023-06-01"
const defaultAnthropicBaseURL = "https://api.anthropic.com"
const defaultMaxTokens = 1024

func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:  apiKey,
		model:   model,
		http:    http.DefaultClient,
		baseURL: defaultAnthropicBaseURL,
	}
}

// wire types mirror the Messages API's JSON shape exactly; they exist only
// to marshal/unmarshal at this one boundary -- ModelRequest/ModelResponse
// (agent.go) are what the rest of the agent works with.

type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type wireContentBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
}

type wireResponse struct {
	StopReason string             `json:"stop_reason"`
	Content    []wireContentBlock `json:"content"`
}

func (c *AnthropicClient) CreateMessage(ctx context.Context, req ModelRequest) (ModelResponse, error) {
	wireReq := wireRequest{
		Model:     c.model,
		MaxTokens: defaultMaxTokens,
		System:    req.System,
		Messages:  toWireMessages(req.Messages),
		Tools:     toWireTools(req.Tools),
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ModelResponse{}, fmt.Errorf("Anthropic API returned %d: %s", resp.StatusCode, respBody)
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return ModelResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	return ModelResponse{
		StopReason: wireResp.StopReason,
		Content:    fromWireBlocks(wireResp.Content),
	}, nil
}

func toWireMessages(messages []Message) []wireMessage {
	out := make([]wireMessage, len(messages))
	for i, m := range messages {
		out[i] = wireMessage{Role: m.Role, Content: toWireBlocks(m.Content)}
	}
	return out
}

func toWireBlocks(blocks []ContentBlock) []wireContentBlock {
	out := make([]wireContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = wireContentBlock{
			Type:      b.Type,
			Text:      b.Text,
			ID:        b.ID,
			Name:      b.Name,
			Input:     b.Input,
			ToolUseID: b.ToolUseID,
			Content:   b.Content,
			IsError:   b.IsError,
		}
	}
	return out
}

func fromWireBlocks(blocks []wireContentBlock) []ContentBlock {
	out := make([]ContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = ContentBlock{
			Type:      b.Type,
			Text:      b.Text,
			ID:        b.ID,
			Name:      b.Name,
			Input:     b.Input,
			ToolUseID: b.ToolUseID,
			Content:   b.Content,
			IsError:   b.IsError,
		}
	}
	return out
}

func toWireTools(tools []ToolDef) []wireTool {
	out := make([]wireTool, len(tools))
	for i, t := range tools {
		out[i] = wireTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	return out
}
