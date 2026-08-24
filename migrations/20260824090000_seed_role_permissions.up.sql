-- Seeds role_permissions -- item 07 (FEATURES.md). Design of record:
-- docs/requirements.md §4. Presence of a row IS the permission (D6); there
-- is no allowed boolean. Runs as krane_migrator, same as every migration --
-- role_permissions is GRANT SELECT only to krane_app (item 02), so this
-- table can only ever change via a migration, never a runtime write.
--
-- Exactly 31 rows -- adapter/authz's tests assert this count against the
-- fully-expanded matrix below, so a dropped row fails loud instead of
-- silently denying a permission that should exist.
--   admin       17  event 3 + member 4 + room 4 + session 4 + invitation 2
--   contributor 12  event 1 + member 1 + room 4 + session 4 + invitation 2
--   attendee     2  event 1 + session 1

INSERT INTO role_permissions (role, resource, action) VALUES
    -- admin -- full control, including member/role management.
    ('admin', 'event',      'read'),
    ('admin', 'event',      'update'),
    ('admin', 'event',      'delete'),
    ('admin', 'member',     'read'),
    ('admin', 'member',     'create'),
    ('admin', 'member',     'delete'),
    ('admin', 'member',     'assign-role'),
    ('admin', 'room',       'create'),
    ('admin', 'room',       'read'),
    ('admin', 'room',       'update'),
    ('admin', 'room',       'delete'),
    ('admin', 'session',    'read'),
    ('admin', 'session',    'create'),
    ('admin', 'session',    'update'),
    ('admin', 'session',    'delete'),
    ('admin', 'invitation', 'create'),
    ('admin', 'invitation', 'read'),

    -- contributor -- manages sessions and invites attendees; no role changes
    -- and no member create/delete (member:assign-role is deliberately
    -- absent -- this is item 07's must: contributor gets 403 on it).
    ('contributor', 'event',      'read'),
    ('contributor', 'member',     'read'),
    ('contributor', 'room',       'create'),
    ('contributor', 'room',       'read'),
    ('contributor', 'room',       'update'),
    ('contributor', 'room',       'delete'),
    ('contributor', 'session',    'read'),
    ('contributor', 'session',    'create'),
    ('contributor', 'session',    'update'),
    ('contributor', 'session',    'delete'),
    ('contributor', 'invitation', 'create'),
    ('contributor', 'invitation', 'read'),

    -- attendee -- read-only on events and sessions they belong to. No
    -- member/room/invitation access at all.
    ('attendee', 'event',   'read'),
    ('attendee', 'session', 'read');
