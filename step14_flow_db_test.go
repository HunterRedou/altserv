package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type billingLineAPIResponse struct {
	ID          uuid.UUID `json:"id"`
	RunID       uuid.UUID `json:"run_id"`
	ActivityID  uuid.UUID `json:"activity_id"`
	Quantity    int       `json:"quantity"`
	ServiceCode string    `json:"service_code"`
	ServiceName string    `json:"service_name"`
	LegalArea   string    `json:"legal_area"`
	PayerName   string    `json:"payer_name"`
	ContractRef string    `json:"contract_reference"`
	PerformedAt time.Time `json:"performed_at"`
}

type billingFindingAPIResponse struct {
	Severity string     `json:"severity"`
	Code     string     `json:"code"`
	Message  string     `json:"message"`
	Activity *uuid.UUID `json:"activity_id"`
}

type billingRunAPIResponse struct {
	ID          uuid.UUID                   `json:"id"`
	TenantID    uuid.UUID                   `json:"tenant_id"`
	ReportID    uuid.UUID                   `json:"report_id"`
	Status      string                      `json:"status"`
	Version     int                         `json:"version"`
	GeneratedBy uuid.UUID                   `json:"generated_by"`
	ApprovedBy  *uuid.UUID                  `json:"approved_by"`
	Lines       []billingLineAPIResponse    `json:"lines"`
	Findings    []billingFindingAPIResponse `json:"findings"`
}

type billingRuleAPIResponse struct {
	ID                         uuid.UUID `json:"id"`
	Kind                       string    `json:"kind"`
	ServiceDefinitionID        uuid.UUID `json:"service_definition_id"`
	RelatedServiceDefinitionID uuid.UUID `json:"related_service_definition_id"`
	LegalArea                  string    `json:"legal_area"`
	Description                string    `json:"description"`
}

type transmissionAPIResponse struct {
	Status     string `json:"status"`
	Procedures []struct {
		LegalArea string `json:"legal_area"`
		Procedure string `json:"procedure"`
		Status    string `json:"status"`
		Note      string `json:"note"`
	} `json:"procedures"`
}

// TestBillingHTTPFlow covers Point 14 end to end: a released report becomes a
// deterministic billing run, the chef approves and exports it, a void allows a
// corrected run, and employees and platform admins cannot touch billing data.
func TestBillingHTTPFlow(t *testing.T) {
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
	secondDefinitionID := seedHTTPDefinition(t, tx, "LK-002", "Hauswirtschaft", "sgb_xi")

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	patientID := createPatientViaAPI(t, handler, chefLogin, "Anna", "A")
	assignBody := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employee.ID.String())
	if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", assignBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("assign employee status = %d, body = %s", rec.Code, rec.Body.String())
	}

	validFrom := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	contractBody := fmt.Sprintf(`{"service_definition_id":%q,"payer_name":"AOK Brandenburg","contract_reference":"V-2026-1","legal_area":"sgb_xi","valid_from":%q}`, definitionID, validFrom)
	rec := doRequest(handler, http.MethodPost, "/api/service-contracts", contractBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contract status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var contract contractAPIResponse
	decodeJSON(t, rec, &contract)

	planned := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	visitsPath := "/api/patients/" + patientID.String() + "/visits"
	createVisitBody := fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employee.ID.String(), planned.Format(time.RFC3339))
	rec = doRequest(handler, http.MethodPost, visitsPath, createVisitBody, chefLogin.cookie, chefLogin.csrf)
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

	activitiesPath := "/api/patients/" + patientID.String() + "/activities"
	activityBody := fmt.Sprintf(
		`{"employee_id":%q,"service_definition_id":%q,"contract_id":%q,"performed_at":%q,"duration_minutes":30,"note":"Grundpflege"}`,
		employee.ID, definitionID, contract.ID, time.Now().Add(-30*time.Minute).UTC().Format(time.RFC3339),
	)
	rec = doRequest(handler, http.MethodPost, activitiesPath, activityBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create activity status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var activity activityAPIResponse
	decodeJSON(t, rec, &activity)

	// Employee writes and submits the report; the chef approves and releases it.
	reportsPath := "/api/visits/" + visit.ID.String() + "/reports"
	createReportBody := fmt.Sprintf(`{"note":"Pflegebericht","activities":[{"activity_id":%q,"quantity":1}]}`, activity.ID.String())
	rec = doRequest(handler, http.MethodPost, reportsPath, createReportBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report reportAPIResponse
	decodeJSON(t, rec, &report)
	reportPath := "/api/reports/" + report.ID.String()
	rec = doRequest(handler, http.MethodPost, reportPath+"/submit", fmt.Sprintf(`{"version":%d}`, report.Version), employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &report)
	rec = doRequest(handler, http.MethodPost, reportPath+"/approve", fmt.Sprintf(`{"version":%d}`, report.Version), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &report)
	rec = doRequest(handler, http.MethodPost, reportPath+"/ready-for-billing", fmt.Sprintf(`{"version":%d}`, report.Version), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready for billing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &report)
	if report.Status != "ready_for_billing" {
		t.Fatalf("report status = %q", report.Status)
	}

	billingPath := "/api/reports/" + report.ID.String() + "/billing"

	// Employees and platform admins are denied before any billing data is read.
	if rec := doRequest(handler, http.MethodPost, billingPath, `{"version":0}`, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee generate status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, billingPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin read status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(handler, http.MethodPost, billingPath, `{"version":0}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("generate billing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var run billingRunAPIResponse
	decodeJSON(t, rec, &run)
	if run.Status != "draft" || run.Version != 1 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Lines) != 1 || len(run.Findings) != 0 {
		t.Fatalf("lines = %+v findings = %+v", run.Lines, run.Findings)
	}
	if run.Lines[0].ServiceCode != "LK-001" || run.Lines[0].PayerName != "AOK Brandenburg" {
		t.Fatalf("line = %+v", run.Lines[0])
	}

	if rec := doRequest(handler, http.MethodGet, billingPath, "", chefLogin.cookie, ""); rec.Code != http.StatusOK {
		t.Fatalf("get active billing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	runsPath := "/api/reports/" + report.ID.String() + "/billing-runs"
	rec = doRequest(handler, http.MethodGet, runsPath, "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list billing runs status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var runs []billingRunAPIResponse
	decodeJSON(t, rec, &runs)
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs = %+v", runs)
	}

	runPath := "/api/billing-runs/" + run.ID.String()
	if rec := doRequest(handler, http.MethodGet, runPath, "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee run read status = %d, want 403", rec.Code)
	}

	// Optimistic locking on approve.
	if rec := doRequest(handler, http.MethodPost, runPath+"/approve", `{"version":99}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("stale approve status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, http.MethodPost, runPath+"/approve", fmt.Sprintf(`{"version":%d}`, run.Version), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve billing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approved billingRunAPIResponse
	decodeJSON(t, rec, &approved)
	if approved.Status != "approved" || approved.ApprovedBy == nil || *approved.ApprovedBy != chef.ID {
		t.Fatalf("approved = %+v", approved)
	}

	// An approved run cannot be regenerated.
	if rec := doRequest(handler, http.MethodPost, billingPath, fmt.Sprintf(`{"version":%d}`, approved.Version), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("regenerate approved status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	// Internal exports.
	rec = doRequest(handler, http.MethodGet, runPath+"/export.csv", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export csv status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("csv content type = %q", contentType)
	}
	if !strings.Contains(rec.Body.String(), "LK-001") {
		t.Fatalf("csv body does not contain the service code: %s", rec.Body.String())
	}
	rec = doRequest(handler, http.MethodGet, runPath+"/export.pdf", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export pdf status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/pdf" {
		t.Fatalf("pdf content type = %q", contentType)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-1.4") {
		t.Fatalf("pdf body is not a PDF")
	}
	if rec := doRequest(handler, http.MethodGet, runPath+"/export.csv", "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee export status = %d, want 403", rec.Code)
	}

	// Void deletes the lines so the activity can be billed again.
	if rec := doRequest(handler, http.MethodPost, runPath+"/void", `{"version":99}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("stale void status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(handler, http.MethodPost, runPath+"/void", fmt.Sprintf(`{"version":%d}`, approved.Version), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("void status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var voided billingRunAPIResponse
	decodeJSON(t, rec, &voided)
	if voided.Status != "voided" {
		t.Fatalf("voided = %+v", voided)
	}
	if rec := doRequest(handler, http.MethodGet, billingPath, "", chefLogin.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("active billing after void status = %d, want 404", rec.Code)
	}
	rec = doRequest(handler, http.MethodPost, billingPath, `{"version":0}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rebill status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rebilled billingRunAPIResponse
	decodeJSON(t, rec, &rebilled)
	if rebilled.ID == approved.ID || len(rebilled.Lines) != 1 {
		t.Fatalf("rebilled = %+v", rebilled)
	}

	// Tenant rules are chef-only and must match both service legal areas.
	ruleBody := fmt.Sprintf(`{"kind":"exclusion","service_definition_id":%q,"related_service_definition_id":%q,"legal_area":"sgb_xi","description":"Nicht gemeinsam","valid_from":%q}`, definitionID, secondDefinitionID, validFrom)
	if rec := doRequest(handler, http.MethodPost, "/api/billing-rules", ruleBody, employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee create rule status = %d, want 403", rec.Code)
	}
	rec = doRequest(handler, http.MethodPost, "/api/billing-rules", ruleBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rule billingRuleAPIResponse
	decodeJSON(t, rec, &rule)
	if rule.Kind != "exclusion" || rule.ServiceDefinitionID != definitionID {
		t.Fatalf("rule = %+v", rule)
	}
	rec = doRequest(handler, http.MethodGet, "/api/billing-rules", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list rules status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rules []billingRuleAPIResponse
	decodeJSON(t, rec, &rules)
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules = %+v", rules)
	}

	// Transmission is explicitly not configured; no data is sent.
	rec = doRequest(handler, http.MethodGet, "/api/billing-transmission", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("transmission status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var transmission transmissionAPIResponse
	decodeJSON(t, rec, &transmission)
	if transmission.Status != "not_configured" || len(transmission.Procedures) != 2 {
		t.Fatalf("transmission = %+v", transmission)
	}
}
