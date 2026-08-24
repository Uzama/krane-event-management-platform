package response_test

import (
	"encoding/json"
	"strings"
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

func TestNewMemberResponse_AdminSeesEmail_MapsAllFields(t *testing.T) {
	got := response.NewMemberResponse(sampleMember("mem-1"), "admin")

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
	if asMap["user_email"] != "person@example.com" {
		t.Errorf("got user_email %v, want person@example.com -- admin must see it", asMap["user_email"])
	}
}

// TestNewMemberResponse_ContributorNeverSeesEmailKey is item 10's must:
// test (a) at the presenter level -- D11 (docs/requirements.md §7.2): email
// is admin-only PII. The assertion is key ABSENCE, not an empty value --
// json's omitempty on a nil *string drops the key, which is what "no email
// key anywhere" (CLAUDE.md) actually requires.
func TestNewMemberResponse_ContributorNeverSeesEmailKey(t *testing.T) {
	got := response.NewMemberResponse(sampleMember("mem-1"), "contributor")

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	assertNoKeyContaining(t, data, "email")
}

func TestNewMemberListResponse_OmitsNextCursorWhenNil(t *testing.T) {
	page := member.Page{Members: []member.Member{sampleMember("mem-1")}}
	got := response.NewMemberListResponse(page, "admin")

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
	got := response.NewMemberListResponse(page, "admin")
	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatal("expected a non-empty NextCursor")
	}
}

// TestNewMemberListResponse_ContributorNeverSeesEmailKey_AcrossRoster is the
// list-shape counterpart to the single-response test -- the roster is a
// JSON array, and a map-only key walk would silently miss every element's
// user_email, so this exercises the full list envelope with 2+ rows.
func TestNewMemberListResponse_ContributorNeverSeesEmailKey_AcrossRoster(t *testing.T) {
	page := member.Page{Members: []member.Member{sampleMember("mem-1"), sampleMember("mem-2")}}
	got := response.NewMemberListResponse(page, "contributor")

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	assertNoKeyContaining(t, data, "email")
}

// assertNoKeyContaining walks the full JSON tree -- maps AND slices, at
// every nesting depth -- and fails if any object key contains substr,
// case-insensitively. It is deliberately KEY-only: a value smuggled under a
// differently-named key (e.g. {"contact": "a@b.com"}) would not be caught.
// That is an accepted limit for this feature, not a general PII scanner.
func assertNoKeyContaining(t *testing.T, body []byte, substr string) {
	t.Helper()

	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	walkNoKeyContaining(t, tree, substr, body)
}

func walkNoKeyContaining(t *testing.T, node any, substr string, body []byte) {
	t.Helper()

	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if strings.Contains(strings.ToLower(k), strings.ToLower(substr)) {
				t.Errorf("found key %q containing %q in response body: %s", k, substr, body)
			}
			walkNoKeyContaining(t, child, substr, body)
		}
	case []any:
		for _, child := range v {
			walkNoKeyContaining(t, child, substr, body)
		}
	}
}
