-- Item 08 (FEATURES.md): the keyset-traversal index GET /v1/events paginates
-- through. Mirrors invitations_keyset_idx's shape (item 02) -- without an
-- index matching the ORDER BY exactly, the keyset scan silently degrades to
-- a full sort per page instead of an index range scan. Partial on
-- deleted_at IS NULL: soft-deleted events never appear in a list page, so
-- there is no reason for the index to carry them.
CREATE INDEX events_keyset_idx ON events (created_at, id) WHERE deleted_at IS NULL;
