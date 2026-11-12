package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type catalogAdminAPIResponse struct {
	ID         uuid.UUID `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Region     string    `json:"region"`
	SourceNote string    `json:"source_note"`
	ValidFrom  time.Time `json:"valid_from"`
}

type definitionAdminAPIResponse struct {
	ID        uuid.UUID `json:"id"`
	CatalogID uuid.UUID `json:"catalog_id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	LegalArea string    `json:"legal_area"`
}

// TestCatalogAdminHTTPFlow covers the catalog data path end to end: a platform
// admin creates a global catalog and a billable service, tenant roles can read
// them, and only a platform admin may write. It is skipped unless TEST_DB_URL is
// set.
func TestCatalogAdminHTTPFlow(t *testing.T) {
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

	handler, queries, tx := newSessionTestAPI(t, conn)
	defer tx.Rollback()

	tenant := createTenant(t, queries, "valid")
	chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
	employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")
	admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

	adminLogin := loginAs(t, handler, admin.Email, "secret")
	chefLogin := loginAs(t, handler, chef.Email, "secret")
	employeeLogin := loginAs(t, handler, employee.Email, "secret")

	validFrom := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	validUntil := validFrom.AddDate(1, 0, 0)
	catalogBody := fmt.Sprintf(
		`{"code":"BRB-2026","name":"Brandenburger Leistungskatalog","region":"brandenburg","source_note":"Fachlich geprueft, Quelle: Test","valid_from":%q,"valid_until":%q}`,
		validFrom.Format(time.RFC3339), validUntil.Format(time.RFC3339),
	)

	rec := doRequest(handler, http.MethodPost, "/api/service-catalogs", catalogBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create catalog status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var catalog catalogAdminAPIResponse
	decodeJSON(t, rec, &catalog)
	if catalog.ID == uuid.Nil || catalog.Code != "BRB-2026" || catalog.SourceNote == "" {
		t.Fatalf("catalog = %+v, want persisted catalog", catalog)
	}

	definitionBody := fmt.Sprintf(
		`{"catalog_id":%q,"code":"LK-001","name":"Grundpflege","description":"","legal_area":"sgb_xi","unit":"Einsatz","valid_from":%q,"valid_until":%q}`,
		catalog.ID, validFrom.Format(time.RFC3339), validUntil.Format(time.RFC3339),
	)
	rec = doRequest(handler, http.MethodPost, "/api/service-definitions", definitionBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create definition status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var definition definitionAdminAPIResponse
	decodeJSON(t, rec, &definition)
	if definition.ID == uuid.Nil || definition.CatalogID != catalog.ID || definition.Code != "LK-001" {
		t.Fatalf("definition = %+v, want persisted definition", definition)
	}

	// A chef may read the global catalog and its services.
	rec = doRequest(handler, http.MethodGet, "/api/service-catalogs", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list catalogs status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var catalogs []catalogAdminAPIResponse
	decodeJSON(t, rec, &catalogs)
	if len(catalogs) != 1 || catalogs[0].ID != catalog.ID {
		t.Fatalf("catalogs = %+v, want the created catalog", catalogs)
	}

	rec = doRequest(handler, http.MethodGet, "/api/service-definitions?catalog_id="+catalog.ID.String()+"&q=Grund", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef search definitions status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var definitions []definitionAdminAPIResponse
	decodeJSON(t, rec, &definitions)
	if len(definitions) != 1 || definitions[0].ID != definition.ID {
		t.Fatalf("definitions = %+v, want the created definition", definitions)
	}

	// A platform admin may also read, but only global data.
	rec = doRequest(handler, http.MethodGet, "/api/service-catalogs", "", adminLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list catalogs status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Tenant roles must not write global reference data.
	for name, login := range map[string]loginResult{
		"chef":     chefLogin,
		"employee": employeeLogin,
	} {
		t.Run("create_catalog_denied_"+name, func(t *testing.T) {
			rec := doRequest(handler, http.MethodPost, "/api/service-catalogs", catalogBody, login.cookie, login.csrf)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
			}
		})
		t.Run("create_definition_denied_"+name, func(t *testing.T) {
			rec := doRequest(handler, http.MethodPost, "/api/service-definitions", definitionBody, login.cookie, login.csrf)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Validation errors are rejected before they reach the database.
	noSourceNote := fmt.Sprintf(
		`{"code":"BRB-2027","name":"Katalog","source_note":"","valid_from":%q}`,
		validFrom.Format(time.RFC3339),
	)
	if rec := doRequest(handler, http.MethodPost, "/api/service-catalogs", noSourceNote, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing source note status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	if rec := doRequest(handler, http.MethodPost, "/api/service-definitions", `{"catalog_id":"not-a-uuid","code":"LK","name":"Pflege","legal_area":"sgb_xi","valid_from":"2026-01-01T00:00:00Z"}`, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad catalog id status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	// This one reaches the database and aborts the test transaction, so it runs
	// last.
	badWindow := fmt.Sprintf(
		`{"catalog_id":%q,"code":"LK-002","name":"Pflege","legal_area":"sgb_xi","valid_from":%q}`,
		catalog.ID, validFrom.Add(-time.Hour).Format(time.RFC3339),
	)
	if rec := doRequest(handler, http.MethodPost, "/api/service-definitions", badWindow, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("definition before catalog status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestCatalogAdminHTTPDuplicate checks the duplicate-version conflict on its own
// transaction because a unique violation aborts the test transaction.
func TestCatalogAdminHTTPDuplicate(t *testing.T) {
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

	handler, queries, tx := newSessionTestAPI(t, conn)
	defer tx.Rollback()

	tenant := createTenant(t, queries, "valid")
	admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	validFrom := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	body := fmt.Sprintf(
		`{"code":"BRB-2026","name":"Katalog","source_note":"Quelle","valid_from":%q}`,
		validFrom.Format(time.RFC3339),
	)

	if rec := doRequest(handler, http.MethodPost, "/api/service-catalogs", body, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPost, "/api/service-catalogs", body, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestCatalogAdminHTTPUnknownCatalog checks the not-found mapping on its own
// transaction because the database error aborts the test transaction.
func TestCatalogAdminHTTPUnknownCatalog(t *testing.T) {
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

	handler, queries, tx := newSessionTestAPI(t, conn)
	defer tx.Rollback()

	tenant := createTenant(t, queries, "valid")
	admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	body := fmt.Sprintf(
		`{"catalog_id":%q,"code":"LK-001","name":"Pflege","legal_area":"sgb_xi","valid_from":%q}`,
		uuid.New(), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	)
	if rec := doRequest(handler, http.MethodPost, "/api/service-definitions", body, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown catalog status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}
