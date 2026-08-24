package main

import (
	"regexp"
	"testing"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDv7_MatchesRFC9562Shape(t *testing.T) {
	id := newUUIDv7()
	if !uuidPattern.MatchString(id) {
		t.Fatalf("got %q, want a UUIDv7-shaped string (version nibble 7, variant 10xx)", id)
	}
}

func TestNewUUIDv7_NoCollisionsAcrossManyCalls(t *testing.T) {
	const n = 20000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := newUUIDv7()
		if _, dup := seen[id]; dup {
			t.Fatalf("newUUIDv7 produced a duplicate at call %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewUUIDv7_TimestampPrefixIsNonDecreasing(t *testing.T) {
	const n = 500
	prev := ""
	for i := 0; i < n; i++ {
		id := newUUIDv7()
		prefix := id[:8] + id[9:13] // 48-bit ms timestamp, hyphens stripped
		if prefix < prev {
			t.Fatalf("call %d: timestamp prefix %q < previous %q -- UUIDv7 IDs should sort roughly by creation time", i, prefix, prev)
		}
		prev = prefix
	}
}
