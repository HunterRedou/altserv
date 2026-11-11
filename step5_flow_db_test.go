package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestTenantTermination covers Step 5: a tenant with audit events and care data
// can be terminated through the controlled procedure, and the audit trigger still
// blocks ordinary deletes.
func TestTenantTermination(t *testing.T) {
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

	tx, queries := newTestTx(t, conn)
	defer tx.Rollback()

	ctx := context.Background()
	tenant := createTenant(t, queries, "valid")
	chef := createUserWithPassword(t, queries, tenant.ID, "chef@term.de", "secret", "chef")

	// Add care data and an audit event so the termination has something to
	// remove and the append-only trigger is in the way.
	patientID := uuid.New()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO patients (id, tenant_id, first_name_enc, last_name_enc, birth_date_enc, key_version)
		VALUES ($1, $2, 'a', 'b', 'c', 1)`, patientID, tenant.ID); err != nil {
		t.Fatalf("seed patient: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (tenant_id, actor_user_id, action, entity_type)
		VALUES ($1, $2, 'test.action', 'test')`, tenant.ID, chef.ID); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}

	// The append-only trigger still blocks an ordinary delete.
	if _, err := tx.ExecContext(ctx, "SAVEPOINT audit_append_only"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM audit_events WHERE tenant_id = $1", tenant.ID); err == nil {
		t.Fatal("ordinary delete from audit_events succeeded; trigger is broken")
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT audit_append_only"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Terminate through the controlled procedure.
	if _, err := tx.ExecContext(ctx, "SELECT terminate_tenant($1)", tenant.ID); err != nil {
		t.Fatalf("terminate_tenant: %v", err)
	}

	for _, check := range []struct {
		table string
	}{
		{"tenants"},
		{"users"},
		{"patients"},
		{"audit_events"},
	} {
		var count int
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE tenant_id = $1", check.table)
		if check.table == "tenants" {
			query = "SELECT count(*) FROM tenants WHERE id = $1"
		}
		if err := tx.QueryRowContext(ctx, query, tenant.ID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows for terminated tenant = %d, want 0", check.table, count)
		}
	}

	// A second termination is a not-found error.
	if _, err := tx.ExecContext(ctx, "SELECT terminate_tenant($1)", tenant.ID); err == nil {
		t.Fatal("terminating a missing tenant succeeded")
	}
}
