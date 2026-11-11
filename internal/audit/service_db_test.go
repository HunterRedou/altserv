package audit

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestAuditServiceDatabase covers the restricted read, tenant isolation,
// filters, the audit-of-read and the append-only guarantees. It is skipped
// unless TEST_DB_URL is set and migration 021 is applied.
func TestAuditServiceDatabase(t *testing.T) {
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
	service := NewService(NewRepository(), SQLRecorder{})

	t.Run("chef reads own tenant and the read is audited", func(t *testing.T) {
		tx, queries := newAuditTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAuditTenant(t, queries)
		employee := seedAuditUser(t, queries, tenant.ID, "employee")

		recordAuditEvent(t, tx, tenant.ID, chef.ID, "patient.created", "patient", uuid.New())
		recordAuditEvent(t, tx, tenant.ID, chef.ID, "patient.created", "patient", uuid.New())
		recordAuditEvent(t, tx, tenant.ID, employee.ID, "auth.login", "user", employee.ID)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		var events []EventRecord
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			events, err = service.List(ctx, q, chefActor, "req-list", ListFilter{})
			return err
		}); err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(events) != 3 {
			t.Fatalf("events = %d, want 3", len(events))
		}
		for _, event := range events {
			if event.TenantID != tenant.ID {
				t.Fatalf("event tenant = %s, want %s", event.TenantID, tenant.ID)
			}
		}

		// The read itself is audited, so the next read sees the audit.listed
		// event of the previous one.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			events, err = service.List(ctx, q, chefActor, "req-list-2", ListFilter{})
			return err
		}); err != nil {
			t.Fatalf("list again: %v", err)
		}
		foundListed := false
		for _, event := range events {
			if event.Action == "audit.listed" {
				foundListed = true
				if event.ActorUserID == nil || *event.ActorUserID != chef.ID {
					t.Fatalf("audit.listed actor = %v", event.ActorUserID)
				}
			}
		}
		if !foundListed {
			t.Fatal("audit.listed event not found")
		}

		// A filtered read is also audited, and returns only the filter match.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			events, err = service.List(ctx, q, chefActor, "req-filter", ListFilter{Action: "patient.created"})
			return err
		}); err != nil {
			t.Fatalf("list filtered: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("filtered events = %d, want 2", len(events))
		}
		for _, event := range events {
			if event.Action != "patient.created" {
				t.Fatalf("filtered action = %q", event.Action)
			}
		}
	})

	t.Run("employee and platform admin are denied and the denial is audited", func(t *testing.T) {
		tx, queries := newAuditTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAuditTenant(t, queries)
		employee := seedAuditUser(t, queries, tenant.ID, "employee")
		admin := seedAuditUser(t, queries, tenant.ID, "platform_admin")

		for _, actor := range []Actor{
			{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"},
			{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"},
		} {
			if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				_, err := service.List(ctx, q, actor, "req-denied", ListFilter{})
				return err
			}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s err = %v, want ErrForbidden", actor.Role, err)
			}
		}

		var events []EventRecord
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			events, err = service.List(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req-check", ListFilter{Action: "access.denied"})
			return err
		}); err != nil {
			t.Fatalf("list denials: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("denied events = %d, want 2", len(events))
		}
	})

	t.Run("another tenant cannot read the events", func(t *testing.T) {
		tx, queries := newAuditTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedAuditTenant(t, queries)
		tenantB, chefB := seedAuditTenant(t, queries)
		recordAuditEvent(t, tx, tenantA.ID, chefA.ID, "patient.created", "patient", uuid.New())
		recordAuditEvent(t, tx, tenantB.ID, chefB.ID, "patient.updated", "patient", uuid.New())

		var events []EventRecord
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			events, err = service.List(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "", ListFilter{})
			return err
		}); err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, event := range events {
			if event.TenantID != tenantA.ID {
				t.Fatalf("leaked event from tenant %s", event.TenantID)
			}
		}
	})

	t.Run("filters and limits", func(t *testing.T) {
		tx, queries := newAuditTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAuditTenant(t, queries)
		recordAuditEvent(t, tx, tenant.ID, chef.ID, "patient.created", "patient", uuid.New())
		recordAuditEvent(t, tx, tenant.ID, chef.ID, "patient.created", "patient", uuid.New())
		recordAuditEvent(t, tx, tenant.ID, chef.ID, "auth.login", "user", chef.ID)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		now := time.Now()
		from := now.Add(-24 * time.Hour)
		to := now.Add(24 * time.Hour)

		cases := []struct {
			name   string
			filter ListFilter
			want   int
		}{
			{name: "action", filter: ListFilter{Action: "patient.created"}, want: 2},
			{name: "entity type", filter: ListFilter{EntityType: "user"}, want: 1},
			{name: "time window", filter: ListFilter{EntityType: "patient", From: &from, To: &to}, want: 2},
			{name: "limit", filter: ListFilter{Limit: 1}, want: 1},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				var events []EventRecord
				if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
					var err error
					events, err = service.List(ctx, q, chefActor, "", tt.filter)
					return err
				}); err != nil {
					t.Fatalf("list: %v", err)
				}
				if len(events) != tt.want {
					t.Fatalf("events = %d, want %d", len(events), tt.want)
				}
			})
		}
	})

	t.Run("audit rows are append-only", func(t *testing.T) {
		tx, queries := newAuditTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAuditTenant(t, queries)
		recordAuditEvent(t, tx, tenant.ID, chef.ID, "patient.created", "patient", uuid.New())

		statements := []struct {
			name string
			sql  string
		}{
			{name: "update", sql: `UPDATE audit_events SET action = 'tampered'`},
			{name: "delete", sql: `DELETE FROM audit_events`},
			{name: "truncate", sql: `TRUNCATE audit_events`},
		}
		for _, tt := range statements {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := tx.ExecContext(ctx, "SAVEPOINT append_only"); err != nil {
					t.Fatalf("savepoint: %v", err)
				}
				if _, err := tx.ExecContext(ctx, tt.sql); err == nil {
					t.Fatalf("%s was allowed", tt.name)
				}
				if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT append_only"); err != nil {
					t.Fatalf("rollback: %v", err)
				}
			})
		}

		// The application role can append and read but not rewrite.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := q.CreateAuditEvent(ctx, db.CreateAuditEventParams{
				TenantID:    tenant.ID,
				ActorUserID: uuid.NullUUID{UUID: chef.ID, Valid: true},
				Action:      "patient.read",
				EntityType:  "patient",
				RequestID:   "req-append",
				Metadata:    []byte(`{}`),
			})
			return err
		}); err != nil {
			t.Fatalf("append as app role: %v", err)
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE tenant_id = $1`, tenant.ID)
			return err
		}); err == nil {
			t.Fatal("app role could delete audit rows")
		}
	})
}

func newAuditTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedAuditTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "audit-" + uuid.NewString() + "@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenant, err = queries.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{ID: tenant.ID, Status: "valid"})
	if err != nil {
		t.Fatalf("set tenant status: %v", err)
	}
	return tenant, seedAuditUser(t, queries, tenant.ID, "chef")
}

func seedAuditUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
	t.Helper()

	user, err := queries.CreateUser(context.Background(), db.CreateUserParams{
		Name:           role,
		Role:           role,
		HashedPassword: "hash",
		Email:          role + "-" + uuid.NewString() + "@example.com",
		TenantID:       tenantID,
	})
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return user
}

func recordAuditEvent(t *testing.T, tx *sql.Tx, tenantID, actorID uuid.UUID, action, entityType string, entityID uuid.UUID) {
	t.Helper()

	if err := db.WithTenantTx(context.Background(), tx, tenantID, func(q *db.Queries) error {
		return (SQLRecorder{}).Record(context.Background(), q, Event{
			TenantID:    tenantID,
			ActorUserID: actorID,
			Action:      action,
			EntityType:  entityType,
			EntityID:    entityID,
			RequestID:   "req-seed",
		})
	}); err != nil {
		t.Fatalf("record %s: %v", action, err)
	}
}
