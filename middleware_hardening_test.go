package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSecurityHeaders(t *testing.T) {
	cfg := &apiConfig{}
	handler := cfg.middlewareSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/patients?q=secret", nil))

	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	}
	for name, want := range headers {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("Content-Security-Policy is missing")
	}
}

func TestBodyLimitRejectsLargeBody(t *testing.T) {
	cfg := &apiConfig{}
	handler := cfg.middlewareBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	body := strings.Repeat("a", maxRequestBodyBytes+1)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/patients", strings.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRecoverReturns500WithoutPanicDetails(t *testing.T) {
	cfg := &apiConfig{}
	handler := cfg.middlewareRecover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret panic detail")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/patients", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret panic detail") {
		t.Fatal("response leaked the panic detail")
	}
}

func TestLoginRateLimiter(t *testing.T) {
	limiter := newLoginRateLimiter(2, time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	if !limiter.allow("1.2.3.4") || !limiter.allow("1.2.3.4") {
		t.Fatal("first two attempts were denied")
	}
	if limiter.allow("1.2.3.4") {
		t.Fatal("third attempt was allowed")
	}
	if !limiter.allow("5.6.7.8") {
		t.Fatal("a different client was limited")
	}

	now = now.Add(time.Minute + time.Second)
	if !limiter.allow("1.2.3.4") {
		t.Fatal("attempt after the window was denied")
	}
}

func TestLoginRateLimitMiddleware(t *testing.T) {
	cfg := &apiConfig{loginLimiter: newLoginRateLimiter(1, time.Minute)}
	handler := cfg.middlewareLoginRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/login", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first login status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/login", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second login status = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header is missing")
	}
}

func TestLoggingDoesNotLogQueryString(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	cfg := &apiConfig{}
	handler := cfg.middlewareLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/patients?q=SecretTerm", nil))

	if strings.Contains(buf.String(), "SecretTerm") {
		t.Fatalf("log output contains the search term: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "/api/patients") {
		t.Fatalf("log output is missing the request path: %s", buf.String())
	}
}
