package response_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func sampleSession(id string) session.Session {
	desc := "A talk"
	starts := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC) // 09:00 EDT
	return session.Session{
		ID:          id,
		EventID:     "event-1",
		RoomID:      "room-1",
		SpeakerID:   "speaker-1",
		Title:       "Keynote",
		Description: &desc,
		StartsAt:    starts,
		EndsAt:      starts.Add(time.Hour),
		Version:     1,
		CreatedAt:   starts,
		UpdatedAt:   starts,
	}
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestNewSessionResponse_MapsFields(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	got := response.NewSessionResponse(sampleSession("session-1"), loc)

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "event_id", "room_id", "speaker_id", "title", "description", "starts_at", "ends_at", "duration_minutes", "version", "created_at", "updated_at"} {
		if _, ok := asMap[field]; !ok {
			t.Errorf("response missing %q: %s", field, data)
		}
	}
	if asMap["id"] != "session-1" {
		t.Errorf("got id %v, want session-1", asMap["id"])
	}
	if asMap["duration_minutes"] != float64(60) {
		t.Errorf("got duration_minutes %v, want 60", asMap["duration_minutes"])
	}
}

func TestNewSessionResponse_LocalizesToEventTimezone(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	got := response.NewSessionResponse(sampleSession("session-1"), loc)

	// sampleSession's StartsAt is 13:00 UTC = 09:00 EDT (-04:00), outside
	// any DST transition.
	if got.StartsAt.Hour() != 9 {
		t.Errorf("got localized hour %d, want 9 (09:00 EDT)", got.StartsAt.Hour())
	}
	if _, offset := got.StartsAt.Zone(); offset != -4*3600 {
		t.Errorf("got offset %ds, want -04:00 (EDT)", offset)
	}
}

func TestNewSessionResponse_NullDescription(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	s := sampleSession("session-1")
	s.Description = nil
	got := response.NewSessionResponse(s, loc)

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := asMap["description"]; !ok || v != nil {
		t.Errorf("got description %v (present=%v), want key present with null value", v, ok)
	}
}

// TestNewSessionResponse_SpringForwardCrossing_DurationUsesActualInstants
// is the read-path half of the DST must-test at the unit level: a session
// crossing 2026-03-08's spring-forward (2:00am -> 3:00am, the 2-3am hour
// never happens) must report the true 60-minute elapsed duration, not the
// naive 120-minute wall-clock difference a component subtraction would
// produce, and each of starts_at/ends_at must carry ITS OWN correct offset.
func TestNewSessionResponse_SpringForwardCrossing_DurationUsesActualInstants(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	starts, err := time.ParseInLocation("2006-01-02T15:04:05", "2026-03-08T01:30:00", loc)
	if err != nil {
		t.Fatalf("parsing starts: %v", err)
	}
	ends, err := time.ParseInLocation("2006-01-02T15:04:05", "2026-03-08T03:30:00", loc)
	if err != nil {
		t.Fatalf("parsing ends: %v", err)
	}

	s := sampleSession("session-1")
	s.StartsAt, s.EndsAt = starts, ends
	got := response.NewSessionResponse(s, loc)

	if got.DurationMinutes != 60 {
		t.Fatalf("got DurationMinutes %d, want 60 (actual elapsed time, not the naive 120-minute wall-clock diff)", got.DurationMinutes)
	}
	if _, startOffset := got.StartsAt.Zone(); startOffset != -5*3600 {
		t.Errorf("got starts_at offset %ds, want -05:00 (EST, before the transition)", startOffset)
	}
	if _, endOffset := got.EndsAt.Zone(); endOffset != -4*3600 {
		t.Errorf("got ends_at offset %ds, want -04:00 (EDT, after the transition)", endOffset)
	}
}

// TestNewSessionResponse_FallBackCrossing_DurationUsesActualInstants is the
// opposite-direction case: 2026-11-01's fall-back (2:00am -> 1:00am, the
// 1-2am hour happens twice) must report the true 180-minute elapsed
// duration, not the naive 120-minute wall-clock difference.
func TestNewSessionResponse_FallBackCrossing_DurationUsesActualInstants(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	starts, err := time.ParseInLocation("2006-01-02T15:04:05", "2026-11-01T00:30:00", loc)
	if err != nil {
		t.Fatalf("parsing starts: %v", err)
	}
	ends, err := time.ParseInLocation("2006-01-02T15:04:05", "2026-11-01T02:30:00", loc)
	if err != nil {
		t.Fatalf("parsing ends: %v", err)
	}

	s := sampleSession("session-1")
	s.StartsAt, s.EndsAt = starts, ends
	got := response.NewSessionResponse(s, loc)

	if got.DurationMinutes != 180 {
		t.Fatalf("got DurationMinutes %d, want 180 (actual elapsed time, not the naive 120-minute wall-clock diff)", got.DurationMinutes)
	}
	if _, startOffset := got.StartsAt.Zone(); startOffset != -4*3600 {
		t.Errorf("got starts_at offset %ds, want -04:00 (EDT, before the transition)", startOffset)
	}
	if _, endOffset := got.EndsAt.Zone(); endOffset != -5*3600 {
		t.Errorf("got ends_at offset %ds, want -05:00 (EST, after the transition)", endOffset)
	}
}

func TestNewSessionListResponse_NoCursorWhenNoFurtherPage(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	page := session.Page{Sessions: []session.Session{sampleSession("session-1")}}
	got := response.NewSessionListResponse(page, loc)

	if len(got.Data) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Data))
	}
	if got.NextCursor != nil {
		t.Fatalf("got NextCursor %v, want nil", *got.NextCursor)
	}
}

func TestNewSessionListResponse_EncodesCursorWhenFurtherPageExists(t *testing.T) {
	loc := mustLoadLocation(t, "America/New_York")
	page := session.Page{
		Sessions:   []session.Session{sampleSession("session-1")},
		NextCursor: &session.Cursor{CreatedAt: time.Now(), ID: "session-1"},
	}
	got := response.NewSessionListResponse(page, loc)

	if got.NextCursor == nil || *got.NextCursor == "" {
		t.Fatalf("got NextCursor %v, want a non-empty opaque token", got.NextCursor)
	}
}
