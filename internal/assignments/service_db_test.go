package assignments

import (
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

// TestAssignmentServiceDatabase exercises assignments, object-level access and
// audit against PostgreSQL. It is skipped unless TEST_DB_URL is set and
// migration 014 is applied. Every subtest rolls back its transaction.
func TestAssignmentServiceDatabase(t *testing.T) {
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

	repository := NewRepository()
	service := NewService(repository, audit.SQLRecorder{})
	ctx := context.Background()

	t.Run("active assignment grants access and changes are audited", func(t *testing.T) {
		tx, queries := newAssignmentTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAssignmentTenant(t, queries)
		employee := seedAssignmentUser(t, queries, tenant.ID, "employee")
		patientID := seedAssignmentPatient(ctx, t, tx, tenant.ID)

		var assignment Assignment
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			assignment, err = service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req-create", patientID, CreateInput{
				EmployeeID:     employee.ID,
				AssignmentType: "regular",
				ValidFrom:      time.Now().Add(-time.Minute),
			})
			return err
		})
		if err != nil {
			t.Fatalf("create assignment: %v", err)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			allowed, err := repository.IsAssigned(ctx, q, tenant.ID, patientID, employee.ID)
			if err != nil {
				return err
			}
			if !allowed {
				t.Error("active assignment did not grant access")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("check assignment: %v", err)
		}

		var action, requestID string
		if err := tx.QueryRowContext(ctx,
			"SELECT action, request_id FROM audit_events WHERE entity_id = $1",
			assignment.ID,
		).Scan(&action, &requestID); err != nil {
			t.Fatalf("read audit event: %v", err)
		}
		if action != "assignment.created" {
			t.Errorf("audit action = %q, want assignment.created", action)
		}
		if requestID != "req-create" {
			t.Errorf("audit request id = %q, want req-create", requestID)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Revoke(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req-revoke", assignment.ID)
			return err
		})
		if err != nil {
			t.Fatalf("revoke assignment: %v", err)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			allowed, err := repository.IsAssigned(ctx, q, tenant.ID, patientID, employee.ID)
			if err != nil {
				return err
			}
			if allowed {
				t.Error("revoked assignment still grants access")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("check revoked assignment: %v", err)
		}

		if err := tx.QueryRowContext(ctx,
			"SELECT action FROM audit_events WHERE entity_id = $1 AND action = 'assignment.revoked'",
			assignment.ID,
		).Scan(&action); err != nil {
			t.Fatalf("read revoke audit event: %v", err)
		}
		if action != "assignment.revoked" {
			t.Errorf("audit action = %q, want assignment.revoked", action)
		}
	})

	t.Run("expired future and revoked assignments deny access", func(t *testing.T) {
		tx, queries := newAssignmentTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedAssignmentTenant(t, queries)
		employee := seedAssignmentUser(t, queries, tenant.ID, "employee")
		patientID := seedAssignmentPatient(ctx, t, tx, tenant.ID)
		now := time.Now()

		type window struct {
			name       string
			validFrom  time.Time
			validUntil *time.Time
			revokedAt  *time.Time
		}
		expiredUntil := now.Add(-time.Hour)
		revokedAt := now.Add(-time.Minute)
		windows := []window{
			{name: "active", validFrom: now.Add(-time.Hour)},
			{name: "expired", validFrom: now.Add(-2 * time.Hour), validUntil: &expiredUntil},
			{name: "future", validFrom: now.Add(time.Hour)},
			{name: "revoked", validFrom: now.Add(-time.Hour), revokedAt: &revokedAt},
		}

		for _, w := range windows {
			t.Run(w.name, func(t *testing.T) {
				err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
					// Start each window from a clean slate so the check below
					// only sees this window's assignment.
					if _, err := tx.ExecContext(ctx, "DELETE FROM patient_assignments WHERE tenant_id = $1", tenant.ID); err != nil {
						return err
					}
					assignment, err := repository.Create(ctx, q, CreateParams{
						TenantID:       tenant.ID,
						PatientID:      patientID,
						EmployeeID:     employee.ID,
						AssignedBy:     chef.ID,
						AssignmentType: "regular",
						ValidFrom:      w.validFrom,
						ValidUntil:     w.validUntil,
					})
					if err != nil {
						return err
					}
					if w.revokedAt != nil {
						if _, err := tx.ExecContext(ctx, "UPDATE patient_assignments SET revoked_at = $1 WHERE id = $2", *w.revokedAt, assignment.ID); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatalf("create %s assignment: %v", w.name, err)
				}

				err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
					allowed, err := repository.IsAssigned(ctx, q, tenant.ID, patientID, employee.ID)
					if err != nil {
						return err
					}
					want := w.name == "active"
					if allowed != want {
						t.Errorf("%s assignment allowed = %v, want %v", w.name, allowed, want)
					}
					return nil
				})
				if err != nil {
					t.Fatalf("check %s assignment: %v", w.name, err)
				}
			})
		}
	})

	t.Run("cross-tenant assignment is rejected", func(t *testing.T) {
		tx, queries := newAssignmentTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedAssignmentTenant(t, queries)
		tenantB, _ := seedAssignmentTenant(t, queries)
		employeeB := seedAssignmentUser(t, queries, tenantB.ID, "employee")
		patientA := seedAssignmentPatient(ctx, t, tx, tenantA.ID)

		err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			_, err := repository.Create(ctx, q, CreateParams{
				TenantID:       tenantA.ID,
				PatientID:      patientA,
				EmployeeID:     employeeB.ID,
				AssignedBy:     chefA.ID,
				AssignmentType: "regular",
				ValidFrom:      time.Now(),
			})
			return err
		})
		if err == nil {
			t.Fatal("cross-tenant assignment was accepted")
		}
	})

	t.Run("assignments are tenant isolated", func(t *testing.T) {
		tx, queries := newAssignmentTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedAssignmentTenant(t, queries)
		tenantB, chefB := seedAssignmentTenant(t, queries)
		employeeA := seedAssignmentUser(t, queries, tenantA.ID, "employee")
		employeeB := seedAssignmentUser(t, queries, tenantB.ID, "employee")
		patientA := seedAssignmentPatient(ctx, t, tx, tenantA.ID)
		patientB := seedAssignmentPatient(ctx, t, tx, tenantB.ID)

		seedAssignment(ctx, t, tx, tenantA.ID, patientA, employeeA.ID, chefA.ID)
		seedAssignment(ctx, t, tx, tenantB.ID, patientB, employeeB.ID, chefB.ID)

		err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			list, err := repository.ListByTenant(ctx, q, tenantA.ID)
			if err != nil {
				return err
			}
			if len(list) != 1 || list[0].PatientID != patientA {
				t.Errorf("tenant A assignments = %+v, want only %s", list, patientA)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("list tenant assignments: %v", err)
		}
	})
}

func newAssignmentTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedAssignmentTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
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

	return tenant, seedAssignmentUser(t, queries, tenant.ID, "chef")
}

func seedAssignmentUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func seedAssignmentPatient(ctx context.Context, t *testing.T, tx *sql.Tx, tenantID uuid.UUID) uuid.UUID {
	t.Helper()

	patientID := uuid.New()
	err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		_, err := q.CreatePatient(ctx, db.CreatePatientParams{
			ID:           patientID,
			TenantID:     tenantID,
			FirstNameEnc: []byte{1, 2, 3},
			LastNameEnc:  []byte{4, 5, 6},
			BirthDateEnc: []byte{7, 8, 9},
			Status:       "active",
			KeyVersion:   1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("create patient: %v", err)
	}
	return patientID
}

func seedAssignment(ctx context.Context, t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID, assignedBy uuid.UUID) {
	t.Helper()

	err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		_, err := q.CreateAssignment(ctx, db.CreateAssignmentParams{
			TenantID:       tenantID,
			PatientID:      patientID,
			EmployeeID:     employeeID,
			AssignedBy:     assignedBy,
			AssignmentType: "regular",
			ValidFrom:      time.Now().Add(-time.Hour),
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}
