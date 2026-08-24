package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// cursorPayload is the tuple every keyset-paginated list orders by:
// (created_at, id). CLAUDE.md: pagination is keyset only, never OFFSET.
type cursorPayload struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// EncodeCursor produces the opaque token a list response hands back as
// next_cursor. Callers must never construct or inspect the token's
// contents -- it round-trips only through DecodeCursor.
func EncodeCursor(createdAt time.Time, id string) string {
	data, err := json.Marshal(cursorPayload{CreatedAt: createdAt, ID: id})
	if err != nil {
		// cursorPayload's fields are always JSON-marshalable; a failure
		// here would be a programming error, not a runtime condition to
		// recover from.
		panic(fmt.Sprintf("utils: encoding cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeCursor recovers the (created_at, id) tuple from a token produced by
// EncodeCursor. Any malformed or tampered input -- bad base64, bad JSON, or
// a missing id -- is an error the caller renders as 400, never a panic and
// never a value that silently widens what a query returns.
func DecodeCursor(token string) (createdAt time.Time, id string, err error) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decoding cursor: %w", err)
	}

	var p cursorPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return time.Time{}, "", fmt.Errorf("decoding cursor: %w", err)
	}
	if p.ID == "" || p.CreatedAt.IsZero() {
		return time.Time{}, "", fmt.Errorf("decoding cursor: missing created_at or id")
	}

	return p.CreatedAt, p.ID, nil
}
