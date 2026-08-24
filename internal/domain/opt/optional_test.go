package opt_test

import (
	"encoding/json"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// TestOptional_UnmarshalJSON_ThreeOutcomes is the foundational proof behind
// CLAUDE.md's PATCH-semantics invariant: absent, explicit null, and an
// explicit value are three distinct wire states, and a plain *string can't
// tell the first two apart. Item 20 later proves this at the full
// endpoint/database level for every resource; this proves the type itself.
func TestOptional_UnmarshalJSON_ThreeOutcomes(t *testing.T) {
	type body struct {
		Name opt.Optional[*string] `json:"name"`
	}

	t.Run("absent", func(t *testing.T) {
		var b body
		if err := json.Unmarshal([]byte(`{}`), &b); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if b.Name.Set {
			t.Fatalf("Set = true, want false for an absent field")
		}
		if b.Name.Value != nil {
			t.Fatalf("Value = %v, want nil for an absent field", *b.Name.Value)
		}
	})

	t.Run("explicit null", func(t *testing.T) {
		var b body
		if err := json.Unmarshal([]byte(`{"name":null}`), &b); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !b.Name.Set {
			t.Fatalf("Set = false, want true for an explicit null")
		}
		if b.Name.Value != nil {
			t.Fatalf("Value = %v, want nil for an explicit null", *b.Name.Value)
		}
	})

	t.Run("explicit value", func(t *testing.T) {
		var b body
		if err := json.Unmarshal([]byte(`{"name":"room 4"}`), &b); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !b.Name.Set {
			t.Fatalf("Set = false, want true for an explicit value")
		}
		if b.Name.Value == nil || *b.Name.Value != "room 4" {
			t.Fatalf("Value = %v, want \"room 4\"", b.Name.Value)
		}
	})
}

func TestOptional_UnmarshalJSON_NonPointerValue(t *testing.T) {
	type body struct {
		Version opt.Optional[int] `json:"version"`
	}

	var b body
	if err := json.Unmarshal([]byte(`{"version":3}`), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !b.Version.Set || b.Version.Value != 3 {
		t.Fatalf("got Set=%v Value=%d, want Set=true Value=3", b.Version.Set, b.Version.Value)
	}
}

func TestOptional_UnmarshalJSON_InvalidValue(t *testing.T) {
	type body struct {
		Version opt.Optional[int] `json:"version"`
	}

	var b body
	if err := json.Unmarshal([]byte(`{"version":"not-a-number"}`), &b); err == nil {
		t.Fatalf("Unmarshal: got nil error, want a type error")
	}
}

func TestOptional_MarshalJSON_RoundTrips(t *testing.T) {
	type body struct {
		Version opt.Optional[int] `json:"version"`
	}

	in := body{Version: opt.Of(5)}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out body
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !out.Version.Set || out.Version.Value != 5 {
		t.Fatalf("got Set=%v Value=%d, want Set=true Value=5", out.Version.Set, out.Version.Value)
	}
}
