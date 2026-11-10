package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var errTestVIES = errors.New("vies unavailable")

// TestVerifyTenantDatabase is an integration test: it needs a real Postgres with
// the goose migrations applied. It is skipped unless TEST_DB_URL is set, so
// `go test ./...` stays database-free.
//
// Setup:
//
//	psql "$DB_URL" -c 'CREATE DATABASE altserv_test TEMPLATE template0;'
//	goose -dir internal/db/sql/schema postgres "$TEST_DB_URL" up
//	TEST_DB_URL='postgres://user:pass@localhost:5432/altserv_test?sslmode=disable' \
//	  go test -run TestVerifyTenantDatabase -v .
//
// Every subtest runs inside a transaction that is rolled back, so the database
// is left untouched.
func TestVerifyTenantDatabase(t *testing.T) {
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

	t.Run("invalid verification deletes the tenant and its users", func(t *testing.T) {
		tx, queries := newTestTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedTenantAndUser(t, queries)
		assertRowCount(t, tx, "tenants", tenant.ID, 1)
		assertRowCount(t, tx, "users", user.ID, 1)

		err := verifyTenant(context.Background(), queries, tenant.ID, tenant.Ustid,
			func(context.Context, string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatalf("verifyTenant: %v", err)
		}

		assertRowCount(t, tx, "tenants", tenant.ID, 0)
		assertRowCount(t, tx, "users", user.ID, 0)
	})

	t.Run("valid verification keeps the tenant and its users", func(t *testing.T) {
		tx, queries := newTestTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedTenantAndUser(t, queries)

		err := verifyTenant(context.Background(), queries, tenant.ID, tenant.Ustid,
			func(context.Context, string) (bool, error) { return true, nil })
		if err != nil {
			t.Fatalf("verifyTenant: %v", err)
		}

		assertRowCount(t, tx, "tenants", tenant.ID, 1)
		assertRowCount(t, tx, "users", user.ID, 1)

		var status string
		if err := tx.QueryRow("SELECT status FROM tenants WHERE id = $1", tenant.ID).Scan(&status); err != nil {
			t.Fatalf("read tenant status: %v", err)
		}
		if status != "valid" {
			t.Fatalf("tenant status = %q, want %q", status, "valid")
		}
	})

	t.Run("verification error keeps the tenant pending", func(t *testing.T) {
		tx, queries := newTestTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedTenantAndUser(t, queries)

		err := verifyTenant(context.Background(), queries, tenant.ID, tenant.Ustid,
			func(context.Context, string) (bool, error) { return false, errTestVIES })
		if err == nil {
			t.Fatal("verifyTenant returned nil, want an error")
		}

		assertRowCount(t, tx, "tenants", tenant.ID, 1)
		assertRowCount(t, tx, "users", user.ID, 1)

		var status string
		if err := tx.QueryRow("SELECT status FROM tenants WHERE id = $1", tenant.ID).Scan(&status); err != nil {
			t.Fatalf("read tenant status: %v", err)
		}
		if status != "pending" {
			t.Fatalf("tenant status = %q, want %q", status, "pending")
		}
	})
}

func newTestTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedTenantAndUser(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()

	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "tenant@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "hash",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:           "Test User",
		Role:           "chef",
		HashedPassword: "hash",
		Email:          "user@example.com",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	return tenant, user
}

func assertRowCount(t *testing.T, tx *sql.Tx, table string, id uuid.UUID, want int) {
	t.Helper()

	var got int
	// table is a test-controlled literal, never user input.
	query := "SELECT count(*) FROM " + table + " WHERE id = $1"
	if err := tx.QueryRow(query, id).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows for id %s = %d, want %d", table, id, got, want)
	}
}
