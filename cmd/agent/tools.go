package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// api is the subset of Client the tool layer needs -- letting tests
// substitute a fake without a live server or Postgres.
type api interface {
	ListEvents(ctx context.Context, cursor string) (EventList, error)
	GetEvent(ctx context.Context, eventID string) (Event, error)
	ListSessions(ctx context.Context, eventID, cursor string) (SessionList, error)
	ListRooms(ctx context.Context, eventID, cursor string) (RoomList, error)
}

// ToolDef is the JSON-schema-shaped tool description the model needs to
// decide when and how to call a tool.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

var ErrUnknownTool = errors.New("unknown tool")

func ToolDefinitions() []ToolDef {
	return []ToolDef{
		{
			Name:        "list_events",
			Description: "List the events the caller is a member of. Read-only; results are already scoped to the caller's own authorization -- it never sees events it doesn't belong to.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cursor": map[string]any{"type": "string", "description": "Opaque pagination cursor from a previous list_events call's next_cursor."},
				},
			},
		},
		{
			Name:        "get_event",
			Description: "Get one event by id, including its IANA timezone and start/end. 403 if the caller isn't a member of that event.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"event_id": map[string]any{"type": "string", "description": "The event's UUID."},
				},
				"required": []string{"event_id"},
			},
		},
		{
			Name:        "list_sessions",
			Description: "List an event's sessions (room, speaker, localized start/end, duration). 403 if the caller isn't a member of that event.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"event_id": map[string]any{"type": "string", "description": "The event's UUID."},
					"cursor":   map[string]any{"type": "string", "description": "Opaque pagination cursor from a previous list_sessions call's next_cursor."},
				},
				"required": []string{"event_id"},
			},
		},
		{
			Name:        "list_rooms",
			Description: "List an event's rooms and their capacity. 403 if the caller isn't a member of that event.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"event_id": map[string]any{"type": "string", "description": "The event's UUID."},
					"cursor":   map[string]any{"type": "string", "description": "Opaque pagination cursor from a previous list_rooms call's next_cursor."},
				},
				"required": []string{"event_id"},
			},
		},
	}
}

// Dispatch runs one tool call. It never retries and never widens the
// caller's authority -- a). the api given here is bound to a single
// caller's bearer token; b). an API error (403, 404, ...) is reported back
// to the model as a tool error (isErr=true), not swallowed or papered over.
// The returned Go error is reserved for programmer mistakes (an unknown
// tool name) that the model can't fix by trying again.
func Dispatch(ctx context.Context, a api, name string, argsJSON json.RawMessage) (result string, isErr bool, err error) {
	switch name {
	case "list_events":
		var args struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err), true, nil
		}
		out, apiErr := a.ListEvents(ctx, args.Cursor)
		return encodeToolResult(out, apiErr)

	case "get_event":
		var args struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err), true, nil
		}
		if args.EventID == "" {
			return "missing required argument: event_id", true, nil
		}
		out, apiErr := a.GetEvent(ctx, args.EventID)
		return encodeToolResult(out, apiErr)

	case "list_sessions":
		var args struct {
			EventID string `json:"event_id"`
			Cursor  string `json:"cursor"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err), true, nil
		}
		if args.EventID == "" {
			return "missing required argument: event_id", true, nil
		}
		out, apiErr := a.ListSessions(ctx, args.EventID, args.Cursor)
		return encodeToolResult(out, apiErr)

	case "list_rooms":
		var args struct {
			EventID string `json:"event_id"`
			Cursor  string `json:"cursor"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return fmt.Sprintf("invalid arguments: %v", err), true, nil
		}
		if args.EventID == "" {
			return "missing required argument: event_id", true, nil
		}
		out, apiErr := a.ListRooms(ctx, args.EventID, args.Cursor)
		return encodeToolResult(out, apiErr)

	default:
		return "", false, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
}

func encodeToolResult(v any, apiErr error) (string, bool, error) {
	if apiErr != nil {
		var e *APIError
		if errors.As(apiErr, &e) {
			return fmt.Sprintf("%s: %s", e.Code, e.Message), true, nil
		}
		return apiErr.Error(), true, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false, fmt.Errorf("encoding tool result: %w", err)
	}
	return string(b), false, nil
}
