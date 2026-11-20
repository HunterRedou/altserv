package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/tenantadmin"
	"github.com/google/uuid"
)

// TestSuspendedTenantLosesAccess covers WP-05: a suspended tenant cannot log
// in, and a session issued before the suspension stops working.
func TestSuspendedTenantLosesAccess(t *testing.T) {
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

	handler, queries, tx := newSessionTestAPI(t, conn)
	defer tx.Rollback()

	tenant := createTenant(t, queries, "valid")
	user := createUserWithPassword(t, queries, tenant.ID, "suspend-"+uuid.NewString()+"@a.de", "secret", "employee")

	login := loginAs(t, handler, user.Email, "secret")
	if rec := doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me before suspend = %d, want 200", rec.Code)
	}

	run := func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
		return db.WithTenantTx(ctx, tx, tenantID, fn)
	}
	if _, err := tenantadmin.Apply(context.Background(), run, audit.SQLRecorder{}, tenant.ID, tenantadmin.ActionSuspend, "req-suspend"); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}

	if rec := doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/me after suspend = %d, want 401", rec.Code)
	}

	rec := doRequest(handler, http.MethodPost, "/api/login", fmt.Sprintf(`{"email":%q,"password":"secret"}`, user.Email), nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login after suspend = %d, want 403", rec.Code)
	}

	// Activation restores access; the tenant and its users were never deleted.
	if _, err := tenantadmin.Apply(context.Background(), run, audit.SQLRecorder{}, tenant.ID, tenantadmin.ActionActivate, "req-activate"); err != nil {
		t.Fatalf("activate tenant: %v", err)
	}
	loginAs(t, handler, user.Email, "secret")
}
