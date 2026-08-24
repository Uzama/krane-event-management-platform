package response_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func sampleEvent(id string) event.Event {
	return event.Event{
		ID:        id,
		Name:      "Conf",
		Timezone:  "Asia/Colombo",
		StartsAt:  time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
		EndsAt:    time.Date(2026, 3, 15, 11, 0, 0, 0, time.UTC),
		Version:   1,
		CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewEventResponse_MapsFieldsAndOmitsDeletedAt(t *testing.T) {
	got := response.NewEventResponse(sampleEvent("evt-1"))

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := asMap["deleted_at"]; ok {
		t.Fatalf("response leaked deleted_at: %s", data)
	}
	for _, field := range []string{"id", "name", "timezone", "starts_at", "ends_at", "version", "created_at", "updated_at"} {
		if _, ok := asMap[field]; !ok {
			t.Errorf("response missing %q: %s", field, data)
		}
	}
	if asMap["id"] != "evt-1" {
		t.Errorf("got id %v, want evt-1", asMap["id"])
	}
}

func TestNewEventListResponse_NoCursorWhenNoFurtherPage(t *testing.T) {
	page := event.Page{Events: []event.Event{sampleEvent("evt-1")}}
	got := response.NewEventListResponse(page)

	if len(got.Data) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Data))
	}
	if got.NextCursor != nil {
		t.Fatalf("got NextCursor %v, want nil", *got.NextCursor)
	}
}

func TestNewEventListResponse_EncodesCursorWhenFurtherPageExists(t *testing.T) {
	page := event.Page{
		Events:     []event.Event{sampleEvent("evt-1")},
		NextCursor: &event.Cursor{CreatedAt: time.Now(), ID: "evt-1"},
	}
	got := response.NewEventListResponse(page)

	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatalf("got NextCursor %v, want a non-empty opaque token", got.NextCursor)
	}
}
