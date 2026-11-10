package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type definitionAPIResponse struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	LegalArea string    `json:"legal_area"`
}

type contractAPIResponse struct {
	ID                  uuid.UUID `json:"id"`
	ServiceDefinitionID uuid.UUID `json:"service_definition_id"`
	PayerName           string    `json:"payer_name"`
	LegalArea           string    `json:"legal_area"`
}

type activityAPIResponse struct {
	ID                  uuid.UUID  `json:"id"`
	PatientID           uuid.UUID  `json:"patient_id"`
	EmployeeID          uuid.UUID  `json:"employee_id"`
	ServiceDefinitionID *uuid.UUID `json:"service_definition_id"`
	ContractID          *uuid.UUID `json:"contract_id"`
	DurationMinutes     int        `json:"duration_minutes"`
	Note                string     `json:"note"`
	ServiceCode         string     `json:"service_code"`
	ServiceName         string     `json:"service_name"`
	LegalArea           string     `json:"legal_area"`
	PayerName           string     `json:"payer_name"`
}

// TestCatalogActivityHTTPFlow covers the Step 8 endpoints end to end: service
// search, chef-only contract creation, activity recording with an encrypted
// note, assignment enforcement and platform-admin denial.
func TestCatalogActivityHTTPFlow(t *testing.T) {
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

	t.Run("chef manages contracts and records a billable activity", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")
		admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

		definitionID := seedHTTPDefinition(t, tx, "LK-001", "Grundpflege", "sgb_xi")
		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "Muster")

		// The service search works for the chef and returns the seeded service.
		rec := doRequest(handler, http.MethodGet, "/api/service-definitions?q=Grund", "", chefLogin.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var definitions []definitionAPIResponse
		decodeJSON(t, rec, &definitions)
		if len(definitions) != 1 || definitions[0].ID != definitionID {
			t.Fatalf("definitions = %+v, want the seeded definition", definitions)
		}

		// Create a contract version.
		validFrom := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"service_definition_id":%q,"payer_name":"AOK Brandenburg","contract_reference":"V-2026-1","legal_area":"sgb_xi","valid_from":%q}`, definitionID, validFrom)
		rec = doRequest(handler, http.MethodPost, "/api/service-contracts", body, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create contract status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var contract contractAPIResponse
		decodeJSON(t, rec, &contract)
		if contract.ServiceDefinitionID != definitionID || contract.LegalArea != "sgb_xi" {
			t.Fatalf("contract = %+v", contract)
		}

		// The chef assigns the patient and records an activity for the employee.
		assignBody := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
		rec = doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", assignBody, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create assignment status = %d, body = %s", rec.Code, rec.Body.String())
		}

		performedAt := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
		activityBody := fmt.Sprintf(
			`{"employee_id":%q,"service_definition_id":%q,"contract_id":%q,"performed_at":%q,"duration_minutes":30,"note":"Hilfe beim Anziehen"}`,
			employee.ID, definitionID, contract.ID, performedAt,
		)
		rec = doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/activities", activityBody, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create activity status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var activity activityAPIResponse
		decodeJSON(t, rec, &activity)
		if activity.ServiceCode != "LK-001" || activity.ServiceName != "Grundpflege" || activity.PayerName != "AOK Brandenburg" {
			t.Fatalf("activity snapshot = %+v", activity)
		}
		if activity.Note != "Hilfe beim Anziehen" {
			t.Errorf("note = %q, want the original note", activity.Note)
		}

		// Rename the definition. The historical activity keeps its snapshot.
		if _, err := tx.Exec("UPDATE service_definitions SET name = 'Umbenannt' WHERE id = $1", definitionID); err != nil {
			t.Fatalf("rename definition: %v", err)
		}
		rec = doRequest(handler, http.MethodGet, "/api/activities/"+activity.ID.String(), "", chefLogin.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get activity status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var reloaded activityAPIResponse
		decodeJSON(t, rec, &reloaded)
		if reloaded.ServiceName != "Grundpflege" {
			t.Errorf("snapshot name = %q, want the historical name", reloaded.ServiceName)
		}

		// The employee can read their assigned patient's activities.
		employeeLogin := loginAs(t, handler, employee.Email, "secret")
		rec = doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String()+"/activities", "", employeeLogin.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("employee activity list status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var activities []activityAPIResponse
		decodeJSON(t, rec, &activities)
		if len(activities) != 1 || activities[0].ID != activity.ID {
			t.Fatalf("employee activities = %+v, want the recorded activity", activities)
		}

		// The employee cannot list every tenant activity.
		if rec := doRequest(handler, http.MethodGet, "/api/activities", "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("employee list-all status = %d, want 403", rec.Code)
		}

		// A platform admin gets no automatic access to activities or catalogs.
		adminLogin := loginAs(t, handler, admin.Email, "secret")
		if rec := doRequest(handler, http.MethodGet, "/api/activities", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("admin list activities status = %d, want 403", rec.Code)
		}
		if rec := doRequest(handler, http.MethodGet, "/api/service-catalogs", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("admin list catalogs status = %d, want 403", rec.Code)
		}
	})

	t.Run("employee cannot record an activity for an unassigned patient", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Berta", "Beispiel")

		employeeLogin := loginAs(t, handler, employee.Email, "secret")
		body := fmt.Sprintf(`{"performed_at":%q,"duration_minutes":15}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
		rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/activities", body, employeeLogin.cookie, employeeLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("unassigned activity status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("employees cannot create contracts", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")
		definitionID := seedHTTPDefinition(t, tx, "LK-002", "Pflege", "sgb_xi")

		employeeLogin := loginAs(t, handler, employee.Email, "secret")
		body := fmt.Sprintf(`{"service_definition_id":%q,"payer_name":"AOK","legal_area":"sgb_xi","valid_from":%q}`, definitionID, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
		rec := doRequest(handler, http.MethodPost, "/api/service-contracts", body, employeeLogin.cookie, employeeLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("employee contract status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func seedHTTPDefinition(t *testing.T, tx *sql.Tx, code, name, legalArea string) uuid.UUID {
	t.Helper()

	var catalogID uuid.UUID
	if err := tx.QueryRow(
		"INSERT INTO service_catalogs (code, name, region, source_note, valid_from) VALUES ($1, $2, 'brandenburg', 'test', $3) RETURNING id",
		"BRB-"+uuid.NewString(), "Brandenburg Testkatalog", time.Now().Add(-72*time.Hour),
	).Scan(&catalogID); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	var definitionID uuid.UUID
	if err := tx.QueryRow(
		`INSERT INTO service_definitions (catalog_id, code, name, description, legal_area, unit, valid_from)
		 VALUES ($1, $2, $3, '', $4, 'Stunde', $5) RETURNING id`,
		catalogID, code, name, legalArea, time.Now().Add(-48*time.Hour),
	).Scan(&definitionID); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	return definitionID
}

type ruleAPIResponse struct {
	ID                  uuid.UUID       `json:"id"`
	TenantID            uuid.UUID       `json:"tenant_id"`
	ServiceDefinitionID uuid.UUID       `json:"service_definition_id"`
	ContractID          *uuid.UUID      `json:"contract_id"`
	ActivityLabel       string          `json:"activity_label"`
	LegalArea           string          `json:"legal_area"`
	Criteria            json.RawMessage `json:"criteria"`
}

// TestActivityRuleHTTPFlow covers the chef-only activity-rule endpoints and the
// platform-admin denial.
func TestActivityRuleHTTPFlow(t *testing.T) {
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

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	validFrom := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"service_definition_id":%q,"activity_label":"Grundpflege","legal_area":"sgb_xi","criteria":{"min_duration":15},"valid_from":%q}`, definitionID, validFrom)

	rec := doRequest(handler, http.MethodPost, "/api/activity-rules", body, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("chef create rule status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rule ruleAPIResponse
	decodeJSON(t, rec, &rule)
	if rule.ServiceDefinitionID != definitionID || rule.ActivityLabel != "Grundpflege" {
		t.Fatalf("rule = %+v", rule)
	}
	if string(rule.Criteria) != `{"min_duration":15}` {
		t.Errorf("criteria = %s, want the stored JSON", rule.Criteria)
	}

	rec = doRequest(handler, http.MethodGet, "/api/activity-rules", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list rules status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rules []ruleAPIResponse
	decodeJSON(t, rec, &rules)
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules = %+v, want the created rule", rules)
	}

	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	rec = doRequest(handler, http.MethodPost, "/api/activity-rules", body, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("employee create rule status = %d, want 403", rec.Code)
	}

	adminLogin := loginAs(t, handler, admin.Email, "secret")
	rec = doRequest(handler, http.MethodGet, "/api/activity-rules", "", adminLogin.cookie, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin list rules status = %d, want 403", rec.Code)
	}
}
