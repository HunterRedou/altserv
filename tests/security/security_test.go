// Package security_test contains negative authorization and crypto tests that
// cross package boundaries. They run against a real PostgreSQL database so the
// service rules, the row-level security policies and the AES-GCM path are
// exercised together (doc 19).
package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/devseed"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/tests/testutil"
	"github.com/google/uuid"
)

func TestCrossTenantPatientReadDenied(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	tenantB, chefB := testutil.CreateTenantWithUser(t, tx, "chef")
	actor := patients.Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}

	var getErr, listErr error
	var visible int
	if err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
		_, getErr = svc.Patients.Get(ctx, q, actor, "sec", seed.PatientIDs[0])
		_, listErr = svc.Patients.List(ctx, q, actor, "sec")
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM patients`).Scan(&visible)
	}); err != nil {
		t.Fatalf("tenant B transaction: %v", err)
	}

	if !errors.Is(getErr, patients.ErrNotFound) {
		t.Errorf("cross-tenant get err = %v, want ErrNotFound", getErr)
	}
	if listErr != nil {
		t.Errorf("cross-tenant list err = %v, want an empty list", listErr)
	}
	if visible != 0 {
		t.Errorf("tenant B sees %d patients under RLS, want 0", visible)
	}
}

func TestEmployeeSeesOnlyAssignedPatients(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	chef := patients.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	employee := patients.Actor{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"}

	var unassignedID uuid.UUID
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		patient, err := svc.Patients.Create(ctx, q, chef, "sec", patients.CreateInput{
			FirstName: "Unassigned",
			LastName:  "Patient",
			BirthDate: time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		unassignedID = patient.ID
		return nil
	}); err != nil {
		t.Fatalf("create unassigned patient: %v", err)
	}

	var assignedErr, unassignedErr error
	var list []patients.Patient
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		_, assignedErr = svc.Patients.Get(ctx, q, employee, "sec", seed.PatientIDs[0])
		_, unassignedErr = svc.Patients.Get(ctx, q, employee, "sec", unassignedID)
		var err error
		list, err = svc.Patients.List(ctx, q, employee, "sec")
		return err
	}); err != nil {
		t.Fatalf("employee transaction: %v", err)
	}

	if assignedErr != nil {
		t.Errorf("employee cannot read an assigned patient: %v", assignedErr)
	}
	if !errors.Is(unassignedErr, patients.ErrNotFound) {
		t.Errorf("unassigned patient err = %v, want ErrNotFound", unassignedErr)
	}
	if len(list) != 2 {
		t.Errorf("employee list = %d patients, want the 2 assigned patients", len(list))
	}
	for _, patient := range list {
		if patient.ID == unassignedID {
			t.Error("employee list contains the unassigned patient")
		}
	}
}

func TestExpiredSubstituteDenied(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	chef := patients.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	employee := patients.Actor{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"}

	var patientID uuid.UUID
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		patient, err := svc.Patients.Create(ctx, q, chef, "sec", patients.CreateInput{
			FirstName: "Substitute",
			LastName:  "Patient",
			BirthDate: time.Date(1955, 3, 3, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		patientID = patient.ID

		now := time.Now().UTC()
		validFrom := now.Add(-2 * time.Hour)
		validUntil := now.Add(-time.Hour)
		_, err = svc.Assignments.Create(ctx, q, assignmentsActor(seed), "sec", patientID, assignmentsInput(seed.EmployeeID, validFrom, &validUntil))
		return err
	}); err != nil {
		t.Fatalf("create expired substitute: %v", err)
	}

	var allowed bool
	var getErr error
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		var err error
		allowed, err = svc.Assignments.CanAccessPatient(ctx, q, seed.TenantID, seed.EmployeeID, patientID)
		if err != nil {
			return err
		}
		_, getErr = svc.Patients.Get(ctx, q, employee, "sec", patientID)
		return nil
	}); err != nil {
		t.Fatalf("employee transaction: %v", err)
	}

	if allowed {
		t.Error("an expired substitute assignment still grants access")
	}
	if !errors.Is(getErr, patients.ErrNotFound) {
		t.Errorf("expired substitute get err = %v, want ErrNotFound", getErr)
	}
}

func TestRevokedAssignmentDenied(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	employee := patients.Actor{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"}

	var assignmentID uuid.UUID
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		list, err := svc.Assignments.ListByPatient(ctx, q, assignmentsActor(seed), seed.PatientIDs[0])
		if err != nil {
			return err
		}
		if len(list) == 0 {
			t.Fatal("seed has no assignment for the first patient")
		}
		assignmentID = list[0].ID
		_, err = svc.Assignments.Revoke(ctx, q, assignmentsActor(seed), "sec", assignmentID)
		return err
	}); err != nil {
		t.Fatalf("revoke assignment: %v", err)
	}

	var allowed bool
	var getErr error
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		var err error
		allowed, err = svc.Assignments.CanAccessPatient(ctx, q, seed.TenantID, seed.EmployeeID, seed.PatientIDs[0])
		if err != nil {
			return err
		}
		_, getErr = svc.Patients.Get(ctx, q, employee, "sec", seed.PatientIDs[0])
		return nil
	}); err != nil {
		t.Fatalf("employee transaction: %v", err)
	}

	if allowed {
		t.Error("a revoked assignment still grants access")
	}
	if !errors.Is(getErr, patients.ErrNotFound) {
		t.Errorf("revoked assignment get err = %v, want ErrNotFound", getErr)
	}
}

func TestPlatformAdminCannotReadPatients(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	admin := patients.Actor{UserID: seed.AdminID, TenantID: seed.TenantID, Role: "platform_admin"}

	var getErr, listErr, searchErr error
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		_, getErr = svc.Patients.Get(ctx, q, admin, "sec", seed.PatientIDs[0])
		_, listErr = svc.Patients.List(ctx, q, admin, "sec")
		_, searchErr = svc.Patients.Search(ctx, q, admin, "sec", "Demo")
		return nil
	}); err != nil {
		t.Fatalf("platform admin transaction: %v", err)
	}

	if !errors.Is(getErr, patients.ErrForbidden) {
		t.Errorf("platform admin get err = %v, want ErrForbidden", getErr)
	}
	if !errors.Is(listErr, patients.ErrForbidden) {
		t.Errorf("platform admin list err = %v, want ErrForbidden", listErr)
	}
	if !errors.Is(searchErr, patients.ErrForbidden) {
		t.Errorf("platform admin search err = %v, want ErrForbidden", searchErr)
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	conn := testutil.OpenDB(t)
	ctx := context.Background()

	t.Run("bit flip", func(t *testing.T) {
		tx := testutil.BeginTx(t, conn)
		opts, seed := testutil.SeedDemo(t, tx)
		svc := testutil.NewServices(t, opts)

		var ciphertext []byte
		if err := tx.QueryRowContext(ctx,
			`SELECT first_name_enc FROM patients WHERE id = $1`, seed.PatientIDs[0],
		).Scan(&ciphertext); err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}
		ciphertext[len(ciphertext)-1] ^= 0xff
		if _, err := tx.ExecContext(ctx,
			`UPDATE patients SET first_name_enc = $1 WHERE id = $2`, ciphertext, seed.PatientIDs[0],
		); err != nil {
			t.Fatalf("tamper ciphertext: %v", err)
		}

		var readErr error
		if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
			_, readErr = svc.Patients.Get(ctx, q, chefActor(seed), "sec", seed.PatientIDs[0])
			return nil
		}); err != nil {
			t.Fatalf("read transaction: %v", err)
		}
		if readErr == nil {
			t.Fatal("a tampered ciphertext decrypted successfully")
		}
		if !strings.Contains(readErr.Error(), "decrypt") {
			t.Errorf("err = %v, want a decryption failure", readErr)
		}
	})

	t.Run("swapped ciphertext", func(t *testing.T) {
		tx := testutil.BeginTx(t, conn)
		opts, seed := testutil.SeedDemo(t, tx)
		svc := testutil.NewServices(t, opts)

		var other []byte
		if err := tx.QueryRowContext(ctx,
			`SELECT first_name_enc FROM patients WHERE id = $1`, seed.PatientIDs[1],
		).Scan(&other); err != nil {
			t.Fatalf("read other ciphertext: %v", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE patients SET first_name_enc = $1 WHERE id = $2`, other, seed.PatientIDs[0],
		); err != nil {
			t.Fatalf("swap ciphertext: %v", err)
		}

		var readErr error
		if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
			_, readErr = svc.Patients.Get(ctx, q, chefActor(seed), "sec", seed.PatientIDs[0])
			return nil
		}); err != nil {
			t.Fatalf("read transaction: %v", err)
		}
		if readErr == nil {
			t.Fatal("a ciphertext swapped between patients decrypted successfully")
		}
		if !strings.Contains(readErr.Error(), "decrypt") {
			t.Errorf("err = %v, want a decryption failure", readErr)
		}
	})
}

func TestAuditMetadataContainsNoPatientNames(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	firstName := "First-" + uuid.NewString()[:8]
	lastName := "Last-" + uuid.NewString()[:8]

	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		patient, err := svc.Patients.Create(ctx, q, chefActor(seed), "sec", patients.CreateInput{
			FirstName: firstName,
			LastName:  lastName,
			BirthDate: time.Date(1949, 12, 24, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		_, err = svc.Patients.Get(ctx, q, chefActor(seed), "sec", patient.ID)
		return err
	}); err != nil {
		t.Fatalf("create and read patient: %v", err)
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT action, metadata::text FROM audit_events WHERE tenant_id = $1`, seed.TenantID)
	if err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	defer rows.Close()

	events := 0
	for rows.Next() {
		var action, metadata string
		if err := rows.Scan(&action, &metadata); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		events++
		if strings.Contains(metadata, firstName) || strings.Contains(metadata, lastName) {
			t.Errorf("audit metadata for %q contains the patient name: %s", action, metadata)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if events == 0 {
		t.Fatal("no audit events were written")
	}
}

func TestTenantRolesCannotWriteGlobalCatalog(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	now := time.Now().UTC()
	code := "SEC-" + uuid.NewString()

	writeCatalog := func(actor catalog.Actor) error {
		var err error
		if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
			_, err = svc.Catalog.CreateCatalog(ctx, q, actor, "sec", catalog.CreateCatalogInput{
				Code:       code,
				Name:       "Security test catalog",
				Region:     "brandenburg",
				SourceNote: "synthetic security test, not a real catalog",
				ValidFrom:  now,
			})
			return nil
		}); err != nil {
			t.Fatalf("%s transaction: %v", actor.Role, err)
		}
		return err
	}

	for _, actor := range []catalog.Actor{
		{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"},
		{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"},
	} {
		if err := writeCatalog(actor); !errors.Is(err, catalog.ErrForbidden) {
			t.Errorf("%s catalog write err = %v, want ErrForbidden", actor.Role, err)
		}
	}

	// The platform admin is the only role allowed to write shared reference
	// data, so the positive path must still work.
	admin := catalog.Actor{UserID: seed.AdminID, TenantID: seed.TenantID, Role: "platform_admin"}
	var created catalog.Catalog
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		var err error
		created, err = svc.Catalog.CreateCatalog(ctx, q, admin, "sec", catalog.CreateCatalogInput{
			Code:       code,
			Name:       "Security test catalog",
			Region:     "brandenburg",
			SourceNote: "synthetic security test, not a real catalog",
			ValidFrom:  now,
		})
		return err
	}); err != nil {
		t.Fatalf("platform admin catalog write: %v", err)
	}
	if created.Code != code {
		t.Errorf("created catalog code = %q, want %q", created.Code, code)
	}

	var definitionErr error
	for _, actor := range []catalog.Actor{
		{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"},
		{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"},
	} {
		if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
			_, definitionErr = svc.Catalog.CreateDefinition(ctx, q, actor, "sec", catalog.CreateDefinitionInput{
				CatalogID: created.ID,
				Code:      "SEC-DEF-" + uuid.NewString(),
				Name:      "Security test service",
				LegalArea: "sgb_xi",
				Unit:      "Einsatz",
				ValidFrom: now,
			})
			return nil
		}); err != nil {
			t.Fatalf("%s definition transaction: %v", actor.Role, err)
		}
		if !errors.Is(definitionErr, catalog.ErrForbidden) {
			t.Errorf("%s definition write err = %v, want ErrForbidden", actor.Role, definitionErr)
		}
	}
}

func TestPlatformAdminCannotWriteTenantRules(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)
	admin := catalog.Actor{UserID: seed.AdminID, TenantID: seed.TenantID, Role: "platform_admin"}
	now := time.Now().UTC()

	var ruleErr, contractErr, listRulesErr, listContractsErr error
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		_, ruleErr = svc.Catalog.CreateRule(ctx, q, admin, "sec", catalog.CreateRuleInput{
			ServiceDefinitionID: seed.DefinitionIDs[0],
			ActivityLabel:       "Platform admin rule",
			LegalArea:           "sgb_xi",
			ValidFrom:           now,
		})
		_, contractErr = svc.Catalog.CreateContract(ctx, q, admin, "sec", catalog.CreateContractInput{
			ServiceDefinitionID: seed.DefinitionIDs[0],
			PayerName:           "Synthetic payer",
			LegalArea:           "sgb_xi",
			ValidFrom:           now,
		})
		_, listRulesErr = svc.Catalog.ListRules(ctx, q, admin)
		_, listContractsErr = svc.Catalog.ListContracts(ctx, q, admin)
		return nil
	}); err != nil {
		t.Fatalf("platform admin transaction: %v", err)
	}

	if !errors.Is(ruleErr, catalog.ErrForbidden) {
		t.Errorf("platform admin rule write err = %v, want ErrForbidden", ruleErr)
	}
	if !errors.Is(contractErr, catalog.ErrForbidden) {
		t.Errorf("platform admin contract write err = %v, want ErrForbidden", contractErr)
	}
	if !errors.Is(listRulesErr, catalog.ErrForbidden) {
		t.Errorf("platform admin rule read err = %v, want ErrForbidden", listRulesErr)
	}
	if !errors.Is(listContractsErr, catalog.ErrForbidden) {
		t.Errorf("platform admin contract read err = %v, want ErrForbidden", listContractsErr)
	}

	// The chef still owns the tenant-bound configuration.
	chef := catalog.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	var created catalog.Rule
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		var err error
		created, err = svc.Catalog.CreateRule(ctx, q, chef, "sec", catalog.CreateRuleInput{
			ServiceDefinitionID: seed.DefinitionIDs[0],
			ActivityLabel:       "Chef rule " + uuid.NewString(),
			LegalArea:           "sgb_xi",
			ValidFrom:           now,
		})
		return err
	}); err != nil {
		t.Fatalf("chef rule write: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("chef rule was not created")
	}
}

func chefActor(seed devseed.Result) patients.Actor {
	return patients.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
}

func assignmentsActor(seed devseed.Result) assignments.Actor {
	return assignments.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
}

func assignmentsInput(employeeID uuid.UUID, validFrom time.Time, validUntil *time.Time) assignments.CreateInput {
	return assignments.CreateInput{
		EmployeeID:     employeeID,
		AssignmentType: "substitute",
		ValidFrom:      validFrom,
		ValidUntil:     validUntil,
	}
}
