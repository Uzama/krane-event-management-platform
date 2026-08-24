package request_test

import (
	"encoding/json"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/request"
)

func TestCreateRoomRequest_Validate_AcceptsValidInput(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "Hall A", "capacity": 50}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestCreateRoomRequest_Validate_AcceptsNullCapacity(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "Hall A"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

func TestCreateRoomRequest_Validate_RejectsBlankName(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "   "}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["name"]; !ok {
		t.Fatalf("issues missing name: %v", issues)
	}
}

func TestCreateRoomRequest_Validate_RejectsZeroCapacity(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "Hall A", "capacity": 0}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["capacity"]; !ok {
		t.Fatalf("issues missing capacity: %v", issues)
	}
}

func TestCreateRoomRequest_Validate_RejectsNegativeCapacity(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "Hall A", "capacity": -1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["capacity"]; !ok {
		t.Fatalf("issues missing capacity: %v", issues)
	}
}

func TestCreateRoomRequest_ToCreateInput(t *testing.T) {
	var r request.CreateRoomRequest
	if err := json.Unmarshal([]byte(`{"name": "Hall A", "capacity": 50}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	in := r.ToCreateInput()
	if in.Name != "Hall A" || in.Capacity == nil || *in.Capacity != 50 {
		t.Fatalf("got %+v", in)
	}
}

func TestPatchRoomRequest_Validate_RequiresVersion(t *testing.T) {
	var r request.PatchRoomRequest
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["version"]; !ok {
		t.Fatalf("issues missing version: %v", issues)
	}
}

func TestPatchRoomRequest_Validate_AcceptsAbsentFields(t *testing.T) {
	var r request.PatchRoomRequest
	if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if issues := r.Validate(); len(issues) != 0 {
		t.Fatalf("got issues %v, want none", issues)
	}
}

// TestPatchRoomRequest_Validate_CapacityThreeStates proves absent, explicit
// null, and an explicit value decode into three distinct Optional states --
// the request-level half of the same proof room_repo_test.go's
// TestRoomRepository_Update_CapacityThreeStates runs end to end.
func TestPatchRoomRequest_Validate_CapacityThreeStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		var r request.PatchRoomRequest
		if err := json.Unmarshal([]byte(`{"version": 1}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.Capacity.Set {
			t.Fatalf("Capacity.Set = true, want false")
		}
	})
	t.Run("explicit null", func(t *testing.T) {
		var r request.PatchRoomRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "capacity": null}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Capacity.Set || r.Capacity.Value != nil {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=nil", r.Capacity.Set, r.Capacity.Value)
		}
		if issues := r.Validate(); len(issues) != 0 {
			t.Fatalf("got issues %v, want none -- null clears, it is not a rejected zero", issues)
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		var r request.PatchRoomRequest
		if err := json.Unmarshal([]byte(`{"version": 1, "capacity": 75}`), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !r.Capacity.Set || r.Capacity.Value == nil || *r.Capacity.Value != 75 {
			t.Fatalf("got Set=%v Value=%v, want Set=true Value=75", r.Capacity.Set, r.Capacity.Value)
		}
	})
}

// TestPatchRoomRequest_Validate_RejectsZeroCapacity_NotConflatedWithClear
// is the case Capacity's opt.Optional[*int] can most easily collapse: a
// *set* to 0 must be rejected by the rooms_capacity_check-mirroring rule,
// distinctly from an explicit null (which clears and is valid).
func TestPatchRoomRequest_Validate_RejectsZeroCapacity_NotConflatedWithClear(t *testing.T) {
	var r request.PatchRoomRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "capacity": 0}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["capacity"]; !ok {
		t.Fatalf("issues missing capacity: %v -- capacity:0 must be rejected, not treated as a clear", issues)
	}
}

func TestPatchRoomRequest_Validate_RejectsBlankName(t *testing.T) {
	var r request.PatchRoomRequest
	if err := json.Unmarshal([]byte(`{"version": 1, "name": "   "}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	issues := r.Validate()
	if _, ok := issues["name"]; !ok {
		t.Fatalf("issues missing name: %v", issues)
	}
}

func TestPatchRoomRequest_ToPatch(t *testing.T) {
	var r request.PatchRoomRequest
	if err := json.Unmarshal([]byte(`{"version": 3, "name": "New name"}`), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	patch := r.ToPatch()
	if !patch.Name.Set || patch.Name.Value != "New name" {
		t.Fatalf("got %+v", patch)
	}
	if patch.Capacity.Set {
		t.Fatalf("unset Capacity leaked into patch: %+v", patch)
	}
}
