package activities

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestActivityServiceDatabase covers assignment enforcement, note encryption,
// tenant isolation and the historical service snapshot. It is skipped unless
// TEST_DB_URL is set and migration 016 is applied.
func TestActivityServiceDatabase(t *testing.T) {
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
	recorder := audit.SQLRecorder{}
	catalogRepository := catalog.NewRepository()
	assignmentService := assignments.NewService(assignments.NewRepository(), recorder)
	patientService := patients.NewService(patients.NewRepository(cipher), recorder)
	service := NewService(NewRepository(cipher), catalogRepository, recorder, assignmentService)
	ctx := context.Background()

	t.Run("note is encrypted and assignment is enforced", func(t *testing.T) {
		tx, queries := newActivityTx(t, conn)
		defer tx.Rollback()

		tenant, chef, employee := seedActivityTenant(t, queries)
		patient := createActivityPatient(t, ctx, tx, patientService, tenant.ID, chef.ID, "Anna", "Muster")
		assigned := createActivityPatient(t, ctx, tx, patientService, tenant.ID, chef.ID, "Berta", "Beispiel")
		seedActivityAssignment(t, ctx, tx, tenant.ID, patient.ID, employee.ID, chef.ID)

		note := "Hilfe beim Anziehen und Waschen"
		var created Activity
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}, "req", CreateInput{
				PatientID:       patient.ID,
				PerformedAt:     time.Now().Add(-time.Hour),
				DurationMinutes: 45,
				Note:            note,
			})
			return err
		}); err != nil {
			t.Fatalf("create activity: %v", err)
		}
		if created.Note != note {
			t.Errorf("note = %q, want the original note", created.Note)
		}
		if created.EmployeeID != employee.ID {
			t.Errorf("employee = %s, want the actor %s", created.EmployeeID, employee.ID)
		}

		var noteEnc []byte
		var keyVersion sql.NullInt32
		if err := tx.QueryRow("SELECT note_enc, note_key_version FROM activities WHERE id = $1", created.ID).Scan(&noteEnc, &keyVersion); err != nil {
			t.Fatalf("read note ciphertext: %v", err)
		}
		if len(noteEnc) == 0 || !keyVersion.Valid {
			t.Fatal("note was not encrypted")
		}
		if bytes.Contains(noteEnc, []byte(note)) {
			t.Fatal("note ciphertext contains the plaintext")
		}

		var loaded Activity
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			loaded, err = service.Get(ctx, q, Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}, "req-read", created.ID)
			return err
		}); err != nil {
			t.Fatalf("get activity: %v", err)
		}
		if loaded.Note != note {
			t.Errorf("decrypted note = %q, want %q", loaded.Note, note)
		}

		// The employee has no assignment for the second patient.
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, Actor{UserID: employee.ID, TenantID: tenant.ID, Role: "employee"}, "req", CreateInput{
				PatientID:       assigned.ID,
				PerformedAt:     time.Now().Add(-time.Hour),
				DurationMinutes: 15,
			})
			return err
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("unassigned create err = %v, want ErrForbidden", err)
		}

		// The audit event must not contain the free text.
		rows, err := tx.Query("SELECT metadata::text FROM audit_events WHERE tenant_id = $1", tenant.ID)
		if err != nil {
			t.Fatalf("read audit metadata: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var metadata string
			if err := rows.Scan(&metadata); err != nil {
				t.Fatalf("scan audit metadata: %v", err)
			}
			if strings.Contains(metadata, "Anziehen") {
				t.Fatalf("audit metadata leaked the note text: %s", metadata)
			}
		}
	})

	t.Run("snapshot survives a catalog change", func(t *testing.T) {
		tx, queries := newActivityTx(t, conn)
		defer tx.Rollback()

		tenant, chef, employee := seedActivityTenant(t, queries)
		patient := createActivityPatient(t, ctx, tx, patientService, tenant.ID, chef.ID, "Carla", "Schnell")
		seedActivityAssignment(t, ctx, tx, tenant.ID, patient.ID, employee.ID, chef.ID)
		definition := seedActivityDefinition(t, tx, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-48*time.Hour), nil)
		contract := createActivityContract(t, ctx, tx, catalogRepository, tenant.ID, chef.ID, definition)

		var created Activity
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			created, err = service.Create(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req", CreateInput{
				PatientID:           patient.ID,
				EmployeeID:          employee.ID,
				ServiceDefinitionID: &definition.ID,
				ContractID:          &contract.ID,
				PerformedAt:         time.Now().Add(-time.Hour),
				DurationMinutes:     30,
			})
			return err
		}); err != nil {
			t.Fatalf("create billable activity: %v", err)
		}
		if created.ServiceName != "Grundpflege" || created.ServiceCode != "LK-001" {
			t.Fatalf("snapshot = %+v, want the original definition", created)
		}
		if created.PayerName != "AOK Brandenburg" {
			t.Errorf("payer = %q, want AOK Brandenburg", created.PayerName)
		}

		// Rename the global definition. The activity keeps its snapshot.
		if _, err := tx.Exec("UPDATE service_definitions SET name = 'Umbenannte Leistung' WHERE id = $1", definition.ID); err != nil {
			t.Fatalf("rename definition: %v", err)
		}

		var loaded Activity
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			loaded, err = service.Get(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req", created.ID)
			return err
		}); err != nil {
			t.Fatalf("get activity: %v", err)
		}
		if loaded.ServiceName != "Grundpflege" {
			t.Errorf("snapshot name = %q, want the historical Grundpflege", loaded.ServiceName)
		}
	})

	t.Run("contract from another tenant is rejected", func(t *testing.T) {
		tx, queries := newActivityTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA, employeeA := seedActivityTenant(t, queries)
		tenantB, chefB, employeeB := seedActivityTenant(t, queries)
		patientA := createActivityPatient(t, ctx, tx, patientService, tenantA.ID, chefA.ID, "Dora", "A")
		patientB := createActivityPatient(t, ctx, tx, patientService, tenantB.ID, chefB.ID, "Emil", "B")
		definition := seedActivityDefinition(t, tx, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-48*time.Hour), nil)
		contract := createActivityContract(t, ctx, tx, catalogRepository, tenantA.ID, chefA.ID, definition)

		// A valid contract in tenant A works.
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "", CreateInput{
				PatientID:           patientA.ID,
				EmployeeID:          employeeA.ID,
				ServiceDefinitionID: &definition.ID,
				ContractID:          &contract.ID,
				PerformedAt:         time.Now().Add(-time.Hour),
				DurationMinutes:     20,
			})
			return err
		}); err != nil {
			t.Fatalf("tenant A create: %v", err)
		}

		// Tenant B cannot reference tenant A's contract.
		err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			_, err := service.Create(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "", CreateInput{
				PatientID:           patientB.ID,
				EmployeeID:          employeeB.ID,
				ServiceDefinitionID: &definition.ID,
				ContractID:          &contract.ID,
				PerformedAt:         time.Now().Add(-time.Hour),
				DurationMinutes:     20,
			})
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("cross-tenant contract err = %v, want ErrInvalid", err)
		}
	})
}

func newActivityTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

func seedActivityTenant(t *testing.T, queries *db.Queries) (db.Tenant, db.User, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "activity-" + uuid.NewString() + "@example.com",
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

	chef := seedActivityUser(t, queries, tenant.ID, "chef")
	employee := seedActivityUser(t, queries, tenant.ID, "employee")
	return tenant, chef, employee
}

func seedActivityUser(t *testing.T, queries *db.Queries, tenantID uuid.UUID, role string) db.User {
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

func createActivityPatient(t *testing.T, ctx context.Context, tx *sql.Tx, service *patients.Service, tenantID, chefID uuid.UUID, firstName, lastName string) patients.Patient {
	t.Helper()

	var patient patients.Patient
	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		var err error
		patient, err = service.Create(ctx, q, patients.Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "req", patients.CreateInput{
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

func seedActivityAssignment(t *testing.T, ctx context.Context, tx *sql.Tx, tenantID, patientID, employeeID, chefID uuid.UUID) {
	t.Helper()

	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		_, err := q.CreateAssignment(ctx, db.CreateAssignmentParams{
			TenantID:       tenantID,
			PatientID:      patientID,
			EmployeeID:     employeeID,
			AssignedBy:     chefID,
			AssignmentType: "regular",
			ValidFrom:      time.Now().Add(-time.Hour),
		})
		return err
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}

func seedActivityDefinition(t *testing.T, tx *sql.Tx, code, name, legalArea string, validFrom time.Time, validUntil *time.Time) catalog.Definition {
	t.Helper()

	var catalogID uuid.UUID
	if err := tx.QueryRow(
		"INSERT INTO service_catalogs (code, name, region, source_note, valid_from) VALUES ($1, $2, 'brandenburg', 'test', $3) RETURNING id",
		"BRB-"+uuid.NewString(), "Brandenburg Testkatalog", time.Now().Add(-72*time.Hour),
	).Scan(&catalogID); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	var until sql.NullTime
	if validUntil != nil {
		until = sql.NullTime{Time: *validUntil, Valid: true}
	}

	var id uuid.UUID
	if err := tx.QueryRow(
		`INSERT INTO service_definitions (catalog_id, code, name, description, legal_area, unit, valid_from, valid_until)
		 VALUES ($1, $2, $3, '', $4, 'Stunde', $5, $6) RETURNING id`,
		catalogID, code, name, legalArea, validFrom, until,
	).Scan(&id); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	return catalog.Definition{ID: id, CatalogID: catalogID, Code: code, Name: name, LegalArea: legalArea, ValidFrom: validFrom}
}

func createActivityContract(t *testing.T, ctx context.Context, tx *sql.Tx, repository *catalog.Repository, tenantID, chefID uuid.UUID, definition catalog.Definition) catalog.Contract {
	t.Helper()

	var contract catalog.Contract
	if err := db.WithTenantTx(ctx, tx, tenantID, func(q *db.Queries) error {
		var err error
		contract, err = repository.CreateContract(ctx, q, tenantID, catalog.CreateContractInput{
			ServiceDefinitionID: definition.ID,
			PayerName:           "AOK Brandenburg",
			ContractReference:   "V-2026-1",
			LegalArea:           definition.LegalArea,
			ValidFrom:           time.Now().Add(-24 * time.Hour).Truncate(time.Second),
		})
		return err
	}); err != nil {
		t.Fatalf("create contract: %v", err)
	}
	return contract
}
