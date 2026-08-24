package request_test

import (
	"encoding/json"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/request"
)

func TestInvitationCreateRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.InvitationCreateRequest
	if err := json.Unmarshal([]byte(`{"email":"person@example.com","role":"attendee"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestInvitationCreateRequest_Validate_RejectsMissingEmail(t *testing.T) {
	var r request.InvitationCreateRequest
	if err := json.Unmarshal([]byte(`{"role":"attendee"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["email"]; !ok {
		t.Errorf("issues missing email: %v", issues)
	}
}

func TestInvitationCreateRequest_Validate_RejectsUnknownRole(t *testing.T) {
	var r request.InvitationCreateRequest
	if err := json.Unmarshal([]byte(`{"email":"person@example.com","role":"organiser"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["role"]; !ok {
		t.Errorf("issues missing role: %v", issues)
	}
}

// TestInvitationCreateRequest_Validate_DoesNotRejectAdminRole proves the
// role-escalation guard is deliberately NOT enforced here -- it's an
// authorization decision (403), not a shape-validation one (422); a
// contributor sending role=admin passes this Validate() and is rejected
// later, by the repository, as ErrForbidden.
func TestInvitationCreateRequest_Validate_DoesNotRejectAdminRole(t *testing.T) {
	var r request.InvitationCreateRequest
	if err := json.Unmarshal([]byte(`{"email":"person@example.com","role":"admin"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none -- role=admin is a shape-valid request regardless of who sends it", issues)
	}
}

func TestInvitationCreateRequest_ToCreateInput(t *testing.T) {
	r := request.InvitationCreateRequest{Email: "person@example.com", Role: "contributor"}
	in := r.ToCreateInput()
	if in.Email != "person@example.com" || in.Role != "contributor" {
		t.Fatalf("got %+v", in)
	}
}
