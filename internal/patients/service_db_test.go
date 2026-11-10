package patients

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPatientServiceDatabase runs the full encryption, RLS and audit path
// against PostgreSQL. It is skipped unless TEST_DB_URL is set and migrations
// through 013 are applied.
func TestPatientServiceDatabase(t *testing.T) {
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
	repository := NewRepository(cipher)
	service := NewService(repository, audit.SQLRecorder{})
	ctx := context.Background()

	t.Run("create stores ciphertext and an audit event", func(t *testing.T) {
		tx, queries := newPatientTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedPatientTenantAndUser(t, queries)
		patient, err := createPatientInTenant(ctx, t, tx, service, tenant.ID, user.ID, "Anna", "Muster")
		if err != nil {
			t.Fatalf("create patient: %v", err)
		}
		if patient.FirstName != "Anna" || patient.LastName != "Muster" {
			t.Errorf("decrypted patient = %+v", patient)
		}

		var firstNameEnc []byte
		if err := tx.QueryRowContext(ctx, "SELECT first_name_enc FROM patients WHERE id = $1", patient.ID).Scan(&firstNameEnc); err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}
		if bytes.Contains(firstNameEnc, []byte("Anna")) {
			t.Fatal("first name is stored in plaintext")
		}

		var action, requestID string
		var metadata []byte
		if err := tx.QueryRowContext(ctx,
			"SELECT action, request_id, metadata FROM audit_events WHERE entity_id = $1",
			patient.ID,
		).Scan(&action, &requestID, &metadata); err != nil {
			t.Fatalf("read audit event: %v", err)
		}
		if action != "patient.created" {
			t.Errorf("audit action = %q, want patient.created", action)
		}
		if requestID != "req-create" {
			t.Errorf("audit request id = %q, want req-create", requestID)
		}
		if bytes.Contains(metadata, []byte("Anna")) || bytes.Contains(metadata, []byte("Muster")) {
			t.Fatal("audit metadata contains patient names")
		}
	})

	t.Run("tenant A cannot read tenant B patients", func(t *testing.T) {
		tx, queries := newPatientTx(t, conn)
		defer tx.Rollback()

		tenantA, userA := seedPatientTenantAndUser(t, queries)
		tenantB, userB := seedPatientTenantAndUser(t, queries)

		patientA, err := createPatientInTenant(ctx, t, tx, service, tenantA.ID, userA.ID, "Anna", "A")
		if err != nil {
			t.Fatalf("create patient A: %v", err)
		}
		patientB, err := createPatientInTenant(ctx, t, tx, service, tenantB.ID, userB.ID, "Berta", "B")
		if err != nil {
			t.Fatalf("create patient B: %v", err)
		}

		err = db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			if _, err := repository.Get(ctx, q, tenantA.ID, patientB.ID); !errors.Is(err, ErrNotFound) {
				t.Errorf("tenant A read tenant B patient: %v", err)
			}

			// Even an explicit tenant B argument must be filtered by RLS.
			if _, err := repository.Get(ctx, q, tenantB.ID, patientB.ID); !errors.Is(err, ErrNotFound) {
				t.Errorf("explicit tenant B read from tenant A context: %v", err)
			}

			patients, err := repository.List(ctx, q, tenantA.ID)
			if err != nil {
				return err
			}
			if len(patients) != 1 || patients[0].ID != patientA.ID {
				t.Errorf("tenant A list = %+v, want only %s", patients, patientA.ID)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("tenant-scoped reads: %v", err)
		}
	})

	t.Run("tampered ciphertext is rejected", func(t *testing.T) {
		tx, queries := newPatientTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedPatientTenantAndUser(t, queries)
		patient, err := createPatientInTenant(ctx, t, tx, service, tenant.ID, user.ID, "Anna", "Muster")
		if err != nil {
			t.Fatalf("create patient: %v", err)
		}

		var encrypted []byte
		if err := tx.QueryRowContext(ctx, "SELECT first_name_enc FROM patients WHERE id = $1", patient.ID).Scan(&encrypted); err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}
		encrypted[len(encrypted)-1] ^= 0xff
		if _, err := tx.ExecContext(ctx, "UPDATE patients SET first_name_enc = $1 WHERE id = $2", encrypted, patient.ID); err != nil {
			t.Fatalf("tamper ciphertext: %v", err)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			if _, err := repository.Get(ctx, q, tenant.ID, patient.ID); err == nil {
				t.Error("Get accepted tampered ciphertext")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("tenant-scoped read: %v", err)
		}
	})

	t.Run("audit events are append-only", func(t *testing.T) {
		tx, queries := newPatientTx(t, conn)
		defer tx.Rollback()

		tenant, user := seedPatientTenantAndUser(t, queries)
		if _, err := createPatientInTenant(ctx, t, tx, service, tenant.ID, user.ID, "Anna", "Muster"); err != nil {
			t.Fatalf("create patient: %v", err)
		}

		var eventID uuid.UUID
		if err := tx.QueryRowContext(ctx, "SELECT id FROM audit_events WHERE tenant_id = $1 LIMIT 1", tenant.ID).Scan(&eventID); err != nil {
			t.Fatalf("read audit event id: %v", err)
		}

		if _, err := tx.ExecContext(ctx, "UPDATE audit_events SET action = 'tampered' WHERE id = $1", eventID); err == nil {
			t.Fatal("audit event update was allowed")
		}
	})
}

func createPatientInTenant(ctx context.Context, t *testing.T, tx *sql.Tx, service *Service, tenantID, userID uuid.UUID, firstName, lastName string) (Patient, error) {
	t.Helper()

	var patient Patient
	err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		var err error
		patient, err = service.Create(ctx, q, Actor{
			UserID:   userID,
			TenantID: tenantID,
			Role:     "chef",
		}, "req-create", CreateInput{
			FirstName: firstName,
			LastName:  lastName,
			BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
		})
		return err
	})
	return patient, err
}

func newPatientTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedPatientTenantAndUser(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
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

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:           "Chef",
		Role:           "chef",
		HashedPassword: "hash",
		Email:          "chef-" + uuid.NewString() + "@example.com",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return tenant, user
}
