package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/billing"
	"github.com/google/uuid"
)

type billingTransmissionAPIResponse struct {
	ID                uuid.UUID `json:"id"`
	RunID             uuid.UUID `json:"run_id"`
	LegalArea         string    `json:"legal_area"`
	Procedure         string    `json:"procedure"`
	Adapter           string    `json:"adapter"`
	Status            string    `json:"status"`
	IdempotencyKey    string    `json:"idempotency_key"`
	PayloadSHA256     string    `json:"payload_sha256"`
	Attempt           int       `json:"attempt"`
	ExternalReference string    `json:"external_reference"`
	ResponseCode      string    `json:"response_code"`
}

// fixedTransmitter drives a rejected or failed payer outcome over HTTP.
type fixedTransmitter struct {
	result billing.TransmissionResult
	err    error
}

func (f fixedTransmitter) Name() string { return "fixed" }

func (f fixedTransmitter) Submit(context.Context, billing.TransmissionRequest) (billing.TransmissionResult, error) {
	return f.result, f.err
}

// TestBillingTransmissionHTTPFlow covers the transmission boundary end to end:
// the production default answers 503 and sends nothing, only a chef may
// submit, rejected and failed attempts are recorded and retryable, the test
// payer accepts, a replay returns the same attempt, a second key conflicts and
// an accepted run cannot be voided.
func TestBillingTransmissionHTTPFlow(t *testing.T) {
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

	cfg, queries, tx := newSessionTestAPIConfig(t, conn)
	defer tx.Rollback()

	// Every handler shares the same transaction and sessions; only the wired
	// adapter differs. routes() captures the service at call time.
	unconfigured := cfg.routes()
	newBilling := func(transmitter billing.Transmitter) http.Handler {
		cfg.billingService = billing.NewServiceWithTransmitter(billing.NewRepository(), cfg.auditRecorder, transmitter)
		return cfg.routes()
	}
	rejecting := newBilling(fixedTransmitter{result: billing.TransmissionResult{Status: billing.TransmissionStatusRejected, ResponseCode: "R01"}})
	failing := newBilling(fixedTransmitter{err: errors.New("connection reset")})
	payer := newBilling(billing.TestPayer{})

	suffix := uuid.NewString()[:8]
	tenant := createTenant(t, queries, "valid")
	chef := createUserWithPassword(t, queries, tenant.ID, "tx-chef-"+suffix+"@a.de", "secret", "chef")
	employee := createUserWithPassword(t, queries, tenant.ID, "tx-employee-"+suffix+"@a.de", "secret", "employee")
	admin := createUserWithPassword(t, queries, tenant.ID, "tx-admin-"+suffix+"@a.de", "secret", "platform_admin")
	definitionID := seedHTTPDefinition(t, tx, "SYNTH-TX-"+suffix, "Synthetische Leistung", "sgb_xi")

	chefLogin := loginAs(t, payer, chef.Email, "secret")
	employeeLogin := loginAs(t, payer, employee.Email, "secret")
	adminLogin := loginAs(t, payer, admin.Email, "secret")

	runID := approveRunViaAPI(t, payer, chefLogin, employeeLogin, employee.ID, definitionID)
	runPath := "/api/billing-runs/" + runID.String()
	transmitPath := runPath + "/transmit"
	body := func(key string) string {
		return fmt.Sprintf(`{"legal_area":"sgb_xi","idempotency_key":%q}`, key)
	}

	// Status endpoint reports the wired adapter.
	var status struct {
		Status  string `json:"status"`
		Adapter string `json:"adapter"`
	}
	rec := doRequest(unconfigured, http.MethodGet, "/api/billing-transmission", "", chefLogin.cookie, "")
	decodeJSON(t, rec, &status)
	if rec.Code != http.StatusOK || status.Adapter != billing.AdapterUnconfigured {
		t.Fatalf("status = %d %+v", rec.Code, status)
	}

	// Employees and platform admins are denied.
	if rec := doRequest(payer, http.MethodPost, transmitPath, body("key-employee"), employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee transmit status = %d, want 403", rec.Code)
	}
	if rec := doRequest(payer, http.MethodPost, transmitPath, body("key-admin01"), adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("admin transmit status = %d, want 403", rec.Code)
	}
	if rec := doRequest(payer, http.MethodGet, runPath+"/transmissions", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin list status = %d, want 403", rec.Code)
	}

	// CSRF is required on the state-changing route.
	if rec := doRequest(payer, http.MethodPost, transmitPath, body("key-nocsrf1"), chefLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", rec.Code)
	}

	// Input validation.
	for name, payload := range map[string]string{
		"missing key":  `{"legal_area":"sgb_xi"}`,
		"bad area":     `{"legal_area":"sgb_ix","idempotency_key":"key-00000001"}`,
		"mixed area":   `{"legal_area":"sgb_v","idempotency_key":"key-00000001"}`,
		"invalid json": `{"legal_area":`,
	} {
		if rec := doRequest(payer, http.MethodPost, transmitPath, payload, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400, body = %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := doRequest(payer, http.MethodPost, "/api/billing-runs/not-a-uuid/transmit", body("key-00000001"), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d, want 400", rec.Code)
	}
	if rec := doRequest(payer, http.MethodPost, "/api/billing-runs/"+uuid.NewString()+"/transmit", body("key-00000001"), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run status = %d, want 404", rec.Code)
	}

	// Production default: 503 and no transmission row.
	if rec := doRequest(unconfigured, http.MethodPost, transmitPath, body("key-unconf-1"), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	if got := countTransmissionRows(t, tx, runID); got != 0 {
		t.Fatalf("unconfigured created %d rows", got)
	}

	// Rejected and failed attempts are recorded and returned.
	var rejected billingTransmissionAPIResponse
	rec = doRequest(rejecting, http.MethodPost, transmitPath, body("key-reject-1"), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &rejected)
	if rejected.Status != "rejected" || rejected.ResponseCode != "R01" || rejected.Attempt != 1 {
		t.Fatalf("rejected = %+v", rejected)
	}
	var failed billingTransmissionAPIResponse
	rec = doRequest(failing, http.MethodPost, transmitPath, body("key-failed-1"), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("failed status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &failed)
	if failed.Status != "failed" || failed.ResponseCode != "adapter_error" || failed.Attempt != 2 {
		t.Fatalf("failed = %+v", failed)
	}
	if strings.Contains(rec.Body.String(), "connection reset") {
		t.Fatalf("response leaks adapter error: %s", rec.Body.String())
	}

	// The test payer accepts. The Idempotency-Key header wins over the body.
	var accepted billingTransmissionAPIResponse
	rec = doRequestWithHeaders(payer, http.MethodPost, transmitPath, body("ignored-key-1"), chefLogin.cookie, chefLogin.csrf,
		map[string]string{"Idempotency-Key": "key-accept-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("accepted status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &accepted)
	if accepted.Status != "accepted" || accepted.Adapter != "test" || accepted.Attempt != 3 ||
		accepted.IdempotencyKey != "key-accept-1" || accepted.Procedure != "SGB XI §105" || len(accepted.PayloadSHA256) != 64 {
		t.Fatalf("accepted = %+v", accepted)
	}

	// Replay returns the same attempt with 200.
	var replay billingTransmissionAPIResponse
	rec = doRequest(payer, http.MethodPost, transmitPath, body("key-accept-1"), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &replay)
	if replay.ID != accepted.ID {
		t.Fatalf("replay id = %s, want %s", replay.ID, accepted.ID)
	}

	// A new key for an accepted run conflicts.
	if rec := doRequest(payer, http.MethodPost, transmitPath, body("key-accept-2"), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("second key status = %d, want 409", rec.Code)
	}

	var list []billingTransmissionAPIResponse
	rec = doRequest(payer, http.MethodGet, runPath+"/transmissions", "", chefLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &list)
	if len(list) != 3 || list[0].ID != accepted.ID {
		t.Fatalf("list = %+v", list)
	}
	if rec := doRequest(payer, http.MethodGet, runPath+"/transmissions", "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee list status = %d, want 403", rec.Code)
	}

	// An accepted claim cannot be voided silently.
	var run billingRunAPIResponse
	rec = doRequest(payer, http.MethodGet, runPath, "", chefLogin.cookie, "")
	decodeJSON(t, rec, &run)
	if rec := doRequest(payer, http.MethodPost, runPath+"/void", fmt.Sprintf(`{"version":%d}`, run.Version), chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("void accepted status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	var actions []string
	rows, err := tx.Query(`SELECT action FROM audit_events WHERE tenant_id = $1 AND action LIKE 'billing.transmission%' ORDER BY action`, tenant.ID)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatalf("scan: %v", err)
		}
		actions = append(actions, action)
	}
	want := "billing.transmission_accepted,billing.transmission_failed,billing.transmission_not_configured," +
		"billing.transmission_rejected,billing.transmission_requested,billing.transmission_requested,billing.transmission_requested"
	if strings.Join(actions, ",") != want {
		t.Fatalf("audit actions = %v", actions)
	}
}

func countTransmissionRows(t *testing.T, tx *sql.Tx, runID uuid.UUID) int {
	t.Helper()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM billing_transmissions WHERE run_id = $1`, runID).Scan(&count); err != nil {
		t.Fatalf("count transmissions: %v", err)
	}
	return count
}

// approveRunViaAPI walks one synthetic SGB XI activity from contract to an
// approved billing run over HTTP and returns the run id.
func approveRunViaAPI(t *testing.T, handler http.Handler, chefLogin, employeeLogin loginResult, employeeID, definitionID uuid.UUID) uuid.UUID {
	t.Helper()

	patientID := createPatientViaAPI(t, handler, chefLogin, "Synth", "Patient")
	assignBody := fmt.Sprintf(`{"employee_id":%q,"assignment_type":"regular"}`, employeeID.String())
	if rec := doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/assignments", assignBody, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusCreated {
		t.Fatalf("assign status = %d, body = %s", rec.Code, rec.Body.String())
	}

	validFrom := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	contractBody := fmt.Sprintf(`{"service_definition_id":%q,"payer_name":"Synthetische Pflegekasse","contract_reference":"SYNTH-V-1","legal_area":"sgb_xi","valid_from":%q}`, definitionID, validFrom)
	rec := doRequest(handler, http.MethodPost, "/api/service-contracts", contractBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("contract status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var contract contractAPIResponse
	decodeJSON(t, rec, &contract)

	planned := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	rec = doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/visits",
		fmt.Sprintf(`{"employee_id":%q,"planned_start":%q}`, employeeID.String(), planned.Format(time.RFC3339)), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("visit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var visit visitAPIResponse
	decodeJSON(t, rec, &visit)
	visitPath := "/api/visits/" + visit.ID.String()
	for _, step := range []string{"/start", "/complete"} {
		if rec := doRequest(handler, http.MethodPost, visitPath+step, "", employeeLogin.cookie, employeeLogin.csrf); rec.Code != http.StatusOK {
			t.Fatalf("visit %s status = %d, body = %s", step, rec.Code, rec.Body.String())
		}
	}

	activityBody := fmt.Sprintf(
		`{"employee_id":%q,"service_definition_id":%q,"contract_id":%q,"performed_at":%q,"duration_minutes":30,"note":"Synthetisch"}`,
		employeeID, definitionID, contract.ID, time.Now().Add(-30*time.Minute).UTC().Format(time.RFC3339),
	)
	rec = doRequest(handler, http.MethodPost, "/api/patients/"+patientID.String()+"/activities", activityBody, employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("activity status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var activity activityAPIResponse
	decodeJSON(t, rec, &activity)

	rec = doRequest(handler, http.MethodPost, visitPath+"/reports",
		fmt.Sprintf(`{"note":"Synthetischer Bericht","activities":[{"activity_id":%q,"quantity":1}]}`, activity.ID), employeeLogin.cookie, employeeLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report reportAPIResponse
	decodeJSON(t, rec, &report)
	reportPath := "/api/reports/" + report.ID.String()
	for _, step := range []struct {
		path  string
		login loginResult
	}{{"/submit", employeeLogin}, {"/approve", chefLogin}, {"/ready-for-billing", chefLogin}} {
		rec = doRequest(handler, http.MethodPost, reportPath+step.path, fmt.Sprintf(`{"version":%d}`, report.Version), step.login.cookie, step.login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("report %s status = %d, body = %s", step.path, rec.Code, rec.Body.String())
		}
		decodeJSON(t, rec, &report)
	}

	rec = doRequest(handler, http.MethodPost, reportPath+"/billing", `{"version":0}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("generate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var run billingRunAPIResponse
	decodeJSON(t, rec, &run)
	rec = doRequest(handler, http.MethodPost, "/api/billing-runs/"+run.ID.String()+"/approve", fmt.Sprintf(`{"version":%d}`, run.Version), chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve run status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return run.ID
}
