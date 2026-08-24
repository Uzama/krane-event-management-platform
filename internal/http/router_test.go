package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/http/validator"
)

type fakePinger struct {
	err error
}

func (f fakePinger) Ping(ctx context.Context) error {
	return f.err
}

// TestRouter_Health_ResponseMatchesSpec is the CI contract check FEATURES.md
// item 05 asks for: it boots the real, unmodified production router and
// validates the LIVE recorded response -- not a hand-built stand-in -- so
// spec/handler drift is actually caught, in either direction.
func TestRouter_Health_ResponseMatchesSpec(t *testing.T) {
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
	}{
		{name: "database reachable", pingErr: nil, wantStatus: http.StatusOK},
		{name: "database unreachable", pingErr: errors.New("connection refused"), wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := apihttp.NewRouter(handler.NewHealthHandler(fakePinger{err: tt.pingErr}, discardLogger()))

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			// Re-issue a fresh request for validation: the recorder's request
			// was already consumed by the handler, but the LIVE body/header
			// recorded on rec is what actually went over the wire -- that is
			// what must match the contract.
			validationReq := httptest.NewRequest(http.MethodGet, "/health", nil)
			if err := validator.ValidateRequest(context.Background(), spec, validationReq); err != nil {
				t.Errorf("ValidateRequest: %v", err)
			}

			if err := validator.ValidateResponse(
				context.Background(), spec, validationReq,
				rec.Code, rec.Header(), rec.Body.Bytes(),
			); err != nil {
				t.Errorf("ValidateResponse(live recorded body): %v\nbody: %s", err, rec.Body.String())
			}
		})
	}
}
