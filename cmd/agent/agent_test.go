package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

// scriptedModel replays a fixed sequence of Messages API responses so
// agent_test.go proves the tool-call loop's control flow deterministically
// -- no live Anthropic API, no network, no flakiness.
type scriptedModel struct {
	turns []ModelResponse
	calls []ModelRequest // every request the loop made, for assertions
}

func (m *scriptedModel) CreateMessage(ctx context.Context, req ModelRequest) (ModelResponse, error) {
	m.calls = append(m.calls, req)
	if len(m.turns) == 0 {
		panic("scriptedModel: ran out of scripted turns")
	}
	next := m.turns[0]
	m.turns = m.turns[1:]
	return next, nil
}

func textBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

func toolUseBlock(id, name string, input string) ContentBlock {
	return ContentBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)}
}

func TestRun_SingleToolCall_ThenFinalAnswer(t *testing.T) {
	model := &scriptedModel{
		turns: []ModelResponse{
			{StopReason: "tool_use", Content: []ContentBlock{
				textBlock("Let me check."),
				toolUseBlock("call1", "list_events", `{}`),
			}},
			{StopReason: "end_turn", Content: []ContentBlock{
				textBlock("You have one event: Launch."),
			}},
		},
	}
	api := &fakeAPI{
		listEvents: func(ctx context.Context, cursor string) (EventList, error) {
			return EventList{Data: []Event{{ID: "e1", Name: "Launch"}}}, nil
		},
	}

	logged := []map[string]any{}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))

	final, err := Run(context.Background(), RunDeps{
		Model:  model,
		API:    api,
		Logger: logger,
		OnToolCall: func(fields map[string]any) {
			logged = append(logged, fields)
		},
	}, "you are a read-only assistant", "what events do I have?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "You have one event: Launch." {
		t.Fatalf("unexpected final answer: %q", final)
	}
	if len(model.calls) != 2 {
		t.Fatalf("expected 2 model turns, got %d", len(model.calls))
	}
	if len(logged) != 1 {
		t.Fatalf("expected exactly 1 logged tool call, got %d: %+v", len(logged), logged)
	}
	if logged[0]["tool"] != "list_events" {
		t.Fatalf("expected logged tool 'list_events', got %v", logged[0]["tool"])
	}
}

func TestRun_ToolReturns403_SurfacedNotRetried(t *testing.T) {
	model := &scriptedModel{
		turns: []ModelResponse{
			{StopReason: "tool_use", Content: []ContentBlock{
				toolUseBlock("call1", "get_event", `{"event_id":"e1"}`),
			}},
			{StopReason: "end_turn", Content: []ContentBlock{
				textBlock("I don't have access to that event."),
			}},
		},
	}
	api := &fakeAPI{
		getEvent: func(ctx context.Context, eventID string) (Event, error) {
			return Event{}, &APIError{Status: 403, Code: "forbidden", Message: "not a member"}
		},
	}

	final, err := Run(context.Background(), RunDeps{
		Model:  model,
		API:    api,
		Logger: slog.New(slog.NewTextHandler(discardWriter{}, nil)),
	}, "you are a read-only assistant", "what's happening at event e1?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "I don't have access to that event." {
		t.Fatalf("unexpected final answer: %q", final)
	}
	// Exactly 2 model turns -- the loop must not retry the 403 itself.
	if len(model.calls) != 2 {
		t.Fatalf("expected 2 model turns (no retry of the 403), got %d", len(model.calls))
	}
	// The second request must carry the tool_result marked as an error.
	secondReq := model.calls[1]
	found := false
	for _, m := range secondReq.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected a tool_result block with is_error=true fed back to the model")
	}
}

func TestRun_ExceedsMaxTurns_ReturnsError(t *testing.T) {
	turns := []ModelResponse{}
	for i := 0; i < maxAgentTurns+2; i++ {
		turns = append(turns, ModelResponse{StopReason: "tool_use", Content: []ContentBlock{
			toolUseBlock("call1", "list_events", `{}`),
		}})
	}
	model := &scriptedModel{turns: turns}
	api := &fakeAPI{
		listEvents: func(ctx context.Context, cursor string) (EventList, error) {
			return EventList{}, nil
		},
	}

	_, err := Run(context.Background(), RunDeps{
		Model:  model,
		API:    api,
		Logger: slog.New(slog.NewTextHandler(discardWriter{}, nil)),
	}, "you are a read-only assistant", "loop forever")
	if err == nil {
		t.Fatalf("expected an error when the model never stops calling tools")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
