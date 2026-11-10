package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRouter(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Auth") != "yes" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	csrf := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-Test-CSRF") != "yes" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	router := NewRouter(Handlers{
		App:           http.HandlerFunc(ok),
		Reset:         ok,
		Login:         ok,
		Logout:        ok,
		Me:            ok,
		Users:         ok,
		Create:        ok,
		Tenant:        ok,
		PatientCreate: ok,
		PatientList:   ok,
		PatientGet:    ok,
		Auth:          auth,
		CSRF:          csrf,
		RequestID: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "test-request-id")
				next.ServeHTTP(w, r)
			})
		},
	})

	tests := []struct {
		name   string
		method string
		path   string
		auth   bool
		csrf   bool
		want   int
	}{
		{name: "public login", method: http.MethodPost, path: "/api/login", want: http.StatusOK},
		{name: "public tenant creation", method: http.MethodPost, path: "/api/tenants", want: http.StatusOK},
		{name: "protected route without session", method: http.MethodGet, path: "/api/me", want: http.StatusUnauthorized},
		{name: "protected read with session", method: http.MethodGet, path: "/api/me", auth: true, want: http.StatusOK},
		{name: "protected write without csrf", method: http.MethodPost, path: "/api/users", auth: true, want: http.StatusForbidden},
		{name: "protected write with csrf", method: http.MethodPost, path: "/api/users", auth: true, csrf: true, want: http.StatusOK},
		{name: "unknown route", method: http.MethodGet, path: "/api/nope", want: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.auth {
				req.Header.Set("X-Test-Auth", "yes")
			}
			if tt.csrf {
				req.Header.Set("X-Test-CSRF", "yes")
			}
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
