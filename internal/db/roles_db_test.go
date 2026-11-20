package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// runtimeRole is the login role of the application (deployments/sql/roles.sql).
const runtimeRole = "altserv_runtime"

// TestRuntimeRoleCannotEscalate checks that the application login role cannot
// change the schema, switch off Row Level Security or rewrite the audit log
// (WP-04). It is skipped unless TEST_DB_URL is set and
// deployments/sql/roles.sql was applied. The roles are optional in dev.
//
// TEST_DB_URL usually connects as a superuser for seeding. The test seeds as
// that user and then runs SET LOCAL ROLE altserv_runtime, so every check runs
// with the privileges and attributes of the runtime role.
func TestRuntimeRoleCannotEscalate(t *testing.T) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping database integration test")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	ctx := context.Background()

	var exists bool
	if err := conn.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", runtimeRole,
	).Scan(&exists); err != nil {
		t.Fatalf("look up %s: %v", runtimeRole, err)
	}
	if !exists {
		t.Skipf("role %s does not exist; apply deployments/sql/roles.sql to run this test", runtimeRole)
	}

	t.Run("role attributes", func(t *testing.T) {
		var super, bypass, createDB, createRole, login bool
		err := conn.QueryRowContext(ctx, `
			SELECT rolsuper, rolbypassrls, rolcreatedb, rolcreaterole, rolcanlogin
			FROM pg_roles WHERE rolname = $1`, runtimeRole,
		).Scan(&super, &bypass, &createDB, &createRole, &login)
		if err != nil {
			t.Fatalf("read role attributes: %v", err)
		}
		if super || bypass || createDB || createRole {
			t.Errorf("%s: super=%v bypassrls=%v createdb=%v createrole=%v, want all false",
				runtimeRole, super, bypass, createDB, createRole)
		}
		if !login {
			t.Errorf("%s cannot log in", runtimeRole)
		}

		var member bool
		if err := conn.QueryRowContext(ctx,
			"SELECT pg_has_role($1, $2, 'MEMBER')", runtimeRole, tenantRole,
		).Scan(&member); err != nil {
			t.Fatalf("check membership: %v", err)
		}
		if !member {
			t.Errorf("%s is not a member of %s", runtimeRole, tenantRole)
		}
	})

	t.Run("CREATE TABLE fails", func(t *testing.T) {
		tx := beginAsRuntime(t, conn)
		defer tx.Rollback()

		_, err := tx.ExecContext(ctx, "CREATE TABLE wp04_escalation_probe (id integer)")
		requireInsufficientPrivilege(t, err)
	})

	t.Run("row_security off does not bypass RLS", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		tenant := createRLSTenant(t, queries)
		createRLSUser(t, queries, tenant.ID, "wp04-runtime@example.com")
		setRuntimeRole(t, tx)

		if _, err := tx.ExecContext(ctx, "SET LOCAL row_security = off"); err != nil {
			t.Fatalf("set row_security: %v", err)
		}
		var n int
		err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE tenant_id = $1", tenant.ID).Scan(&n)
		if err == nil {
			t.Fatalf("query with row_security = off returned %d rows, want an error", n)
		}
		requireInsufficientPrivilege(t, err)
	})

	t.Run("DELETE FROM audit_events fails", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		tenant := createRLSTenant(t, queries)
		if _, err := queries.CreateAuditEvent(ctx, CreateAuditEventParams{
			TenantID:   tenant.ID,
			Action:     "wp04.probe",
			EntityType: "tenant",
			Metadata:   []byte("{}"),
		}); err != nil {
			t.Fatalf("seed audit event: %v", err)
		}
		setRuntimeRole(t, tx)

		// Even with the tenant context and the termination switch of
		// migration 025, the runtime role has no DELETE privilege.
		if _, err := tx.ExecContext(ctx,
			"SELECT set_config('app.tenant_id', $1, true), set_config('app.allow_audit_delete', 'true', true)",
			tenant.ID.String(),
		); err != nil {
			t.Fatalf("set tenant context: %v", err)
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM audit_events WHERE tenant_id = $1", tenant.ID)
		requireInsufficientPrivilege(t, err)
	})
}

func beginAsRuntime(t *testing.T, conn *sql.DB) *sql.Tx {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	setRuntimeRole(t, tx)
	return tx
}

func setRuntimeRole(t *testing.T, tx *sql.Tx) {
	t.Helper()

	if _, err := tx.Exec("SET LOCAL ROLE " + runtimeRole); err != nil {
		t.Fatalf("set role %s: %v", runtimeRole, err)
	}
}

func requireInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("statement succeeded, want insufficient_privilege (42501)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("error = %v, want insufficient_privilege (42501)", err)
	}
}
