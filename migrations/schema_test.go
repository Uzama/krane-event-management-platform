// Package migrations_test proves that the migrations in this directory actually
// applied, against a real Postgres, as the role the API will really use.
//
// It is deliberately the smallest test that exercises the whole `make up`
// pipeline end to end: compose starts Postgres as krane_migrator -> golang-migrate
// applies the schema -> the one-shot app-role service sets krane_app's password
// from the environment -> this test connects as krane_app and finds both the
// schema and the privilege matrix it is supposed to have. A test that only
// asserted 1 == 1 would prove none of those five links.
package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Matches the Makefile's TEST_DATABASE_URL default. Duplicated rather than
// imported because there is no config package until item 04, and a test that
// silently picked up a wrong default would be worse than one that repeats it.
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

// The eleven tables of docs/er-diagram.md: the nine from the init migration
// plus item 23's session_series / session_exceptions. Listed literally: the
// point is to catch a migration that half-applied, so deriving this from the
// migration files would defeat the check. (Feature 29 found this list stuck
// at nine for two migrations -- a half-applied recurring-sessions migration
// would have passed.)
var schemaTables = []string{
	"users",
	"events",
	"event_members",
	"role_permissions",
	"rooms",
	"sessions",
	"invitations",
	"audit_log",
	"idempotency_keys",
	"session_series",
	"session_exceptions",
}

// runtimeRole is the no-DDL identity the API connects as. krane_migrator owns
// the objects; krane_app must never be the one running tests, or the privilege
// assertions below would pass vacuously (an owner implicitly holds every
// privilege, so has_table_privilege lies for krane_migrator).
const runtimeRole = "krane_app"

func connect(t *testing.T) *pgx.Conn {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		// Fail loudly rather than t.Skip. A skipped test is a green build that
		// proved nothing, which is exactly how a broken pipeline ships.
		t.Fatalf("cannot reach the test database: %v\n\n"+
			"The suite needs Postgres. Run `make up` first, or `make test`, which does it for you.\n"+
			"TEST_DATABASE_URL=%s", err, url)
	}

	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := conn.Close(closeCtx); err != nil {
			t.Errorf("closing the test connection: %v", err)
		}
	})

	return conn
}

// TestConnectsAsRuntimeRole proves the DSN points at the API's runtime identity,
// not at the migrator that owns the schema. Every other assertion in this file
// depends on it: privileges are meaningless when checked as the owner.
func TestConnectsAsRuntimeRole(t *testing.T) {
	conn := connect(t)

	var user string
	if err := conn.QueryRow(context.Background(), "SELECT current_user").Scan(&user); err != nil {
		t.Fatalf("SELECT current_user: %v", err)
	}

	if user != runtimeRole {
		t.Fatalf("connected as %q, want %q -- the test DSN is pointing at the wrong role", user, runtimeRole)
	}
}

// TestSchemaTablesExist proves the migrations ran against the throwaway database,
// not just against the dev one.
func TestSchemaTablesExist(t *testing.T) {
	conn := connect(t)

	rows, err := conn.Query(context.Background(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatalf("listing public tables: %v", err)
	}

	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating public tables: %v", err)
	}

	var missing []string
	for _, table := range schemaTables {
		if !found[table] {
			missing = append(missing, table)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("migrations did not create: %s\n"+
			"Found instead: %v", strings.Join(missing, ", "), keys(found))
	}
}

// TestPrivilegeMatrixApplied proves the GRANTs and REVOKEs landed too, not only
// the CREATE TABLEs.
//
// This is the assertion that earns its keep. Grants are per-database, so a
// migration that created every table in krane_test while silently skipping every
// GRANT and REVOKE would sail past a schema-only check -- and then every later
// authz and audit test would be running against privileges the API does not
// actually have in production. audit_log being append-only and role_permissions
// being read-only are grants, not conventions (CLAUDE.md), so they are checked
// as grants.
func TestPrivilegeMatrixApplied(t *testing.T) {
	conn := connect(t)

	cases := []struct {
		table     string
		privilege string
		want      bool
		why       string
	}{
		{"audit_log", "SELECT", true, "the API reads its own audit trail"},
		{"audit_log", "INSERT", true, "audit rows are written in the mutation's transaction"},
		{"audit_log", "UPDATE", false, "append-only: history must not be rewritable"},
		{"audit_log", "DELETE", false, "append-only: history must not be erasable"},
		{"role_permissions", "SELECT", true, "the authz chokepoint loads the policy at runtime"},
		{"role_permissions", "INSERT", false, "the API must not be able to grant itself permissions"},
		{"role_permissions", "UPDATE", false, "the API must not be able to rewrite the rules that govern it"},
		{"role_permissions", "DELETE", false, "the API must not be able to delete the rules that govern it"},
		{"events", "SELECT", true, "ordinary data table: full DML"},
		{"events", "UPDATE", true, "ordinary data table: full DML"},
		{"events", "DELETE", true, "ordinary data table: full DML"},
		// Item 23's tables were created by a later migration, so they get their
		// privileges from the init migration's ALTER DEFAULT PRIVILEGES, not an
		// explicit GRANT -- these rows prove that default actually applied.
		{"session_series", "SELECT", true, "item 23: the API reads series rules"},
		{"session_series", "INSERT", true, "item 23: the API creates series rules"},
		{"session_exceptions", "SELECT", true, "item 23: the API reads schedule history"},
		{"session_exceptions", "INSERT", true, "item 23: the API records occurrence edits and cancellations"},
	}

	for _, tc := range cases {
		var got bool
		err := conn.QueryRow(context.Background(),
			"SELECT has_table_privilege($1, $2, $3)", runtimeRole, tc.table, tc.privilege).Scan(&got)
		if err != nil {
			t.Fatalf("has_table_privilege(%s, %s, %s): %v", runtimeRole, tc.table, tc.privilege, err)
		}

		if got != tc.want {
			t.Errorf("%s has %s on %s = %t, want %t -- %s",
				runtimeRole, tc.privilege, tc.table, got, tc.want, tc.why)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
