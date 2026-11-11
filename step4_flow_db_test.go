package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type retentionPolicyAPIResponse struct {
	ID           uuid.UUID  `json:"id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	Category     string     `json:"category"`
	TriggerEvent string     `json:"trigger_event"`
	PeriodMonths int        `json:"period_months"`
	Status       string     `json:"status"`
	Note         string     `json:"note"`
	CreatedBy    uuid.UUID  `json:"created_by"`
	ConfirmedBy  *uuid.UUID `json:"confirmed_by"`
}

type legalHoldAPIResponse struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	PatientID  *uuid.UUID `json:"patient_id"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	LiftReason string     `json:"lift_reason"`
	DeclaredBy uuid.UUID  `json:"declared_by"`
	LiftedBy   *uuid.UUID `json:"lifted_by"`
}

type deletionRequestAPIResponse struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	DecisionReason string     `json:"decision_reason"`
	RequestedBy    uuid.UUID  `json:"requested_by"`
	DecidedBy      *uuid.UUID `json:"decided_by"`
	ExecutedBy     *uuid.UUID `json:"executed_by"`
}

type deletionRegisterAPIResponse struct {
	ID                uuid.UUID       `json:"id"`
	TenantID          uuid.UUID       `json:"tenant_id"`
	DeletionRequestID uuid.UUID       `json:"deletion_request_id"`
	PatientID         uuid.UUID       `json:"patient_id"`
	ExecutedBy        uuid.UUID       `json:"executed_by"`
	RowCounts         json.RawMessage `json:"row_counts"`
}

type deletionResultAPIResponse struct {
	Request  deletionRequestAPIResponse  `json:"request"`
	Register deletionRegisterAPIResponse `json:"register"`
}

type retentionCandidateAPIResponse struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
}

// TestRetentionDeletionHTTPFlow covers Step 4 end to end. The scheduled review
// only reports candidate counts. A deletion needs one chef to request and a
// different chef to approve and execute. Legal holds block the workflow, the
// register is append-only, and audit metadata never contains a name or a
// free-text reason.
func TestRetentionDeletionHTTPFlow(t *testing.T) {
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
	handler := cfg.routes()

	ctx := context.Background()
	tenantA := createTenant(t, queries, "valid")
	tenantB := createTenant(t, queries, "valid")
	chefA1 := createUserWithPassword(t, queries, tenantA.ID, "chef-a1@retention.de", "secret", "chef")
	chefA2 := createUserWithPassword(t, queries, tenantA.ID, "chef-a2@retention.de", "secret", "chef")
	employeeA := createUserWithPassword(t, queries, tenantA.ID, "employee-a@retention.de", "secret", "employee")
	chefB := createUserWithPassword(t, queries, tenantB.ID, "chef-b@retention.de", "secret", "chef")
	admin := createUserWithPassword(t, queries, tenantB.ID, "admin@retention.de", "secret", "platform_admin")

	chefA1Login := loginAs(t, handler, chefA1.Email, "secret")
	chefA2Login := loginAs(t, handler, chefA2.Email, "secret")
	employeeALogin := loginAs(t, handler, employeeA.Email, "secret")
	chefBLogin := loginAs(t, handler, chefB.Email, "secret")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	patientID := createPatientViaAPI(t, handler, chefA1Login, "Anna", "Support")

	// platform_admin and employee have no retention or deletion access.
	for _, denied := range []struct {
		name   string
		login  loginResult
		method string
		path   string
		body   string
	}{
		{"admin create policy", adminLogin, http.MethodPost, "/api/retention/policies", `{"category":"patient_master_data","period_months":1}`},
		{"admin list policies", adminLogin, http.MethodGet, "/api/retention/policies", ""},
		{"admin review", adminLogin, http.MethodPost, "/api/retention/review", ""},
		{"admin list requests", adminLogin, http.MethodGet, "/api/deletion-requests", ""},
		{"admin list register", adminLogin, http.MethodGet, "/api/deletion-register", ""},
		{"employee create policy", employeeALogin, http.MethodPost, "/api/retention/policies", `{"category":"patient_master_data","period_months":1}`},
		{"employee create request", employeeALogin, http.MethodPost, "/api/deletion-requests", fmt.Sprintf(`{"patient_id":%q,"reason":"not allowed"}`, patientID)},
		{"employee execute", employeeALogin, http.MethodPost, "/api/deletion-requests/" + uuid.NewString() + "/execute", ""},
	} {
		t.Run(denied.name, func(t *testing.T) {
			rec := doRequest(handler, denied.method, denied.path, denied.body, denied.login.cookie, denied.login.csrf)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s status = %d, want 403; body = %s", denied.name, rec.Code, rec.Body.String())
			}
		})
	}

	// A new policy starts disabled. Confirmation is the explicit chef decision.
	rec := doRequest(handler, http.MethodPost, "/api/retention/policies",
		`{"category":"patient_master_data","period_months":1,"note":"Reviewed by the data protection officer"}`,
		chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var policy retentionPolicyAPIResponse
	decodeJSON(t, rec, &policy)
	if policy.Status != "disabled" || policy.TriggerEvent != "end_of_care" {
		t.Fatalf("new policy = %+v, want disabled end_of_care", policy)
	}
	if policy.CreatedBy != chefA1.ID || policy.TenantID != tenantA.ID {
		t.Fatalf("policy identity = %+v", policy)
	}

	confirmPath := "/api/retention/policies/" + policy.ID.String() + "/confirm"
	rec = doRequest(handler, http.MethodPost, confirmPath, "", chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm policy status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var confirmed retentionPolicyAPIResponse
	decodeJSON(t, rec, &confirmed)
	if confirmed.Status != "confirmed" || confirmed.ConfirmedBy == nil || *confirmed.ConfirmedBy != chefA1.ID {
		t.Fatalf("confirmed policy = %+v", confirmed)
	}

	// A changed period is a new decision, so it returns to disabled.
	updatePath := "/api/retention/policies/" + policy.ID.String()
	rec = doRequest(handler, http.MethodPatch, updatePath,
		`{"period_months":2,"note":"Second review by the data protection officer"}`,
		chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("update policy status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated retentionPolicyAPIResponse
	decodeJSON(t, rec, &updated)
	if updated.Status != "disabled" || updated.ConfirmedBy != nil || updated.PeriodMonths != 2 {
		t.Fatalf("updated policy = %+v, want disabled with period 2", updated)
	}
	rec = doRequest(handler, http.MethodPost, confirmPath, "", chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-confirm policy status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Make the patient a retention candidate: care ended ten years ago and the
	// confirmed period is two months.
	if _, err := tx.ExecContext(ctx,
		"UPDATE patients SET status = 'archived', status_changed_at = clock_timestamp() - interval '10 years' WHERE id = $1",
		patientID); err != nil {
		t.Fatalf("age patient: %v", err)
	}

	rec = doRequest(handler, http.MethodPost, "/api/retention/review", "", chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant review status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var review struct {
		Candidates []retentionCandidateAPIResponse `json:"candidates"`
	}
	decodeJSON(t, rec, &review)
	if len(review.Candidates) != 1 || review.Candidates[0].Category != "patient_master_data" || review.Candidates[0].Count != 1 {
		t.Fatalf("tenant review candidates = %+v", review.Candidates)
	}

	// Another tenant cannot read tenant A's candidate counts.
	rec = doRequest(handler, http.MethodPost, "/api/retention/review", "", chefBLogin.cookie, chefBLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("other tenant review status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var otherReview struct {
		Candidates []retentionCandidateAPIResponse `json:"candidates"`
	}
	decodeJSON(t, rec, &otherReview)
	if len(otherReview.Candidates) != 0 {
		t.Fatalf("other tenant saw candidates: %+v", otherReview.Candidates)
	}

	// The scheduled review reports counts and never deletes or requests.
	total, err := cfg.reviewRetention(ctx)
	if err != nil {
		t.Fatalf("reviewRetention: %v", err)
	}
	if total < 1 {
		t.Fatalf("scheduled review total = %d, want at least 1", total)
	}
	if got := countWhere(t, tx, "patients", "id", patientID); got != 1 {
		t.Fatalf("review deleted the patient: rows = %d", got)
	}
	if got := countWhere(t, tx, "deletion_requests", "tenant_id", tenantA.ID); got != 0 {
		t.Fatalf("review created %d deletion requests", got)
	}

	// An active legal hold blocks the request immediately.
	rec = doRequest(handler, http.MethodPost, "/api/legal-holds",
		fmt.Sprintf(`{"patient_id":%q,"reason":"Tax audit for Anna Support"}`, patientID),
		chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create legal hold status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var hold legalHoldAPIResponse
	decodeJSON(t, rec, &hold)
	if hold.Status != "active" || hold.PatientID == nil || *hold.PatientID != patientID {
		t.Fatalf("legal hold = %+v", hold)
	}

	requestBody := fmt.Sprintf(`{"patient_id":%q,"reason":"Patient asked for erasure"}`, patientID)
	if rec := doRequest(handler, http.MethodPost, "/api/deletion-requests", requestBody, chefA1Login.cookie, chefA1Login.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("request under hold status = %d, want 409", rec.Code)
	}
	if got := countWhere(t, tx, "deletion_requests", "tenant_id", tenantA.ID); got != 0 {
		t.Fatalf("a held request was created: rows = %d", got)
	}

	liftPath := "/api/legal-holds/" + hold.ID.String() + "/lift"
	rec = doRequest(handler, http.MethodPost, liftPath, `{"reason":"Audit closed"}`, chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("lift legal hold status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var lifted legalHoldAPIResponse
	decodeJSON(t, rec, &lifted)
	if lifted.Status != "lifted" || lifted.LiftedBy == nil || *lifted.LiftedBy != chefA1.ID {
		t.Fatalf("lifted hold = %+v", lifted)
	}

	rec = doRequest(handler, http.MethodPost, "/api/deletion-requests", requestBody, chefA1Login.cookie, chefA1Login.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create deletion request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var request deletionRequestAPIResponse
	decodeJSON(t, rec, &request)
	if request.Status != "pending" || request.RequestedBy != chefA1.ID {
		t.Fatalf("deletion request = %+v", request)
	}

	// Seed one row in every dependent table so the deletion order is exercised.
	seedPatientDependents(t, tx, tenantA.ID, patientID, chefA1.ID, employeeA.ID, admin.ID)

	approvePath := "/api/deletion-requests/" + request.ID.String() + "/approve"
	if rec := doRequest(handler, http.MethodPost, approvePath, `{}`, chefA1Login.cookie, chefA1Login.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("requester approve status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, approvePath, `{}`, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("admin approve status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPost, approvePath, `{}`, chefBLogin.cookie, chefBLogin.csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("other tenant approve status = %d, want 404", rec.Code)
	}

	rec = doRequest(handler, http.MethodPost, approvePath, `{"reason":"Second chef checked the request"}`, chefA2Login.cookie, chefA2Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("second chef approve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approved deletionRequestAPIResponse
	decodeJSON(t, rec, &approved)
	if approved.Status != "approved" || approved.DecidedBy == nil || *approved.DecidedBy != chefA2.ID {
		t.Fatalf("approved request = %+v", approved)
	}

	executePath := "/api/deletion-requests/" + request.ID.String() + "/execute"
	if rec := doRequest(handler, http.MethodPost, executePath, "", chefA1Login.cookie, chefA1Login.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("requester execute status = %d, want 403", rec.Code)
	}

	rec = doRequest(handler, http.MethodPost, executePath, "", chefA2Login.cookie, chefA2Login.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result deletionResultAPIResponse
	decodeJSON(t, rec, &result)
	if result.Request.Status != "executed" || result.Request.ExecutedBy == nil || *result.Request.ExecutedBy != chefA2.ID {
		t.Fatalf("executed request = %+v", result.Request)
	}
	if result.Register.PatientID != patientID || result.Register.DeletionRequestID != request.ID {
		t.Fatalf("register entry = %+v", result.Register)
	}

	// The register stores counts, not the deleted personal data.
	var counts map[string]any
	if err := json.Unmarshal(result.Register.RowCounts, &counts); err != nil {
		t.Fatalf("decode row counts: %v", err)
	}
	for _, key := range []string{"billing_runs", "reports", "report_activities", "visits", "activities", "templates", "patient_assignments", "patient_search_tokens", "break_glass_grants"} {
		if value, ok := counts[key].(float64); !ok || value < 1 {
			t.Fatalf("row_counts[%s] = %v, want at least 1; counts = %v", key, counts[key], counts)
		}
	}
	if counts["audit_events_retained"] != true {
		t.Fatalf("row_counts audit_events_retained = %v, want true", counts["audit_events_retained"])
	}

	// Patient and dependents are gone; the request and register survive.
	if got := countWhere(t, tx, "patients", "id", patientID); got != 0 {
		t.Fatalf("patient rows = %d, want 0", got)
	}
	for _, table := range []string{"patient_assignments", "activities", "visits", "templates", "patient_search_tokens", "break_glass_grants"} {
		if got := countWhere(t, tx, table, "patient_id", patientID); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
	if got := countWhere(t, tx, "reports", "patient_id", patientID); got != 0 {
		t.Fatalf("reports rows = %d, want 0", got)
	}
	if got := countWhere(t, tx, "deletion_register", "deletion_request_id", request.ID); got != 1 {
		t.Fatalf("register rows = %d, want 1", got)
	}

	if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", chefA1Login.cookie, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("read deleted patient status = %d, want 404", rec.Code)
	}

	// A second execution is a conflict, not a second deletion.
	if rec := doRequest(handler, http.MethodPost, executePath, "", chefA2Login.cookie, chefA2Login.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("second execute status = %d, want 409", rec.Code)
	}

	// The register is readable by chefs of the tenant and append-only.
	rec = doRequest(handler, http.MethodGet, "/api/deletion-register", "", chefA2Login.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var register []deletionRegisterAPIResponse
	decodeJSON(t, rec, &register)
	if len(register) != 1 || register[0].ID != result.Register.ID {
		t.Fatalf("register list = %+v", register)
	}
	rec = doRequest(handler, http.MethodGet, "/api/deletion-register", "", chefBLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("other tenant register status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var otherRegister []deletionRegisterAPIResponse
	decodeJSON(t, rec, &otherRegister)
	if len(otherRegister) != 0 {
		t.Fatalf("other tenant saw %d register rows", len(otherRegister))
	}

	assertRegisterAppendOnly(t, tx, result.Register.ID)

	// Audit metadata names the entity, not the patient. Names and free-text
	// reasons stay in the tenant-bound rows.
	assertAuditMetadataClean(t, tx, tenantA.ID, []string{
		"Anna",
		"Support",
		"Patient asked for erasure",
		"Tax audit",
		"Audit closed",
		"data protection officer",
	})

	// The review and deletion actions are audited.
	for _, action := range []string{"retention.policy_created", "retention.policy_confirmed", "retention.reviewed", "deletion.requested", "deletion.approved", "deletion.executed"} {
		if got := countWhere(t, tx, "audit_events", "action", action); got < 1 {
			t.Fatalf("audit action %s = %d, want at least 1", action, got)
		}
	}
}

// seedPatientDependents inserts one row in every table that a patient deletion
// must remove. The report_activities row references the activity with
// ON DELETE RESTRICT, so the deletion order is observable: reports must go
// before activities.
func seedPatientDependents(t *testing.T, tx *sql.Tx, tenantID, patientID, chefID, employeeID, adminID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO patient_assignments (tenant_id, patient_id, employee_id, assigned_by, assignment_type, valid_from)
		VALUES ($1, $2, $3, $4, 'regular', clock_timestamp() - interval '2 days')`,
		tenantID, patientID, employeeID, chefID); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	var activityID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO activities (tenant_id, patient_id, employee_id, performed_at, duration_minutes, created_by)
		VALUES ($1, $2, $3, clock_timestamp() - interval '1 day', 30, $4)
		RETURNING id`,
		tenantID, patientID, employeeID, chefID).Scan(&activityID); err != nil {
		t.Fatalf("seed activity: %v", err)
	}

	var visitID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO visits (tenant_id, patient_id, employee_id, planned_start, created_by)
		VALUES ($1, $2, $3, clock_timestamp() - interval '1 day', $4)
		RETURNING id`,
		tenantID, patientID, employeeID, chefID).Scan(&visitID); err != nil {
		t.Fatalf("seed visit: %v", err)
	}

	var reportID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO reports (tenant_id, visit_id, patient_id, employee_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		tenantID, visitID, patientID, employeeID, chefID).Scan(&reportID); err != nil {
		t.Fatalf("seed report: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO report_activities (tenant_id, report_id, activity_id, quantity, performed_at)
		VALUES ($1, $2, $3, 1, clock_timestamp() - interval '1 day')`,
		tenantID, reportID, activityID); err != nil {
		t.Fatalf("seed report activity: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO templates (tenant_id, patient_id, name, created_by, version)
		VALUES ($1, $2, 'Care plan', $3, 1)`,
		tenantID, patientID, chefID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing_runs (tenant_id, report_id, generated_by)
		VALUES ($1, $2, $3)`,
		tenantID, reportID, chefID); err != nil {
		t.Fatalf("seed billing run: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO break_glass_grants (tenant_id, patient_id, requested_by, reason)
		VALUES ($1, $2, $3, 'Support ticket 4711')`,
		tenantID, patientID, adminID); err != nil {
		t.Fatalf("seed break-glass grant: %v", err)
	}
}

// assertRegisterAppendOnly proves that neither the application role nor the
// table owner can rewrite the deletion register. Each failing statement runs
// inside a savepoint so the outer test transaction stays usable.
func assertRegisterAppendOnly(t *testing.T, tx *sql.Tx, registerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	if _, err := tx.ExecContext(ctx, "SAVEPOINT register_append_only"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE altserv_app"); err != nil {
		t.Fatalf("set app role: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE deletion_register SET row_counts = '{}'::jsonb WHERE id = $1", registerID); err == nil {
		t.Fatal("the application role updated the deletion register")
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT register_append_only"); err != nil {
		t.Fatalf("rollback after update: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE altserv_app"); err != nil {
		t.Fatalf("set app role: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM deletion_register WHERE id = $1", registerID); err == nil {
		t.Fatal("the application role deleted a deletion register row")
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT register_append_only"); err != nil {
		t.Fatalf("rollback after delete: %v", err)
	}
	// The owner is blocked by the append-only trigger as well.
	if _, err := tx.ExecContext(ctx, "UPDATE deletion_register SET row_counts = '{}'::jsonb WHERE id = $1", registerID); err == nil {
		t.Fatal("the table owner updated the deletion register")
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT register_append_only"); err != nil {
		t.Fatalf("rollback after owner update: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT register_append_only"); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "RESET ROLE"); err != nil {
		t.Fatalf("reset role: %v", err)
	}
}

func assertAuditMetadataClean(t *testing.T, tx *sql.Tx, tenantID uuid.UUID, forbidden []string) {
	t.Helper()

	rows, err := tx.QueryContext(context.Background(),
		"SELECT action, metadata::text FROM audit_events WHERE tenant_id = $1", tenantID)
	if err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var action, metadata string
		if err := rows.Scan(&action, &metadata); err != nil {
			t.Fatalf("scan audit metadata: %v", err)
		}
		for _, value := range forbidden {
			if strings.Contains(metadata, value) {
				t.Fatalf("audit action %s metadata contains %q: %s", action, value, metadata)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit metadata: %v", err)
	}
}

func countWhere(t *testing.T, tx *sql.Tx, table, column string, value any) int {
	t.Helper()
	// table and column are test-controlled literals, never user input.
	query := "SELECT count(*) FROM " + table + " WHERE " + column + " = $1"
	var count int
	if err := tx.QueryRow(query, value).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
