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

type patientAPIResponse struct {
	ID        uuid.UUID `json:"id"`
	FirstName string    `json:"first_name"`
}

type assignmentAPIResponse struct {
	ID         uuid.UUID `json:"id"`
	PatientID  uuid.UUID `json:"patient_id"`
	EmployeeID uuid.UUID `json:"employee_id"`
}

// TestAssignmentHTTPFlow covers the end-to-end object-level authorization:
// a chef manages assignments and an employee can only reach assigned patients.
func TestAssignmentHTTPFlow(t *testing.T) {
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

	t.Run("chef assigns and employee sees only assigned patients", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientA := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")
		patientB := createPatientViaAPI(t, handler, chefLogin, "Berta", "B")

		employeeLogin := loginAs(t, handler, employee.Email, "secret")

		// Before any assignment the employee must see nothing.
		if list := listPatientsViaAPI(t, handler, employeeLogin); len(list) != 0 {
			t.Fatalf("employee saw %d patients before assignment, want 0", len(list))
		}
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientA.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("unassigned patient status = %d, want 404", rec.Code)
		}

		// Employees cannot manage assignments.
		body := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
		if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientA.String()+"/assignments", body, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
			t.Fatalf("employee create assignment status = %d, want 403", rec.Code)
		}

		// The chef assigns patient A to the employee.
		rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientA.String()+"/assignments", body, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("chef create assignment status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var assignment assignmentAPIResponse
		decodeJSON(t, rec, &assignment)
		if assignment.PatientID != patientA || assignment.EmployeeID != employee.ID {
			t.Errorf("assignment = %+v, want patient %s employee %s", assignment, patientA, employee.ID)
		}

		// The employee now sees exactly patient A.
		list := listPatientsViaAPI(t, handler, employeeLogin)
		if len(list) != 1 || list[0].ID != patientA {
			t.Fatalf("employee list = %+v, want only %s", list, patientA)
		}
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientA.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusOK {
			t.Fatalf("assigned patient status = %d, want 200", rec.Code)
		}
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientB.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("unassigned patient status = %d, want 404", rec.Code)
		}

		// Assignment listings are chef-only.
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientA.String()+"/assignments", "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("employee list assignments status = %d, want 403", rec.Code)
		}

		// Revoking removes access immediately.
		rec = doRequest(handler, http.MethodPost, "/api/assignments/"+assignment.ID.String()+"/revoke", "", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("chef revoke status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if list := listPatientsViaAPI(t, handler, employeeLogin); len(list) != 0 {
			t.Fatalf("employee saw %d patients after revoke, want 0", len(list))
		}
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientA.String(), "", employeeLogin.cookie, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("revoked patient status = %d, want 404", rec.Code)
		}
	})

	t.Run("platform admin is denied patient data", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")
		admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")

		adminLogin := loginAs(t, handler, admin.Email, "secret")
		if rec := doRequest(handler, http.MethodGet, "/api/patients", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("platform admin list status = %d, want 403", rec.Code)
		}
		if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("platform admin get status = %d, want 403", rec.Code)
		}
		body := `{"first_name":"Berta","last_name":"B","birth_date":"1950-05-01"}`
		if rec := doRequest(handler, http.MethodPost, "/api/patients", body, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
			t.Fatalf("platform admin create status = %d, want 403", rec.Code)
		}
	})

	t.Run("chef cannot assign an employee from another tenant", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenantA := createTenant(t, queries, "valid")
		tenantB := createTenant(t, queries, "valid")
		chefA := createUserWithPassword(t, queries, tenantA.ID, "chef-a@a.de", "secret", "chef")
		employeeB := createUserWithPassword(t, queries, tenantB.ID, "employee-b@b.de", "secret", "employee")

		chefLogin := loginAs(t, handler, chefA.Email, "secret")
		patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")

		body := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employeeB.ID.String())
		rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", body, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("cross-tenant assignment status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
	})
}

func createPatientViaAPI(t *testing.T, handler http.Handler, login loginResult, firstName, lastName string) uuid.UUID {
	t.Helper()

	body := fmt.Sprintf(`{"first_name":%q,"last_name":%q,"birth_date":"1950-05-01"}`, firstName, lastName)
	rec := doRequest(handler, http.MethodPost, "/api/patients", body, login.cookie, login.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create patient status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patient patientAPIResponse
	decodeJSON(t, rec, &patient)
	return patient.ID
}

func listPatientsViaAPI(t *testing.T, handler http.Handler, login loginResult) []patientAPIResponse {
	t.Helper()

	rec := doRequest(handler, http.MethodGet, "/api/patients", "", login.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list patients status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patients []patientAPIResponse
	decodeJSON(t, rec, &patients)
	return patients
}
