package visits

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestVisitServiceDatabase covers the visit lifecycle, tenant isolation,
// substitutions, assignment-scoped employee access and audit events. It is
// skipped unless TEST_DB_URL is set and migration 018 is applied. Every subtest
// rolls back its transaction.
func TestVisitServiceDatabase(t *testing.T) {
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

	newService := func() *Service {
		assignmentService := assignments.NewService(assignments.NewRepository(), audit.SQLRecorder{})
		return NewService(NewRepository(), audit.SQLRecorder{}, assignmentService)
	}
	ctx := context.Background()

	t.Run("create list reschedule start complete correct cancel", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedVisitTenant(t, queries)
		employee := seedVisitUser(t, queries, tenant.ID, "employee")
		patientID := seedVisitPatient(t, tx, tenant.ID)
		seedVisitAssignment(t, tx, tenant.ID, patientID, employee.ID, chef.ID)
		service := newService()

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		employeeActor := Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}
		planned := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

		var created Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, chefActor, "req-create", CreateInput{
				PatientID:    patientID,
				EmployeeID:   employee.ID,
				PlannedStart: planned,
			})
			return err
		}); err != nil {
			t.Fatalf("create visit: %v", err)
		}
		if created.Status != "planned" || created.EmployeeID != employee.ID || created.ActualEmployeeID != nil {
			t.Fatalf("created = %+v, want planned for %s", created, employee.ID)
		}

		var listed []Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			listed, err = service.ListByPatient(ctx, q, chefActor, "", patientID)
			return err
		}); err != nil {
			t.Fatalf("list visits: %v", err)
		}
		if len(listed) != 1 || listed[0].ID != created.ID {
			t.Fatalf("listed = %+v, want the created visit", listed)
		}

		// The assigned employee can read the visit.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			got, err := service.Get(ctx, q, employeeActor, "", created.ID)
			if err != nil {
				return err
			}
			if got.ID != created.ID {
				t.Errorf("got visit = %s, want %s", got.ID, created.ID)
			}
			return nil
		}); err != nil {
			t.Fatalf("employee get: %v", err)
		}

		// Reschedule moves the planned start.
		newStart := planned.Add(30 * time.Minute)
		var rescheduled Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			rescheduled, err = service.Reschedule(ctx, q, chefActor, "req-reschedule", created.ID, ScheduleInput{PlannedStart: &newStart})
			return err
		}); err != nil {
			t.Fatalf("reschedule: %v", err)
		}
		if !rescheduled.PlannedStart.Equal(newStart) {
			t.Errorf("planned start = %v, want %v", rescheduled.PlannedStart, newStart)
		}

		// The employee starts the visit; the actual employee and start are recorded.
		var started Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			started, err = service.Start(ctx, q, employeeActor, "req-start", created.ID)
			return err
		}); err != nil {
			t.Fatalf("start: %v", err)
		}
		if started.Status != "in_progress" || started.ActualEmployeeID == nil || *started.ActualEmployeeID != employee.ID {
			t.Fatalf("started = %+v, want in_progress with actual employee %s", started, employee.ID)
		}
		if started.ActualStart == nil {
			t.Fatal("started visit has no actual start")
		}

		// Completing records the end time.
		var completed Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			completed, err = service.Complete(ctx, q, employeeActor, "req-complete", created.ID)
			return err
		}); err != nil {
			t.Fatalf("complete: %v", err)
		}
		if completed.Status != "completed" || completed.ActualEnd == nil {
			t.Fatalf("completed = %+v, want completed with actual end", completed)
		}
		if completed.ActualEnd.Before(*completed.ActualStart) {
			t.Errorf("actual end %v is before actual start %v", completed.ActualEnd, completed.ActualStart)
		}

		// The chef corrects the start time with a reason. Moving it earlier keeps
		// the existing end time valid.
		correctedStart := completed.ActualStart.Add(-10 * time.Minute)
		var corrected Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			corrected, err = service.Correct(ctx, q, chefActor, "req-correct", created.ID, CorrectInput{
				ActualStart: &correctedStart,
				Reason:      "clock was wrong",
			})
			return err
		}); err != nil {
			t.Fatalf("correct: %v", err)
		}
		if corrected.ActualStart == nil || !corrected.ActualStart.Equal(correctedStart) {
			t.Errorf("corrected start = %v, want %v", corrected.ActualStart, correctedStart)
		}

		// A second planned visit can be cancelled by the chef.
		var second Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			second, err = service.Create(ctx, q, chefActor, "", CreateInput{
				PatientID:    patientID,
				EmployeeID:   employee.ID,
				PlannedStart: time.Now().Add(24 * time.Hour),
			})
			return err
		}); err != nil {
			t.Fatalf("create second visit: %v", err)
		}
		var cancelled Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			cancelled, err = service.Cancel(ctx, q, chefActor, "req-cancel", second.ID)
			return err
		}); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if cancelled.Status != "cancelled" {
			t.Errorf("cancelled status = %q, want cancelled", cancelled.Status)
		}

		// Every lifecycle step wrote an audit event.
		for _, action := range []string{
			"visit.created", "visit.rescheduled", "visit.started",
			"visit.completed", "visit.corrected", "visit.cancelled",
		} {
			var count int
			if err := tx.QueryRowContext(ctx,
				"SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = $2",
				tenant.ID, action,
			).Scan(&count); err != nil {
				t.Fatalf("count audit %s: %v", action, err)
			}
			if count == 0 {
				t.Errorf("no audit event for %s", action)
			}
		}

		// The correction audit records the reason.
		var reason string
		if err := tx.QueryRowContext(ctx,
			"SELECT metadata->>'reason' FROM audit_events WHERE tenant_id = $1 AND action = 'visit.corrected'",
			tenant.ID,
		).Scan(&reason); err != nil {
			t.Fatalf("read correction reason: %v", err)
		}
		if reason != "clock was wrong" {
			t.Errorf("audit reason = %q, want %q", reason, "clock was wrong")
		}
	})

	t.Run("visits are tenant isolated", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedVisitTenant(t, queries)
		tenantB, chefB := seedVisitTenant(t, queries)
		employeeA := seedVisitUser(t, queries, tenantA.ID, "employee")
		patientA := seedVisitPatient(t, tx, tenantA.ID)
		seedVisitAssignment(t, tx, tenantA.ID, patientA, employeeA.ID, chefA.ID)
		service := newService()

		var created Visit
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "", CreateInput{
				PatientID:    patientA,
				EmployeeID:   employeeA.ID,
				PlannedStart: time.Now(),
			})
			return err
		}); err != nil {
			t.Fatalf("create tenant A visit: %v", err)
		}

		// Tenant B cannot read it by id.
		err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant get err = %v, want ErrNotFound", err)
		}

		// Tenant B cannot list tenant A's patient visits.
		if err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			list, err := service.ListByPatient(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", patientA)
			if err != nil {
				return err
			}
			if len(list) != 0 {
				t.Errorf("tenant B saw %d tenant A visits", len(list))
			}
			return nil
		}); err != nil {
			t.Fatalf("cross-tenant list: %v", err)
		}
	})

	t.Run("an assigned substitute is recorded", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedVisitTenant(t, queries)
		plannedEmployee := seedVisitUser(t, queries, tenant.ID, "employee")
		substitute := seedVisitUser(t, queries, tenant.ID, "employee")
		patientID := seedVisitPatient(t, tx, tenant.ID)
		seedVisitAssignment(t, tx, tenant.ID, patientID, plannedEmployee.ID, chef.ID)
		seedVisitAssignment(t, tx, tenant.ID, patientID, substitute.ID, chef.ID)
		service := newService()

		var created Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "", CreateInput{
				PatientID:    patientID,
				EmployeeID:   plannedEmployee.ID,
				PlannedStart: time.Now(),
			})
			return err
		}); err != nil {
			t.Fatalf("create: %v", err)
		}

		var started Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			started, err = service.Start(ctx, q, Actor{UserID: substitute.ID, TenantID: tenant.ID, Role: "employee"}, "req", created.ID)
			return err
		}); err != nil {
			t.Fatalf("substitute start: %v", err)
		}
		if started.EmployeeID != plannedEmployee.ID {
			t.Errorf("planned employee = %s, want %s", started.EmployeeID, plannedEmployee.ID)
		}
		if started.ActualEmployeeID == nil || *started.ActualEmployeeID != substitute.ID {
			t.Errorf("actual employee = %v, want the substitute %s", started.ActualEmployeeID, substitute.ID)
		}

		var substitution bool
		if err := tx.QueryRowContext(ctx,
			"SELECT (metadata->>'substitution')::boolean FROM audit_events WHERE tenant_id = $1 AND action = 'visit.started'",
			tenant.ID,
		).Scan(&substitution); err != nil {
			t.Fatalf("read substitution flag: %v", err)
		}
		if !substitution {
			t.Error("audit did not flag the substitution")
		}
	})

	t.Run("employee access follows the assignment", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedVisitTenant(t, queries)
		assigned := seedVisitUser(t, queries, tenant.ID, "employee")
		unassigned := seedVisitUser(t, queries, tenant.ID, "employee")
		patientID := seedVisitPatient(t, tx, tenant.ID)
		seedVisitAssignment(t, tx, tenant.ID, patientID, assigned.ID, chef.ID)
		service := newService()

		var created Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "", CreateInput{
				PatientID:    patientID,
				EmployeeID:   assigned.ID,
				PlannedStart: time.Now(),
			})
			return err
		}); err != nil {
			t.Fatalf("create: %v", err)
		}

		unassignedActor := Actor{UserID: unassigned.ID, TenantID: tenant.ID, Role: "employee"}

		// Reads hide the visit.
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, unassignedActor, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("unassigned get err = %v, want ErrNotFound", err)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.ListByPatient(ctx, q, unassignedActor, "", patientID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("unassigned list err = %v, want ErrNotFound", err)
		}

		// Starting is a write and is forbidden.
		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Start(ctx, q, unassignedActor, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("unassigned start err = %v, want ErrForbidden", err)
		}

		// Revoking the assignment removes access immediately.
		if _, err := tx.ExecContext(ctx, "UPDATE patient_assignments SET revoked_at = clock_timestamp() WHERE patient_id = $1", patientID); err != nil {
			t.Fatalf("revoke assignment: %v", err)
		}
		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, Actor{UserID: assigned.ID, TenantID: tenant.ID, Role: "employee"}, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("revoked get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("visit times round-trip as instants under a non-UTC session", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		// Reproduce a database whose session timezone is not UTC. TIMESTAMP
		// columns would silently shift the instant by the offset here.
		if _, err := tx.ExecContext(ctx, "SET LOCAL TIME ZONE 'America/New_York'"); err != nil {
			t.Fatalf("set session timezone: %v", err)
		}

		tenant, chef := seedVisitTenant(t, queries)
		employee := seedVisitUser(t, queries, tenant.ID, "employee")
		patientID := seedVisitPatient(t, tx, tenant.ID)
		seedVisitAssignment(t, tx, tenant.ID, patientID, employee.ID, chef.ID)
		service := newService()
		actor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}

		planned := time.Date(2026, 6, 1, 8, 30, 0, 0, time.UTC)
		var created Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, actor, "", CreateInput{
				PatientID:    patientID,
				EmployeeID:   employee.ID,
				PlannedStart: planned,
			})
			return err
		}); err != nil {
			t.Fatalf("create visit: %v", err)
		}
		if !created.PlannedStart.Equal(planned) {
			t.Fatalf("planned start = %v, want the same instant as %v", created.PlannedStart, planned)
		}

		// The actual start comes from the database clock. It must be close to
		// the real now, not shifted by a timezone offset.
		var started Visit
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			started, err = service.Start(ctx, q, actor, "", created.ID)
			return err
		}); err != nil {
			t.Fatalf("start visit: %v", err)
		}
		if started.ActualStart == nil {
			t.Fatal("started visit has no actual start")
		}
		if skew := started.ActualStart.Sub(time.Now()); skew > time.Minute || skew < -time.Minute {
			t.Fatalf("actual start %v is %v away from now; timezone was not preserved", started.ActualStart, skew)
		}
	})

	t.Run("a visit for a missing patient is rejected", func(t *testing.T) {
		tx, queries := newVisitTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedVisitTenant(t, queries)
		employee := seedVisitUser(t, queries, tenant.ID, "employee")
		service := newService()

		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "", CreateInput{
				PatientID:    uuid.New(),
				EmployeeID:   employee.ID,
				PlannedStart: time.Now(),
			})
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func newVisitTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedVisitTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "visit-" + uuid.NewString() + "@example.com",
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
	return tenant, seedVisitUser(t, queries, tenant.ID, "chef")
}

func seedVisitUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func seedVisitPatient(t *testing.T, tx *sql.Tx, tenantID uuid.UUID) uuid.UUID {
	t.Helper()

	patientID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO patients (id, tenant_id, first_name_enc, last_name_enc, birth_date_enc, key_version)
		 VALUES ($1, $2, $3, $4, $5, 1)`,
		patientID, tenantID, []byte("first"), []byte("last"), []byte("birth"),
	); err != nil {
		t.Fatalf("seed patient: %v", err)
	}
	return patientID
}

func seedVisitAssignment(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID, assignedBy uuid.UUID) {
	t.Helper()

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO patient_assignments (tenant_id, patient_id, employee_id, assigned_by, assignment_type, valid_from)
		 VALUES ($1, $2, $3, $4, 'regular', clock_timestamp() - interval '1 hour')`,
		tenantID, patientID, employeeID, assignedBy,
	); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}
