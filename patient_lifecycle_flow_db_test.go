package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type patientDetailResponse struct {
	ID        uuid.UUID `json:"id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Status    string    `json:"status"`
}

// TestPatientLifecycleHTTPFlow covers PATCH, the protected search endpoint and
// the archived-patient visibility rules end to end.
func TestPatientLifecycleHTTPFlow(t *testing.T) {
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

	t.Run("chef rename updates the search index and old name disappears", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "Muster")

		if found := searchPatientsViaAPI(t, handler, chefLogin, "Muster"); len(found) != 1 || found[0].ID != patientID {
			t.Fatalf("search before rename = %+v, want %s", found, patientID)
		}

		rec := doRequest(handler, http.MethodPatch, "/api/patients/"+patientID.String(), `{"last_name":"Schmidt"}`, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var updated patientDetailResponse
		decodeJSON(t, rec, &updated)
		if updated.LastName != "Schmidt" {
			t.Errorf("last name = %q, want Schmidt", updated.LastName)
		}
		if updated.FirstName != "Anna" {
			t.Errorf("first name = %q, want the unchanged Anna", updated.FirstName)
		}

		if found := searchPatientsViaAPI(t, handler, chefLogin, "Muster"); len(found) != 0 {
			t.Fatalf("old name still matched %d patients", len(found))
		}
		if found := searchPatientsViaAPI(t, handler, chefLogin, "Schmidt"); len(found) != 1 || found[0].ID != patientID {
			t.Fatalf("new name search = %+v, want %s", found, patientID)
		}

		// Neither the raw search term nor the name may appear in audit metadata.
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
			if strings.Contains(metadata, "Muster") || strings.Contains(metadata, "Schmidt") {
				t.Fatalf("audit metadata leaked a patient name or search term: %s", metadata)
			}
		}
	})

	t.Run("employees cannot update and platform admins cannot search", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")
		admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "Muster")

		employeeLogin := loginAs(t, handler, employee.Email, "secret")
		if rec := doRequest(handler, http.MethodPatch, "/api/patients/"+patientID.String(), `{"last_name":"Hacked"}`, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
			t.Fatalf("employee PATCH status = %d, want 403", rec.Code)
		}

		adminLogin := loginAs(t, handler, admin.Email, "secret")
		if rec := doRequest(handler, http.MethodGet, "/api/patients?q=Anna", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("platform admin search status = %d, want 403", rec.Code)
		}
	})

	t.Run("archived patients disappear for employees but not chefs", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Carla", "Archived")

		body := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
		if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", body, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusCreated {
			t.Fatalf("create assignment status = %d, body = %s", rec.Code, rec.Body.String())
		}

		employeeLogin := loginAs(t, handler, employee.Email, "secret")
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusOK {
			t.Fatalf("employee GET before archive = %d, want 200", rec.Code)
		}

		if rec := doRequest(handler, http.MethodPatch, "/api/patients/"+patientID.String(), `{"status":"archived"}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusOK {
			t.Fatalf("archive status = %d, body = %s", rec.Code, rec.Body.String())
		}

		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("employee GET archived patient = %d, want 404", rec.Code)
		}
		if list := listPatientsViaAPI(t, handler, employeeLogin); len(list) != 0 {
			t.Fatalf("employee list after archive = %+v, want empty", list)
		}
		if found := searchPatientsViaAPI(t, handler, employeeLogin, "Carla"); len(found) != 0 {
			t.Fatalf("employee search found archived patient: %+v", found)
		}

		// The chef keeps access to the archived record.
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", chefLogin.cookie, ""); rec.Code != http.StatusOK {
			t.Fatalf("chef GET archived patient = %d, want 200", rec.Code)
		}
		if list := listPatientsViaAPI(t, handler, chefLogin); len(list) != 1 {
			t.Fatalf("chef list after archive = %+v, want one patient", list)
		}
		if found := searchPatientsViaAPI(t, handler, chefLogin, "Carla"); len(found) != 1 || found[0].ID != patientID {
			t.Fatalf("chef search for archived patient = %+v, want %s", found, patientID)
		}
	})
}

func searchPatientsViaAPI(t *testing.T, handler http.Handler, login loginResult, query string) []patientAPIResponse {
	t.Helper()

	rec := doRequest(handler, http.MethodGet, "/api/patients?q="+query, "", login.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patients []patientAPIResponse
	decodeJSON(t, rec, &patients)
	return patients
}
