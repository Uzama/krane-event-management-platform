package request_test

import (
	"encoding/json"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/request"
)

func TestAddMemberRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.AddMemberRequest
	if err := json.Unmarshal([]byte(`{"email":"person@example.com","role":"attendee"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestAddMemberRequest_Validate_RejectsMissingEmail(t *testing.T) {
	var r request.AddMemberRequest
	if err := json.Unmarshal([]byte(`{"role":"attendee"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["email"]; !ok {
		t.Errorf("issues missing email: %v", issues)
	}
}

func TestAddMemberRequest_Validate_RejectsUnknownRole(t *testing.T) {
	var r request.AddMemberRequest
	if err := json.Unmarshal([]byte(`{"email":"person@example.com","role":"organiser"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["role"]; !ok {
		t.Errorf("issues missing role: %v", issues)
	}
}

func TestAddMemberRequest_ToCreateInput(t *testing.T) {
	r := request.AddMemberRequest{Email: "person@example.com", Role: "contributor"}
	in := r.ToCreateInput()
	if in.Email != "person@example.com" || in.Role != "contributor" {
		t.Fatalf("got %+v", in)
	}
}

func TestAssignRoleRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.AssignRoleRequest
	if err := json.Unmarshal([]byte(`{"role":"contributor","version":1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestAssignRoleRequest_Validate_RejectsUnknownRole(t *testing.T) {
	var r request.AssignRoleRequest
	if err := json.Unmarshal([]byte(`{"role":"organiser","version":1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["role"]; !ok {
		t.Errorf("issues missing role: %v", issues)
	}
}

func TestAssignRoleRequest_Validate_RejectsMissingVersion(t *testing.T) {
	var r request.AssignRoleRequest
	if err := json.Unmarshal([]byte(`{"role":"contributor"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["version"]; !ok {
		t.Errorf("issues missing version: %v", issues)
	}
}
