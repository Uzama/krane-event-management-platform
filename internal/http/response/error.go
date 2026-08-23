// Package response holds response DTOs and the one error envelope every
// handler's failure path uses.
package response

import (
	"encoding/json"
	"net/http"
)

// Envelope is the stable, machine-readable error shape from CLAUDE.md:
//
//	{ "error": { "code": "...", "message": "...", "details": {} } }
type Envelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the machine-readable code, a human message, and
// structured details. Details is never omitted -- an absent field would
// break clients that always look for it.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// WriteError writes the standard error envelope with the given status.
// details may be nil; it renders as {} rather than null.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	return json.NewEncoder(w).Encode(Envelope{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
