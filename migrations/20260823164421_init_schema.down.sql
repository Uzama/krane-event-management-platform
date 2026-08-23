-- Inverse of 20260823164421_init_schema.up.sql. A migration you cannot roll
-- back has never been tested.
--
-- ORDER MATTERS IN BOTH DIRECTIONS, AND BOTH WERE PROVEN AGAINST postgres:18:
--   1. Default privileges are revoked FOR ROLE krane_migrator — symmetric with
--      the up file. Without this, DROP ROLE krane_app fails: the default-ACL
--      entries count as dependent objects.
--   2. Live grants are revoked while the objects still exist.
--   3. Tables drop in reverse FK order.
--   4. DROP ROLE only once nothing references it.
--   5. Extensions drop LAST — dropping citext while a citext column exists
--      errors ("other objects depend on it"), and CASCADE would take the
--      columns with it.

-- 1. Default privileges. Same FOR ROLE krane_migrator clause as the up file —
--    a mismatched (or omitted) FOR ROLE leaves a pg_default_acl entry behind and
--    DROP ROLE below fails on it. Proven: stripping these two statements makes
--    the down migration exit non-zero at DROP ROLE.
--    The SEQUENCES revoke is kept deliberately even though the up file no longer
--    grants sequence defaults (this schema owns no sequences — every PK is
--    uuidv7()). It is a no-op when nothing was granted, and it guarantees that a
--    future sequence default grant cannot strand this migration's DROP ROLE.
ALTER DEFAULT PRIVILEGES FOR ROLE krane_migrator IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM krane_app;
ALTER DEFAULT PRIVILEGES FOR ROLE krane_migrator IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM krane_app;

-- 2. Live grants, revoked while the objects exist.
REVOKE ALL ON ALL TABLES    IN SCHEMA public FROM krane_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM krane_app;
REVOKE USAGE ON SCHEMA public FROM krane_app;

-- 3. Tables, reverse FK order.
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS invitations;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS rooms;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS event_members;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS users;

-- 4. The runtime role. Guarded the same way it was created.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'krane_app') THEN
        DROP ROLE krane_app;
    END IF;
END
$$;

-- 5. Extensions last.
DROP EXTENSION IF EXISTS btree_gist;
DROP EXTENSION IF EXISTS citext;
