package patients

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPatientSearchDatabase covers the protected blind index and the patient
// lifecycle against PostgreSQL. It is skipped unless TEST_DB_URL is set and
// migration 015 is applied. Every subtest rolls back its transaction.
func TestPatientSearchDatabase(t *testing.T) {
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

	cipher, err := NewCipher(bytes.Repeat([]byte{9}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	indexer, err := NewSearchIndexer(bytes.Repeat([]byte{3}, 32), 1)
	if err != nil {
		t.Fatalf("NewSearchIndexer: %v", err)
	}
	repository := NewRepository(cipher)
	service := NewServiceWithSearch(repository, audit.SQLRecorder{}, nil, indexer)
	ctx := context.Background()

	t.Run("the same name in two tenants does not collide", func(t *testing.T) {
		tx, queries := newSearchTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedSearchTenantAndUser(t, queries, "chef")
		tenantB, chefB := seedSearchTenantAndUser(t, queries, "chef")

		patientA := createSearchPatient(t, ctx, tx, service, tenantA.ID, chefA.ID, "Anna", "Muster")
		createSearchPatient(t, ctx, tx, service, tenantB.ID, chefB.ID, "Anna", "Muster")

		found := searchPatients(t, ctx, tx, service, tenantA.ID, chefA.ID, "chef", "Anna")
		if len(found) != 1 || found[0].ID != patientA.ID {
			t.Fatalf("tenant A search = %+v, want only %s", found, patientA.ID)
		}
	})

	t.Run("rename replaces tokens and the old name disappears", func(t *testing.T) {
		tx, queries := newSearchTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedSearchTenantAndUser(t, queries, "chef")
		patient := createSearchPatient(t, ctx, tx, service, tenant.ID, chef.ID, "Anna", "Muster")

		var before []byte
		if err := tx.QueryRowContext(ctx, "SELECT first_name_enc FROM patients WHERE id = $1", patient.ID).Scan(&before); err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}

		if found := searchPatients(t, ctx, tx, service, tenant.ID, chef.ID, "chef", "Muster"); len(found) != 1 {
			t.Fatalf("search before rename = %d, want 1", len(found))
		}

		newName := "Schmidt"
		var updated Patient
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			updated, err = service.Update(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req-update", patient.ID, UpdateInput{LastName: &newName})
			return err
		}); err != nil {
			t.Fatalf("update patient: %v", err)
		}
		if updated.LastName != "Schmidt" {
			t.Fatalf("last name = %q, want Schmidt", updated.LastName)
		}

		// Even the unchanged first name is re-encrypted with a fresh nonce.
		var after []byte
		if err := tx.QueryRowContext(ctx, "SELECT first_name_enc FROM patients WHERE id = $1", patient.ID).Scan(&after); err != nil {
			t.Fatalf("read updated ciphertext: %v", err)
		}
		if bytes.Equal(before, after) {
			t.Fatal("update reused the old nonce/ciphertext for an unchanged field")
		}

		if found := searchPatients(t, ctx, tx, service, tenant.ID, chef.ID, "chef", "Muster"); len(found) != 0 {
			t.Fatalf("old name still matched %d patients", len(found))
		}
		if found := searchPatients(t, ctx, tx, service, tenant.ID, chef.ID, "chef", "Schmidt"); len(found) != 1 || found[0].ID != patient.ID {
			t.Fatalf("new name search = %+v, want %s", found, patient.ID)
		}
	})

	t.Run("employee search is assignment scoped and archived patients are hidden", func(t *testing.T) {
		tx, queries := newSearchTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedSearchTenantAndUser(t, queries, "chef")
		employee := seedSearchUser(t, queries, tenant.ID, "employee")

		assigned := createSearchPatient(t, ctx, tx, service, tenant.ID, chef.ID, "Anna", "Assigned")
		unassigned := createSearchPatient(t, ctx, tx, service, tenant.ID, chef.ID, "Berta", "Unassigned")
		archived := createSearchPatient(t, ctx, tx, service, tenant.ID, chef.ID, "Carla", "Archived")

		archivePatient(t, ctx, tx, service, tenant.ID, chef.ID, archived.ID)
		seedSearchAssignment(t, ctx, tx, tenant.ID, assigned.ID, employee.ID, chef.ID)
		seedSearchAssignment(t, ctx, tx, tenant.ID, archived.ID, employee.ID, chef.ID)

		// The employee only sees the active, assigned patient.
		if found := searchPatients(t, ctx, tx, service, tenant.ID, employee.ID, "employee", "Anna"); len(found) != 1 || found[0].ID != assigned.ID {
			t.Fatalf("employee search = %+v, want only %s", found, assigned.ID)
		}
		if found := searchPatients(t, ctx, tx, service, tenant.ID, employee.ID, "employee", "Berta"); len(found) != 0 {
			t.Fatalf("employee found the unassigned patient: %+v", found)
		}
		if found := searchPatients(t, ctx, tx, service, tenant.ID, employee.ID, "employee", "Carla"); len(found) != 0 {
			t.Fatalf("employee found the archived patient: %+v", found)
		}

		// A chef still sees the archived record.
		if found := searchPatients(t, ctx, tx, service, tenant.ID, chef.ID, "chef", "Carla"); len(found) != 1 || found[0].ID != archived.ID {
			t.Fatalf("chef search for archived patient = %+v, want %s", found, archived.ID)
		}
		if found := searchPatients(t, ctx, tx, service, tenant.ID, chef.ID, "chef", "Berta"); len(found) != 1 || found[0].ID != unassigned.ID {
			t.Fatalf("chef search = %+v, want %s", found, unassigned.ID)
		}
	})
}

func searchPatients(t *testing.T, ctx context.Context, tx *sql.Tx, service *Service, tenantID, userID uuid.UUID, role, query string) []Patient {
	t.Helper()

	var found []Patient
	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		var err error
		found, err = service.Search(ctx, q, Actor{UserID: userID, TenantID: tenantID, Role: role}, "req-search", query)
		return err
	}); err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return found
}

func archivePatient(t *testing.T, ctx context.Context, tx *sql.Tx, service *Service, tenantID, userID, patientID uuid.UUID) {
	t.Helper()

	status := "archived"
	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		_, err := service.Update(ctx, q, Actor{UserID: userID, TenantID: tenantID, Role: "chef"}, "req-archive", patientID, UpdateInput{Status: &status})
		return err
	}); err != nil {
		t.Fatalf("archive patient: %v", err)
	}
}

func createSearchPatient(t *testing.T, ctx context.Context, tx *sql.Tx, service *Service, tenantID, userID uuid.UUID, firstName, lastName string) Patient {
	t.Helper()

	var patient Patient
	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		var err error
		patient, err = service.Create(ctx, q, Actor{UserID: userID, TenantID: tenantID, Role: "chef"}, "req-create", CreateInput{
			FirstName: firstName,
			LastName:  lastName,
			BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
		})
		return err
	}); err != nil {
		t.Fatalf("create patient: %v", err)
	}
	return patient
}

func seedSearchAssignment(t *testing.T, ctx context.Context, tx *sql.Tx, tenantID, patientID, employeeID, assignedBy uuid.UUID) {
	t.Helper()

	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		_, err := q.CreateAssignment(ctx, db.CreateAssignmentParams{
			TenantID:       tenantID,
			PatientID:      patientID,
			EmployeeID:     employeeID,
			AssignedBy:     assignedBy,
			AssignmentType: "regular",
			ValidFrom:      time.Now().Add(-time.Hour),
		})
		return err
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}

func newSearchTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedSearchTenantAndUser(t *testing.T, queries *db.Queries, role string) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "tenant-" + uuid.NewString() + "@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenant, err = queries.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{
		ID:     tenant.ID,
		Status: "valid",
	})
	if err != nil {
		t.Fatalf("set tenant status: %v", err)
	}
	return tenant, seedSearchUser(t, queries, tenant.ID, role)
}

func seedSearchUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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
