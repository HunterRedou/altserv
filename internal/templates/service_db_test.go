package templates

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestTemplateServiceDatabase covers the versioned template lifecycle, tenant
// isolation and assignment-scoped employee access. It is skipped unless
// TEST_DB_URL is set and migration 017 is applied. Every subtest rolls back.
func TestTemplateServiceDatabase(t *testing.T) {
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
		catalogRepository := catalog.NewRepository()
		assignmentService := assignments.NewService(assignments.NewRepository(), audit.SQLRecorder{})
		return NewService(NewRepository(), catalogRepository, audit.SQLRecorder{}, assignmentService)
	}
	ctx := context.Background()

	t.Run("create list get update version and archive", func(t *testing.T) {
		tx, queries := newTemplateTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedTemplateTenant(t, queries, "chef")
		patientID := seedTemplatePatient(t, tx, tenant.ID)
		catalogID := seedTemplateCatalog(t, tx, "BRB-2026")
		first := seedTemplateDefinition(t, tx, catalogID, "LK-001", "Grundpflege")
		second := seedTemplateDefinition(t, tx, catalogID, "LK-002", "Koerperpflege")
		service := newService()
		actor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}

		var created Template
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, actor, "req-create", CreateInput{
				PatientID: patientID,
				Name:      "Morgenpflege",
				Activities: []ActivityInput{
					{ActivityID: second.ID, DefaultQuantity: 2, SortOrder: 1},
					{ActivityID: first.ID, DefaultQuantity: 1, SortOrder: 0},
				},
			})
			return err
		}); err != nil {
			t.Fatalf("create template: %v", err)
		}
		if created.Version != 1 || created.Status != "active" {
			t.Fatalf("created = version %d status %q, want 1 active", created.Version, created.Status)
		}
		if len(created.Activities) != 2 {
			t.Fatalf("activities = %d, want 2", len(created.Activities))
		}
		if created.Activities[0].ActivityID != first.ID {
			t.Errorf("first activity = %s, want sort_order 0 (%s)", created.Activities[0].ActivityID, first.ID)
		}
		if created.Activities[0].ServiceCode != "LK-001" || created.Activities[0].ServiceName != "Grundpflege" {
			t.Errorf("activity service details = %+v", created.Activities[0])
		}

		var listed []Template
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			listed, err = service.ListByPatient(ctx, q, actor, "", patientID)
			return err
		}); err != nil {
			t.Fatalf("list templates: %v", err)
		}
		if len(listed) != 1 || listed[0].ID != created.ID {
			t.Fatalf("listed = %+v, want the created template", listed)
		}

		var updated Template
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			updated, err = service.Update(ctx, q, actor, "req-update", created.ID, UpdateInput{
				Activities: []ActivityInput{{ActivityID: first.ID, DefaultQuantity: 3, SortOrder: 0}},
			})
			return err
		}); err != nil {
			t.Fatalf("update template: %v", err)
		}
		if updated.Version != 2 || updated.ID == created.ID {
			t.Fatalf("updated = version %d id %s, want a new version 2", updated.Version, updated.ID)
		}
		if len(updated.Activities) != 1 || updated.Activities[0].DefaultQuantity != 3 {
			t.Fatalf("updated activities = %+v", updated.Activities)
		}

		// The old version stays readable and is marked superseded.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			old, err := service.Get(ctx, q, actor, "", created.ID)
			if err != nil {
				return err
			}
			if old.Status != "superseded" || old.Version != 1 {
				t.Errorf("old version = status %q version %d, want superseded 1", old.Status, old.Version)
			}
			return nil
		}); err != nil {
			t.Fatalf("get old version: %v", err)
		}

		// Listing only returns the active version.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			listed, err = service.ListByPatient(ctx, q, actor, "", patientID)
			return err
		}); err != nil {
			t.Fatalf("list after update: %v", err)
		}
		if len(listed) != 1 || listed[0].ID != updated.ID {
			t.Fatalf("listed = %+v, want only version 2", listed)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			return service.Archive(ctx, q, actor, "req-archive", updated.ID)
		}); err != nil {
			t.Fatalf("archive template: %v", err)
		}
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			listed, err = service.ListByPatient(ctx, q, actor, "", patientID)
			return err
		}); err != nil {
			t.Fatalf("list after archive: %v", err)
		}
		if len(listed) != 0 {
			t.Fatalf("listed = %+v, want none after archive", listed)
		}

		// A new template with the same name starts at version 3.
		var recreated Template
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			recreated, err = service.Create(ctx, q, actor, "", CreateInput{
				PatientID:  patientID,
				Name:       "Morgenpflege",
				Activities: []ActivityInput{{ActivityID: first.ID, DefaultQuantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("recreate template: %v", err)
		}
		if recreated.Version != 3 {
			t.Errorf("recreated version = %d, want 3", recreated.Version)
		}
	})

	t.Run("templates are tenant isolated", func(t *testing.T) {
		tx, queries := newTemplateTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedTemplateTenant(t, queries, "chef")
		tenantB, chefB := seedTemplateTenant(t, queries, "chef")
		patientA := seedTemplatePatient(t, tx, tenantA.ID)
		catalogID := seedTemplateCatalog(t, tx, "BRB-2026")
		definition := seedTemplateDefinition(t, tx, catalogID, "LK-001", "Grundpflege")
		service := newService()

		var created Template
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "", CreateInput{
				PatientID:  patientA,
				Name:       "Morgenpflege",
				Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("create tenant A template: %v", err)
		}

		// Tenant B cannot read it by id.
		err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant get err = %v, want ErrNotFound", err)
		}

		// Tenant B cannot list tenant A's patient.
		if err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			list, err := service.ListByPatient(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", patientA)
			if err != nil {
				return err
			}
			if len(list) != 0 {
				t.Errorf("tenant B saw %d tenant A templates", len(list))
			}
			return nil
		}); err != nil {
			t.Fatalf("cross-tenant list: %v", err)
		}
	})

	t.Run("employee access follows the assignment", func(t *testing.T) {
		tx, queries := newTemplateTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedTemplateTenant(t, queries, "chef")
		employee := seedTemplateUser(t, queries, tenant.ID, "employee")
		assignedPatient := seedTemplatePatient(t, tx, tenant.ID)
		otherPatient := seedTemplatePatient(t, tx, tenant.ID)
		catalogID := seedTemplateCatalog(t, tx, "BRB-2026")
		definition := seedTemplateDefinition(t, tx, catalogID, "LK-001", "Grundpflege")
		seedTemplateAssignment(t, tx, tenant.ID, assignedPatient, employee.ID, chef.ID)
		service := newService()
		actor := Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}

		var created Template
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, actor, "", CreateInput{
				PatientID:  assignedPatient,
				Name:       "Morgenpflege",
				Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 1}},
			})
			return err
		}); err != nil {
			t.Fatalf("assigned employee create: %v", err)
		}

		// The unassigned patient is forbidden for writes and hidden for reads.
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, actor, "", CreateInput{
				PatientID:  otherPatient,
				Name:       "Abendpflege",
				Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 1}},
			})
			return err
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("unassigned create err = %v, want ErrForbidden", err)
		}

		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.ListByPatient(ctx, q, actor, "", otherPatient)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("unassigned list err = %v, want ErrNotFound", err)
		}

		// Revoking the assignment removes access immediately.
		if _, err := tx.ExecContext(ctx, "UPDATE patient_assignments SET revoked_at = clock_timestamp() WHERE patient_id = $1", assignedPatient); err != nil {
			t.Fatalf("revoke assignment: %v", err)
		}
		err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Get(ctx, q, actor, "", created.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("revoked get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown service definition is rejected", func(t *testing.T) {
		tx, queries := newTemplateTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedTemplateTenant(t, queries, "chef")
		patientID := seedTemplatePatient(t, tx, tenant.ID)
		service := newService()

		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "", CreateInput{
				PatientID:  patientID,
				Name:       "Morgenpflege",
				Activities: []ActivityInput{{ActivityID: uuid.New(), DefaultQuantity: 1}},
			})
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("a second active template with the same name conflicts", func(t *testing.T) {
		tx, queries := newTemplateTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedTemplateTenant(t, queries, "chef")
		patientID := seedTemplatePatient(t, tx, tenant.ID)
		catalogID := seedTemplateCatalog(t, tx, "BRB-2026")
		definition := seedTemplateDefinition(t, tx, catalogID, "LK-001", "Grundpflege")
		service := newService()
		actor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
		input := CreateInput{
			PatientID:  patientID,
			Name:       "Morgenpflege",
			Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 1}},
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, actor, "", input)
			return err
		}); err != nil {
			t.Fatalf("first create: %v", err)
		}

		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, actor, "", input)
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate create err = %v, want ErrConflict", err)
		}
	})
}

func newTemplateTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedTemplateTenant(t *testing.T, queries *db.Queries, role string) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "template-" + uuid.NewString() + "@example.com",
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
	user := seedTemplateUser(t, queries, tenant.ID, role)
	return tenant, user
}

func seedTemplateUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func seedTemplatePatient(t *testing.T, tx *sql.Tx, tenantID uuid.UUID) uuid.UUID {
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

func seedTemplateAssignment(t *testing.T, tx *sql.Tx, tenantID, patientID, employeeID, assignedBy uuid.UUID) {
	t.Helper()

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO patient_assignments (tenant_id, patient_id, employee_id, assigned_by, assignment_type, valid_from)
		 VALUES ($1, $2, $3, $4, 'regular', clock_timestamp() - interval '1 hour')`,
		tenantID, patientID, employeeID, assignedBy,
	); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}

func seedTemplateCatalog(t *testing.T, tx *sql.Tx, code string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := tx.QueryRow(
		"INSERT INTO service_catalogs (code, name, region, source_note, valid_from) VALUES ($1, $2, 'brandenburg', 'test', $3) RETURNING id",
		code, "Brandenburg Testkatalog", time.Now().Add(-48*time.Hour),
	).Scan(&id); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	return id
}

func seedTemplateDefinition(t *testing.T, tx *sql.Tx, catalogID uuid.UUID, code, name string) catalog.Definition {
	t.Helper()

	validFrom := time.Now().Add(-24 * time.Hour)
	var id uuid.UUID
	if err := tx.QueryRow(
		`INSERT INTO service_definitions (catalog_id, code, name, description, legal_area, unit, valid_from)
		 VALUES ($1, $2, $3, '', 'sgb_xi', 'Stunde', $4) RETURNING id`,
		catalogID, code, name, validFrom,
	).Scan(&id); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	return catalog.Definition{ID: id, CatalogID: catalogID, Code: code, Name: name, LegalArea: "sgb_xi", Unit: "Stunde", ValidFrom: validFrom}
}
