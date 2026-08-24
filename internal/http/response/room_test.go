package response_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func sampleRoom(id string) room.Room {
	capacity := 50
	return room.Room{
		ID:        id,
		EventID:   "event-1",
		Name:      "Hall A",
		Capacity:  &capacity,
		Version:   1,
		CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewRoomResponse_MapsFields(t *testing.T) {
	got := response.NewRoomResponse(sampleRoom("room-1"))

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "event_id", "name", "capacity", "version", "created_at", "updated_at"} {
		if _, ok := asMap[field]; !ok {
			t.Errorf("response missing %q: %s", field, data)
		}
	}
	if asMap["id"] != "room-1" {
		t.Errorf("got id %v, want room-1", asMap["id"])
	}
	if asMap["capacity"] != float64(50) {
		t.Errorf("got capacity %v, want 50", asMap["capacity"])
	}
}

func TestNewRoomResponse_NullCapacity(t *testing.T) {
	r := sampleRoom("room-1")
	r.Capacity = nil
	got := response.NewRoomResponse(r)

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := asMap["capacity"]; !ok || v != nil {
		t.Errorf("got capacity %v (present=%v), want key present with null value", v, ok)
	}
}

func TestNewRoomListResponse_NoCursorWhenNoFurtherPage(t *testing.T) {
	page := room.Page{Rooms: []room.Room{sampleRoom("room-1")}}
	got := response.NewRoomListResponse(page)

	if len(got.Data) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Data))
	}
	if got.NextCursor != nil {
		t.Fatalf("got NextCursor %v, want nil", *got.NextCursor)
	}
}

func TestNewRoomListResponse_EncodesCursorWhenFurtherPageExists(t *testing.T) {
	page := room.Page{
		Rooms:      []room.Room{sampleRoom("room-1")},
		NextCursor: &room.Cursor{CreatedAt: time.Now(), ID: "room-1"},
	}
	got := response.NewRoomListResponse(page)

	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatalf("got NextCursor %v, want a non-empty opaque token", got.NextCursor)
	}
}
