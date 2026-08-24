-- Item 23 (FEATURES.md, go-further pick): recurring sessions, deliberately
-- cut down (TRADEOFFS.md carries the full list of what was cut). A series
-- is eagerly materialized into ordinary sessions rows at creation time --
-- each occurrence is a real session, subject to item 16's EXCLUDE
-- constraints, item 17's version-gating, and item 22's audit trail
-- unchanged, exactly like a session created one at a time. There is no
-- separate recurring-scheduling engine.
CREATE TABLE session_series (
    id             uuid PRIMARY KEY     DEFAULT uuidv7(),
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    room_id        uuid        NOT NULL REFERENCES rooms (id),
    speaker_id     uuid        NOT NULL REFERENCES users (id),
    title          text        NOT NULL,
    description    text,
    freq           text        NOT NULL,
    interval_count integer     NOT NULL DEFAULT 1,
    occurrences    integer     NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT session_series_freq_check        CHECK (freq IN ('daily', 'weekly')),
    CONSTRAINT session_series_interval_check     CHECK (interval_count > 0),
    CONSTRAINT session_series_occurrences_check  CHECK (occurrences BETWEEN 1 AND 52)
);

COMMENT ON TABLE session_series IS
    'A recurrence rule, kept purely for history/attribution -- materialize-on-read was cut (TRADEOFFS.md); every occurrence already exists as a sessions row by the time this is read.';

-- sessions.series_id is nullable: most sessions are still standalone. Set
-- once, at materialization, and never changed afterward -- occurrence
-- edits/cancellations go through the ordinary session PATCH/DELETE paths
-- and are recorded in session_exceptions, not by moving a session out of
-- its series.
ALTER TABLE sessions ADD COLUMN series_id uuid REFERENCES session_series (id);

CREATE INDEX sessions_series_id_idx ON sessions (series_id) WHERE series_id IS NOT NULL;

-- session_exceptions is a history log, not a scheduling input: a series
-- occurrence's actual current state always lives in its sessions row
-- (including a soft-deleted one). This table only records THAT an
-- occurrence deviated from the series' original plan, and how.
CREATE TABLE session_exceptions (
    id               uuid PRIMARY KEY     DEFAULT uuidv7(),
    series_id        uuid        NOT NULL REFERENCES session_series (id) ON DELETE CASCADE,
    session_id       uuid        NOT NULL REFERENCES sessions (id),
    status           text        NOT NULL,
    original_starts_at timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT session_exceptions_status_check CHECK (status IN ('modified', 'cancelled'))
);

COMMENT ON COLUMN session_exceptions.original_starts_at IS
    'The occurrence''s starts_at as materialized, before this exception''s edit/cancel -- lets a reader tell "moved to a new time" apart from "cancelled in place" without re-deriving the series rule.';
