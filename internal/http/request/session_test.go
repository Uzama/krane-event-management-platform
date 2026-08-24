package request_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/http/request"
)

func newYorkLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

func TestCreateSessionRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.CreateSessionRequest
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-06-15T09:00:00","ends_at":"2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestCreateSessionRequest_Validate_RejectsBlankTitle(t *testing.T) {
	var r request.CreateSessionRequest
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"   ","starts_at":"2026-06-15T09:00:00","ends_at":"2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["title"]; !ok {
		t.Fatalf("issues missing title: %v", issues)
	}
}

func TestCreateSessionRequest_Validate_RejectsMissingRoomOrSpeaker(t *testing.T) {
	var r request.CreateSessionRequest
	body := `{"title":"Keynote","starts_at":"2026-06-15T09:00:00","ends_at":"2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["room_id"]; !ok {
		t.Fatalf("issues missing room_id: %v", issues)
	}
	if _, ok := issues["speaker_id"]; !ok {
		t.Fatalf("issues missing speaker_id: %v", issues)
	}
}

func TestCreateSessionRequest_Validate_RejectsEndsBeforeStarts(t *testing.T) {
	var r request.CreateSessionRequest
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-06-15T10:00:00","ends_at":"2026-06-15T09:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["ends_at"]; !ok {
		t.Fatalf("issues missing ends_at: %v", issues)
	}
}

// TestCreateSessionRequest_Validate_RejectsNonexistentLocalTime is the
// write-path DST proof at the request layer: a local time inside a
// spring-forward gap must surface as a 422 issue, not silently normalize
// or panic.
func TestCreateSessionRequest_Validate_RejectsNonexistentLocalTime(t *testing.T) {
	var r request.CreateSessionRequest
	// 2026-03-08: America/New_York jumps 2:00am -> 3:00am. 2:30am never happens.
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-03-08T02:30:00","ends_at":"2026-03-08T04:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["starts_at"]; !ok {
		t.Fatalf("issues missing starts_at: %v", issues)
	}
}

func TestCreateSessionRequest_Validate_RejectsMalformedTimeFormat(t *testing.T) {
	var r request.CreateSessionRequest
	// An offset-bearing timestamp is rejected -- the server resolves the
	// instant against the event's own timezone, the client never supplies one.
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-06-15T09:00:00-04:00","ends_at":"2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["starts_at"]; !ok {
		t.Fatalf("issues missing starts_at: %v", issues)
	}
}

func TestCreateSessionRequest_ToCreateInput(t *testing.T) {
	var r request.CreateSessionRequest
	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","description":"A talk","starts_at":"2026-06-15T09:00:00","ends_at":"2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("Validate: got issues %v, want none", issues)
	}

	in := r.ToCreateInput(newYorkLoc(t))
	if in.RoomID != "room-1" || in.SpeakerID != "speaker-1" || in.Title != "Keynote" {
		t.Fatalf("got %+v", in)
	}
	if in.Description == nil || *in.Description != "A talk" {
		t.Fatalf("got Description %v, want \"A talk\"", in.Description)
	}
	if !in.EndsAt.After(in.StartsAt) {
		t.Fatalf("got StartsAt %v, EndsAt %v -- ends must be after starts", in.StartsAt, in.EndsAt)
	}
	// 2026-06-15 is outside DST transitions -- America/New_York is EDT (-04:00).
	if _, offset := in.StartsAt.Zone(); offset != -4*3600 {
		t.Fatalf("got offset %ds, want -04:00 (EDT)", offset)
	}
}

func TestPatchSessionRequest_Validate_RequiresVersion(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["version"]; !ok {
		t.Fatalf("issues missing version: %v", issues)
	}
}

func TestPatchSessionRequest_Validate_AcceptsAbsentFields(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestPatchSessionRequest_Validate_RejectsOneSidedTimeChange(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "starts_at": "2026-06-15T09:00:00"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["starts_at/ends_at"]; !ok {
		t.Fatalf("issues missing starts_at/ends_at: %v", issues)
	}
}

func TestPatchSessionRequest_Validate_AcceptsBothTimesTogether(t *testing.T) {
	var r request.PatchSessionRequest
	body := `{"version": 1, "starts_at": "2026-06-15T09:00:00", "ends_at": "2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestPatchSessionRequest_Validate_RejectsNonexistentLocalTime(t *testing.T) {
	var r request.PatchSessionRequest
	body := `{"version": 1, "starts_at": "2026-03-08T02:30:00", "ends_at": "2026-03-08T04:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["starts_at"]; !ok {
		t.Fatalf("issues missing starts_at: %v", issues)
	}
}

// TestPatchSessionRequest_Validate_DescriptionThreeStates is the
// request-level half of the Optional[T] PATCH proof case
// docs/requirements.md names sessions.description for -- absent, explicit
// null, and an explicit value must decode into three distinct states.
func TestPatchSessionRequest_Validate_DescriptionThreeStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		var r request.PatchSessionRequest
		if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.Description.Set {
			t.Fatalf("Description.Set = true, want false")
		}
	})
	t.Run("explicit null", func(t *testing.T) {
		var r request.PatchSessionRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "description": null}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Description.Set || r.Description.Value != nil {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=nil", r.Description.Set, r.Description.Value)
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		var r request.PatchSessionRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "description": "New desc"}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Description.Set || r.Description.Value == nil || *r.Description.Value != "New desc" {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=\"New desc\"", r.Description.Set, r.Description.Value)
		}
	})
}

func TestPatchSessionRequest_Validate_RejectsBlankTitle(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "title": "   "}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["title"]; !ok {
		t.Fatalf("issues missing title: %v", issues)
	}
}

// TestPatchSessionRequest_Validate_RejectsExplicitNullForTitle is item
// 20's missing coverage: the existing RejectsBlankTitle test only sent
// whitespace ("   "), never a literal JSON null -- which
// opt.Optional[string] accepts silently as Set=true, Value="" -- so this
// proves that path is actually caught, not merely assumed.
func TestPatchSessionRequest_Validate_RejectsExplicitNullForTitle(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "title": null}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !r.Title.Set {
		t.Fatalf("Title.Set = false, want true (opt.Optional[string] treats null as Set=true, Value=\"\")")
	}
	issues := r.Validate(newYorkLoc(t))
	if _, ok := issues["title"]; !ok {
		t.Fatalf("issues missing title: %v -- explicit null must be rejected, not silently applied as a blank title", issues)
	}
}

// TestPatchSessionRequest_Validate_RejectsExplicitNullForBothStartsAndEnds
// is item 20's missing coverage: unlike event.Patch, session's
// StartsAt/EndsAt are opt.Optional[string] (local wall-clock strings,
// item 12), so a literal JSON null lands as Set=true, Value="" -- caught
// here via utils.ResolveLocalTime("", loc) failing to parse, not a
// TrimSpace check, so this is a genuinely different code path from
// Title's and needs its own proof.
func TestPatchSessionRequest_Validate_RejectsExplicitNullForBothStartsAndEnds(t *testing.T) {
	var r request.PatchSessionRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "starts_at": null, "ends_at": null}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !r.StartsAt.Set || !r.EndsAt.Set {
		t.Fatalf("got StartsAt.Set=%v EndsAt.Set=%v, want both true", r.StartsAt.Set, r.EndsAt.Set)
	}
	issues := r.Validate(newYorkLoc(t))
	if len(issues) == 0 {
		t.Fatal("got no issues, want a starts_at/ends_at parse error -- null-for-both must not be silently accepted")
	}
}

func TestPatchSessionRequest_ToPatch(t *testing.T) {
	var r request.PatchSessionRequest
	body := `{"version": 3, "title": "New title", "starts_at": "2026-06-15T09:00:00", "ends_at": "2026-06-15T10:00:00"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("Validate: got issues %v, want none", issues)
	}

	patch := r.ToPatch(newYorkLoc(t))
	if !patch.Title.Set || patch.Title.Value != "New title" {
		t.Fatalf("got %+v", patch)
	}
	if patch.Description.Set {
		t.Fatalf("unset Description leaked into patch: %+v", patch)
	}
	if !patch.StartsAt.Set || !patch.EndsAt.Set {
		t.Fatalf("got StartsAt.Set=%v EndsAt.Set=%v, want both true", patch.StartsAt.Set, patch.EndsAt.Set)
	}
	if !patch.EndsAt.Value.After(patch.StartsAt.Value) {
		t.Fatalf("got StartsAt %v, EndsAt %v -- ends must be after starts", patch.StartsAt.Value, patch.EndsAt.Value)
	}
}

func TestPatchSessionRequest_ToPatch_NoRoomOrSpeakerFields(t *testing.T) {
	// room_id/speaker_id are not patchable at all -- fixed at creation
	// (docs/requirements.md §8). Any such keys in the body are simply
	// ignored: PatchSessionRequest has no fields to decode them into.
	var r request.PatchSessionRequest
	body := `{"version": 1, "room_id": "should-be-ignored", "speaker_id": "should-be-ignored"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(newYorkLoc(t)); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}
