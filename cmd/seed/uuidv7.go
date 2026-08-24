package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newUUIDv7 generates an RFC 9562 UUIDv7: a 48-bit big-endian millisecond
// timestamp, the version/variant bits, and 74 random bits. Client-side ID
// generation (rather than letting Postgres apply the schema's DEFAULT
// uuidv7()) is what lets the seed generator wire cross-row FK references
// (event_id, room_id, speaker_id, ...) before anything is written to the
// database -- see cmd/seed/generate.go. There's no v7-capable UUID
// dependency available without bumping google/uuid past what oapi-codegen
// currently pulls in (v1.5.0 predates NewV7), so this is standard-library
// only, local to cmd/seed since nothing else needs it.
func newUUIDv7() string {
	var b [16]byte

	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	if _, err := rand.Read(b[6:]); err != nil {
		panic("newUUIDv7: reading random bytes: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
