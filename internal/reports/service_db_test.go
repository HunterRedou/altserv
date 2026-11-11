package reports

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestReportServiceDatabase covers the report lifecycle, the approval flow, the
// return transition, tenant isolation, access rules, validation and audit
// events. It is skipped unless TEST_DB_URL is set and migration 019 is applied.
func TestReportServiceDatabase(t *testing.T) {
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

	cipher, err := patients.NewCipher(bytes.Repeat([]byte{7}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	newService := func() *Service {
		assignmentService := assignments.NewService(assignments.NewRepository(), audit.SQLRecorder{})
		return NewService(NewRepository(cipher), audit.SQLRecorder{}, assignmentService)
	}
	ctx := context.Background()

	t.Run("draft submit approve return and ready for billing", func(t *testing.T) {
		tx, queries := newReportTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedReportTenant(t, queries)
		employee := seedReportUser(t, queries, tenant.ID, "employee")
		outsider := seedReportUser(t, queries, tenant.ID, "employee")
		admin := seedReportUser(t, queries, tenant.ID, "platform_admin")
		patientID := seedReportPatient(t, tx, tenant.ID)
		seedReportAssignment(t, tx, tenant.ID, patientID, employee.ID, chef.ID)
		visitID := seedReportVisit(t, tx, tenant.ID, patientID, employee.ID, "completed")
		activityID := seedReportActivity(t, tx, tenant.ID, patientID, employee.ID)
		service := newService()

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		employeeActor := Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}
		outsiderActor := Actor{UserID: outsider.ID, TenantID: tenant.ID, Role: "employee"}
		adminActor := Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}

		var created Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, chefActor, "req-create", visitID, CreateInput{
				Note:       "Pflegebericht",
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 2}},
			})
			return err
		}); err != nil {
			t.Fatalf("create report: %v", err)
		}
		if created.Status != "draft" || created.Version != 1 || created.Note != "Pflegebericht" {
			t.Fatalf("created = %+v", created)
		}
		if len(created.Activities) != 1 || created.Activities[0].ActivityID != activityID || created.Activities[0].Quantity != 2 {
			t.Fatalf("activities = %+v", created.Activities)
		}

		var noteEnc []byte
		if err := tx.QueryRow("SELECT note_enc FROM reports WHERE id = $1", created.ID).Scan(&noteEnc); err != nil {
			t.Fatalf("read note_enc: %v", err)
		}
		if bytes.Contains(noteEnc, []byte("Pflegebericht")) {
			t.Fatal("report note is stored in plaintext")
		}

		var loaded Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			loaded, err = service.Get(ctx, q, employeeActor, "", created.ID)
			return err
		}); err != nil {
			t.Fatalf("employee get report: %v", err)
		}
		if loaded.Note != "Pflegebericht" || len(loaded.Activities) != 1 {
			t.Fatalf("loaded = %+v", loaded)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, outsiderActor, "", created.ID)
			return err
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("outsider get err = %v, want ErrNotFound", err)
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, adminActor, "", created.ID)
			return err
		}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("admin get err = %v, want ErrForbidden", err)
		}

		// A draft can be edited with the current version.
		note := "Pflegebericht korrigiert"
		var updated Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			updated, err = service.Update(ctx, q, employeeActor, "req-update", created.ID, UpdateInput{Version: 1, Note: &note})
			return err
		}); err != nil {
			t.Fatalf("update draft: %v", err)
		}
		if updated.Version != 2 || updated.Note != note {
			t.Fatalf("updated = %+v", updated)
		}

		// A stale version is rejected.
		stale := "stale"
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Update(ctx, q, employeeActor, "", created.ID, UpdateInput{Version: 1, Note: &stale})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale update err = %v, want ErrConflict", err)
		}

		var submitted Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			submitted, err = service.Submit(ctx, q, employeeActor, "req-submit", created.ID, 2)
			return err
		}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if submitted.Status != "submitted" || submitted.Version != 3 || submitted.SubmittedAt == nil {
			t.Fatalf("submitted = %+v", submitted)
		}

		// After submission there is no silent change.
		after := "after submit"
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Update(ctx, q, employeeActor, "", created.ID, UpdateInput{Version: 3, Note: &after})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("update after submit err = %v, want ErrConflict", err)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Approve(ctx, q, employeeActor, "", created.ID, 3)
			return err
		}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("employee approve err = %v, want ErrForbidden", err)
		}

		var approved Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			approved, err = service.Approve(ctx, q, chefActor, "req-approve", created.ID, 3)
			return err
		}); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if approved.Status != "approved" || approved.Version != 4 || approved.ApprovedBy == nil || *approved.ApprovedBy != chef.ID {
			t.Fatalf("approved = %+v", approved)
		}

		var returned Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			returned, err = service.Return(ctx, q, chefActor, "req-return", created.ID, ReturnInput{Version: 4, Reason: "Menge fehlt"})
			return err
		}); err != nil {
			t.Fatalf("return: %v", err)
		}
		if returned.Status != "draft" || returned.Version != 5 || returned.ApprovedAt != nil || returned.ApprovedBy != nil {
			t.Fatalf("returned = %+v", returned)
		}

		var transitions []ReportTransition
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			transitions, err = service.ListTransitions(ctx, q, chefActor, "", created.ID)
			return err
		}); err != nil {
			t.Fatalf("list transitions: %v", err)
		}
		if len(transitions) != 1 || transitions[0].FromStatus != "approved" || transitions[0].ToStatus != "draft" || transitions[0].Kind != "correction" || transitions[0].Reason != "Menge fehlt" {
			t.Fatalf("transitions = %+v", transitions)
		}

		// A returned report can be submitted again and released for billing.
		var resubmitted Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			resubmitted, err = service.Submit(ctx, q, employeeActor, "", created.ID, 5)
			return err
		}); err != nil {
			t.Fatalf("resubmit: %v", err)
		}
		var reapproved Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			reapproved, err = service.Approve(ctx, q, chefActor, "", created.ID, resubmitted.Version)
			return err
		}); err != nil {
			t.Fatalf("reapprove: %v", err)
		}
		var ready Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			ready, err = service.ReadyForBilling(ctx, q, chefActor, "req-ready", created.ID, reapproved.Version)
			return err
		}); err != nil {
			t.Fatalf("ready for billing: %v", err)
		}
		if ready.Status != "ready_for_billing" || ready.Version != 8 {
			t.Fatalf("ready = %+v", ready)
		}

		for _, action := range []string{"report.created", "report.updated", "report.submitted", "report.approved", "report.returned", "report.ready_for_billing"} {
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM audit_events WHERE entity_id = $1 AND action = $2", created.ID, action).Scan(&count); err != nil {
				t.Fatalf("count audit %s: %v", action, err)
			}
			if count == 0 {
				t.Errorf("missing audit event %s", action)
			}
		}

		// A report in another tenant is invisible.
		tenantB, chefB := seedReportTenant(t, queries)
		patientB := seedReportPatient(t, tx, tenantB.ID)
		visitB := seedReportVisit(t, tx, tenantB.ID, patientB, chefB.ID, "completed")
		activityB := seedReportActivity(t, tx, tenantB.ID, patientB, chefB.ID)
		var reportB Report
		if err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			var err error
			reportB, err = service.Create(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", visitB, CreateInput{
				Activities: []ActivityInput{{ActivityID: activityB, Quantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("create tenant B report: %v", err)
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, chefActor, "", reportB.ID)
			return err
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("validation rules", func(t *testing.T) {
		tx, queries := newReportTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedReportTenant(t, queries)
		employee := seedReportUser(t, queries, tenant.ID, "employee")
		patientID := seedReportPatient(t, tx, tenant.ID)
		otherPatientID := seedReportPatient(t, tx, tenant.ID)
		seedReportAssignment(t, tx, tenant.ID, patientID, employee.ID, chef.ID)
		plannedVisit := seedReportVisit(t, tx, tenant.ID, patientID, employee.ID, "planned")
		completedVisit := seedReportVisit(t, tx, tenant.ID, patientID, employee.ID, "completed")
		otherActivity := seedReportActivity(t, tx, tenant.ID, otherPatientID, employee.ID)
		activityID := seedReportActivity(t, tx, tenant.ID, patientID, employee.ID)
		service := newService()
		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, chefActor, "", plannedVisit, CreateInput{
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 1}},
			})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("planned visit err = %v, want ErrConflict", err)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, chefActor, "", completedVisit, CreateInput{
				Activities: []ActivityInput{{ActivityID: otherActivity, Quantity: 1}},
			})
			return err
		}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("other patient activity err = %v, want ErrInvalid", err)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, chefActor, "", completedVisit, CreateInput{
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 0}},
			})
			return err
		}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("zero quantity err = %v, want ErrInvalid", err)
		}

		var first Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			first, err = service.Create(ctx, q, chefActor, "", completedVisit, CreateInput{
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("create first report: %v", err)
		}

		secondVisit := seedReportVisit(t, tx, tenant.ID, patientID, employee.ID, "completed")
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, chefActor, "", secondVisit, CreateInput{
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 1}},
			})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate activity err = %v, want ErrConflict", err)
		}

		empty := []ActivityInput{}
		var cleared Report
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			cleared, err = service.Update(ctx, q, chefActor, "", first.ID, UpdateInput{Version: first.Version, Activities: &empty})
			return err
		}); err != nil {
			t.Fatalf("clear activities: %v", err)
		}
		if len(cleared.Activities) != 0 {
			t.Fatalf("activities after clear = %+v", cleared.Activities)
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Submit(ctx, q, chefActor, "", first.ID, cleared.Version)
			return err
		}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("submit empty err = %v, want ErrInvalid", err)
		}
	})

	t.Run("concurrent updates use optimistic locking", func(t *testing.T) {
		tenant, chef := seedCommittedReportTenant(t, conn)
		defer func() {
			if _, err := conn.Exec("DELETE FROM tenants WHERE id = $1", tenant.ID); err != nil {
				t.Errorf("cleanup tenant: %v", err)
			}
		}()

		patientID, visitID, activityID := seedCommittedReportData(t, conn, tenant.ID, chef.ID)
		// Use a no-op recorder so the committed tenant has no append-only audit
		// rows and can be removed cleanly after the test.
		service := NewService(NewRepository(cipher), audit.NoopRecorder{}, nil)
		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}

		var report Report
		if err := db.WithTenant(ctx, conn, tenant.ID, func(q *db.Queries) error {
			var err error
			report, err = service.Create(ctx, q, chefActor, "", visitID, CreateInput{
				Note:       "start",
				Activities: []ActivityInput{{ActivityID: activityID, Quantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("create report: %v", err)
		}
		_ = patientID

		const writers = 5
		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			successes int
			conflicts int
			other     []error
		)
		start := make(chan struct{})
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				note := fmt.Sprintf("writer-%d", index)
				err := db.WithTenant(ctx, conn, tenant.ID, func(q *db.Queries) error {
					_, err := service.Update(ctx, q, chefActor, "", report.ID, UpdateInput{Version: 1, Note: &note})
					return err
				})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					successes++
				case errors.Is(err, ErrConflict):
					conflicts++
				default:
					other = append(other, err)
				}
			}(i)
		}
		close(start)
		wg.Wait()

		if len(other) > 0 {
			t.Fatalf("unexpected errors: %v", other)
		}
		if successes != 1 || conflicts != writers-1 {
			t.Fatalf("successes = %d, conflicts = %d, want 1 and %d", successes, conflicts, writers-1)
		}

		var final Report
		if err := db.WithTenant(ctx, conn, tenant.ID, func(q *db.Queries) error {
			var err error
			final, err = service.Get(ctx, q, chefActor, "", report.ID)
			return err
		}); err != nil {
			t.Fatalf("get final report: %v", err)
		}
		if final.Version != 2 {
			t.Fatalf("final version = %d, want 2", final.Version)
		}
	})
}

func newReportTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedReportTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "report-" + uuid.NewString() + "@example.com",
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
	return tenant, seedReportUser(t, queries, tenant.ID, "chef")
}

func seedReportUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func seedReportPatient(t *testing.T, tx *sql.Tx, tenantID uuid.UUID) uuid.UUID {
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

func seedReportAssignment(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID, assignedBy uuid.UUID) {
	t.Helper()

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO patient_assignments (tenant_id, patient_id, employee_id, assigned_by, assignment_type, valid_from)
		 VALUES ($1, $2, $3, $4, 'regular', clock_timestamp() - interval '1 hour')`,
		tenantID, patientID, employeeID, assignedBy,
	); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}

func seedReportVisit(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID uuid.UUID, status string) uuid.UUID {
	t.Helper()

	visitID := uuid.New()
	now := time.Now().UTC()
	var actualStart, actualEnd any
	if status == "completed" || status == "in_progress" {
		actualStart = now.Add(-time.Hour)
	}
	if status == "completed" {
		actualEnd = now
	}

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO visits (id, tenant_id, patient_id, employee_id, actual_employee_id, planned_start, actual_start, actual_end, status, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $4)`,
		visitID, tenantID, patientID, employeeID, employeeID, now.Add(-2*time.Hour), actualStart, actualEnd, status,
	); err != nil {
		t.Fatalf("seed visit: %v", err)
	}
	return visitID
}

func seedReportActivity(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID uuid.UUID) uuid.UUID {
	t.Helper()

	var activityID uuid.UUID
	if err := tx.QueryRow(
		`INSERT INTO activities (tenant_id, patient_id, employee_id, performed_at, duration_minutes, status, created_by)
		 VALUES ($1, $2, $3, clock_timestamp() - interval '30 minutes', 30, 'recorded', $3)
		 RETURNING id`,
		tenantID, patientID, employeeID,
	).Scan(&activityID); err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	return activityID
}

// seedCommittedReportTenant creates committed data because the concurrency test
// needs more than one database connection.
func seedCommittedReportTenant(t *testing.T, conn *sql.DB) (db.Tenant, db.User) {
	t.Helper()

	queries := db.New(conn)
	tenant, err := queries.CreateTenant(context.Background(), db.CreateTenantParams{
		Email:          "concurrent-" + uuid.NewString() + "@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenant, err = queries.UpdateTenantStatus(context.Background(), db.UpdateTenantStatusParams{ID: tenant.ID, Status: "valid"})
	if err != nil {
		t.Fatalf("set tenant status: %v", err)
	}
	chef, err := queries.CreateUser(context.Background(), db.CreateUserParams{
		Name:           "chef",
		Role:           "chef",
		HashedPassword: "hash",
		Email:          "concurrent-chef-" + uuid.NewString() + "@example.com",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create chef: %v", err)
	}
	return tenant, chef
}

func seedCommittedReportData(t *testing.T, conn *sql.DB, tenantID, employeeID uuid.UUID) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()

	ctx := context.Background()
	patientID := uuid.New()
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO patients (id, tenant_id, first_name_enc, last_name_enc, birth_date_enc, key_version)
		 VALUES ($1, $2, $3, $4, $5, 1)`,
		patientID, tenantID, []byte("first"), []byte("last"), []byte("birth"),
	); err != nil {
		t.Fatalf("seed patient: %v", err)
	}

	now := time.Now().UTC()
	visitID := uuid.New()
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO visits (id, tenant_id, patient_id, employee_id, actual_employee_id, planned_start, actual_start, actual_end, status, created_by)
		 VALUES ($1, $2, $3, $4, $4, $5, $5, $6, 'completed', $4)`,
		visitID, tenantID, patientID, employeeID, now.Add(-2*time.Hour), now,
	); err != nil {
		t.Fatalf("seed visit: %v", err)
	}

	var activityID uuid.UUID
	if err := conn.QueryRowContext(ctx,
		`INSERT INTO activities (tenant_id, patient_id, employee_id, performed_at, duration_minutes, status, created_by)
		 VALUES ($1, $2, $3, clock_timestamp() - interval '30 minutes', 30, 'recorded', $3)
		 RETURNING id`,
		tenantID, patientID, employeeID,
	).Scan(&activityID); err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	return patientID, visitID, activityID
}
