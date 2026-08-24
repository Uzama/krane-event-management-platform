-- Item 09 (FEATURES.md): event_members shipped in item 02 without a version
-- column, unlike events/rooms/sessions -- a real gap against CLAUDE.md's
-- "every mutable row has a version column" invariant. Role reassignment
-- (PATCH .../members/{memberId}) is a mutation two admins can race on, so
-- it needs the same optimistic-lock protection every other update gets.
-- DEFAULT 1 backfills existing rows (including item 08's auto-admin grant)
-- without a table rewrite -- Postgres 11+ evaluates a constant DEFAULT once
-- at ALTER time rather than rewriting every row.
ALTER TABLE event_members ADD COLUMN version integer NOT NULL DEFAULT 1;
