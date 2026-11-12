// Package testutil holds shared helpers for the cross-cutting suites under
// tests/. It is test support, not production code.
//
// The suites use a real PostgreSQL database. Set TEST_DB_URL to the same
// database the package tests use; every test runs in one transaction and rolls
// it back, so the suites do not leave data behind.
package testutil

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/devseed"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Services bundles the domain services wired to the real repositories and the
// SQL audit recorder. The suites use it to exercise cross-package rules.
type Services struct {
	Patients    *patients.Service
	Assignments *assignments.Service
	Catalog     *catalog.Service
	Templates   *templates.Service
	Visits      *visits.Service
}

// NewServices wires the real services with the patient keys from opts. It is
// the same wiring the application and the dev seed use.
func NewServices(t *testing.T, opts devseed.Options) Services {
	t.Helper()

	cipher, err := patients.NewCipher(opts.PatientEncryptionKey, opts.PatientKeyVersion)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	search, err := patients.NewSearchIndexer(opts.PatientSearchKey, opts.PatientSearchKeyVersion)
	if err != nil {
		t.Fatalf("NewSearchIndexer: %v", err)
	}

	recorder := audit.SQLRecorder{}
	assignmentService := assignments.NewService(assignments.NewRepository(), recorder)
	return Services{
		Patients:    patients.NewServiceWithSearch(patients.NewRepository(cipher), recorder, assignmentService, search),
		Assignments: assignmentService,
		Catalog:     catalog.NewService(catalog.NewRepository(), recorder),
		Templates:   templates.NewService(templates.NewRepository(), catalog.NewRepository(), recorder, assignmentService),
		Visits:      visits.NewService(visits.NewRepository(), recorder, assignmentService),
	}
}

// OpenDB opens the test database and skips the test when TEST_DB_URL is unset
// or the database is unreachable.
func OpenDB(t *testing.T) *sql.DB {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping database integration test")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		t.Skipf("database not reachable: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// BeginTx starts a transaction and rolls it back when the test ends.
func BeginTx(t *testing.T, conn *sql.DB) *sql.Tx {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// SeedDemo creates the synthetic demo dataset from internal/devseed inside tx.
func SeedDemo(t *testing.T, tx *sql.Tx) (devseed.Options, devseed.Result) {
	t.Helper()

	opts := devseed.Options{
		Platform:                "dev",
		TenantEmail:             "demo-" + uuid.NewString() + "@example.invalid",
		TenantName:              "Demo Pflegedienst",
		Password:                "demo-password-123",
		PatientEncryptionKey:    bytes.Repeat([]byte{1}, 32),
		PatientKeyVersion:       1,
		PatientSearchKey:        bytes.Repeat([]byte{2}, 32),
		PatientSearchKeyVersion: 1,
		Now:                     time.Now().UTC().Truncate(time.Second),
	}

	result, err := devseed.RunTx(context.Background(), tx, opts)
	if err != nil {
		t.Fatalf("seed demo data: %v", err)
	}
	return opts, result
}

// CreateTenantWithUser creates one verified tenant and one user with role.
func CreateTenantWithUser(t *testing.T, tx *sql.Tx, role string) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	queries := db.New(tx)
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "suite-" + uuid.NewString() + "@example.invalid",
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
		t.Fatalf("activate tenant: %v", err)
	}
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:           role,
		Role:           role,
		HashedPassword: "hash",
		Email:          role + "-" + uuid.NewString() + "@example.invalid",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return tenant, user
}
