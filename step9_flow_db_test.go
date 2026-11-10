package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type templateActivityAPIResponse struct {
	ID              uuid.UUID `json:"id"`
	ActivityID      uuid.UUID `json:"activity_id"`
	DefaultQuantity int       `json:"default_quantity"`
	SortOrder       int       `json:"sort_order"`
	ServiceCode     string    `json:"service_code"`
	ServiceName     string    `json:"service_name"`
	LegalArea       string    `json:"legal_area"`
	Unit            string    `json:"unit"`
}

type templateAPIResponse struct {
	ID         uuid.UUID                     `json:"id"`
	TenantID   uuid.UUID                     `json:"tenant_id"`
	PatientID  uuid.UUID                     `json:"patient_id"`
	Name       string                        `json:"name"`
	CreatedBy  uuid.UUID                     `json:"created_by"`
	Version    int                           `json:"version"`
	Status     string                        `json:"status"`
	Activities []templateActivityAPIResponse `json:"activities"`
}

// TestTemplateHTTPFlow covers the chef and assigned-employee template lifecycle
// and the platform-admin denial.
func TestTemplateHTTPFlow(t *testing.T) {
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
	definitionID := seedHTTPDefinition(t, tx, "LK-001", "Grundpflege", "sgb_xi")
	secondDefinitionID := seedHTTPDefinition(t, tx, "LK-002", "Koerperpflege", "sgb_xi")

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")
	templatesPath := "/api/patients/" + patientID.String() + "/templates"

	body := fmt.Sprintf(`{"name":"Morgenpflege","activities":[{"activity_id":%q,"default_quantity":1,"sort_order":0},{"activity_id":%q,"default_quantity":2,"sort_order":1}]}`, definitionID, secondDefinitionID)
	rec := doRequest(handler, http.MethodPost, templatesPath, body, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("chef create template status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created templateAPIResponse
	decodeJSON(t, rec, &created)
	if created.Version != 1 || created.Status != "active" || created.PatientID != patientID {
		t.Fatalf("created template = %+v", created)
	}
	if len(created.Activities) != 2 {
		t.Fatalf("activities = %d, want 2", len(created.Activities))
	}
	if created.Activities[0].ServiceCode != "LK-001" || created.Activities[0].ServiceName != "Grundpflege" {
		t.Errorf("first activity = %+v", created.Activities[0])
	}

	rec = doRequest(handler, http.MethodGet, templatesPath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list templates status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed []templateAPIResponse
	decodeJSON(t, rec, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %+v, want the created template", listed)
	}

	templatePath := "/api/templates/" + created.ID.String()
	rec = doRequest(handler, http.MethodGet, templatePath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef get template status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// An unassigned employee is forbidden to write and cannot see the template.
	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	if rec := doRequest(handler, http.MethodPost, templatesPath, body, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("unassigned employee create status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, templatesPath, "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unassigned employee list status = %d, want 404", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, templatePath, "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unassigned employee get status = %d, want 404", rec.Code)
	}

	// The chef assigns the employee; the employee may now read and edit.
	assignBody := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
	if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", assignBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("assign employee status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, templatesPath, "", employeeLogin.cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("assigned employee list status = %d, body = %s", rec.Code, rec.Body.String())
	}

	updateBody := fmt.Sprintf(`{"activities":[{"activity_id":%q,"default_quantity":3,"sort_order":0}]}`, definitionID)
	rec = doRequest(handler, http.MethodPatch, templatePath, updateBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("assigned employee update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated templateAPIResponse
	decodeJSON(t, rec, &updated)
	if updated.Version != 2 || updated.ID == created.ID {
		t.Fatalf("updated = version %d id %s, want a new version 2", updated.Version, updated.ID)
	}
	if len(updated.Activities) != 1 || updated.Activities[0].DefaultQuantity != 3 {
		t.Fatalf("updated activities = %+v", updated.Activities)
	}

	// The old version is still readable but superseded, and cannot be edited.
	rec = doRequest(handler, http.MethodGet, templatePath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef get old version status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var old templateAPIResponse
	decodeJSON(t, rec, &old)
	if old.Status != "superseded" || old.Version != 1 {
		t.Errorf("old version = status %q version %d, want superseded 1", old.Status, old.Version)
	}
	if rec := doRequest(handler, http.MethodPatch, templatePath, updateBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("chef update old version status = %d, want 409", rec.Code)
	}

	// Archiving the active version retires it.
	updatedPath := "/api/templates/" + updated.ID.String()
	if rec := doRequest(handler, http.MethodPost, updatedPath+"/archive", "", chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("chef archive status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, http.MethodGet, templatesPath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list after archive status = %d, body = %s", rec.Code, rec.Body.String())
	}
	listed = nil
	decodeJSON(t, rec, &listed)
	if len(listed) != 0 {
		t.Fatalf("listed = %+v, want none after archive", listed)
	}

	// A platform admin gets no automatic tenant data access.
	adminLogin := loginAs(t, handler, admin.Email, "secret")
	if rec := doRequest(handler, http.MethodGet, templatesPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin list status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, templatePath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin get status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, templatesPath, body, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin create status = %d, want 403", rec.Code)
	}
}
