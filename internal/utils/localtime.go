package utils

import (
	"errors"
	"fmt"
	"time"
)

// LocalTimeLayout is the wall-clock format session start/end times are
// accepted in on the wire -- no offset, no "Z". The instant is resolved
// server-side against the event's own IANA timezone (FEATURES.md item 12:
// the server, not the client, owns DST resolution for session times), so a
// caller-supplied offset is deliberately rejected, not merely ignored.
const LocalTimeLayout = "2006-01-02T15:04:05"

// ErrNonexistentLocalTime means the given local wall-clock time falls in a
// spring-forward DST gap for the named zone -- it never happened as a real
// instant (e.g. 2:30am on a day the clock jumps from 2:00am to 3:00am).
var ErrNonexistentLocalTime = errors.New("utils: local time does not exist in this timezone")

// ResolveLocalTime interprets local -- a LocalTimeLayout-formatted
// wall-clock string with no offset -- as a time in loc, returning the
// resulting instant. loc is an already-loaded *time.Location, never a zone
// name to parse here -- callers load it once per request (the event's
// timezone doesn't change mid-request) and reuse it across every
// ResolveLocalTime/localization call, rather than paying tzdata-lookup
// cost per field or per row.
//
// Ambiguous times (a fall-back overlap, e.g. 1:30am occurring twice) are
// NOT rejected. time.ParseInLocation resolves them to the earlier of the
// two possible offsets, matching time.Date's documented behavior; that is
// the deliberate, documented convention this function relies on, proven by
// TestResolveLocalTime_FallBackAmbiguousTime_ResolvesToEarlierOffset, not
// merely assumed.
//
// Nonexistent times (a spring-forward gap, e.g. 2:30am skipped entirely)
// ARE rejected, as ErrNonexistentLocalTime. time.ParseInLocation does not
// error on these -- it silently returns an instant shifted by the length
// of the jump. Detected here by round-tripping: the resolved instant is
// formatted back through the same layout, in the same zone, and compared
// to the input by exact string equality. A mismatch means the input was
// never a real local time in this zone; an exact match (the ordinary
// case) means it was.
func ResolveLocalTime(local string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(LocalTimeLayout, local, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("utils: parsing local time %q: %w", local, err)
	}

	if t.In(loc).Format(LocalTimeLayout) != local {
		return time.Time{}, ErrNonexistentLocalTime
	}
	return t, nil
}
