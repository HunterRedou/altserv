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

type reportActivityAPIResponse struct {
	ID         uuid.UUID `json:"id"`
	ReportID   uuid.UUID `json:"report_id"`
	ActivityID uuid.UUID `json:"activity_id"`
	Quantity   int       `json:"quantity"`
}

type reportAPIResponse struct {
	ID          uuid.UUID                   `json:"id"`
	TenantID    uuid.UUID                   `json:"tenant_id"`
	VisitID     uuid.UUID                   `json:"visit_id"`
	PatientID   uuid.UUID                   `json:"patient_id"`
	EmployeeID  uuid.UUID                   `json:"employee_id"`
	Status      string                      `json:"status"`
	Note        string                      `json:"note"`
	Version     int                         `json:"version"`
	SubmittedAt *time.Time                  `json:"submitted_at"`
	ApprovedAt  *time.Time                  `json:"approved_at"`
	ApprovedBy  *uuid.UUID                  `json:"approved_by"`
	Activities  []reportActivityAPIResponse `json:"activities"`
}

type reportTransitionAPIResponse struct {
	ID         uuid.UUID `json:"id"`
	ReportID   uuid.UUID `json:"report_id"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Kind       string    `json:"kind"`
	Reason     string    `json:"reason"`
}

// TestReportHTTPFlow covers the report lifecycle end to end: a completed visit,
// an employee draft, the chef approval, a return with a reason, the optimistic
// lock, assignment-scoped access and platform-admin denial.
func TestReportHTTPFlow(t *testing.T) {
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
	outsider := createUserWithPassword(t, queries, tenant.ID, "outsider@a.de", "secret", "employee")
	admin := createUserWithPassword(t, queries, tenant.ID, "admin@a.de", "secret", "platform_admin")

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	outsiderLogin := loginAs(t, handler, outsider.Email, "secret")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")

	assignBody := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
	if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", assignBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("assign employee status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The chef plans a visit and the assigned employee performs it.
	planned := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	visitsPath := "/api/patients/" + patientID.String() + "/visits"
	createVisitBody := fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employee.ID.String(), planned.Format(time.RFC3339))
	rec := doRequest(handler, http.MethodPost, visitsPath, createVisitBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create visit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var visit visitAPIResponse
	decodeJSON(t, rec, &visit)
	visitPath := "/api/visits/" + visit.ID.String()
	if rec := doRequest(handler, http.MethodPost, visitPath+"/start", "", employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("employee start status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPost, visitPath+"/complete", "", employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("employee complete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The employee records the performed activity.
	activitiesPath := "/api/patients/" + patientID.String() + "/activities"
	activityBody := fmt.Sprintf(`{"performed_at":%q,"duration_minutes":30,"note":"Grundpflege"}`, time.Now().Add(-30*time.Minute).UTC().Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPost, activitiesPath, activityBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create activity status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var activity activityAPIResponse
	decodeJSON(t, rec, &activity)

	// A report cannot be created for a planned visit.
	secondVisitBody := fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employee.ID.String(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPost, visitsPath, secondVisitBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second visit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var plannedVisit visitAPIResponse
	decodeJSON(t, rec, &plannedVisit)
	reportsForPlanned := "/api/visits/" + plannedVisit.ID.String() + "/reports"
	if rec := doRequest(handler, http.MethodPost, reportsForPlanned, `{"note":"too early"}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("report for planned visit status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	// The outsider cannot create a report.
	createReportBody := fmt.Sprintf(`{"note":"Pflegebericht","activities":[{"activity_id":%q,"quantity":1}]}`, activity.ID.String())
	reportsPath := "/api/visits/" + visit.ID.String() + "/reports"
	if rec := doRequest(handler, http.MethodPost, reportsPath, createReportBody, outsiderLogin.cookie, outsiderLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider create report status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(handler, http.MethodPost, reportsPath, createReportBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("employee create report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report reportAPIResponse
	decodeJSON(t, rec, &report)
	if report.Status != "draft" || report.Version != 1 || report.Note != "Pflegebericht" {
		t.Fatalf("created report = %+v", report)
	}
	if len(report.Activities) != 1 || report.Activities[0].ActivityID != activity.ID || report.Activities[0].Quantity != 1 {
		t.Fatalf("report activities = %+v", report.Activities)
	}

	// A second report for the same visit is rejected.
	if rec := doRequest(handler, http.MethodPost, reportsPath, createReportBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate report status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	reportPath := "/api/reports/" + report.ID.String()
	rec = doRequest(handler, http.MethodGet, reportPath, "", employeeLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("employee get report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var loaded reportAPIResponse
	decodeJSON(t, rec, &loaded)
	if loaded.Note != "Pflegebericht" || len(loaded.Activities) != 1 {
		t.Fatalf("loaded report = %+v", loaded)
	}

	if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String()+"/reports", "", chefLogin.cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("list patient reports status = %d, body = %s", rec.Code, rec.Body.String())
	} else {
		var listed []reportAPIResponse
		decodeJSON(t, rec, &listed)
		if len(listed) != 1 || listed[0].ID != report.ID {
			t.Fatalf("patient reports = %+v", listed)
		}
	}
	if rec := doRequest(handler, http.MethodGet, reportsPath, "", chefLogin.cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("list visit reports status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// An unassigned employee cannot see the report; the platform admin is denied.
	if rec := doRequest(handler, http.MethodGet, reportPath, "", outsiderLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider get report status = %d, want 404", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, reportPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin get report status = %d, want 403", rec.Code)
	}

	// A stale edit is rejected and the current edit succeeds.
	staleBody := `{"version":99,"note":"stale"}`
	if rec := doRequest(handler, http.MethodPatch, reportPath, staleBody, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	updateBody := `{"version":1,"note":"Pflegebericht korrigiert"}`
	rec = doRequest(handler, http.MethodPatch, reportPath, updateBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("update draft status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated reportAPIResponse
	decodeJSON(t, rec, &updated)
	if updated.Version != 2 || updated.Note != "Pflegebericht korrigiert" {
		t.Fatalf("updated report = %+v", updated)
	}

	// The employee submits; the chef approves.
	submitBody := `{"version":2}`
	rec = doRequest(handler, http.MethodPost, reportPath+"/submit", submitBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var submitted reportAPIResponse
	decodeJSON(t, rec, &submitted)
	if submitted.Status != "submitted" || submitted.Version != 3 || submitted.SubmittedAt == nil {
		t.Fatalf("submitted report = %+v", submitted)
	}

	// There is no silent change after submission.
	if rec := doRequest(handler, http.MethodPatch, reportPath, `{"version":3,"note":"after submit"}`, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("update after submit status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	if rec := doRequest(handler, http.MethodPost, reportPath+"/approve", `{"version":3}`, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee approve status = %d, want 403", rec.Code)
	}
	rec = doRequest(handler, http.MethodPost, reportPath+"/approve", `{"version":3}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("chef approve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approved reportAPIResponse
	decodeJSON(t, rec, &approved)
	if approved.Status != "approved" || approved.Version != 4 || approved.ApprovedBy == nil || *approved.ApprovedBy != chef.ID {
		t.Fatalf("approved report = %+v", approved)
	}

	// A return needs a reason and creates a separate transition.
	if rec := doRequest(handler, http.MethodPost, reportPath+"/return", `{"version":4}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("return without reason status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, http.MethodPost, reportPath+"/return", `{"version":4,"reason":"Menge fehlt"}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("return status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var returned reportAPIResponse
	decodeJSON(t, rec, &returned)
	if returned.Status != "draft" || returned.Version != 5 || returned.ApprovedAt != nil {
		t.Fatalf("returned report = %+v", returned)
	}

	rec = doRequest(handler, http.MethodGet, reportPath+"/transitions", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list transitions status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var transitions []reportTransitionAPIResponse
	decodeJSON(t, rec, &transitions)
	if len(transitions) != 1 || transitions[0].FromStatus != "approved" || transitions[0].ToStatus != "draft" || transitions[0].Kind != "correction" || transitions[0].Reason != "Menge fehlt" {
		t.Fatalf("transitions = %+v", transitions)
	}

	// The returned report is resubmitted, approved and released for billing.
	if rec := doRequest(handler, http.MethodPost, reportPath+"/submit", `{"version":5}`, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("resubmit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPost, reportPath+"/approve", `{"version":6}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("reapprove status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, http.MethodPost, reportPath+"/ready-for-billing", `{"version":7}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready for billing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ready reportAPIResponse
	decodeJSON(t, rec, &ready)
	if ready.Status != "ready_for_billing" || ready.Version != 8 {
		t.Fatalf("ready report = %+v", ready)
	}

	// The platform admin gets no automatic report access.
	if rec := doRequest(handler, http.MethodPost, reportsPath, createReportBody, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("platform admin create report status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}
