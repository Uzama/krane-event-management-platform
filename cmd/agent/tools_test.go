package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeAPI lets tools_test.go exercise Dispatch without a live server --
// client_test.go already proves Client itself is correct against the real
// stack; this proves the tool layer routes to the right method and shapes
// errors correctly.
type fakeAPI struct {
	listEvents   func(ctx context.Context, cursor string) (EventList, error)
	getEvent     func(ctx context.Context, eventID string) (Event, error)
	listSessions func(ctx context.Context, eventID, cursor string) (SessionList, error)
	listRooms    func(ctx context.Context, eventID, cursor string) (RoomList, error)
}

func (f *fakeAPI) ListEvents(ctx context.Context, cursor string) (EventList, error) {
	return f.listEvents(ctx, cursor)
}
func (f *fakeAPI) GetEvent(ctx context.Context, eventID string) (Event, error) {
	return f.getEvent(ctx, eventID)
}
func (f *fakeAPI) ListSessions(ctx context.Context, eventID, cursor string) (SessionList, error) {
	return f.listSessions(ctx, eventID, cursor)
}
func (f *fakeAPI) ListRooms(ctx context.Context, eventID, cursor string) (RoomList, error) {
	return f.listRooms(ctx, eventID, cursor)
}

func TestDispatch_ListEvents_RoutesArgsAndReturnsJSON(t *testing.T) {
	api := &fakeAPI{
		listEvents: func(ctx context.Context, cursor string) (EventList, error) {
			if cursor != "abc" {
				t.Fatalf("expected cursor %q, got %q", "abc", cursor)
			}
			return EventList{Data: []Event{{ID: "e1", Name: "Launch"}}}, nil
		},
	}

	out, isErr, err := Dispatch(context.Background(), api, "list_events", json.RawMessage(`{"cursor":"abc"}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("expected a successful result, got a tool error: %s", out)
	}
	var decoded EventList
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("result isn't valid EventList JSON: %v (%s)", err, out)
	}
	if len(decoded.Data) != 1 || decoded.Data[0].ID != "e1" {
		t.Fatalf("unexpected result: %s", out)
	}
}

func TestDispatch_GetEvent_MissingEventID_IsToolError(t *testing.T) {
	api := &fakeAPI{}

	_, isErr, err := Dispatch(context.Background(), api, "get_event", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !isErr {
		t.Fatalf("expected a tool error for a missing required argument")
	}
}

func TestDispatch_APIError_SurfacesAsToolError_NotSwallowed(t *testing.T) {
	api := &fakeAPI{
		getEvent: func(ctx context.Context, eventID string) (Event, error) {
			return Event{}, &APIError{Status: 403, Code: "forbidden", Message: "not a member"}
		},
	}

	out, isErr, err := Dispatch(context.Background(), api, "get_event", json.RawMessage(`{"event_id":"e1"}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !isErr {
		t.Fatalf("expected the 403 to surface as a tool error, got a normal result: %s", out)
	}
	if out == "" {
		t.Fatalf("expected a non-empty error message for the model")
	}
}

func TestDispatch_UnknownTool_ReturnsError(t *testing.T) {
	api := &fakeAPI{}

	_, _, err := Dispatch(context.Background(), api, "delete_event", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}

func TestToolDefinitions_CoverAllFourReadOnlyTools(t *testing.T) {
	names := map[string]bool{}
	for _, d := range ToolDefinitions() {
		names[d.Name] = true
	}
	for _, want := range []string{"list_events", "get_event", "list_sessions", "list_rooms"} {
		if !names[want] {
			t.Errorf("missing tool definition %q", want)
		}
	}
	if len(names) != 4 {
		t.Errorf("expected exactly 4 read-only tools, got %d: %v", len(names), names)
	}
}
