// Package e2e_test is a black-box smoke suite against a running altserv
// instance. It checks the transport contract that a load balancer and an
// operator rely on: health, readiness, security headers, metrics protection and
// an optional login/logout flow.
//
// Set E2E_BASE_URL to run it, for example:
//
//	E2E_BASE_URL=http://127.0.0.1:8080 go test ./tests/e2e/...
//
// The optional login flow needs a dedicated test account:
//
//	E2E_EMAIL=loadtest@example.invalid E2E_PASSWORD='...' \
//	  E2E_BASE_URL=https://altserv.example.invalid go test ./tests/e2e/...
//
// Never point the login flow at a real chef account. Use a dedicated employee
// account with synthetic patients.
package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"
)

const sessionCookieName = "__Host-session"

func baseURL(t *testing.T) string {
	t.Helper()

	base := strings.TrimRight(os.Getenv("E2E_BASE_URL"), "/")
	if base == "" {
		t.Skip("E2E_BASE_URL not set; skipping black-box e2e test")
	}
	return base
}

func newClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func get(t *testing.T, client *http.Client, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return response, body
}

func TestHealthEndpoints(t *testing.T) {
	base := baseURL(t)
	client := newClient(t)

	tests := []struct {
		path   string
		status string
	}{
		{path: "/healthz", status: "ok"},
		{path: "/readyz", status: "ready"},
	}
	for _, tt := range tests {
		response, body := get(t, client, base+tt.path, nil)
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200; body: %s", tt.path, response.StatusCode, body)
			continue
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("%s body is not JSON: %v; body: %s", tt.path, err, body)
			continue
		}
		if payload.Status != tt.status {
			t.Errorf("%s status = %q, want %q", tt.path, payload.Status, tt.status)
		}
		if response.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s has no X-Request-Id header", tt.path)
		}
	}
}

func TestUnauthenticatedMe(t *testing.T) {
	base := baseURL(t)
	client := newClient(t)

	response, body := get(t, client, base+"/api/me", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated /api/me status = %d, want 401; body: %s", response.StatusCode, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	base := baseURL(t)
	client := newClient(t)

	response, _ := get(t, client, base+"/healthz", nil)
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for name, value := range want {
		if got := response.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	for _, name := range []string{"Content-Security-Policy", "Permissions-Policy"} {
		if got := response.Header.Get(name); got == "" {
			t.Errorf("%s is missing", name)
		}
	}

	// API responses must never be cached, because they can contain patient
	// data. The unauthenticated 401 response still carries the header.
	response, _ = get(t, client, base+"/api/me", nil)
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on /api/me = %q, want %q", got, "no-store")
	}
}

func TestMetricsToken(t *testing.T) {
	base := baseURL(t)
	client := newClient(t)

	token := os.Getenv("E2E_METRICS_TOKEN")
	if token == "" {
		// Local development leaves /metrics open. A production check must set
		// E2E_METRICS_TOKEN so this test proves the endpoint is protected.
		response, _ := get(t, client, base+"/metrics", nil)
		if response.StatusCode != http.StatusOK {
			t.Errorf("unprotected /metrics status = %d, want 200 in local development", response.StatusCode)
		}
		t.Log("E2E_METRICS_TOKEN not set; only the local-development behaviour was checked")
		return
	}

	response, _ := get(t, client, base+"/metrics", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("/metrics without token status = %d, want 401", response.StatusCode)
	}

	response, body := get(t, client, base+"/metrics", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/metrics with token status = %d, want 200; body: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "altserv_") {
		t.Error("/metrics response does not contain altserv_ metrics")
	}
}

func TestLoginFlow(t *testing.T) {
	base := baseURL(t)
	email := os.Getenv("E2E_EMAIL")
	password := os.Getenv("E2E_PASSWORD")
	if email == "" || password == "" {
		t.Skip("E2E_EMAIL or E2E_PASSWORD not set; skipping the login flow")
	}

	client := newClient(t)
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, base+"/api/login", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST /api/login: %v", err)
	}
	defer response.Body.Close()
	loginBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}

	if response.StatusCode == http.StatusAccepted {
		t.Log("account requires MFA; skipping the authenticated part of the flow")
		return
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body: %s", response.StatusCode, loginBody)
	}

	var login struct {
		CSRFToken string `json:"csrf_token"`
		Email     string `json:"email"`
		Role      string `json:"role"`
	}
	if err := json.Unmarshal(loginBody, &login); err != nil {
		t.Fatalf("decode login response: %v; body: %s", err, loginBody)
	}
	if login.CSRFToken == "" {
		t.Fatal("login response has no csrf_token")
	}
	if login.Email != email {
		t.Errorf("login email = %q, want %q", login.Email, email)
	}

	var session string
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			session = cookie.Value
			break
		}
	}
	if session == "" {
		t.Fatalf("login response has no %s cookie", sessionCookieName)
	}
	cookieHeader := map[string]string{"Cookie": sessionCookieName + "=" + session}

	response, meBody := get(t, client, base+"/api/me", cookieHeader)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/api/me with session status = %d, want 200; body: %s", response.StatusCode, meBody)
	}
	var me struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(meBody, &me); err != nil {
		t.Fatalf("decode /api/me: %v; body: %s", err, meBody)
	}
	if me.Email != email {
		t.Errorf("/api/me email = %q, want %q", me.Email, email)
	}

	// CSRF must protect unsafe methods even with a valid session.
	logout, err := http.NewRequest(http.MethodPost, base+"/api/logout", nil)
	if err != nil {
		t.Fatalf("build logout request: %v", err)
	}
	logout.Header.Set("Cookie", sessionCookieName+"="+session)
	csrfResponse, err := client.Do(logout)
	if err != nil {
		t.Fatalf("POST /api/logout without CSRF: %v", err)
	}
	csrfResponse.Body.Close()
	if csrfResponse.StatusCode != http.StatusForbidden {
		t.Errorf("logout without CSRF status = %d, want 403", csrfResponse.StatusCode)
	}

	logout, err = http.NewRequest(http.MethodPost, base+"/api/logout", nil)
	if err != nil {
		t.Fatalf("build logout request: %v", err)
	}
	logout.Header.Set("Cookie", sessionCookieName+"="+session)
	logout.Header.Set("X-CSRF-Token", login.CSRFToken)
	logoutResponse, err := client.Do(logout)
	if err != nil {
		t.Fatalf("POST /api/logout with CSRF: %v", err)
	}
	logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusOK {
		t.Errorf("logout with CSRF status = %d, want 200", logoutResponse.StatusCode)
	}

	response, _ = get(t, client, base+"/api/me", cookieHeader)
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/me after logout status = %d, want 401", response.StatusCode)
	}
}
