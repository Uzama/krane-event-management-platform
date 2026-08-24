package response_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func sampleInvitation(id string) invitation.Invitation {
	userID := "user-1"
	return invitation.Invitation{
		ID:        id,
		EventID:   "event-1",
		UserID:    &userID,
		Email:     "person@example.com",
		Role:      "attendee",
		CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewInvitationResponse_MapsFields(t *testing.T) {
	got := response.NewInvitationResponse(sampleInvitation("inv-1"))

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "event_id", "user_id", "email", "role", "created_at", "updated_at"} {
		if _, ok := asMap[field]; !ok {
			t.Errorf("response missing %q: %s", field, data)
		}
	}
	if asMap["email"] != "person@example.com" {
		t.Errorf("got email %v, want person@example.com -- email must be visible (D14), not redacted", asMap["email"])
	}
}

// TestNewInvitationResponse_NilUserID_KeyPresentAsNull proves an
// unresolved invitee (D2: email never signed in) renders user_id as an
// explicit null, not an omitted key -- the same convention EventResponse
// uses for Description, distinct from MemberResponse's role-gated
// omitempty.
func TestNewInvitationResponse_NilUserID_KeyPresentAsNull(t *testing.T) {
	inv := sampleInvitation("inv-1")
	inv.UserID = nil
	got := response.NewInvitationResponse(inv)

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := asMap["user_id"]; !ok || v != nil {
		t.Errorf("got user_id %v (present=%v), want key present with null value", v, ok)
	}
}

func TestNewInvitationListResponse_NoCursorWhenNoFurtherPage(t *testing.T) {
	page := invitation.Page{Invitations: []invitation.Invitation{sampleInvitation("inv-1")}}
	got := response.NewInvitationListResponse(page)

	if len(got.Data) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Data))
	}
	if got.NextCursor != nil {
		t.Fatalf("got NextCursor %v, want nil", *got.NextCursor)
	}
}

func TestNewInvitationListResponse_EncodesCursorWhenFurtherPageExists(t *testing.T) {
	page := invitation.Page{
		Invitations: []invitation.Invitation{sampleInvitation("inv-1")},
		NextCursor:  &invitation.Cursor{CreatedAt: time.Now(), ID: "inv-1"},
	}
	got := response.NewInvitationListResponse(page)

	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatalf("got NextCursor %v, want a non-empty opaque token", got.NextCursor)
	}
}
