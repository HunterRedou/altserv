package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func requestWithUser(req *http.Request, user authUser) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), authUserKey, user))
}

func TestCanManageUsers(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "platform_admin", want: true},
		{role: "chef", want: true},
		{role: "employee", want: false},
		{role: "", want: false},
	}

	for _, tt := range tests {
		if got := canManageUsers(tt.role); got != tt.want {
			t.Errorf("canManageUsers(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestCanAssignRole(t *testing.T) {
	tests := []struct {
		actor  string
		target string
		want   bool
	}{
		{actor: "platform_admin", target: "platform_admin", want: true},
		{actor: "platform_admin", target: "chef", want: true},
		{actor: "platform_admin", target: "employee", want: true},
		{actor: "chef", target: "chef", want: true},
		{actor: "chef", target: "employee", want: true},
		{actor: "chef", target: "platform_admin", want: false},
		{actor: "employee", target: "employee", want: false},
	}

	for _, tt := range tests {
		if got := canAssignRole(tt.actor, tt.target); got != tt.want {
			t.Errorf("canAssignRole(%q, %q) = %v, want %v", tt.actor, tt.target, got, tt.want)
		}
	}
}

func TestCanChangeRole(t *testing.T) {
	tests := []struct {
		name    string
		actor   string
		current string
		next    string
		want    bool
	}{
		{name: "admin to chef", actor: "platform_admin", current: "employee", next: "chef", want: true},
		{name: "admin to admin", actor: "platform_admin", current: "chef", next: "platform_admin", want: true},
		{name: "admin from admin", actor: "platform_admin", current: "platform_admin", next: "employee", want: true},
		{name: "chef promotes employee", actor: "chef", current: "employee", next: "chef", want: true},
		{name: "chef demotes chef", actor: "chef", current: "chef", next: "employee", want: true},
		{name: "chef cannot create admin", actor: "chef", current: "employee", next: "platform_admin", want: false},
		{name: "chef cannot demote admin", actor: "chef", current: "platform_admin", next: "employee", want: false},
		{name: "employee cannot change", actor: "employee", current: "employee", next: "chef", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canChangeRole(tt.actor, tt.current, tt.next); got != tt.want {
				t.Fatalf("canChangeRole(%q, %q, %q) = %v, want %v", tt.actor, tt.current, tt.next, got, tt.want)
			}
		})
	}
}

func TestHandlerUserRequiresAuth(t *testing.T) {
	cfg := &apiConfig{}
	body := `{"name":"A","role":"employee","password":"secret","email":"a@b.de"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	rec := httptest.NewRecorder()

	cfg.handlerUser(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandlerUserRejectsEmployee(t *testing.T) {
	cfg := &apiConfig{}
	body := `{"name":"A","role":"employee","password":"secret","email":"a@b.de"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req = requestWithUser(req, authUser{Role: "employee", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUser(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlerUserRejectsInvalidRole(t *testing.T) {
	cfg := &apiConfig{}
	body := `{"name":"A","role":"admin","password":"secret","email":"a@b.de"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req = requestWithUser(req, authUser{Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerUserRejectsChefAssigningPlatformAdmin(t *testing.T) {
	cfg := &apiConfig{}
	body := `{"name":"A","role":"platform_admin","password":"secret","email":"a@b.de"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req = requestWithUser(req, authUser{Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUser(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlerUserRejectsMalformedJSON(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{`))
	req = requestWithUser(req, authUser{Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerGetUsersRequiresAuth(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	cfg.handlerGetUsers(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandlerGetUsersRejectsEmployee(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req = requestWithUser(req, authUser{Role: "employee", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerGetUsers(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlerUpdateUserRoleRequiresAuth(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+uuid.NewString(), strings.NewReader(`{"role":"chef"}`))
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandlerUpdateUserRoleRejectsEmployee(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+uuid.NewString(), strings.NewReader(`{"role":"chef"}`))
	req = requestWithUser(req, authUser{ID: uuid.New(), Role: "employee", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlerUpdateUserRoleRejectsInvalidID(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/not-a-uuid", strings.NewReader(`{"role":"chef"}`))
	req = requestWithUser(req, authUser{ID: uuid.New(), Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerUpdateUserRoleRejectsSelfChange(t *testing.T) {
	actorID := uuid.New()
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+actorID.String(), strings.NewReader(`{"role":"employee"}`))
	req.SetPathValue("id", actorID.String())
	req = requestWithUser(req, authUser{ID: actorID, Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlerUpdateUserRoleRejectsInvalidRole(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+uuid.NewString(), strings.NewReader(`{"role":"admin"}`))
	req.SetPathValue("id", uuid.NewString())
	req = requestWithUser(req, authUser{ID: uuid.New(), Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerUpdateUserRoleRejectsMalformedJSON(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+uuid.NewString(), strings.NewReader(`{`))
	req.SetPathValue("id", uuid.NewString())
	req = requestWithUser(req, authUser{ID: uuid.New(), Role: "chef", TenantID: uuid.New()})
	rec := httptest.NewRecorder()

	cfg.handlerUpdateUserRole(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
