package utils

import (
	"errors"
	"testing"
	"time"
)

// America/New_York's 2026 transitions: spring-forward on 2026-03-08 (2:00am
// -> 3:00am, EST -05:00 -> EDT -04:00) and fall-back on 2026-11-01 (2:00am
// -> 1:00am, EDT -04:00 -> EST -05:00). Both are ordinary US DST rule
// outcomes (2nd Sunday of March / 1st Sunday of November), not hand-picked
// exceptions.
func newYorkLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

func TestResolveLocalTime_OrdinaryTime_RoundTrips(t *testing.T) {
	loc := newYorkLoc(t)
	got, err := ResolveLocalTime("2026-06-15T14:30:00", loc)
	if err != nil {
		t.Fatalf("ResolveLocalTime: %v", err)
	}

	want := time.Date(2026, 6, 15, 14, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveLocalTime_SpringForwardGap_ReturnsErrNonexistentLocalTime(t *testing.T) {
	loc := newYorkLoc(t)
	// 2026-03-08: clocks jump 2:00am -> 3:00am. 2:30am never happens.
	_, err := ResolveLocalTime("2026-03-08T02:30:00", loc)
	if !errors.Is(err, ErrNonexistentLocalTime) {
		t.Fatalf("got err %v, want ErrNonexistentLocalTime", err)
	}
}

// TestResolveLocalTime_AdjacentToSpringForwardGap_DoesNotReject is the
// over-firing guard: the gap test alone proves rejection fires, but not
// that it fires ONLY on a genuine DST roll rather than some cosmetic
// round-trip mismatch. 01:30 (last valid minute before the jump) and 03:30
// (first ordinary minute after it) are both real, once-occurring local
// times immediately adjacent to the exact gap boundary.
func TestResolveLocalTime_AdjacentToSpringForwardGap_DoesNotReject(t *testing.T) {
	loc := newYorkLoc(t)

	before, err := ResolveLocalTime("2026-03-08T01:30:00", loc)
	if err != nil {
		t.Fatalf("ResolveLocalTime(before gap): %v", err)
	}
	if want := time.Date(2026, 3, 8, 1, 30, 0, 0, loc); !before.Equal(want) {
		t.Fatalf("before gap: got %v, want %v", before, want)
	}
	if _, offset := before.Zone(); offset != -5*3600 {
		t.Fatalf("before gap: got offset %ds, want -05:00 (EST)", offset)
	}

	after, err := ResolveLocalTime("2026-03-08T03:30:00", loc)
	if err != nil {
		t.Fatalf("ResolveLocalTime(after gap): %v", err)
	}
	if want := time.Date(2026, 3, 8, 3, 30, 0, 0, loc); !after.Equal(want) {
		t.Fatalf("after gap: got %v, want %v", after, want)
	}
	if _, offset := after.Zone(); offset != -4*3600 {
		t.Fatalf("after gap: got offset %ds, want -04:00 (EDT)", offset)
	}
}

// TestResolveLocalTime_FallBackAmbiguousTime_ResolvesToEarlierOffset
// documents the chosen convention for an ambiguous local time (one that
// occurs twice): resolve to the earlier of the two instants, matching
// Go's own time.Date/ParseInLocation documented behavior. This is a
// deliberate choice, proven here rather than left as an unstated side
// effect of whatever the standard library happens to do.
func TestResolveLocalTime_FallBackAmbiguousTime_ResolvesToEarlierOffset(t *testing.T) {
	loc := newYorkLoc(t)
	// 2026-11-01: clocks fall back 2:00am -> 1:00am. 01:30 occurs twice:
	// once at -04:00 (EDT, before the fold) and once at -05:00 (EST,
	// after it). The earlier occurrence is the EDT one.
	got, err := ResolveLocalTime("2026-11-01T01:30:00", loc)
	if err != nil {
		t.Fatalf("ResolveLocalTime: %v", err)
	}
	if _, offset := got.Zone(); offset != -4*3600 {
		t.Fatalf("got offset %ds, want -04:00 (EDT, the earlier occurrence)", offset)
	}
}

func TestResolveLocalTime_MalformedInput_ReturnsError(t *testing.T) {
	loc := newYorkLoc(t)
	cases := []string{
		"",
		"not-a-time",
		"2026-03-08T02:30:00Z",      // an offset/zulu suffix is rejected -- local input only
		"2026-03-08T02:30:00-05:00", // an explicit offset is rejected -- the server resolves it, not the client
	}
	for _, in := range cases {
		if _, err := ResolveLocalTime(in, loc); err == nil {
			t.Errorf("ResolveLocalTime(%q): got nil error, want one", in)
		}
	}
}
