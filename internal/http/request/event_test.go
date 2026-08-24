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

// TestPatchEventRequest_Validate_RejectsExplicitNullForName is item 20's
// missing coverage on the non-nullable half of Name's three states: the
// existing RejectsBlankName-style test only sent whitespace (""), never
// proving that a literal JSON null -- which opt.Optional[string] accepts
// silently as Set=true, Value="" (encoding/json's documented no-op-on-null
// behavior for a non-pointer destination) -- is actually caught by the
// same TrimSpace=="" check, not merely assumed to be from reading the code.
func TestPatchEventRequest_Validate_RejectsExplicitNullForName(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "name": null}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !r.Name.Set {
		t.Fatalf("Name.Set = false, want true (opt.Optional[string] treats null as Set=true, Value=\"\")")
	}
	issues := r.Validate()
	if _, ok := issues["name"]; !ok {
		t.Fatalf("issues missing name: %v -- explicit null must be rejected, not silently applied as a blank name", issues)
	}
}

// TestPatchEventRequest_Validate_RejectsExplicitNullForTimezone is item
// 20's missing coverage: no test exercised PatchEventRequest's timezone
// validation at all before this (only CreateEventRequest's was tested).
func TestPatchEventRequest_Validate_RejectsExplicitNullForTimezone(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "timezone": null}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !r.Timezone.Set {
		t.Fatalf("Timezone.Set = false, want true")
	}
	issues := r.Validate()
	if _, ok := issues["timezone"]; !ok {
		t.Fatalf("issues missing timezone: %v -- explicit null must be rejected", issues)
	}
}

// TestPatchEventRequest_Validate_AcceptsValidTimezoneOnPatch is the
// positive control for the test above -- proves a real timezone value on
// PATCH passes validation, so the null test isn't vacuously trivial (e.g.
// failing to unmarshal at all would also produce a "timezone" issue for
// the wrong reason).
func TestPatchEventRequest_Validate_AcceptsValidTimezoneOnPatch(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "timezone": "Asia/Colombo"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

// TestPatchEventRequest_Validate_RejectsExplicitNullForBothStartsAndEnds
// is item 20's missing coverage: the existing starts_at/ends_at tests all
// use real timestamps (one-sided, both-set, ends-before-starts); none
// proves null-for-both is rejected. time.Time's own UnmarshalJSON treats a
// literal null as a no-op (leaves the zero value), so both fields land at
// Set=true, Value=zero-time -- this must fail the ends_at-after-starts_at
// check (zero.After(zero) is false), not be silently accepted as "no
// change" the way an absent pair would be.
func TestPatchEventRequest_Validate_RejectsExplicitNullForBothStartsAndEnds(t *testing.T) {
	var r request.PatchEventRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "starts_at": null, "ends_at": null}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !r.StartsAt.Set || !r.EndsAt.Set {
		t.Fatalf("got StartsAt.Set=%v EndsAt.Set=%v, want both true", r.StartsAt.Set, r.EndsAt.Set)
	}
	issues := r.Validate()
	if len(issues) == 0 {
		t.Fatal("got no issues, want an ends_at error -- null-for-both must not be silently accepted")
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
