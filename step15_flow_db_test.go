package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestAuditHTTPFlow covers Point 15 end to end: user creation and role changes
// are audited, only a chef can read the log, denied reads are audited, filters
// work, tenant isolation holds and audit rows cannot be rewritten.
func TestAuditHTTPFlow(t *testing.T) {
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

	chefLogin := loginAs(t, handler, chef.Email, "secret")
	employeeLogin := loginAs(t, handler, employee.Email, "secret")
	adminLogin := loginAs(t, handler, admin.Email, "secret")

	// Creating a user writes a user.created event in the same transaction.
	createBody := `{"name":"New","role":"employee","password":"secret","email":"new@a.de"}`
	rec := doRequest(handler, http.MethodPost, "/api/users", createBody, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created User
	decodeJSON(t, rec, &created)
	if created.Role != "employee" || created.TenantID != tenant.ID {
		t.Fatalf("created user = %+v", created)
	}

	events := listAuditEvents(t, handler, chefLogin, "")
	createdEvent := findAuditEvent(t, events, "user.created", created.ID)
	var createdMeta map[string]any
	if err := json.Unmarshal(createdEvent.Metadata, &createdMeta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if createdMeta["role"] != "employee" {
		t.Fatalf("user.created metadata = %+v", createdMeta)
	}

	// A chef may promote an employee to chef and the change is audited.
	rec = doRequest(handler, http.MethodPatch, "/api/users/"+created.ID.String(), `{"role":"chef"}`, chefLogin.cookie, chefLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var promoted User
	decodeJSON(t, rec, &promoted)
	if promoted.Role != "chef" {
		t.Fatalf("promoted role = %q", promoted.Role)
	}

	events = listAuditEvents(t, handler, chefLogin, "?action=user.role_changed")
	if len(events) != 1 {
		t.Fatalf("role change events = %d, want 1", len(events))
	}
	roleEvent := events[0]
	if roleEvent.EntityID == nil || *roleEvent.EntityID != created.ID {
		t.Fatalf("role change entity = %v", roleEvent.EntityID)
	}
	var roleMeta map[string]any
	if err := json.Unmarshal(roleEvent.Metadata, &roleMeta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if roleMeta["old_role"] != "employee" || roleMeta["new_role"] != "chef" {
		t.Fatalf("role change metadata = %+v", roleMeta)
	}

	// A chef may not escalate to platform_admin or demote a platform_admin.
	if rec := doRequest(handler, http.MethodPatch, "/api/users/"+created.ID.String(), `{"role":"platform_admin"}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("chef escalation status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPatch, "/api/users/"+admin.ID.String(), `{"role":"employee"}`, chefLogin.cookie, chefLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("chef demote admin status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}

	// A platform admin may assign any role in the tenant.
	secondEmployee := createUserWithPassword(t, queries, tenant.ID, "employee2@a.de", "secret", "employee")
	rec = doRequest(handler, http.MethodPatch, "/api/users/"+secondEmployee.ID.String(), `{"role":"chef"}`, adminLogin.cookie, adminLogin.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin promote status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Employees and platform admins cannot read the audit log.
	if rec := doRequest(handler, http.MethodGet, "/api/audit-events", "", employeeLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee audit read status = %d, want 403", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/api/audit-events", "", adminLogin.cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin audit read status = %d, want 403", rec.Code)
	}

	// The denials are recorded, and the chef read itself is audited.
	denials := listAuditEvents(t, handler, chefLogin, "?action=access.denied")
	if len(denials) != 2 {
		t.Fatalf("denied events = %d, want 2", len(denials))
	}
	listed := listAuditEvents(t, handler, chefLogin, "?action=audit.listed")
	if len(listed) == 0 {
		t.Fatal("audit.listed event not found")
	}

	// Filters and limits are applied server-side.
	filtered := listAuditEvents(t, handler, chefLogin, "?entity_type=user&limit=2")
	if len(filtered) != 2 {
		t.Fatalf("filtered events = %d, want 2", len(filtered))
	}
	for _, event := range filtered {
		if event.EntityType != "user" {
			t.Fatalf("filtered entity type = %q", event.EntityType)
		}
	}
	if rec := doRequest(handler, http.MethodGet, "/api/audit-events?limit=9999", "", chefLogin.cookie, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit status = %d, want 400", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/api/audit-events?entity_id=nope", "", chefLogin.cookie, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad entity id status = %d, want 400", rec.Code)
	}

	// Tenant isolation: another tenant's events are never returned.
	otherTenant := createTenant(t, queries, "valid")
	otherChef := createUserWithPassword(t, queries, otherTenant.ID, "other-chef@b.de", "secret", "chef")
	otherLogin := loginAs(t, handler, otherChef.Email, "secret")
	if rec := doRequest(handler, http.MethodPatch, "/api/users/"+otherChef.ID.String(), `{"role":"employee"}`, otherLogin.cookie, otherLogin.csrf); rec.Code != http.StatusForbidden {
		t.Fatalf("self change status = %d, want 403", rec.Code)
	}
	otherEvents := listAuditEvents(t, handler, otherLogin, "")
	for _, event := range otherEvents {
		if event.TenantID != otherTenant.ID {
			t.Fatalf("leaked event from tenant %s", event.TenantID)
		}
	}
	for _, event := range listAuditEvents(t, handler, chefLogin, "") {
		if event.TenantID != tenant.ID {
			t.Fatalf("tenant A read leaked event from %s", event.TenantID)
		}
	}

	// The last chef of a tenant cannot be demoted, even by a platform admin.
	soloTenant := createTenant(t, queries, "valid")
	soloChef := createUserWithPassword(t, queries, soloTenant.ID, "solo-chef@c.de", "secret", "chef")
	soloAdmin := createUserWithPassword(t, queries, soloTenant.ID, "solo-admin@c.de", "secret", "platform_admin")
	soloAdminLogin := loginAs(t, handler, soloAdmin.Email, "secret")
	if rec := doRequest(handler, http.MethodPatch, "/api/users/"+soloChef.ID.String(), `{"role":"employee"}`, soloAdminLogin.cookie, soloAdminLogin.csrf); rec.Code != http.StatusConflict {
		t.Fatalf("last chef status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	// Audit rows are append-only at the database level.
	for _, statement := range []string{
		`UPDATE audit_events SET action = 'tampered'`,
		`DELETE FROM audit_events`,
		`TRUNCATE audit_events`,
	} {
		if _, err := tx.ExecContext(context.Background(), "SAVEPOINT audit_guard"); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := tx.ExecContext(context.Background(), statement); err == nil {
			t.Fatalf("statement was allowed: %s", statement)
		}
		if _, err := tx.ExecContext(context.Background(), "ROLLBACK TO SAVEPOINT audit_guard"); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	}
}

func listAuditEvents(t *testing.T, handler http.Handler, login loginResult, query string) []audit.EventRecord {
	t.Helper()

	rec := doRequest(handler, http.MethodGet, "/api/audit-events"+query, "", login.cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var events []audit.EventRecord
	decodeJSON(t, rec, &events)
	return events
}

func findAuditEvent(t *testing.T, events []audit.EventRecord, action string, entityID uuid.UUID) audit.EventRecord {
	t.Helper()

	for _, event := range events {
		if event.Action == action && event.EntityID != nil && *event.EntityID == entityID {
			return event
		}
	}
	t.Fatalf("audit event %s for %s not found in %v", action, entityID, events)
	return audit.EventRecord{}
}
