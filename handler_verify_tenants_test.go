package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeTenantStore struct {
	updateCalls int
	deleteCalls int
	lastStatus  string
	updateErr   error
	deleteErr   error
}

func (f *fakeTenantStore) UpdateTenantStatus(_ context.Context, arg db.UpdateTenantStatusParams) (db.Tenant, error) {
	f.updateCalls++
	f.lastStatus = arg.Status
	if f.updateErr != nil {
		return db.Tenant{}, f.updateErr
	}
	return db.Tenant{ID: arg.ID, Status: arg.Status}, nil
}

func (f *fakeTenantStore) DeleteTenant(_ context.Context, id uuid.UUID) error {
	f.deleteCalls++
	return f.deleteErr
}

func TestVerifyTenant(t *testing.T) {
	tenantID := uuid.New()
	checkErr := errors.New("vies down")

	tests := []struct {
		name       string
		valid      bool
		checkErr   error
		updateErr  error
		deleteErr  error
		wantUpdate int
		wantDelete int
		wantErr    bool
	}{
		{
			name:       "valid tenant is marked valid",
			valid:      true,
			wantUpdate: 1,
		},
		{
			name:       "invalid tenant is deleted",
			valid:      false,
			wantDelete: 1,
		},
		{
			name:     "check error leaves tenant pending",
			checkErr: checkErr,
			wantErr:  true,
		},
		{
			name:       "update error is returned",
			valid:      true,
			updateErr:  errors.New("db down"),
			wantUpdate: 1,
			wantErr:    true,
		},
		{
			name:       "delete error is returned",
			valid:      false,
			deleteErr:  errors.New("db down"),
			wantDelete: 1,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeTenantStore{updateErr: tt.updateErr, deleteErr: tt.deleteErr}
			check := func(context.Context, string) (bool, error) {
				return tt.valid, tt.checkErr
			}

			err := verifyTenant(context.Background(), store, tenantID, "DE123456789", check)

			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if store.updateCalls != tt.wantUpdate {
				t.Errorf("UpdateTenantStatus calls = %d, want %d", store.updateCalls, tt.wantUpdate)
			}
			if store.deleteCalls != tt.wantDelete {
				t.Errorf("DeleteTenant calls = %d, want %d", store.deleteCalls, tt.wantDelete)
			}
			if tt.wantUpdate == 1 && store.lastStatus != "valid" {
				t.Errorf("status = %q, want %q", store.lastStatus, "valid")
			}
		})
	}
}

func TestValidUStIDFormat(t *testing.T) {
	tests := []struct {
		ustID string
		want  bool
	}{
		{ustID: "", want: false},
		{ustID: "D", want: false},
		{ustID: "DE", want: false},
		{ustID: "DE1", want: true},
		{ustID: "DE123456789", want: true},
	}

	for _, tt := range tests {
		if got := validUStIDFormat(tt.ustID); got != tt.want {
			t.Errorf("validUStIDFormat(%q) = %v, want %v", tt.ustID, got, tt.want)
		}
	}
}

func TestValidRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "platform_admin", want: true},
		{role: "chef", want: true},
		{role: "employee", want: true},
		{role: "admin", want: false},
		{role: "", want: false},
	}

	for _, tt := range tests {
		if got := validRole(tt.role); got != tt.want {
			t.Errorf("validRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestHandlerCreateTenantRejectsShortUstID(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty ust id",
			body: `{"email":"a@b.de","ustId":"","streetName":"X","plz":"12345","password":"secret"}`,
		},
		{
			name: "two characters",
			body: `{"email":"a@b.de","ustId":"DE","streetName":"X","plz":"12345","password":"secret"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// db is nil on purpose: the handler must reject the request before
			// touching the database.
			cfg := &apiConfig{}
			req := httptest.NewRequest(http.MethodPost, "/api/tenants", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			cfg.handlerCreateTenant(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandlerCreateTenantRejectsMalformedJSON(t *testing.T) {
	cfg := &apiConfig{}
	req := httptest.NewRequest(http.MethodPost, "/api/tenants", strings.NewReader(`{`))
	rec := httptest.NewRecorder()

	cfg.handlerCreateTenant(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
