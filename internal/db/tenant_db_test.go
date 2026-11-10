package db

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestRLS exercises Row Level Security against a real PostgreSQL database.
// It is skipped unless TEST_DB_URL is set and migration 012 is applied.
func TestRLS(t *testing.T) {
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

	t.Run("tenant A only sees its own rows", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		ctx := context.Background()
		tenantA := createRLSTenant(t, queries)
		tenantB := createRLSTenant(t, queries)
		userA := createRLSUser(t, queries, tenantA.ID, "employee-a@example.com")
		createRLSUser(t, queries, tenantB.ID, "employee-b@example.com")

		err := WithTenantTx(ctx, tx, tenantA.ID, func(scoped *Queries) error {
			users, err := scoped.ListUsersByTenant(ctx, tenantA.ID)
			if err != nil {
				return err
			}
			if len(users) != 1 || users[0].ID != userA.ID {
				t.Errorf("tenant A users = %v, want only %s", users, userA.ID)
			}

			// Asking explicitly for tenant B must still return nothing.
			usersB, err := scoped.ListUsersByTenant(ctx, tenantB.ID)
			if err != nil {
				return err
			}
			if len(usersB) != 0 {
				t.Errorf("tenant A context saw %d tenant B users", len(usersB))
			}

			var tenants int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tenants").Scan(&tenants); err != nil {
				return err
			}
			if tenants != 1 {
				t.Errorf("visible tenants = %d, want 1", tenants)
			}

			var sessions int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE tenant_id = $1", tenantB.ID).Scan(&sessions); err != nil {
				return err
			}
			if sessions != 0 {
				t.Errorf("tenant A context saw %d tenant B sessions", sessions)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("tenant-scoped queries: %v", err)
		}
	})

	t.Run("cross-tenant insert is rejected", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		ctx := context.Background()
		tenantA := createRLSTenant(t, queries)
		tenantB := createRLSTenant(t, queries)

		err := WithTenantTx(ctx, tx, tenantA.ID, func(scoped *Queries) error {
			_, err := scoped.CreateUser(ctx, CreateUserParams{
				Name:           "Intruder",
				Role:           "employee",
				HashedPassword: "hash",
				Email:          "intruder@example.com",
				TenantID:       tenantB.ID,
			})
			if err == nil {
				t.Error("RLS allowed a cross-tenant insert")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("tenant-scoped insert: %v", err)
		}
	})

	t.Run("no tenant context sees no rows", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		ctx := context.Background()
		tenant := createRLSTenant(t, queries)
		createRLSUser(t, queries, tenant.ID, "employee@example.com")

		if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+tenantRole); err != nil {
			t.Fatalf("set role: %v", err)
		}

		users, err := queries.ListUsersByTenant(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list users: %v", err)
		}
		if len(users) != 0 {
			t.Fatalf("app role without tenant context saw %d users, want 0", len(users))
		}
	})

	t.Run("session cannot reference a user from another tenant", func(t *testing.T) {
		tx, queries := newRLSTx(t, conn)
		defer tx.Rollback()

		ctx := context.Background()
		tenantA := createRLSTenant(t, queries)
		tenantB := createRLSTenant(t, queries)
		userA := createRLSUser(t, queries, tenantA.ID, "employee-a@example.com")

		token := []byte("composite-fk-test-token")
		_, err := queries.CreateSession(ctx, CreateSessionParams{
			TokenHash: token,
			UserID:    userA.ID,
			TenantID:  tenantB.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err == nil {
			t.Fatal("composite foreign key allowed a session across tenants")
		}
	})
}

func TestWithTenantRejectsNilTenant(t *testing.T) {
	err := WithTenant(context.Background(), nil, uuid.Nil, func(*Queries) error { return nil })
	if err == nil {
		t.Fatal("WithTenant returned nil for uuid.Nil")
	}

	err = WithTenantTx(context.Background(), nil, uuid.Nil, func(*Queries) error { return nil })
	if err == nil {
		t.Fatal("WithTenantTx returned nil for uuid.Nil")
	}
}

func newRLSTx(t *testing.T, conn *sql.DB) (*sql.Tx, *Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, New(tx)
}

func createRLSTenant(t *testing.T, queries *Queries) Tenant {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, CreateTenantParams{
		Email:          "tenant-" + uuid.NewString() + "@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	tenant, err = queries.UpdateTenantStatus(ctx, UpdateTenantStatusParams{
		ID:     tenant.ID,
		Status: "valid",
	})
	if err != nil {
		t.Fatalf("set tenant status: %v", err)
	}
	return tenant
}

func createRLSUser(t *testing.T, queries *Queries, tenantID uuid.UUID, email string) User {
	t.Helper()

	user, err := queries.CreateUser(context.Background(), CreateUserParams{
		Name:           email,
		Role:           "employee",
		HashedPassword: "hash",
		Email:          email,
		TenantID:       tenantID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
