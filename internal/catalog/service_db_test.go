package catalog

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

// TestCatalogServiceDatabase covers the global reference search and the
// tenant-bound contract lifecycle. It is skipped unless TEST_DB_URL is set and
// migration 016 is applied. Every subtest rolls back its transaction.
func TestCatalogServiceDatabase(t *testing.T) {
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

	service := NewService(NewRepository(), audit.SQLRecorder{})
	ctx := context.Background()

	t.Run("ILIKE search ranks code and name matches", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedCatalogTenantAndUser(t, queries)
		catalogID := seedCatalog(t, tx, "BRB-2026")
		xi := seedDefinition(t, tx, catalogID, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-24*time.Hour), nil)
		v := seedDefinition(t, tx, catalogID, "LK-900", "Hauswirtschaftliche Versorgung", "sgb_v", time.Now().Add(-24*time.Hour), nil)
		seedDefinition(t, tx, catalogID, "LK-002", "Pflege Grundkurs", "sgb_xi", time.Now().Add(-24*time.Hour), nil)

		var results []Definition
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			results, err = service.SearchDefinitions(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, DefinitionFilter{Query: "LK-001"})
			return err
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 1 || results[0].ID != xi.ID {
			t.Fatalf("code search = %+v, want only %s", results, xi.ID)
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			results, err = service.SearchDefinitions(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, DefinitionFilter{Query: "Grund"})
			return err
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("name search returned %d definitions, want 2", len(results))
		}

		// SGB V must be excluded by the legal-area filter.
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			results, err = service.SearchDefinitions(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, DefinitionFilter{LegalArea: "sgb_v"})
			return err
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 1 || results[0].ID != v.ID {
			t.Fatalf("SGB V search = %+v, want only %s", results, v.ID)
		}
	})

	t.Run("expired and future definitions are hidden", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedCatalogTenantAndUser(t, queries)
		catalogID := seedCatalog(t, tx, "BRB-2026")
		expired := time.Now().Add(-time.Hour)
		seedDefinition(t, tx, catalogID, "LK-EXP", "Alte Leistung", "sgb_xi", time.Now().Add(-48*time.Hour), &expired)
		seedDefinition(t, tx, catalogID, "LK-FUT", "Zukuenftige Leistung", "sgb_xi", time.Now().Add(48*time.Hour), nil)
		active := seedDefinition(t, tx, catalogID, "LK-ACT", "Aktuelle Leistung", "sgb_xi", time.Now().Add(-time.Hour), nil)

		var results []Definition
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			results, err = service.SearchDefinitions(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, DefinitionFilter{})
			return err
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 1 || results[0].ID != active.ID {
			t.Fatalf("active search = %+v, want only %s", results, active.ID)
		}
	})

	t.Run("contracts are tenant scoped and versioned", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedCatalogTenantAndUser(t, queries)
		tenantB, chefB := seedCatalogTenantAndUser(t, queries)
		catalogID := seedCatalog(t, tx, "BRB-2026")
		definition := seedDefinition(t, tx, catalogID, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-24*time.Hour), nil)
		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)

		var contract Contract
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			contract, err = service.CreateContract(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "req", CreateContractInput{
				ServiceDefinitionID: definition.ID,
				PayerName:           "AOK Brandenburg",
				ContractReference:   "V-2026-1",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		}); err != nil {
			t.Fatalf("create contract: %v", err)
		}
		if contract.TenantID != tenantA.ID {
			t.Errorf("contract tenant = %s, want %s", contract.TenantID, tenantA.ID)
		}

		// Tenant B must not see tenant A's contract.
		var err error
		err = db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			_, err = service.GetContract(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, contract.ID)
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant GetContract err = %v, want ErrNotFound", err)
		}

		// The same version cannot be inserted twice.
		err = db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			_, err = service.CreateContract(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "req", CreateContractInput{
				ServiceDefinitionID: definition.ID,
				PayerName:           "AOK Brandenburg",
				ContractReference:   "V-2026-2",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate contract err = %v, want ErrConflict", err)
		}
	})
}

func newCatalogTx(t *testing.T, conn *sql.DB) (*sql.Tx, *db.Queries) {
	t.Helper()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, db.New(tx)
}

// TestCatalogAdministrationDatabase covers the reviewed SECURITY DEFINER write
// path for global catalogs: platform-admin-only access, provenance, catalog
// window containment, duplicate versions and the audit event. It is skipped
// unless TEST_DB_URL is set.
func TestCatalogAdministrationDatabase(t *testing.T) {
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

	service := NewService(NewRepository(), audit.SQLRecorder{})
	ctx := context.Background()

	t.Run("platform admin creates a catalog and a definition", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, admin := seedPlatformAdmin(t, queries)
		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)
		validUntil := validFrom.AddDate(1, 0, 0)

		var catalog Catalog
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			catalog, err = service.CreateCatalog(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req-cat", CreateCatalogInput{
				Code:       "BRB-2026",
				Name:       "Brandenburger Leistungskatalog",
				Region:     "brandenburg",
				SourceNote: "Fachlich geprueft, Quelle: Test",
				ValidFrom:  validFrom,
				ValidUntil: &validUntil,
			})
			return err
		}); err != nil {
			t.Fatalf("create catalog: %v", err)
		}
		if catalog.ID == uuid.Nil || catalog.Code != "BRB-2026" {
			t.Fatalf("catalog = %+v, want persisted row", catalog)
		}

		var definition Definition
		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			var err error
			definition, err = service.CreateDefinition(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req-def", CreateDefinitionInput{
				CatalogID:  catalog.ID,
				Code:       "LK-001",
				Name:       "Grundpflege",
				LegalArea:  "sgb_xi",
				Unit:       "Einsatz",
				ValidFrom:  validFrom,
				ValidUntil: &validUntil,
			})
			return err
		}); err != nil {
			t.Fatalf("create definition: %v", err)
		}
		if definition.CatalogID != catalog.ID || definition.Code != "LK-001" {
			t.Fatalf("definition = %+v, want persisted row", definition)
		}

		var count int
		if err := tx.QueryRow(
			`SELECT count(*) FROM audit_events
			 WHERE tenant_id = $1 AND action IN ('service_catalog.created', 'service_definition.created')`,
			tenant.ID,
		).Scan(&count); err != nil {
			t.Fatalf("count audit events: %v", err)
		}
		if count != 2 {
			t.Errorf("audit events = %d, want 2", count)
		}
	})

	t.Run("tenant roles cannot create global catalogs", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, chef := seedCatalogTenantAndUser(t, queries)
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.CreateCatalog(ctx, q, Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}, "req", CreateCatalogInput{
				Code:       "BRB-2026",
				Name:       "Katalog",
				SourceNote: "Quelle",
				ValidFrom:  time.Now().Add(-time.Hour),
			})
			return err
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}

		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM service_catalogs WHERE code = 'BRB-2026'`).Scan(&count); err != nil {
			t.Fatalf("count catalogs: %v", err)
		}
		if count != 0 {
			t.Errorf("catalog rows = %d, want 0", count)
		}
	})

	t.Run("duplicate catalog version conflicts", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, admin := seedPlatformAdmin(t, queries)
		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)
		input := CreateCatalogInput{
			Code:       "BRB-2026",
			Name:       "Katalog",
			SourceNote: "Quelle",
			ValidFrom:  validFrom,
		}

		if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.CreateCatalog(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req", input)
			return err
		}); err != nil {
			t.Fatalf("first create: %v", err)
		}

		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.CreateCatalog(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req", input)
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate err = %v, want ErrConflict", err)
		}
	})

	t.Run("catalog requires a source note", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, admin := seedPlatformAdmin(t, queries)
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.CreateCatalog(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req", CreateCatalogInput{
				Code:      "BRB-2026",
				Name:      "Katalog",
				ValidFrom: time.Now().Add(-time.Hour),
			})
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("definition must stay inside the catalog window", func(t *testing.T) {
		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)
		validUntil := validFrom.AddDate(1, 0, 0)

		t.Run("open-ended definition is rejected", func(t *testing.T) {
			tx, queries := newCatalogTx(t, conn)
			defer tx.Rollback()

			tenant, admin := seedPlatformAdmin(t, queries)
			actor := Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}

			var catalog Catalog
			if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				var err error
				catalog, err = service.CreateCatalog(ctx, q, actor, "req", CreateCatalogInput{
					Code:       "BRB-2026",
					Name:       "Katalog",
					SourceNote: "Quelle",
					ValidFrom:  validFrom,
					ValidUntil: &validUntil,
				})
				return err
			}); err != nil {
				t.Fatalf("create catalog: %v", err)
			}

			// A definition without an end date is open-ended and would outlive
			// the catalog, so it must be rejected.
			err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				_, err := service.CreateDefinition(ctx, q, actor, "req", CreateDefinitionInput{
					CatalogID: catalog.ID,
					Code:      "LK-001",
					Name:      "Grundpflege",
					LegalArea: "sgb_xi",
					ValidFrom: validFrom,
				})
				return err
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("open-ended definition err = %v, want ErrInvalid", err)
			}
		})

		t.Run("definition before the catalog is rejected", func(t *testing.T) {
			tx, queries := newCatalogTx(t, conn)
			defer tx.Rollback()

			tenant, admin := seedPlatformAdmin(t, queries)
			actor := Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}

			var catalog Catalog
			if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				var err error
				catalog, err = service.CreateCatalog(ctx, q, actor, "req", CreateCatalogInput{
					Code:       "BRB-2026",
					Name:       "Katalog",
					SourceNote: "Quelle",
					ValidFrom:  validFrom,
					ValidUntil: &validUntil,
				})
				return err
			}); err != nil {
				t.Fatalf("create catalog: %v", err)
			}

			err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
				_, err := service.CreateDefinition(ctx, q, actor, "req", CreateDefinitionInput{
					CatalogID:  catalog.ID,
					Code:       "LK-001",
					Name:       "Grundpflege",
					LegalArea:  "sgb_xi",
					ValidFrom:  validFrom.Add(-time.Hour),
					ValidUntil: &validUntil,
				})
				return err
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("early definition err = %v, want ErrInvalid", err)
			}
		})
	})

	t.Run("definition needs an existing catalog", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenant, admin := seedPlatformAdmin(t, queries)
		err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
			_, err := service.CreateDefinition(ctx, q, Actor{UserID: admin.ID, TenantID: tenant.ID, Role: "platform_admin"}, "req", CreateDefinitionInput{
				CatalogID: uuid.New(),
				Code:      "LK-001",
				Name:      "Grundpflege",
				LegalArea: "sgb_xi",
				ValidFrom: time.Now().Add(-time.Hour),
			})
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func seedPlatformAdmin(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "admin-" + uuid.NewString() + "@example.com",
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
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:           "platform admin",
		Role:           "platform_admin",
		HashedPassword: "hash",
		Email:          "admin-" + uuid.NewString() + "@example.com",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create platform admin: %v", err)
	}
	return tenant, user
}

func seedCatalogTenantAndUser(t *testing.T, queries *db.Queries) (db.Tenant, db.User) {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "catalog-" + uuid.NewString() + "@example.com",
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
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:           "chef",
		Role:           "chef",
		HashedPassword: "hash",
		Email:          "chef-" + uuid.NewString() + "@example.com",
		TenantID:       tenant.ID,
	})
	if err != nil {
		t.Fatalf("create chef: %v", err)
	}
	return tenant, user
}

func seedCatalog(t *testing.T, tx *sql.Tx, code string) uuid.UUID {
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

func seedDefinition(t *testing.T, tx *sql.Tx, catalogID uuid.UUID, code, name, legalArea string, validFrom time.Time, validUntil *time.Time) Definition {
	t.Helper()

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
	return Definition{ID: id, CatalogID: catalogID, Code: code, Name: name, LegalArea: legalArea, ValidFrom: validFrom}
}

// TestActivityRuleDatabase covers chef-only rule creation, tenant isolation and
// the duplicate-version conflict. It is skipped unless TEST_DB_URL is set.
func TestActivityRuleDatabase(t *testing.T) {
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

	service := NewService(NewRepository(), audit.SQLRecorder{})
	ctx := context.Background()

	t.Run("chef creates a rule scoped to the tenant", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedCatalogTenantAndUser(t, queries)
		tenantB, chefB := seedCatalogTenantAndUser(t, queries)
		catalogID := seedCatalog(t, tx, "BRB-2026")
		definition := seedDefinition(t, tx, catalogID, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-24*time.Hour), nil)

		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)
		var rule Rule
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			rule, err = service.CreateRule(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "req", CreateRuleInput{
				ServiceDefinitionID: definition.ID,
				ActivityLabel:       "Grundpflege",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		}); err != nil {
			t.Fatalf("create rule: %v", err)
		}
		if rule.TenantID != tenantA.ID {
			t.Errorf("rule tenant = %s, want %s", rule.TenantID, tenantA.ID)
		}

		// Tenant B must not see tenant A's rule.
		var rulesB []Rule
		if err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			var err error
			rulesB, err = service.ListRules(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"})
			return err
		}); err != nil {
			t.Fatalf("list tenant B rules: %v", err)
		}
		if len(rulesB) != 0 {
			t.Fatalf("tenant B rules = %+v, want none", rulesB)
		}

		// The same version cannot be inserted twice.
		err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			_, err := service.CreateRule(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "req", CreateRuleInput{
				ServiceDefinitionID: definition.ID,
				ActivityLabel:       "Grundpflege",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate rule err = %v, want ErrConflict", err)
		}
	})

	t.Run("contract must belong to the same tenant", func(t *testing.T) {
		tx, queries := newCatalogTx(t, conn)
		defer tx.Rollback()

		tenantA, chefA := seedCatalogTenantAndUser(t, queries)
		tenantB, chefB := seedCatalogTenantAndUser(t, queries)
		catalogID := seedCatalog(t, tx, "BRB-2026")
		definition := seedDefinition(t, tx, catalogID, "LK-001", "Grundpflege", "sgb_xi", time.Now().Add(-24*time.Hour), nil)

		validFrom := time.Now().Add(-time.Hour).Truncate(time.Second)
		var contract Contract
		if err := db.WithTenantTx(ctx, tx, tenantA.ID, func(q *db.Queries) error {
			var err error
			contract, err = service.CreateContract(ctx, q, Actor{UserID: chefA.ID, TenantID: tenantA.ID, Role: "chef"}, "req", CreateContractInput{
				ServiceDefinitionID: definition.ID,
				PayerName:           "AOK Brandenburg",
				ContractReference:   "V-2026-1",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		}); err != nil {
			t.Fatalf("create contract: %v", err)
		}

		err := db.WithTenantTx(ctx, tx, tenantB.ID, func(q *db.Queries) error {
			_, err := service.CreateRule(ctx, q, Actor{UserID: chefB.ID, TenantID: tenantB.ID, Role: "chef"}, "req", CreateRuleInput{
				ServiceDefinitionID: definition.ID,
				ContractID:          &contract.ID,
				ActivityLabel:       "Grundpflege",
				LegalArea:           "sgb_xi",
				ValidFrom:           validFrom,
			})
			return err
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant contract err = %v, want ErrNotFound", err)
		}
	})
}
