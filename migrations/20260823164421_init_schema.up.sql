-- Initial schema. Feature 02 (FEATURES.md). Design of record: docs/requirements.md §2, docs/er-diagram.md.
--
-- TWO ROLES, AND ITEM 03 MUST HONOUR BOTH
--   krane_migrator — owns the database and every object the migrations create.
--                    Migrations run as this role: docker-compose sets POSTGRES_USER=krane_migrator,
--                    so the image creates it at init. A migration cannot create the role that runs it.
--   krane_app      — the API's runtime identity, created below. No DDL rights. Item 04's pgx pool
--                    connects as this role.
--   The ALTER DEFAULT PRIVILEGES statements below say FOR ROLE krane_migrator explicitly: default
--   privileges attach to the role that CREATES an object, so if migrations are ever run as some other
--   role, every future table reaches the API with no grants. The two names must stay in step.
--
-- DEFERRED ON PURPOSE
--   The two partial EXCLUDE constraints on sessions  — item 16 (its race test must be able to fail first).
--   The role_permissions seed rows                   — item 07.
--   krane_app's password                             — item 03, from the environment. A credential is
--                                                      never committed in a migration.

-- ---------------------------------------------------------------------------
-- Extensions. Must precede any table that uses them (citext columns below);
-- proven by gate 0 — a citext column without the extension errors outright.
-- btree_gist is created now so item 16 is purely the constraint: an EXCLUDE
-- with `uuid WITH =` needs gist support for the equality operand.
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- ---------------------------------------------------------------------------
-- Runtime role. Roles are cluster-wide, so creation is guarded rather than
-- IF NOT EXISTS (which CREATE ROLE does not support).
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'krane_app') THEN
        CREATE ROLE krane_app LOGIN;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO krane_app;

-- ---------------------------------------------------------------------------
-- users — global identity. Created on first sign-in by mapping the OIDC sub
-- claim to a row; the API validates tokens, it never issues them. No role
-- column: roles are per-event and live only on event_members.
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id         uuid PRIMARY KEY     DEFAULT uuidv7(),
    subject    text        NOT NULL,
    email      citext      NOT NULL,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_subject_key UNIQUE (subject),
    CONSTRAINT users_email_key   UNIQUE (email)
);

COMMENT ON COLUMN users.subject IS 'OIDC sub claim; the only thing the auth middleware looks up.';
COMMENT ON COLUMN users.email   IS 'Never exposed to attendees — item 10 asserts no email key in their responses.';

-- ---------------------------------------------------------------------------
-- events — the aggregate root. Soft-deleted so audit_log history stays
-- resolvable (D4).
-- ---------------------------------------------------------------------------
CREATE TABLE events (
    id          uuid PRIMARY KEY     DEFAULT uuidv7(),
    name        text        NOT NULL,
    description text,
    timezone    text        NOT NULL,
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    version     integer     NOT NULL DEFAULT 1,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT events_ends_after_starts_check CHECK (ends_at > starts_at)
);

COMMENT ON COLUMN events.timezone IS 'IANA name (Asia/Colombo), never a fixed offset — an offset cannot survive a DST transition.';
COMMENT ON COLUMN events.version  IS 'Optimistic lock. Updates are WHERE id = $1 AND version = $2; 0 rows affected is a 409 (item 17).';

-- ---------------------------------------------------------------------------
-- event_members — the sole source of access. No row here, no visibility.
-- ---------------------------------------------------------------------------
CREATE TABLE event_members (
    id         uuid PRIMARY KEY     DEFAULT uuidv7(),
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id),
    role       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT event_members_event_id_user_id_key UNIQUE (event_id, user_id),
    CONSTRAINT event_members_role_check          CHECK (role IN ('admin', 'contributor', 'attendee'))
);

COMMENT ON COLUMN event_members.role IS
    'D10: CHECK-constrained so a typo is rejected at write time rather than silently granting nothing. '
    'A fourth role costs INSERTs into role_permissions plus one ALTER here — a migration, never a handler edit.';

-- ---------------------------------------------------------------------------
-- role_permissions — the data behind the authz chokepoint (item 07).
-- Presence of the row IS the permission; there is no allowed boolean to read
-- backwards (D6). role is deliberately NOT CHECK-constrained: this table is
-- the registry of roles, so adding one stays an INSERT.
-- ---------------------------------------------------------------------------
CREATE TABLE role_permissions (
    role     text NOT NULL,
    resource text NOT NULL,
    action   text NOT NULL,

    CONSTRAINT role_permissions_pkey         PRIMARY KEY (role, resource, action),
    CONSTRAINT role_permissions_resource_chk CHECK (resource IN ('event', 'member', 'room', 'session', 'invitation')),
    CONSTRAINT role_permissions_action_chk   CHECK (action IN ('create', 'read', 'update', 'delete', 'assign-role'))
);

-- ---------------------------------------------------------------------------
-- rooms — per-event (D8). Cross-event physical-room conflicts are out of
-- scope: two events in the same building are two rows here and a clash
-- between them is not detected. See docs/requirements.md §7.1.
-- ---------------------------------------------------------------------------
CREATE TABLE rooms (
    id         uuid PRIMARY KEY     DEFAULT uuidv7(),
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    capacity   integer,
    version    integer     NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT rooms_event_id_name_key UNIQUE (event_id, name),
    CONSTRAINT rooms_capacity_check    CHECK (capacity IS NULL OR capacity > 0)
);

-- ---------------------------------------------------------------------------
-- sessions — carries the two hardest invariants: no double-booking, and
-- correct local time across DST. Time is one tstzrange, not two columns (D5),
-- because an EXCLUDE constraint needs a range type.
--
-- ITEM 16 ADDS, IN ITS OWN MIGRATION:
--   ALTER TABLE sessions ADD CONSTRAINT sessions_room_no_overlap_excl
--       EXCLUDE USING gist (room_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL);
--   ALTER TABLE sessions ADD CONSTRAINT sessions_speaker_no_overlap_excl
--       EXCLUDE USING gist (speaker_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL);
-- Both partial, which encodes product rule D9: cancelling a session frees its
-- room and speaker for that slot immediately. Not added here so item 16's race
-- test can fail for the right reason first.
-- ---------------------------------------------------------------------------
CREATE TABLE sessions (
    id          uuid PRIMARY KEY     DEFAULT uuidv7(),
    event_id    uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    room_id     uuid        NOT NULL REFERENCES rooms (id),
    speaker_id  uuid        NOT NULL REFERENCES users (id),
    title       text        NOT NULL,
    description text,
    time_range  tstzrange   NOT NULL,
    version     integer     NOT NULL DEFAULT 1,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    -- An unbounded or empty range would either block every slot or none of
    -- them once item 16's EXCLUDE lands.
    CONSTRAINT sessions_time_range_bounded_check CHECK (
        lower(time_range) IS NOT NULL
        AND upper(time_range) IS NOT NULL
        AND NOT isempty(time_range)
    )
);

COMMENT ON COLUMN sessions.time_range IS
    'HALF-OPEN [) BY CONVENTION: lower bound inclusive, upper bound exclusive. '
    'Construct every range as tstzrange(starts_at, ends_at, ''[)'') so back-to-back sessions '
    '(10:00-11:00 then 11:00-12:00) share an endpoint WITHOUT overlapping — && is false for [) ranges '
    'that merely touch. A range built as [] would make them falsely conflict under item 16 EXCLUDE. '
    'Items 12 and 16 both depend on this.';
COMMENT ON COLUMN sessions.speaker_id IS 'D3: any user, not an event_members row — an outside speaker should not need a roster row first.';
COMMENT ON COLUMN sessions.deleted_at IS 'D9: soft delete releases the room and speaker slot immediately (item 16 EXCLUDEs are partial on this).';

-- ---------------------------------------------------------------------------
-- invitations — an independent record of who was invited, not a
-- pre-membership state machine (D1). No status, no responded_at.
-- ---------------------------------------------------------------------------
CREATE TABLE invitations (
    id         uuid PRIMARY KEY     DEFAULT uuidv7(),
    event_id   uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id    uuid                 REFERENCES users (id),
    email      citext      NOT NULL,
    role       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT invitations_event_id_email_key UNIQUE (event_id, email),
    CONSTRAINT invitations_role_check         CHECK (role IN ('admin', 'contributor', 'attendee'))
);

COMMENT ON COLUMN invitations.user_id IS 'D2: nullable — an invitation whose recipient must already have an account is not an invitation.';
COMMENT ON CONSTRAINT invitations_event_id_email_key ON invitations IS
    'Row-level invite dedup, independent of item 21 Idempotency-Key replay.';

-- ---------------------------------------------------------------------------
-- audit_log — append-only, written in the same transaction as the mutation it
-- records. entity_id carries no FK on purpose: the log outlives its subject.
-- ---------------------------------------------------------------------------
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY     DEFAULT uuidv7(),
    actor_id    uuid        NOT NULL REFERENCES users (id),
    entity_type text        NOT NULL,
    entity_id   uuid        NOT NULL,
    action      text        NOT NULL,
    before      jsonb,
    after       jsonb,
    request_id  text,
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_log_entity_type_check CHECK (entity_type IN ('event', 'room', 'session', 'event_member', 'invitation')),
    CONSTRAINT audit_log_action_check      CHECK (action IN ('create', 'update', 'delete'))
);

COMMENT ON COLUMN audit_log.actor_id IS 'Who — taken from the authenticated sub, never from the request body.';
COMMENT ON COLUMN audit_log.before   IS 'PG18 UPDATE ... RETURNING OLD.';
COMMENT ON COLUMN audit_log.after    IS 'PG18 UPDATE ... RETURNING NEW.';

-- ---------------------------------------------------------------------------
-- idempotency_keys — the retry is deduplicated by the unique constraint, not
-- by a SELECT first. Check-then-act is exactly what the brief tests for.
-- ---------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    id              uuid PRIMARY KEY     DEFAULT uuidv7(),
    actor_id        uuid        NOT NULL REFERENCES users (id),
    key             text        NOT NULL,
    endpoint        text        NOT NULL,
    request_hash    text        NOT NULL,
    response_status integer     NOT NULL,
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT idempotency_keys_actor_id_key_key UNIQUE (actor_id, key)
);

COMMENT ON COLUMN idempotency_keys.request_hash IS 'Same key replayed with a different body is a 422, not a replay.';

-- ---------------------------------------------------------------------------
-- Indexes. FK columns that get filtered on, plus the keyset-traversal index
-- item 19 paginates 50k invitations through.
-- ---------------------------------------------------------------------------
CREATE INDEX event_members_user_id_idx      ON event_members (user_id);
CREATE INDEX rooms_event_id_idx             ON rooms (event_id);
CREATE INDEX sessions_event_id_idx          ON sessions (event_id);
CREATE INDEX sessions_room_id_idx           ON sessions (room_id);
CREATE INDEX sessions_speaker_id_idx        ON sessions (speaker_id);
CREATE INDEX invitations_keyset_idx         ON invitations (event_id, created_at, id);
CREATE INDEX invitations_user_id_idx        ON invitations (user_id);
CREATE INDEX audit_log_entity_idx           ON audit_log (entity_type, entity_id);
CREATE INDEX audit_log_actor_id_idx         ON audit_log (actor_id);

-- ---------------------------------------------------------------------------
-- Grants. Table by table, by name — there is deliberately no
-- GRANT ... ON ALL TABLES IN SCHEMA public for audit_log to be swept up by.
-- ---------------------------------------------------------------------------
-- Ordinary data tables: full DML.
GRANT SELECT, INSERT, UPDATE, DELETE ON users            TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON events           TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON event_members    TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON rooms            TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON sessions         TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON invitations      TO krane_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON idempotency_keys TO krane_app;

-- TWO TABLES ARE SPECIAL-CASED. Both get their own grant here and their own
-- unconditional REVOKE at the foot of this file.
--
--   audit_log        — append-only. INSERT and SELECT, nothing else.
--   role_permissions — READ-ONLY TO THE API. It is the authorization policy the
--                      chokepoint (item 07) loads at runtime; the API must not be
--                      able to rewrite the rules that govern it. Seeding and any
--                      later change to the permission matrix is a migration, run
--                      as krane_migrator — never a runtime write.
GRANT SELECT, INSERT ON audit_log        TO krane_app;
GRANT SELECT           ON role_permissions TO krane_app;

-- Future migrations' tables reach the API without each having to remember a
-- GRANT. FOR ROLE krane_migrator, not the session role — see the header.
-- Item 22: any future append-only or policy table needs its own REVOKE like
-- the ones below; a default grant will otherwise hand it full DML.
--
-- No ON SEQUENCES counterpart: every primary key in this schema is uuidv7()
-- and no column is serial/bigserial/identity, so the schema owns no sequences.
-- A migration that introduces one must add the sequence grant with it.
ALTER DEFAULT PRIVILEGES FOR ROLE krane_migrator IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO krane_app;

-- LAST STATEMENTS IN THIS FILE, AND THEY MUST STAY LAST.
-- Append-only and read-only-policy are grants, not conventions. These revokes
-- are unconditional and order-independent: they hold no matter how the
-- statements above are later reordered, and no matter what a future blanket
-- grant does. Do not rely on either table merely having been created before
-- ALTER DEFAULT PRIVILEGES.
REVOKE UPDATE, DELETE, TRUNCATE         ON audit_log        FROM krane_app, PUBLIC;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON role_permissions FROM krane_app, PUBLIC;
