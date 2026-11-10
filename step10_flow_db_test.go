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

type visitAPIResponse struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	PatientID        uuid.UUID  `json:"patient_id"`
	EmployeeID       uuid.UUID  `json:"employee_id"`
	ActualEmployeeID *uuid.UUID `json:"actual_employee_id"`
	PlannedStart     time.Time  `json:"planned_start"`
	ActualStart      *time.Time `json:"actual_start"`
	ActualEnd        *time.Time `json:"actual_end"`
	Status           string     `json:"status"`
	CreatedBy        uuid.UUID  `json:"created_by"`
}

// TestVisitHTTPFlow covers the chef lifecycle, the assigned employee starting
// and completing a visit, an unassigned employee being denied, and the
// platform-admin denial.
func TestVisitHTTPFlow(t *testing.T) {
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
	substitute := createUserWithPassword(t, queries, tenant.ID, "substitute@a.de", "secret", "employee")
	outsider := createUserWithPassword(t, queries, tenant.ID, "outsider@a.de", "secret", "employee")
	admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")
	visitsPath := "/api/patients/" + patientID.String() + "/visits"

	// The chef assigns the employee and the substitute to the patient.
	for _, userID := range []uuid.UUID{employee.ID, substitute.ID} {
		body := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, userID.String())
		rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", body, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("assign %s status = %d, body = %s", userID, rec.Code, rec.Body.String())
		}
	}

	planned := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	createBody := fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employee.ID.String(), planned.Format(time.RFC3339))
	rec := doRequest(handler, http.MethodPost, visitsPath, createBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("chef create visit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created visitAPIResponse
	decodeJSON(t, rec, &created)
	if created.Status != "planned" || created.EmployeeID != employee.ID || created.PatientID != patientID {
		t.Fatalf("created visit = %+v", created)
	}
	if created.ActualEmployeeID != nil {
		t.Errorf("planned visit already has an actual employee: %s", *created.ActualEmployeeID)
	}

	rec = doRequest(handler, http.MethodGet, visitsPath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list visits status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed []visitAPIResponse
	decodeJSON(t, rec, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %+v, want the created visit", listed)
	}

	visitPath := "/api/visits/" + created.ID.String()
	rec = doRequest(handler, http.MethodGet, visitPath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef get visit status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// An unassigned employee is forbidden to write and cannot see the visit.
	outsiderLogin := loginAs(t, handler, outsider.Email, "secret")
	if rec := doRequest(handler, http.MethodGet, visitPath, "", outsiderLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider get visit status = %d, want 404", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, visitsPath, "", outsiderLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider list visits status = %d, want 404", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, visitPath+"/start", "", outsiderLogin.cookie, outsiderLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider start visit status = %d, want 403", rec.Code)
	}

	// The planned employee reschedules then starts the visit.
	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	newStart := planned.Add(15 * time.Minute)
	rescheduleBody := fmt.Sprintf(`{"planned_start":%q}`, newStart.Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPatch, visitPath, rescheduleBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("chef reschedule status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rescheduled visitAPIResponse
	decodeJSON(t, rec, &rescheduled)
	if !rescheduled.PlannedStart.Equal(newStart) {
		t.Errorf("rescheduled planned start = %v, want %v", rescheduled.PlannedStart, newStart)
	}

	rec = doRequest(handler, http.MethodPost, visitPath+"/start", "", employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("employee start status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var started visitAPIResponse
	decodeJSON(t, rec, &started)
	if started.Status != "in_progress" || started.ActualEmployeeID == nil || *started.ActualEmployeeID != employee.ID {
		t.Fatalf("started visit = %+v, want in_progress by %s", started, employee.ID)
	}
	if started.ActualStart == nil {
		t.Fatal("started visit has no actual start")
	}

	// A completed visit cannot be started again.
	if rec := doRequest(handler, http.MethodPost, visitPath+"/start", "", employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("double start status = %d, want 409", rec.Code)
	}

	rec = doRequest(handler, http.MethodPost, visitPath+"/complete", "", employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("employee complete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completed visitAPIResponse
	decodeJSON(t, rec, &completed)
	if completed.Status != "completed" || completed.ActualEnd == nil {
		t.Fatalf("completed visit = %+v, want completed with actual end", completed)
	}

	// The chef corrects the start time and supplies a reason.
	correctedStart := completed.ActualStart.Add(-10 * time.Minute).Truncate(time.Second)
	correctBody := fmt.Sprintf(`{"actual_start":%q,"reason":"clock was wrong"}`, correctedStart.Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPost, visitPath+"/correct", correctBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("chef correct status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var corrected visitAPIResponse
	decodeJSON(t, rec, &corrected)
	if corrected.ActualStart == nil || !corrected.ActualStart.Equal(correctedStart) {
		t.Errorf("corrected start = %v, want %v", corrected.ActualStart, correctedStart)
	}

	// A correction without a reason is rejected.
	if rec := doRequest(handler, http.MethodPost, visitPath+"/correct", `{}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("correct without reason status = %d, want 400", rec.Code)
	}

	// An assigned substitute takes over a visit planned for the employee.
	secondBody := fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employee.ID.String(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPost, visitsPath, secondBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("chef create second visit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var second visitAPIResponse
	decodeJSON(t, rec, &second)

	substituteLogin := loginAs(t, handler, substitute.Email, "secret")
	secondPath := "/api/visits/" + second.ID.String()
	rec = doRequest(handler, http.MethodPost, secondPath+"/start", "", substituteLogin.cookie, substituteLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("substitute start status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var substituted visitAPIResponse
	decodeJSON(t, rec, &substituted)
	if substituted.EmployeeID != employee.ID {
		t.Errorf("planned employee = %s, want %s", substituted.EmployeeID, employee.ID)
	}
	if substituted.ActualEmployeeID == nil || *substituted.ActualEmployeeID != substitute.ID {
		t.Errorf("actual employee = %v, want the substitute %s", substituted.ActualEmployeeID, substitute.ID)
	}

	// The chef cancels the substituted visit.
	if rec := doRequest(handler, http.MethodPost, secondPath+"/cancel", "", chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("chef cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Employees cannot manage the schedule or corrections.
	if rec := doRequest(handler, http.MethodPatch, visitPath, rescheduleBody, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee reschedule status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, visitPath+"/cancel", "", employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee cancel status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, visitPath+"/correct", correctBody, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee correct status = %d, want 403", rec.Code)
	}

	// A platform admin gets no automatic tenant data access.
	adminLogin := loginAs(t, handler, admin.Email, "secret")
	if rec := doRequest(handler, http.MethodGet, visitsPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin list status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, visitPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin get status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, visitsPath, createBody, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin create status = %d, want 403", rec.Code)
	}
}
