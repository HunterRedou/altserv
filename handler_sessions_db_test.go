package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/activities"
	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestSessionFlow is an integration test for login, session auth, CSRF and
// tenant isolation. It is skipped unless TEST_DB_URL is set.
//
// Setup:
//
//	TEST_DB_URL='postgres://user:pass@localhost:5432/altserv_test?sslmode=disable' \
//	  go test -run TestSessionFlow -v .
//
// Every test runs inside a transaction that is rolled back.
func TestSessionFlow(t *testing.T) {
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

	t.Run("login sets a hardened cookie and csrf token", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		user := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")

		login := loginAs(t, handler, user.Email, "secret")

		if login.cookie.Name != sessionCookieName {
			t.Fatalf("cookie name = %q, want %q", login.cookie.Name, sessionCookieName)
		}
		if !login.cookie.HttpOnly {
			t.Error("session cookie is not HttpOnly")
		}
		if !login.cookie.Secure {
			t.Error("session cookie is not Secure")
		}
		if login.cookie.Path != "/" {
			t.Errorf("cookie path = %q, want %q", login.cookie.Path, "/")
		}
		if login.cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie SameSite = %v, want Lax", login.cookie.SameSite)
		}
		if login.csrf == "" {
			t.Fatal("login response has no csrf_token")
		}

		rec := doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/me status = %d, want 200", rec.Code)
		}
		var me User
		decodeJSON(t, rec, &me)
		if me.Email != user.Email {
			t.Errorf("me.email = %q, want %q", me.Email, user.Email)
		}
		if me.TenantID != tenant.ID {
			t.Errorf("me.tenant_id = %s, want %s", me.TenantID, tenant.ID)
		}

		rec = doRequest(handler, http.MethodGet, "/api/me", "", nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET /api/me without cookie status = %d, want 401", rec.Code)
		}

		// The database must only contain the hash of the session token.
		var tokenHash []byte
		if err := tx.QueryRow("SELECT token_hash FROM sessions WHERE user_id = $1", user.ID).Scan(&tokenHash); err != nil {
			t.Fatalf("read session: %v", err)
		}
		if !bytes.Equal(tokenHash, auth.HashToken(login.cookie.Value)) {
			t.Fatal("stored token_hash is not the SHA-256 of the session token")
		}
		if string(tokenHash) == login.cookie.Value {
			t.Fatal("session token is stored in plaintext")
		}
	})

	t.Run("expired and revoked sessions are rejected", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		user := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")

		token, err := auth.GenerateToken()
		if err != nil {
			t.Fatalf("generate token: %v", err)
		}
		_, err = queries.CreateSession(context.Background(), db.CreateSessionParams{
			TokenHash: auth.HashToken(token),
			UserID:    user.ID,
			TenantID:  tenant.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("create expired session: %v", err)
		}

		rec := doRequest(handler, http.MethodGet, "/api/me", "", &http.Cookie{Name: sessionCookieName, Value: token}, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expired session status = %d, want 401", rec.Code)
		}
	})

	t.Run("users are listed per tenant only", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenantA := createTenant(t, queries, "valid")
		tenantB := createTenant(t, queries, "valid")
		chefA := createUserWithPassword(t, queries, tenantA.ID, "chef-a@a.de", "secret", "chef")
		employeeA := createUserWithPassword(t, queries, tenantA.ID, "employee-a@a.de", "secret", "employee")
		createUserWithPassword(t, queries, tenantB.ID, "chef-b@b.de", "secret", "chef")

		login := loginAs(t, handler, chefA.Email, "secret")
		rec := doRequest(handler, http.MethodGet, "/api/users", "", login.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/users status = %d, want 200", rec.Code)
		}

		var users []User
		decodeJSON(t, rec, &users)
		if len(users) != 2 {
			t.Fatalf("got %d users, want 2 (tenant A only)", len(users))
		}
		for _, u := range users {
			if u.TenantID != tenantA.ID {
				t.Errorf("user %s belongs to tenant %s, want %s", u.Email, u.TenantID, tenantA.ID)
			}
		}
		if !containsUser(users, employeeA.Email) {
			t.Errorf("tenant A users do not contain %q", employeeA.Email)
		}
		if containsUser(users, "chef-b@b.de") {
			t.Error("tenant B user leaked into tenant A listing")
		}

		// A client-supplied tenant header must never override the session tenant.
		rec = doRequestWithHeaders(handler, http.MethodGet, "/api/users", "", login.cookie, "", map[string]string{
			"X-Tenant-ID": tenantB.ID.String(),
			"X-Tenant":    tenantB.ID.String(),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/users with tenant header status = %d, want 200", rec.Code)
		}
		var headerUsers []User
		decodeJSON(t, rec, &headerUsers)
		for _, u := range headerUsers {
			if u.TenantID != tenantA.ID {
				t.Errorf("tenant header spoof returned user %s from tenant %s", u.Email, u.TenantID)
			}
		}
	})

	t.Run("unsafe requests require a csrf token", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenantA := createTenant(t, queries, "valid")
		tenantB := createTenant(t, queries, "valid")
		chefA := createUserWithPassword(t, queries, tenantA.ID, "chef-a@a.de", "secret", "chef")
		createUserWithPassword(t, queries, tenantB.ID, "chef-b@b.de", "secret", "chef")

		login := loginAs(t, handler, chefA.Email, "secret")

		body := `{"name":"New","role":"employee","password":"secret","email":"new@a.de","tenant_id":"` + tenantB.ID.String() + `"}`

		rec := doRequest(handler, http.MethodPost, "/api/users", body, login.cookie, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST /api/users without csrf status = %d, want 403", rec.Code)
		}

		rec = doRequest(handler, http.MethodPost, "/api/users", body, login.cookie, "wrong-token")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST /api/users with wrong csrf status = %d, want 403", rec.Code)
		}

		rec = doRequest(handler, http.MethodPost, "/api/users", body, login.cookie, login.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/users status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var created User
		decodeJSON(t, rec, &created)
		// The client tried to spoof tenant B; the session tenant must win.
		if created.TenantID != tenantA.ID {
			t.Errorf("created user tenant_id = %s, want session tenant %s", created.TenantID, tenantA.ID)
		}
	})

	t.Run("employees cannot list or create users", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@a.de", "secret", "employee")

		login := loginAs(t, handler, employee.Email, "secret")

		rec := doRequest(handler, http.MethodGet, "/api/users", "", login.cookie, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("employee GET /api/users status = %d, want 403", rec.Code)
		}

		body := `{"name":"New","role":"employee","password":"secret","email":"new@a.de"}`
		rec = doRequest(handler, http.MethodPost, "/api/users", body, login.cookie, login.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("employee POST /api/users status = %d, want 403", rec.Code)
		}
	})

	t.Run("logout revokes the session", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@a.de", "secret", "chef")

		login := loginAs(t, handler, chef.Email, "secret")

		rec := doRequest(handler, http.MethodPost, "/api/logout", "", login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/logout status = %d, want 200", rec.Code)
		}

		rec = doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET /api/me after logout status = %d, want 401", rec.Code)
		}

		var revokedAt sql.NullTime
		if err := tx.QueryRow("SELECT revoked_at FROM sessions WHERE token_hash = $1", auth.HashToken(login.cookie.Value)).Scan(&revokedAt); err != nil {
			t.Fatalf("read session: %v", err)
		}
		if !revokedAt.Valid {
			t.Fatal("session was not marked revoked")
		}
	})

	t.Run("login rejects pending tenants and wrong passwords", func(t *testing.T) {
		handler, queries, tx := newSessionTestAPI(t, conn)
		defer tx.Rollback()

		pendingTenant := createTenant(t, queries, "pending")
		pendingUser := createUserWithPassword(t, queries, pendingTenant.ID, "pending@a.de", "secret", "chef")

		rec := doRequest(handler, http.MethodPost, "/api/login", fmt.Sprintf(`{"email":%q,"password":"secret"}`, pendingUser.Email), nil, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("pending tenant login status = %d, want 403", rec.Code)
		}

		validTenant := createTenant(t, queries, "valid")
		validUser := createUserWithPassword(t, queries, validTenant.ID, "valid@a.de", "secret", "chef")

		rec = doRequest(handler, http.MethodPost, "/api/login", fmt.Sprintf(`{"email":%q,"password":"wrong"}`, validUser.Email), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password login status = %d, want 401", rec.Code)
		}
	})
}

func newSessionTestAPI(t *testing.T, conn *sql.DB) (http.Handler, *db.Queries, *sql.Tx) {
	t.Helper()

	tx, queries := newTestTx(t, conn)
	cipher, err := patients.NewCipher(bytes.Repeat([]byte{9}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	searchIndexer, err := patients.NewSearchIndexer(bytes.Repeat([]byte{8}, 32), 1)
	if err != nil {
		t.Fatalf("NewSearchIndexer: %v", err)
	}
	recorder := audit.SQLRecorder{}
	assignmentService := assignments.NewService(assignments.NewRepository(), recorder)
	catalogRepository := catalog.NewRepository()
	catalogService := catalog.NewService(catalogRepository, recorder)
	activityService := activities.NewService(activities.NewRepository(cipher), catalogRepository, recorder, assignmentService)
	templateService := templates.NewService(templates.NewRepository(), catalogRepository, recorder, assignmentService)
	visitService := visits.NewService(visits.NewRepository(), recorder, assignmentService)
	cfg := &apiConfig{
		db:                queries,
		dbConn:            conn,
		platform:          "test",
		auditRecorder:     recorder,
		patientService:    patients.NewServiceWithSearch(patients.NewRepository(cipher), recorder, assignmentService, searchIndexer),
		assignmentService: assignmentService,
		catalogService:    catalogService,
		activityService:   activityService,
		templateService:   templateService,
		visitService:      visitService,
		tenantRunner: func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
			return db.WithTenantTx(ctx, tx, tenantID, fn)
		},
	}
	return cfg.routes(), queries, tx
}

func createTenant(t *testing.T, queries *db.Queries, status string) db.Tenant {
	t.Helper()

	ctx := context.Background()
	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          "tenant-" + uuid.NewString() + "@example.com",
		Ustid:          "DE123456789",
		Streetname:     "Teststrasse 1",
		Plz:            "10115",
		HashedPassword: "unused",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	tenant, err = queries.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{
		ID:     tenant.ID,
		Status: status,
	})
	if err != nil {
		t.Fatalf("set tenant status: %v", err)
	}
	return tenant
}

func createUserWithPassword(t *testing.T, queries *db.Queries, tenantID uuid.UUID, email, password, role string) db.User {
	t.Helper()

	hashed, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user, err := queries.CreateUser(context.Background(), db.CreateUserParams{
		Name:           strings.Split(email, "@")[0],
		Role:           role,
		HashedPassword: hashed,
		Email:          email,
		TenantID:       tenantID,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return user
}

type loginResult struct {
	cookie *http.Cookie
	csrf   string
}

func loginAs(t *testing.T, handler http.Handler, email, password string) loginResult {
	t.Helper()

	rec := doRequest(handler, http.MethodPost, "/api/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, rec, &resp)
	if resp.CSRFToken == "" {
		t.Fatal("login response has no csrf_token")
	}

	var sessionCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login response has no session cookie")
	}

	return loginResult{cookie: sessionCookie, csrf: resp.CSRFToken}
}

func doRequest(handler http.Handler, method, path, body string, session *http.Cookie, csrf string) *httptest.ResponseRecorder {
	return doRequestWithHeaders(handler, method, path, body, session, csrf, nil)
}

func doRequestWithHeaders(handler http.Handler, method, path, body string, session *http.Cookie, csrf string, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if session != nil {
		req.AddCookie(session)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

func containsUser(users []User, email string) bool {
	for _, user := range users {
		if user.Email == email {
			return true
		}
	}
	return false
}
