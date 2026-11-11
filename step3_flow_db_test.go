package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type breakGlassGrantAPIResponse struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	RequestedBy    uuid.UUID  `json:"requested_by"`
	DecidedBy      *uuid.UUID `json:"decided_by"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	DecisionReason string     `json:"decision_reason"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
}

type breakGlassPatientAPIResponse struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	BirthDate string    `json:"birth_date"`
}

// TestBreakGlassHTTPFlow covers Step 3 end to end: a platform_admin has no
// automatic patient access, requests a named patient in a named tenant, a chef
// of that tenant approves a short window, the read is audited, and revocation,
// denial and expiry close the window again. Employees and other tenants cannot
// use the flow.
func TestBreakGlassHTTPFlow(t *testing.T) {
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

	tenantA := createTenant(t, queries, "valid")
	tenantB := createTenant(t, queries, "valid")
	chefA := createUserWithPassword(t, queries, tenantA.ID, "chef-a@breakglass.de", "secret", "chef")
	employeeA := createUserWithPassword(t, queries, tenantA.ID, "employee-a@breakglass.de", "secret", "employee")
	chefB := createUserWithPassword(t, queries, tenantB.ID, "chef-b@breakglass.de", "secret", "chef")
	admin := createUserWithPassword(t, queries, tenantB.ID, "admin@breakglass.de", "secret", "platform_admin")

	chefALogin := loginAs(t, handler, chefA.Email, "secret")
	employeeALogin := loginAs(t, handler, employeeA.Email, "secret")
	chefBLogin := loginAs(t, handler, chefB.Email, "secret")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	patientID := createPatientViaAPI(t, handler, chefALogin, "Anna", "Support")

	// A platform_admin still has no automatic patient access.
	if rec := doRequest(handler, http.MethodGet, "/api/patients/"+patientID.String(), "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("platform_admin patient read status = %d, want 403", rec.Code)
	}

	requestBody := fmt.Sprintf(
		`{"tenant_id":%q,"patient_id":%q,"reason":"Support ticket 4711"}`,
		tenantA.ID, patientID,
	)
	rec := doRequest(handler, http.MethodPost, "/api/break-glass/grants", requestBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create grant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var grant breakGlassGrantAPIResponse
	decodeJSON(t, rec, &grant)
	if grant.Status != "pending" {
		t.Fatalf("new grant status = %q, want pending", grant.Status)
	}
	if grant.TenantID != tenantA.ID || grant.PatientID != patientID || grant.RequestedBy != admin.ID {
		t.Fatalf("grant identity = %+v", grant)
	}

	// The requester sees the own request, but no patient data before approval.
	rec = doRequest(handler, http.MethodGet, "/api/break-glass/grants?tenant_id="+tenantA.ID.String(), "", adminLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("requester list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var requesterGrants []breakGlassGrantAPIResponse
	decodeJSON(t, rec, &requesterGrants)
	if len(requesterGrants) != 1 || requesterGrants[0].ID != grant.ID {
		t.Fatalf("requester grants = %+v", requesterGrants)
	}
	patientPath := "/api/break-glass/grants/" + grant.ID.String() + "/patient?tenant_id=" + tenantA.ID.String()
	if rec := doRequest(handler, http.MethodGet, patientPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read before approval status = %d, want 403", rec.Code)
	}

	// Another tenant's chef cannot see or decide the grant.
	rec = doRequest(handler, http.MethodGet, "/api/break-glass/grants", "", chefBLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("other tenant list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var otherGrants []breakGlassGrantAPIResponse
	decodeJSON(t, rec, &otherGrants)
	if len(otherGrants) != 0 {
		t.Fatalf("other tenant saw %d grants", len(otherGrants))
	}
	approvePath := "/api/break-glass/grants/" + grant.ID.String() + "/approve"
	if rec := doRequest(handler, http.MethodPost, approvePath, `{"duration_minutes":30}`, chefBLogin.cookie, chefBLogin.csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant approve status = %d, want 404", rec.Code)
	}

	// The chef of the patient's tenant sees the request and can approve it.
	rec = doRequest(handler, http.MethodGet, "/api/break-glass/grants", "", chefALogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chef list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var chefGrants []breakGlassGrantAPIResponse
	decodeJSON(t, rec, &chefGrants)
	if len(chefGrants) != 1 || chefGrants[0].ID != grant.ID || chefGrants[0].Reason != "Support ticket 4711" {
		t.Fatalf("chef grants = %+v", chefGrants)
	}

	// An out-of-range window is rejected.
	if rec := doRequest(handler, http.MethodPost, approvePath, `{"duration_minutes":61}`, chefALogin.cookie, chefALogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid duration status = %d, want 400", rec.Code)
	}

	rec = doRequest(handler, http.MethodPost, approvePath, `{"duration_minutes":1,"decision_reason":"Ticket verified"}`, chefALogin.cookie, chefALogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approved breakGlassGrantAPIResponse
	decodeJSON(t, rec, &approved)
	if approved.Status != "approved" || approved.ExpiresAt == nil || approved.DecidedBy == nil || *approved.DecidedBy != chefA.ID {
		t.Fatalf("approved grant = %+v", approved)
	}

	rec = doRequest(handler, http.MethodGet, patientPath, "", adminLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approved read status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patient breakGlassPatientAPIResponse
	decodeJSON(t, rec, &patient)
	if patient.ID != patientID || patient.FirstName != "Anna" || patient.LastName != "Support" {
		t.Fatalf("read patient = %+v", patient)
	}

	// The read and its grant id are audited, but the free-text reason is not.
	assertBreakGlassAudit(t, queries, tenantA.ID, "break_glass.patient_read", grant.ID, "Support ticket 4711")

	// Revocation closes the window immediately.
	revokePath := "/api/break-glass/grants/" + grant.ID.String() + "/revoke"
	revokeBody := fmt.Sprintf(`{"tenant_id":%q,"reason":"Ticket closed"}`, tenantA.ID)
	rec = doRequest(handler, http.MethodPost, revokePath, revokeBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var revoked breakGlassGrantAPIResponse
	decodeJSON(t, rec, &revoked)
	if revoked.Status != "revoked" || revoked.RevokedAt == nil {
		t.Fatalf("revoked grant = %+v", revoked)
	}
	if rec := doRequest(handler, http.MethodGet, patientPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read after revoke status = %d, want 403", rec.Code)
	}

	// Denial is a terminal decision.
	rec = doRequest(handler, http.MethodPost, "/api/break-glass/grants", requestBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create denied grant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var deniedGrant breakGlassGrantAPIResponse
	decodeJSON(t, rec, &deniedGrant)
	denyPath := "/api/break-glass/grants/" + deniedGrant.ID.String() + "/deny"
	rec = doRequest(handler, http.MethodPost, denyPath, `{"decision_reason":"No legal basis"}`, chefALogin.cookie, chefALogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("deny status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var denied breakGlassGrantAPIResponse
	decodeJSON(t, rec, &denied)
	if denied.Status != "denied" {
		t.Fatalf("denied status = %q, want denied", denied.Status)
	}
	deniedPatientPath := "/api/break-glass/grants/" + deniedGrant.ID.String() + "/patient?tenant_id=" + tenantA.ID.String()
	if rec := doRequest(handler, http.MethodGet, deniedPatientPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read after deny status = %d, want 403", rec.Code)
	}

	// Expiry is enforced by clock_timestamp() in SQL. Move the window into the
	// past so the test does not sleep for a minute.
	rec = doRequest(handler, http.MethodPost, "/api/break-glass/grants", requestBody, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create expiring grant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var expiring breakGlassGrantAPIResponse
	decodeJSON(t, rec, &expiring)
	expirePath := "/api/break-glass/grants/" + expiring.ID.String() + "/approve"
	if rec := doRequest(handler, http.MethodPost, expirePath, `{"duration_minutes":1}`, chefALogin.cookie, chefALogin.csrf); rec.Code != http.StatusOK {
		t.Fatalf("approve expiring grant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := tx.ExecContext(context.Background(),
		"UPDATE break_glass_grants SET requested_at = clock_timestamp() - interval '2 minutes', expires_at = clock_timestamp() - interval '1 second' WHERE id = $1", expiring.ID); err != nil {
		t.Fatalf("expire grant: %v", err)
	}
	expiringPatientPath := "/api/break-glass/grants/" + expiring.ID.String() + "/patient?tenant_id=" + tenantA.ID.String()
	if rec := doRequest(handler, http.MethodGet, expiringPatientPath, "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read after expiry status = %d, want 403", rec.Code)
	}
	rec = doRequest(handler, http.MethodGet, "/api/break-glass/grants?tenant_id="+tenantA.ID.String(), "", adminLogin.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list after expiry status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var afterExpiry []breakGlassGrantAPIResponse
	decodeJSON(t, rec, &afterExpiry)
	if len(afterExpiry) != 3 {
		t.Fatalf("requester grants after expiry = %d, want 3", len(afterExpiry))
	}
	var sawExpired bool
	for _, item := range afterExpiry {
		if item.ID == expiring.ID && item.Status == "expired" {
			sawExpired = true
		}
	}
	if !sawExpired {
		t.Fatalf("expired grant not reported as expired: %+v", afterExpiry)
	}

	// Employees cannot request, list, decide or read through break-glass.
	employeeRequest := fmt.Sprintf(`{"tenant_id":%q,"patient_id":%q,"reason":"not allowed"}`, tenantA.ID, patientID)
	if rec := doRequest(handler, http.MethodPost, "/api/break-glass/grants", employeeRequest, employeeALogin.cookie, employeeALogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("employee request status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/api/break-glass/grants", "", employeeALogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee list status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, patientPath, "", employeeALogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee patient read status = %d, want 403", rec.Code)
	}

	// Every forbidden attempt leaves an access.denied row in the tenant. The
	// handler commits this row even though the request is rejected.
	deniedEvents, err := queries.ListAuditEvents(context.Background(), db.ListAuditEventsParams{
		TenantID: tenantA.ID,
		Action:   "access.denied",
		RowLimit: 50,
	})
	if err != nil {
		t.Fatalf("list denied audit events: %v", err)
	}
	if len(deniedEvents) < 3 {
		t.Fatalf("denied audit events = %d, want at least 3", len(deniedEvents))
	}

	// A missing reason and an unknown patient are rejected.
	missingReason := fmt.Sprintf(`{"tenant_id":%q,"patient_id":%q,"reason":"  "}`, tenantA.ID, patientID)
	if rec := doRequest(handler, http.MethodPost, "/api/break-glass/grants", missingReason, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reason status = %d, want 400", rec.Code)
	}
	unknownPatient := fmt.Sprintf(`{"tenant_id":%q,"patient_id":%q,"reason":"unknown"}`, tenantA.ID, uuid.New())
	if rec := doRequest(handler, http.MethodPost, "/api/break-glass/grants", unknownPatient, adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown patient status = %d, want 404", rec.Code)
	}

	// Break-glass has no write path. The patient route is GET only.
	if rec := doRequest(handler, http.MethodPost, patientPath, "", adminLogin.cookie, adminLogin.csrf); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write through break-glass status = %d, want 405", rec.Code)
	}
}

func assertBreakGlassAudit(t *testing.T, queries *db.Queries, tenantID uuid.UUID, action string, grantID uuid.UUID, forbiddenReason string) {
	t.Helper()

	events, err := queries.ListAuditEvents(context.Background(), db.ListAuditEventsParams{
		TenantID: tenantID,
		Action:   action,
		RowLimit: 50,
	})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no %s audit event", action)
	}
	var sawGrant bool
	for _, event := range events {
		if strings.Contains(string(event.Metadata), grantID.String()) {
			sawGrant = true
		}
		if forbiddenReason != "" && strings.Contains(string(event.Metadata), forbiddenReason) {
			t.Fatalf("audit metadata contains the free-text reason: %s", event.Metadata)
		}
	}
	if !sawGrant {
		t.Fatalf("no %s audit event references grant %s", action, grantID)
	}
}
