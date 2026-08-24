package response_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func sampleMember(id string) member.Member {
	return member.Member{
		ID:        id,
		EventID:   "evt-1",
		UserID:    "user-1",
		UserEmail: "person@example.com",
		UserName:  "Person",
		Role:      "contributor",
		Version:   1,
		CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewMemberResponse_MapsFields(t *testing.T) {
	got := response.NewMemberResponse(sampleMember("mem-1"))

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "event_id", "user_id", "user_email", "user_name", "role", "version", "created_at", "updated_at"} {
		if _, ok := asMap[field]; !ok {
			t.Errorf("response missing field %q: %s", field, data)
		}
	}
	if asMap["role"] != "contributor" {
		t.Errorf("got role %v, want contributor", asMap["role"])
	}
}

func TestNewMemberListResponse_OmitsNextCursorWhenNil(t *testing.T) {
	page := member.Page{Members: []member.Member{sampleMember("mem-1")}}
	got := response.NewMemberListResponse(page)

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := asMap["next_cursor"]; ok {
		t.Fatalf("next_cursor should be omitted when nil: %s", data)
	}
	if len(got.Data) != 1 {
		t.Fatalf("got %d members, want 1", len(got.Data))
	}
}

func TestNewMemberListResponse_EncodesNextCursorWhenPresent(t *testing.T) {
	page := member.Page{
		Members:    []member.Member{sampleMember("mem-1")},
		NextCursor: &member.Cursor{CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), ID: "mem-1"},
	}
	got := response.NewMemberListResponse(page)
	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatal("expected a non-empty NextCursor")
	}
}
