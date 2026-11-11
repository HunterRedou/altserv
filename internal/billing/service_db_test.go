package billing

import (
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

// TestBillingServiceDatabase covers the deterministic run lifecycle, the
// chef-only access rules, tenant rules, optimistic locking and audit events. It
// is skipped unless TEST_DB_URL is set and migration 020 is applied.
func TestBillingServiceDatabase(t *testing.T) {
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
	service := NewService(NewRepository(), audit.SQLRecorder{})

	t.Run("generate approve export void and regenerate", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedBillingTenant(t, queries)
		employee := seedBillingUser(t, queries, tenant.ID, "employee")
		admin := seedBillingUser(t, queries, tenant.ID, "platform_admin")
		patientID := seedBillingPatient(t, tx, tenant.ID)
		definitionID := seedBillingDefinition(t, tx, "sgb_xi")
		contractID := seedBillingContract(t, tx, tenant.ID, definitionID, "sgb_xi")
		visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
		activityID := seedBillingActivity(t, tx, tenant.ID, patientID, employee.ID, definitionID, contractID, "sgb_xi")
		reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "ready_for_billing")
		seedBillingReportActivity(t, tx, tenant.ID, reportID, activityID, 1)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		employeeActor := Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}
		adminActor := Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}

		for _, actor := range []Actor{employeeActor, adminActor} {
			if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				_, err := service.Generate(ctx, q, actor, "req", reportID, GenerateInput{})
				return err
			}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s generate err = %v, want ErrForbidden", actor.Role, err)
			}
			if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				_, err := service.Get(ctx, q, actor, "req", uuid.New())
				return err
			}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s get err = %v, want ErrForbidden", actor.Role, err)
			}
		}

		var run BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			run, err = service.Generate(ctx, q, chefActor, "req-generate", reportID, GenerateInput{Version: 0})
			return err
		}); err != nil {
			t.Fatalf("generate: %v", err)
		}
		if run.Status != "draft" || run.Version != 1 {
			t.Fatalf("run = %+v", run)
		}
		if len(run.Lines) != 1 {
			t.Fatalf("lines = %+v, want one", run.Lines)
		}
		if len(run.Findings) != 0 {
			t.Fatalf("findings = %+v, want none", run.Findings)
		}
		line := run.Lines[0]
		if line.ActivityID != activityID || line.ServiceCode == "" || line.ServiceName != "Testleistung" || line.PayerName != "Pflegekasse" {
			t.Fatalf("line = %+v", line)
		}

		// Regenerating needs the current version. A stale version is rejected
		// and a second correct call replaces the lines instead of duplicating.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Generate(ctx, q, chefActor, "req", reportID, GenerateInput{Version: 0})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale regenerate err = %v, want ErrConflict", err)
		}
		var regenerated BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			regenerated, err = service.Generate(ctx, q, chefActor, "req-regenerate", reportID, GenerateInput{Version: run.Version})
			return err
		}); err != nil {
			t.Fatalf("regenerate: %v", err)
		}
		if regenerated.Version != run.Version+1 || len(regenerated.Lines) != 1 {
			t.Fatalf("regenerated = %+v", regenerated)
		}
		run = regenerated

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Approve(ctx, q, chefActor, "req", run.ID, run.Version-1)
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale approve err = %v, want ErrConflict", err)
		}

		var approved BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			approved, err = service.Approve(ctx, q, chefActor, "req-approve", run.ID, run.Version)
			return err
		}); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if approved.Status != "approved" || approved.ApprovedBy == nil || *approved.ApprovedBy != chef.ID {
			t.Fatalf("approved = %+v", approved)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Generate(ctx, q, chefActor, "req", reportID, GenerateInput{Version: approved.Version})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("generate approved err = %v, want ErrConflict", err)
		}

		var csvData, pdfData []byte
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			csvData, err = service.ExportCSV(ctx, q, chefActor, "req-export", approved.ID)
			return err
		}); err != nil {
			t.Fatalf("export csv: %v", err)
		}
		if len(csvData) == 0 {
			t.Fatal("empty csv export")
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			pdfData, err = service.ExportPDF(ctx, q, chefActor, "req-export", approved.ID)
			return err
		}); err != nil {
			t.Fatalf("export pdf: %v", err)
		}
		if len(pdfData) == 0 || string(pdfData[:8]) != "%PDF-1.4" {
			t.Fatalf("pdf export is invalid")
		}

		var voided BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			voided, err = service.Void(ctx, q, chefActor, "req-void", approved.ID, approved.Version)
			return err
		}); err != nil {
			t.Fatalf("void: %v", err)
		}
		if voided.Status != "voided" {
			t.Fatalf("voided = %+v", voided)
		}
		var lineCount int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM billing_lines WHERE run_id = $1`, approved.ID).Scan(&lineCount); err != nil {
			t.Fatalf("count lines: %v", err)
		}
		if lineCount != 0 {
			t.Fatalf("lines after void = %d, want 0", lineCount)
		}

		// The activity can be billed again by a corrected run.
		var rebilled BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			rebilled, err = service.Generate(ctx, q, chefActor, "req-rebill", reportID, GenerateInput{Version: 0})
			return err
		}); err != nil {
			t.Fatalf("rebill: %v", err)
		}
		if rebilled.ID == approved.ID || len(rebilled.Lines) != 1 {
			t.Fatalf("rebilled = %+v", rebilled)
		}

		var actions []string
		rows, err := tx.QueryContext(ctx,
			`SELECT action FROM audit_events WHERE tenant_id = $1 AND entity_type IN ('billing_run', 'billing_rule') ORDER BY occurred_at, action`,
			tenant.ID)
		if err != nil {
			t.Fatalf("query audit events: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var action string
			if err := rows.Scan(&action); err != nil {
				t.Fatalf("scan audit action: %v", err)
			}
			actions = append(actions, action)
		}
		for _, expected := range []string{"billing.run_generated", "billing.run_approved", "billing.run_voided", "billing.run_exported"} {
			found := false
			for _, action := range actions {
				if action == expected {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("audit actions = %v, want %s", actions, expected)
			}
		}
	})

	t.Run("report must be ready for billing", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedBillingTenant(t, queries)
		employee := seedBillingUser(t, queries, tenant.ID, "employee")
		patientID := seedBillingPatient(t, tx, tenant.ID)
		visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
		reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "approved")

		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Generate(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req", reportID, GenerateInput{})
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("invalid activity blocks approval", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedBillingTenant(t, queries)
		employee := seedBillingUser(t, queries, tenant.ID, "employee")
		patientID := seedBillingPatient(t, tx, tenant.ID)
		visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
		activityID := seedBillingUnbillableActivity(t, tx, tenant.ID, patientID, employee.ID)
		reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "ready_for_billing")
		seedBillingReportActivity(t, tx, tenant.ID, reportID, activityID, 1)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		var run BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			run, err = service.Generate(ctx, q, chefActor, "req", reportID, GenerateInput{})
			return err
		}); err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(run.Lines) != 0 {
			t.Fatalf("lines = %+v, want none", run.Lines)
		}
		if !hasBillingFinding(run.Findings, CodeActivityNotBillable) {
			t.Fatalf("findings = %+v, want %s", run.Findings, CodeActivityNotBillable)
		}
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Approve(ctx, q, chefActor, "req", run.ID, run.Version)
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("approve err = %v, want ErrInvalid", err)
		}
	})

	t.Run("approved run protects its report activities", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedBillingTenant(t, queries)
		employee := seedBillingUser(t, queries, tenant.ID, "employee")
		patientID := seedBillingPatient(t, tx, tenant.ID)
		definitionID := seedBillingDefinition(t, tx, "sgb_xi")
		contractID := seedBillingContract(t, tx, tenant.ID, definitionID, "sgb_xi")
		visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
		activityID := seedBillingActivity(t, tx, tenant.ID, patientID, employee.ID, definitionID, contractID, "sgb_xi")
		reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "ready_for_billing")
		seedBillingReportActivity(t, tx, tenant.ID, reportID, activityID, 1)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		var run BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			run, err = service.Generate(ctx, q, chefActor, "req", reportID, GenerateInput{})
			return err
		}); err != nil {
			t.Fatalf("generate: %v", err)
		}
		var approved BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			approved, err = service.Approve(ctx, q, chefActor, "req", run.ID, run.Version)
			return err
		}); err != nil {
			t.Fatalf("approve: %v", err)
		}

		// A correction must not silently delete a line of the approved run. The
		// rejected statement aborts the transaction, so run it in a savepoint.
		if _, err := tx.ExecContext(ctx, `SAVEPOINT protect`); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM report_activities WHERE tenant_id = $1 AND report_id = $2`,
			tenant.ID, reportID,
		); err == nil {
			t.Fatal("deleting a report activity of an approved billing run was allowed")
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT protect`); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
		var lineCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM billing_lines WHERE run_id = $1`, approved.ID,
		).Scan(&lineCount); err != nil {
			t.Fatalf("count lines: %v", err)
		}
		if lineCount != 1 {
			t.Fatalf("lines after blocked correction = %d, want 1", lineCount)
		}

		// Voiding removes the lines, so the correction is then possible.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Void(ctx, q, chefActor, "req", approved.ID, approved.Version)
			return err
		}); err != nil {
			t.Fatalf("void: %v", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM report_activities WHERE tenant_id = $1 AND report_id = $2`,
			tenant.ID, reportID,
		); err != nil {
			t.Fatalf("delete report activity after void: %v", err)
		}
	})

	t.Run("tenant rules are created and applied", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedBillingTenant(t, queries)
		employee := seedBillingUser(t, queries, tenant.ID, "employee")
		patientID := seedBillingPatient(t, tx, tenant.ID)
		definitionA := seedBillingDefinition(t, tx, "sgb_xi")
		definitionB := seedBillingDefinition(t, tx, "sgb_xi")
		contractA := seedBillingContract(t, tx, tenant.ID, definitionA, "sgb_xi")
		contractB := seedBillingContract(t, tx, tenant.ID, definitionB, "sgb_xi")
		visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
		activityA := seedBillingActivity(t, tx, tenant.ID, patientID, employee.ID, definitionA, contractA, "sgb_xi")
		activityB := seedBillingActivity(t, tx, tenant.ID, patientID, employee.ID, definitionB, contractB, "sgb_xi")
		reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "ready_for_billing")
		seedBillingReportActivity(t, tx, tenant.ID, reportID, activityA, 1)
		seedBillingReportActivity(t, tx, tenant.ID, reportID, activityB, 2)

		chefActor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		var rule BillingRule
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			rule, err = service.CreateRule(ctx, q, chefActor, "req", CreateRuleInput{
				Kind:                       RuleKindExclusion,
				ServiceDefinitionID:        definitionA,
				RelatedServiceDefinitionID: definitionB,
				LegalArea:                  "sgb_xi",
				Description:                "Nicht gemeinsam",
				ValidFrom:                  time.Now().Add(-time.Hour).UTC(),
			})
			return err
		}); err != nil {
			t.Fatalf("create rule: %v", err)
		}
		if rule.Kind != RuleKindExclusion {
			t.Fatalf("rule = %+v", rule)
		}

		var run BillingRun
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			run, err = service.Generate(ctx, q, chefActor, "req", reportID, GenerateInput{})
			return err
		}); err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !hasBillingFinding(run.Findings, CodeRuleExclusion) {
			t.Fatalf("findings = %+v, want %s", run.Findings, CodeRuleExclusion)
		}
	})
}

func newBillingTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedBillingTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "billing-" + uuid.NewString() + "@example.com",
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
	return tenant, seedBillingUser(t, queries, tenant.ID, "chef")
}

func seedBillingUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func seedBillingPatient(t *testing.T, tx *sql.Tx, tenantID uuid.UUID) uuid.UUID {
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

func seedBillingVisit(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID uuid.UUID) uuid.UUID {
	t.Helper()

	visitID := uuid.New()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO visits (id, tenant_id, patient_id, employee_id, actual_employee_id, planned_start, actual_start, actual_end, status, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'completed', $4)`,
		visitID, tenantID, patientID, employeeID, employeeID, now.Add(-2*time.Hour), now.Add(-time.Hour), now,
	); err != nil {
		t.Fatalf("seed visit: %v", err)
	}
	return visitID
}

func seedBillingDefinition(t *testing.T, tx *sql.Tx, legalArea string) uuid.UUID {
	t.Helper()

	catalogID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO service_catalogs (id, code, name, source_note, valid_from)
		 VALUES ($1, $2, 'Testkatalog', 'test only', $3)`,
		catalogID, "CAT-"+uuid.NewString(), time.Now().Add(-365*24*time.Hour).UTC(),
	); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	definitionID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO service_definitions (id, catalog_id, code, name, legal_area, valid_from)
		 VALUES ($1, $2, $3, 'Testleistung', $4, $5)`,
		definitionID, catalogID, "S-"+uuid.NewString()[:8], legalArea, time.Now().Add(-365*24*time.Hour).UTC(),
	); err != nil {
		t.Fatalf("seed service definition: %v", err)
	}
	return definitionID
}

func seedBillingContract(t *testing.T, tx *sql.Tx, tenantID, definitionID uuid.UUID, legalArea string) uuid.UUID {
	t.Helper()

	contractID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO tenant_service_contracts (id, tenant_id, service_definition_id, payer_name, contract_reference, legal_area, valid_from)
		 VALUES ($1, $2, $3, 'Pflegekasse', 'V-1', $4, $5)`,
		contractID, tenantID, definitionID, legalArea, time.Now().Add(-365*24*time.Hour).UTC(),
	); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	return contractID
}

func seedBillingActivity(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID, definitionID, contractID uuid.UUID, legalArea string) uuid.UUID {
	t.Helper()

	activityID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO activities (
		   id, tenant_id, patient_id, employee_id, service_definition_id, contract_id,
		   performed_at, duration_minutes, status, service_code, service_name,
		   legal_area, contract_reference, payer_name, created_by
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, 30, 'recorded', 'S-1', 'Testleistung', $8, 'V-1', 'Pflegekasse', $4)`,
		activityID, tenantID, patientID, employeeID, definitionID, contractID, time.Now().Add(-30*time.Minute).UTC(), legalArea,
	); err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	return activityID
}

func seedBillingUnbillableActivity(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID uuid.UUID) uuid.UUID {
	t.Helper()

	activityID := uuid.New()
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO activities (id, tenant_id, patient_id, employee_id, performed_at, duration_minutes, status, created_by)
		 VALUES ($1, $2, $3, $4, $5, 30, 'recorded', $4)`,
		activityID, tenantID, patientID, employeeID, time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("seed unbillable activity: %v", err)
	}
	return activityID
}

func seedBillingReport(t *testing.T, tx *sql.Tx, tenantID, patientID, visitID, employeeID, chefID uuid.UUID, status string) uuid.UUID {
	t.Helper()

	reportID := uuid.New()
	now := time.Now().UTC()
	var submittedAt, approvedAt any
	var approvedBy any
	if status != "draft" {
		submittedAt = now
	}
	if status == "approved" || status == "ready_for_billing" {
		approvedAt = now
		approvedBy = chefID
	}
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO reports (id, tenant_id, visit_id, patient_id, employee_id, status, version, created_by, submitted_at, approved_at, approved_by)
		 VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, $9, $10)`,
		reportID, tenantID, visitID, patientID, employeeID, status, chefID, submittedAt, approvedAt, approvedBy,
	); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	return reportID
}

func seedBillingReportActivity(t *testing.T, tx *sql.Tx, tenantID, reportID, activityID uuid.UUID, sortOrder int) {
	t.Helper()

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO report_activities (tenant_id, report_id, activity_id, quantity, performed_at, sort_order)
		 VALUES ($1, $2, $3, 1, $4, $5)`,
		tenantID, reportID, activityID, time.Now().Add(-30*time.Minute).UTC(), sortOrder,
	); err != nil {
		t.Fatalf("seed report activity: %v", err)
	}
}
