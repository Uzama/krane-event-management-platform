package utils_test

import (
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

func TestCursor_EncodeDecode_RoundTrips(t *testing.T) {
	createdAt := time.Date(2026, 3, 15, 10, 30, 0, 123456000, time.UTC)
	id := "0194f8b2-9c1a-7c3e-8a1b-000000000001"

	token := utils.EncodeCursor(createdAt, id)
	if token == "" {
		t.Fatalf("EncodeCursor returned empty token")
	}

	gotCreatedAt, gotID, err := utils.DecodeCursor(token)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !gotCreatedAt.Equal(createdAt) {
		t.Fatalf("got createdAt %v, want %v", gotCreatedAt, createdAt)
	}
	if gotID != id {
		t.Fatalf("got id %q, want %q", gotID, id)
	}
}

func TestCursor_EncodeDecode_IsOpaque(t *testing.T) {
	token := utils.EncodeCursor(time.Now(), "some-id")
	if token == "some-id" {
		t.Fatalf("token is not opaque: %q", token)
	}
}

func TestCursor_Decode_RejectsMalformedInput(t *testing.T) {
	cases := []string{
		"",
		"not-base64!!!",
		"aGVsbG8=", // valid base64 ("hello"), but not a valid cursor payload
	}
	for _, c := range cases {
		if _, _, err := utils.DecodeCursor(c); err == nil {
			t.Fatalf("DecodeCursor(%q): got nil error, want an error", c)
		}
	}
}

func TestCursor_Decode_RejectsTamperedToken(t *testing.T) {
	token := utils.EncodeCursor(time.Now(), "some-id")
	tampered := token[:len(token)-1] + "x"

	if _, _, err := utils.DecodeCursor(tampered); err == nil {
		// Not every single-character flip is guaranteed to break base64/JSON
		// decoding, but this specific construction (dropping the last char)
		// reliably does for our encoding -- if it ever stops erroring, the
		// encoding scheme changed and this test should be revisited.
		t.Fatalf("DecodeCursor(tampered): got nil error, want an error")
	}
}
