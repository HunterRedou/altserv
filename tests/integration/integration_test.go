// Package integration_test contains cross-package integration tests against a
// real PostgreSQL database. The package unit tests stay next to the code they
// cover; these suites prove that the packages work together and that tenant
// isolation holds across package boundaries.
package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/HunterRedou/altserv/tests/testutil"
	"github.com/google/uuid"
)

func TestSeededDatasetWorksAcrossPackages(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)

	chef := patients.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	employee := patients.Actor{UserID: seed.EmployeeID, TenantID: seed.TenantID, Role: "employee"}
	catalogActor := catalog.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	templateActor := templates.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	visitActor := visits.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}

	err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		patient, err := svc.Patients.Get(ctx, q, chef, "it", seed.PatientIDs[0])
		if err != nil {
			t.Fatalf("chef get seeded patient: %v", err)
		}
		if patient.FirstName != "Demo" || patient.LastName != "Patientin Eins" {
			t.Errorf("patient name = %q %q, want the decrypted seed name", patient.FirstName, patient.LastName)
		}

		assigned, err := svc.Assignments.CanAccessPatient(ctx, q, seed.TenantID, seed.EmployeeID, seed.PatientIDs[0])
		if err != nil {
			t.Fatalf("check assignment: %v", err)
		}
		if !assigned {
			t.Error("employee is not assigned to the seeded patient")
		}

		list, err := svc.Patients.List(ctx, q, employee, "it")
		if err != nil {
			t.Fatalf("employee list patients: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("employee sees %d patients, want 2", len(list))
		}

		catalogs, err := svc.Catalog.ListCatalogs(ctx, q, catalogActor, false)
		if err != nil {
			t.Fatalf("list catalogs: %v", err)
		}
		foundCatalog := false
		for _, item := range catalogs {
			if item.ID == seed.CatalogID {
				foundCatalog = true
			}
		}
		if !foundCatalog {
			t.Error("seeded catalog is missing from the global catalog list")
		}

		visit, err := svc.Visits.Get(ctx, q, visitActor, "it", seed.VisitID)
		if err != nil {
			t.Fatalf("get seeded visit: %v", err)
		}
		if visit.PatientID != seed.PatientIDs[0] || visit.EmployeeID != seed.EmployeeID {
			t.Errorf("visit = %+v, want the seeded patient and employee", visit)
		}

		template, err := svc.Templates.Get(ctx, q, templateActor, "it", seed.TemplateID)
		if err != nil {
			t.Fatalf("get seeded template: %v", err)
		}
		if template.PatientID != seed.PatientIDs[0] || len(template.Activities) != 2 {
			t.Errorf("template = %+v, want the seeded patient and two activities", template)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tenant A: %v", err)
	}
}

func TestTenantIsolationAcrossPackages(t *testing.T) {
	conn := testutil.OpenDB(t)
	tx := testutil.BeginTx(t, conn)
	ctx := context.Background()

	opts, seed := testutil.SeedDemo(t, tx)
	svc := testutil.NewServices(t, opts)

	tenantB, chefB := testutil.CreateTenantWithUser(t, tx, "chef")
	chefBActor := patients.Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}
	catalogBActor := catalog.Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}

	err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
		_, err := svc.Patients.Get(ctx, q, chefBActor, "it", seed.PatientIDs[0])
		if !errors.Is(err, patients.ErrNotFound) {
			t.Fatalf("cross-tenant patient get err = %v, want ErrNotFound", err)
		}

		var visible int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM patients`).Scan(&visible); err != nil {
			t.Fatalf("count visible patients: %v", err)
		}
		if visible != 0 {
			t.Errorf("tenant B sees %d patients under RLS, want 0", visible)
		}

		// Global reference data stays shared; tenant-bound data does not.
		catalogs, err := svc.Catalog.ListCatalogs(ctx, q, catalogBActor, false)
		if err != nil {
			t.Fatalf("tenant B list catalogs: %v", err)
		}
		found := false
		for _, item := range catalogs {
			if item.ID == seed.CatalogID {
				found = true
			}
		}
		if !found {
			t.Error("tenant B cannot read the global catalog, want shared reference data")
		}

		contracts, err := svc.Catalog.ListContracts(ctx, q, catalogBActor)
		if err != nil {
			t.Fatalf("tenant B list contracts: %v", err)
		}
		if len(contracts) != 0 {
			t.Errorf("tenant B sees %d tenant A contracts, want 0", len(contracts))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tenant B: %v", err)
	}

	chefA := patients.Actor{UserID: seed.ChefID, TenantID: seed.TenantID, Role: "chef"}
	if err := db.WithTenantTx(ctx, tx, seed.TenantID, func(q *db.Queries) error {
		if _, err := svc.Patients.Get(ctx, q, chefA, "it", seed.PatientIDs[0]); err != nil {
			t.Fatalf("tenant A lost access to its own patient: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("tenant A: %v", err)
	}

	if seed.TenantID == tenantB.ID || seed.TenantID == uuid.Nil {
		t.Fatal("fixture did not create two distinct tenants")
	}
}
