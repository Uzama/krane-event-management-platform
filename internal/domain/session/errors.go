package session

import "errors"

// ErrInvalidRoom means room_id doesn't reference a room belonging to this
// session's event -- maps to 404 (room_not_found), the same treatment a
// room existing in a different event already gets via room.Repository.Get
// (item 11). Per-aggregate sentinel beyond domain's shared four, same
// precedent as domain/user's ErrTokenInvalid/ErrMissingClaims.
var ErrInvalidRoom = errors.New("session: room does not belong to this event")

// ErrInvalidSpeaker means speaker_id doesn't reference an existing user --
// maps to 404 (speaker_not_found).
var ErrInvalidSpeaker = errors.New("session: speaker does not exist")
