-- Item 16 (FEATURES.md): the two EXCLUDE constraints deferred at item 02, now
-- added exactly as commented in migrations/20260823164421_init_schema.up.sql.
-- Both are partial on deleted_at IS NULL, encoding D9: cancelling a session
-- frees its room and speaker slot immediately. btree_gist (needed for the
-- plain-equality room_id/speaker_id operand alongside the range's &&) was
-- already created in the init migration for exactly this.
ALTER TABLE sessions ADD CONSTRAINT sessions_room_no_overlap_excl
    EXCLUDE USING gist (room_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL);

ALTER TABLE sessions ADD CONSTRAINT sessions_speaker_no_overlap_excl
    EXCLUDE USING gist (speaker_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL);
