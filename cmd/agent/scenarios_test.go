package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestScenarios_ReturnsThreeRequiredDemosInOrder pins FEATURES.md item 15's
// "3 scripted scenarios": a normal read, a permission boundary, a
// composition -- in that order, each prompt aimed at the right event.
func TestScenarios_ReturnsThreeRequiredDemosInOrder(t *testing.T) {
	const eventID, foreignEventID, localDate = "ev-mine", "ev-foreign", "2026-06-15"

	got := Scenarios(eventID, foreignEventID, localDate)

	wantNames := []string{"normal-read", "permission-boundary", "composition"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d scenarios, want %d: %+v", len(got), len(wantNames), got)
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("scenario %d is %q, want %q", i, got[i].Name, want)
		}
		if strings.TrimSpace(got[i].Prompt) == "" {
			t.Errorf("scenario %q has an empty prompt", want)
		}
	}
	if !strings.Contains(got[1].Prompt, foreignEventID) {
		t.Errorf("permission-boundary prompt must target the foreign event %q: %q", foreignEventID, got[1].Prompt)
	}
	if strings.Contains(got[1].Prompt, eventID) {
		t.Errorf("permission-boundary prompt must not mention the caller's own event %q: %q", eventID, got[1].Prompt)
	}
	if !strings.Contains(got[2].Prompt, eventID) || !strings.Contains(got[2].Prompt, localDate) {
		t.Errorf("composition prompt must name the event %q and the local date %q: %q", eventID, localDate, got[2].Prompt)
	}
}

// TestRun_CompositionScenario_ResolvesEventThenRoomsThenSessionsBeforeAnswering
// proves the composition scenario's shape through the real loop: the agent
// resolves the event (for its timezone), the rooms, and that day's sessions
// -- three reads, in that order, all scoped to the same event -- and only
// then answers with the room that has nothing booked. The assertion is on
// the SEQUENCE of tool calls, not just the final text: a model that answered
// without looking, or looked at the wrong event, fails here.
func TestRun_CompositionScenario_ResolvesEventThenRoomsThenSessionsBeforeAnswering(t *testing.T) {
	const eventID, localDate = "ev-mine", "2026-06-15"

	model := &scriptedModel{
		turns: []ModelResponse{
			{StopReason: "tool_use", Content: []ContentBlock{
				textBlock("First I need the event's timezone."),
				toolUseBlock("c1", "get_event", `{"event_id":"`+eventID+`"}`),
			}},
			{StopReason: "tool_use", Content: []ContentBlock{
				toolUseBlock("c2", "list_rooms", `{"event_id":"`+eventID+`"}`),
			}},
			{StopReason: "tool_use", Content: []ContentBlock{
				toolUseBlock("c3", "list_sessions", `{"event_id":"`+eventID+`"}`),
			}},
			{StopReason: "end_turn", Content: []ContentBlock{
				textBlock("On 2026-06-15 (Asia/Colombo), Hall A is booked 09:00-10:00; Hall B has nothing scheduled, so Hall B is free."),
			}},
		},
	}

	var seenEventIDs []string
	api := &fakeAPI{
		getEvent: func(ctx context.Context, id string) (Event, error) {
			seenEventIDs = append(seenEventIDs, id)
			return Event{ID: id, Name: "Launch", Timezone: "Asia/Colombo", StartsAt: "2026-06-01T00:00:00+05:30", EndsAt: "2026-06-30T23:59:59+05:30"}, nil
		},
		listRooms: func(ctx context.Context, id, cursor string) (RoomList, error) {
			seenEventIDs = append(seenEventIDs, id)
			return RoomList{Data: []Room{{ID: "r-a", EventID: id, Name: "Hall A"}, {ID: "r-b", EventID: id, Name: "Hall B"}}}, nil
		},
		listSessions: func(ctx context.Context, id, cursor string) (SessionList, error) {
			seenEventIDs = append(seenEventIDs, id)
			return SessionList{Data: []Session{{ID: "s1", EventID: id, RoomID: "r-a", Title: "Keynote", StartsAt: localDate + "T09:00:00+05:30", EndsAt: localDate + "T10:00:00+05:30"}}}, nil
		},
		listEvents: func(ctx context.Context, cursor string) (EventList, error) {
			t.Errorf("list_events must not be needed once the event id is known")
			return EventList{}, nil
		},
	}

	var logged []map[string]any
	final, err := Run(context.Background(), RunDeps{
		Model:      model,
		API:        api,
		Logger:     slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		OnToolCall: func(fields map[string]any) { logged = append(logged, fields) },
	}, systemPrompt, Scenarios(eventID, "ev-foreign", localDate)[2].Prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{"get_event", "list_rooms", "list_sessions"}
	if len(logged) != len(wantSequence) {
		t.Fatalf("logged %d tool calls, want exactly %d (%v): %+v", len(logged), len(wantSequence), wantSequence, logged)
	}
	for i, want := range wantSequence {
		if logged[i]["tool"] != want {
			t.Errorf("tool call %d was %v, want %q -- the composition must resolve event, rooms, then sessions before answering", i, logged[i]["tool"], want)
		}
	}
	for i, id := range seenEventIDs {
		if id != eventID {
			t.Errorf("API call %d was scoped to event %q, want %q", i, id, eventID)
		}
	}
	if len(model.calls) != 4 {
		t.Errorf("expected 4 model turns (3 tool rounds + the answer), got %d", len(model.calls))
	}
	if !strings.Contains(final, "Hall B") || !strings.Contains(final, "free") {
		t.Errorf("final answer should name the free room: %q", final)
	}
}
