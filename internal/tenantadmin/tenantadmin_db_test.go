package tenantadmin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestApplyDatabase needs a migrated Postgres in TEST_DB_URL. Every subtest
// runs in one transaction that is rolled back.
func TestApplyDatabase(t *testing.T) {
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

	t.Run("activate on a pending tenant sets valid and writes one audit event", func(t *testing.T) {
		tx, run := beginTx(t, conn)
		tenant := createPendingTenant(t, tx)

		result, err := Apply(ctx, run, audit.SQLRecorder{}, tenant.ID, ActionActivate, "req-activate")
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !result.Changed || result.OldStatus != "pending" || result.NewStatus != "valid" {
			t.Fatalf("result = %+v, want pending -> valid", result)
		}
		if got := tenantStatus(t, tx, tenant.ID); got != "valid" {
			t.Fatalf("status = %q, want valid", got)
		}

		events := auditEvents(t, tx, tenant.ID)
		if len(events) != 1 {
			t.Fatalf("audit events = %d, want 1", len(events))
		}
		event := events[0]
		if event.action != "tenant.activated" || event.entityType != "tenant" {
			t.Fatalf("event = %+v, want tenant.activated on tenant", event)
		}
		want := map[string]string{"old_status": "pending", "new_status": "valid"}
		if len(event.metadata) != len(want) {
			t.Fatalf("metadata = %v, want only %v", event.metadata, want)
		}
		for key, value := range want {
			if event.metadata[key] != value {
				t.Fatalf("metadata[%q] = %v, want %q", key, event.metadata[key], value)
			}
		}

		// A repeated activate changes nothing and writes no second event.
		result, err = Apply(ctx, run, audit.SQLRecorder{}, tenant.ID, ActionActivate, "req-again")
		if err != nil {
			t.Fatalf("Apply again: %v", err)
		}
		if result.Changed {
			t.Fatal("second activate reported a change")
		}
		if got := len(auditEvents(t, tx, tenant.ID)); got != 1 {
			t.Fatalf("audit events after repeat = %d, want 1", got)
		}
	})

	t.Run("suspend keeps the tenant and writes tenant.suspended", func(t *testing.T) {
		tx, run := beginTx(t, conn)
		tenant := createPendingTenant(t, tx)
		if _, err := Apply(ctx, run, audit.SQLRecorder{}, tenant.ID, ActionActivate, "req-1"); err != nil {
			t.Fatalf("activate: %v", err)
		}

		result, err := Apply(ctx, run, audit.SQLRecorder{}, tenant.ID, ActionSuspend, "req-2")
		if err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if result.OldStatus != "valid" || result.NewStatus != "suspended" {
			t.Fatalf("result = %+v, want valid -> suspended", result)
		}
		if got := tenantStatus(t, tx, tenant.ID); got != "suspended" {
			t.Fatalf("status = %q, want suspended", got)
		}
		// Both events share the transaction timestamp, so check membership,
		// not order.
		events := auditEvents(t, tx, tenant.ID)
		suspended := 0
		for _, event := range events {
			if event.action == "tenant.suspended" {
				suspended++
				if event.metadata["old_status"] != "valid" || event.metadata["new_status"] != "suspended" {
					t.Fatalf("suspend metadata = %v", event.metadata)
				}
			}
		}
		if len(events) != 2 || suspended != 1 {
			t.Fatalf("events = %+v, want one activated and one suspended", events)
		}
	})

	t.Run("activate on a terminated tenant fails", func(t *testing.T) {
		tx, run := beginTx(t, conn)
		tenant := createPendingTenant(t, tx)
		if _, err := tx.ExecContext(ctx, "SELECT terminate_tenant($1)", tenant.ID); err != nil {
			t.Fatalf("terminate tenant: %v", err)
		}

		_, err := Apply(ctx, run, audit.SQLRecorder{}, tenant.ID, ActionActivate, "req-terminated")
		if !errors.Is(err, ErrTenantNotFound) {
			t.Fatalf("Apply error = %v, want ErrTenantNotFound", err)
		}

		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tenants WHERE id = $1", tenant.ID).Scan(&count); err != nil {
			t.Fatalf("count tenants: %v", err)
		}
		if count != 0 {
			t.Fatal("activate recreated a terminated tenant")
		}
	})
}

func beginTx(t *testing.T, conn *sql.DB) (*sql.Tx, TenantRunner) {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	run := func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
		return db.WithTenantTx(ctx, tx, tenantID, fn)
	}
	return tx, run
}

func createPendingTenant(t *testing.T, tx *sql.Tx) db.Tenant {
	t.Helper()
	tenant, err := db.New(tx).CreateTenant(context.Background(), db.CreateTenantParams{
		Email:          "tenantadmin-" + uuid.NewString() + "@example.invalid",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if tenant.Status != "pending" {
		t.Fatalf("new tenant status = %q, want pending", tenant.Status)
	}
	return tenant
}

func tenantStatus(t *testing.T, tx *sql.Tx, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := tx.QueryRow("SELECT status FROM tenants WHERE id = $1", id).Scan(&status); err != nil {
		t.Fatalf("read tenant status: %v", err)
	}
	return status
}

type auditRow struct {
	action     string
	entityType string
	metadata   map[string]any
}

func auditEvents(t *testing.T, tx *sql.Tx, tenantID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := tx.Query(`SELECT action, entity_type, metadata FROM audit_events
		WHERE tenant_id = $1 AND action LIKE 'tenant.%' ORDER BY occurred_at, id`, tenantID)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()

	var events []auditRow
	for rows.Next() {
		var row auditRow
		var raw []byte
		if err := rows.Scan(&row.action, &row.entityType, &raw); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		if err := json.Unmarshal(raw, &row.metadata); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit events: %v", err)
	}
	return events
}
