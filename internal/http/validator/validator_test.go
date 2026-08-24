package validator_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/validator"
)

func TestLoadSpec(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec == nil {
		t.Fatal("LoadSpec returned a nil spec with no error")
	}
}

func TestValidateRequest_MatchingRoute(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	if err := validator.ValidateRequest(context.Background(), spec, req); err != nil {
		t.Errorf("ValidateRequest(GET /health) = %v, want nil", err)
	}
}

// TestValidateRequest_NoMatchingRoute proves the validator actually rejects a
// contract violation rather than rubber-stamping every request: the spec
// only declares GET /health, so POST /health must not match.
func TestValidateRequest_NoMatchingRoute(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	err = validator.ValidateRequest(context.Background(), spec, req)
	if err == nil {
		t.Fatal("ValidateRequest(POST /health) = nil, want an error -- the spec only declares GET")
	}
	// kin-openapi's legacy router returns a fresh *routers.RouteError each
	// time rather than the routers.ErrMethodNotAllowed sentinel itself (even
	// its own tests compare by message, not errors.Is), so match the same
	// way.
	if !strings.Contains(err.Error(), "method not allowed") {
		t.Errorf("ValidateRequest(POST /health) error = %v, want it to mention %q", err, "method not allowed")
	}
}

func TestValidateResponse_MatchingSchema(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	header := http.Header{"Content-Type": []string{"application/json"}}
	body := []byte(`{"status":"ok","database":"ok"}`)

	if err := validator.ValidateResponse(context.Background(), spec, req, http.StatusOK, header, body); err != nil {
		t.Errorf("ValidateResponse(valid 200 body) = %v, want nil", err)
	}
}

// TestValidateResponse_ViolatesSchema proves response validation actually
// catches a contract violation: HealthStatus requires both `status` and
// `database`, so a body missing one must be rejected.
func TestValidateResponse_ViolatesSchema(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	header := http.Header{"Content-Type": []string{"application/json"}}
	body := []byte(`{"status":"ok"}`) // missing required "database"

	err = validator.ValidateResponse(context.Background(), spec, req, http.StatusOK, header, body)
	if err == nil {
		t.Fatal("ValidateResponse(body missing required field) = nil, want an error")
	}
}
