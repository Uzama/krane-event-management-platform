package request_test

import (
	"encoding/json"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/request"
)

func validCreateEventJSON() string {
	return `{
		"name": "Conf",
		"timezone": "Asia/Colombo",
		"starts_at": "2026-03-15T10:00:00Z",
		"ends_at": "2026-03-15T11:00:00Z"
	}`
}

func TestCreateEventRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.CreateEventRequest
	if err := json.Unmarshal([]byte(validCreateEventJSON()), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestCreateEventRequest_Validate_RejectsMissingRequiredFields(t *testing.T) {
	var r request.CreateEventRequest
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	for _, field := range []string{"name", "timezone", "starts_at", "ends_at"} {
		if _, ok := issues[field]; !ok {
			t.Errorf("issues missing %q: %v", field, issues)
		}
	}
}

func TestCreateEventRequest_Validate_RejectsEndsAtNotAfterStartsAt(t *testing.T) {
	var r request.CreateEventRequest
	if err := json.Unmarshal([]byte(`{
		"name": "Conf",
		"timezone": "Asia/Colombo",
		"starts_at": "2026-03-15T11:00:00Z",
		"ends_at": "2026-03-15T10:00:00Z"
	}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["ends_at"]; !ok {
		t.Fatalf("issues missing ends_at: %v", issues)
	}
}

func TestCreateEventRequest_Validate_RejectsUnknownTimezone(t *testing.T) {
	var r request.CreateEventRequest
	if err := json.Unmarshal([]byte(`{
		"name": "Conf",
		"timezone": "Not/A_Zone",
		"starts_at": "2026-03-15T10:00:00Z",
		"ends_at": "2026-03-15T11:00:00Z"
	}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["timezone"]; !ok {
		t.Fatalf("issues missing timezone: %v", issues)
	}
}

func TestCreateEventRequest_ToCreateInput(t *testing.T) {
	var r request.CreateEventRequest
	if err := json.Unmarshal([]byte(validCreateEventJSON()), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	in := r.ToCreateInput()
	if in.Name != "Conf" || in.Timezone != "Asia/Colombo" {
		t.Fatalf("got %+v", in)
	}
	if !in.EndsAt.After(in.StartsAt) {
		t.Fatalf("EndsAt %v not after StartsAt %v", in.EndsAt, in.StartsAt)
	}
}

func TestPatchEventRequest_Validate_AcceptsAbsentFields(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestPatchEventRequest_Validate_RequiresVersion(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["version"]; !ok {
		t.Fatalf("issues missing version: %v", issues)
	}
}

func TestPatchEventRequest_Validate_RejectsExactlyOneOfStartsEndsSet(t *testing.T) {
	cases := []string{
		`{"version": 1, "starts_at": "2026-03-15T10:00:00Z"}`,
		`{"version": 1, "ends_at": "2026-03-15T11:00:00Z"}`,
	}
	for _, body := range cases {
		var r request.PatchEventRequest
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("Unmarshal(%s): %v", body, err)
		}
		issues := r.Validate()
		if len(issues) == 0 {
			t.Fatalf("Validate(%s): got no issues, want a both-or-neither error", body)
		}
	}
}

func TestPatchEventRequest_Validate_AcceptsBothStartsAndEndsSet(t *testing.T) {
	var r request.PatchEventRequest
	body := `{"version": 1, "starts_at": "2026-03-15T10:00:00Z", "ends_at": "2026-03-15T11:00:00Z"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestPatchEventRequest_Validate_RejectsEndsAtNotAfterStartsAtWhenBothSet(t *testing.T) {
	var r request.PatchEventRequest
	body := `{"version": 1, "starts_at": "2026-03-15T11:00:00Z", "ends_at": "2026-03-15T10:00:00Z"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) == 0 {
		t.Fatalf("got no issues, want an ends_at error")
	}
}

func TestPatchEventRequest_Validate_DistinguishesAbsentNullAndValueForDescription(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		var r request.PatchEventRequest
		if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.Description.Set {
			t.Fatalf("Description.Set = true, want false")
		}
	})
	t.Run("explicit null", func(t *testing.T) {
		var r request.PatchEventRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "description": null}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Description.Set || r.Description.Value != nil {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=nil", r.Description.Set, r.Description.Value)
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		var r request.PatchEventRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "description": "hi"}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Description.Set || r.Description.Value == nil || *r.Description.Value != "hi" {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=\"hi\"", r.Description.Set, r.Description.Value)
		}
	})
}

func TestPatchEventRequest_ToPatch(t *testing.T) {
	var r request.PatchEventRequest
	body := `{"version": 3, "name": "New name"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	patch := r.ToPatch()
	if !patch.Name.Set || patch.Name.Value != "New name" {
		t.Fatalf("got %+v", patch)
	}
	if patch.Description.Set || patch.Timezone.Set || patch.StartsAt.Set || patch.EndsAt.Set {
		t.Fatalf("unset fields leaked into patch: %+v", patch)
	}
}
