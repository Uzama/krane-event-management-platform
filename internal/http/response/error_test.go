package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

func TestWriteError_EnvelopeShape(t *testing.T) {
	cases := []struct {
		name    string
		details map[string]any
		want    string
	}{
		{
			name:    "nil details renders as empty object",
			details: nil,
			want:    `{"error":{"code":"service_unavailable","message":"database unreachable","details":{}}}`,
		},
		{
			name:    "details are preserved",
			details: map[string]any{"reason": "timeout"},
			want:    `{"error":{"code":"service_unavailable","message":"database unreachable","details":{"reason":"timeout"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			err := response.WriteError(rec, http.StatusServiceUnavailable, "service_unavailable", "database unreachable", tc.details)
			if err != nil {
				t.Fatalf("WriteError: %v", err)
			}

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			got := strings.TrimSpace(rec.Body.String())
			if got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}

			// Round-trip through the real envelope type, not just byte-compare.
			var decoded response.Envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decoding body: %v", err)
			}
			if decoded.Error.Code != "service_unavailable" || decoded.Error.Message != "database unreachable" {
				t.Errorf("decoded envelope = %+v, want code=service_unavailable message=%q", decoded, "database unreachable")
			}
		})
	}
}
