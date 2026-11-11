package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/observability"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestAuthenticationHardening covers Point 2 end to end: MFA login, recovery
// codes, enrollment enforcement, password reset, user disable and the session
// purge.
func TestAuthenticationHardening(t *testing.T) {
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

	t.Run("mfa challenge is single use and wrong codes are counted", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@mfa.de", "secret", "chef")

		rec := doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, chef.Email), nil, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("MFA challenge response set a session cookie")
		}
		var challenge struct {
			MFARequired bool   `json:"mfa_required"`
			Challenge   string `json:"challenge"`
		}
		decodeJSON(t, rec, &challenge)
		if !challenge.MFARequired || challenge.Challenge == "" {
			t.Fatalf("bad challenge response: %+v", challenge)
		}

		correct, err := auth.TOTPCode(testTOTPSecret(chef.Email), time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		wrong := "000000"
		if correct == wrong {
			wrong = "111111"
		}

		rec = doRequest(handler, http.MethodPost, "/api/login/mfa",
			fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge.Challenge, wrong), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code status = %d, want 401", rec.Code)
		}

		var attempts int
		if err := tx.QueryRow("SELECT attempts FROM mfa_challenges WHERE token_hash = $1",
			auth.HashToken(challenge.Challenge)).Scan(&attempts); err != nil {
			t.Fatalf("read challenge attempts: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("attempts = %d, want 1", attempts)
		}

		rec = doRequest(handler, http.MethodPost, "/api/login/mfa",
			fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge.Challenge, correct), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("correct code status = %d, body = %s", rec.Code, rec.Body.String())
		}
		login := loginFromRecorder(t, rec)

		// Replaying the consumed challenge must fail.
		rec = doRequest(handler, http.MethodPost, "/api/login/mfa",
			fmt.Sprintf(`{"challenge":%q,"code":%q}`, challenge.Challenge, correct), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("replayed challenge status = %d, want 401", rec.Code)
		}

		rec = doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/me after MFA status = %d, want 200", rec.Code)
		}
	})

	t.Run("recovery code works once", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@recovery.de", "secret", "chef")
		const recoveryCode = "AAAAA-BBBBB"
		if err := queries.CreateRecoveryCode(context.Background(), db.CreateRecoveryCodeParams{
			TenantID: tenant.ID,
			UserID:   chef.ID,
			CodeHash: auth.HashRecoveryCode(recoveryCode),
		}); err != nil {
			t.Fatalf("seed recovery code: %v", err)
		}

		challenge := requestMFAChallenge(t, handler, chef.Email)
		rec := doRequest(handler, http.MethodPost, "/api/login/mfa",
			fmt.Sprintf(`{"challenge":%q,"recovery_code":%q}`, challenge, recoveryCode), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("recovery login status = %d, body = %s", rec.Code, rec.Body.String())
		}

		challenge = requestMFAChallenge(t, handler, chef.Email)
		rec = doRequest(handler, http.MethodPost, "/api/login/mfa",
			fmt.Sprintf(`{"challenge":%q,"recovery_code":%q}`, challenge, recoveryCode), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("reused recovery code status = %d, want 401", rec.Code)
		}
	})

	t.Run("privileged account must enroll before other routes", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithoutMFA(t, queries, tenant.ID, "chef@enroll.de", "secret", "chef")

		rec := doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, chef.Email), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var loginResp struct {
			CSRFToken             string `json:"csrf_token"`
			MFAEnrollmentRequired bool   `json:"mfa_enrollment_required"`
		}
		decodeJSON(t, rec, &loginResp)
		if !loginResp.MFAEnrollmentRequired {
			t.Fatal("login did not require MFA enrollment")
		}
		login := loginFromRecorder(t, rec)

		rec = doRequest(handler, http.MethodGet, "/api/users", "", login.cookie, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET /api/users before enrollment = %d, want 403", rec.Code)
		}
		rec = doRequest(handler, http.MethodGet, "/api/me", "", login.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/me before enrollment = %d, want 200", rec.Code)
		}

		rec = doRequest(handler, http.MethodPost, "/api/mfa/enroll", "", login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("enroll status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var enroll struct {
			Secret string `json:"secret"`
			URI    string `json:"otpauth_uri"`
		}
		decodeJSON(t, rec, &enroll)
		if enroll.Secret == "" || enroll.URI == "" {
			t.Fatalf("bad enrollment response: %+v", enroll)
		}

		rec = doRequest(handler, http.MethodPost, "/api/mfa/confirm", `{"code":"000000"}`, login.cookie, login.csrf)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("wrong confirm code status = %d, want 400", rec.Code)
		}

		code, err := auth.TOTPCode(enroll.Secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		rec = doRequest(handler, http.MethodPost, "/api/mfa/confirm",
			fmt.Sprintf(`{"code":%q}`, code), login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("confirm status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var confirm struct {
			RecoveryCodes []string `json:"recovery_codes"`
		}
		decodeJSON(t, rec, &confirm)
		if len(confirm.RecoveryCodes) != 10 {
			t.Fatalf("recovery code count = %d, want 10", len(confirm.RecoveryCodes))
		}

		rec = doRequest(handler, http.MethodGet, "/api/users", "", login.cookie, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/users after enrollment = %d, want 200", rec.Code)
		}

		var enabled bool
		var encrypted []byte
		if err := tx.QueryRow("SELECT mfa_enabled, mfa_secret_enc FROM users WHERE id = $1", chef.ID).
			Scan(&enabled, &encrypted); err != nil {
			t.Fatalf("read MFA state: %v", err)
		}
		if !enabled {
			t.Fatal("MFA was not enabled")
		}
		if string(encrypted) == enroll.Secret {
			t.Fatal("MFA secret is stored in plaintext")
		}
	})

	t.Run("disabling mfa restores the enrollment gate", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@disable-mfa.de", "secret", "chef")
		login := loginAs(t, handler, chef.Email, "secret")
		code, err := auth.TOTPCode(testTOTPSecret(chef.Email), time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}

		rec := doRequest(handler, http.MethodPost, "/api/mfa/disable",
			fmt.Sprintf(`{"password":"wrong","code":%q}`, code), login.cookie, login.csrf)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("disable with wrong password status = %d, want 401", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/mfa/disable",
			`{"password":"secret","code":"000000"}`, login.cookie, login.csrf)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("disable with wrong code status = %d, want 401", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/mfa/disable",
			fmt.Sprintf(`{"password":"secret","code":%q}`, code), login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable status = %d, body = %s", rec.Code, rec.Body.String())
		}

		rec = doRequest(handler, http.MethodGet, "/api/users", "", login.cookie, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET /api/users after disabling MFA = %d, want 403", rec.Code)
		}
	})

	t.Run("self-service password reset is single use and revokes sessions", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		cfg.platform = "dev"
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@reset.de", "secret", "employee")
		oldLogin := loginAs(t, handler, employee.Email, "secret")

		rec := doRequest(handler, http.MethodPost, "/api/password-reset/request",
			fmt.Sprintf(`{"email":%q}`, employee.Email), nil, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("reset request status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var requested struct {
			Token string `json:"token"`
		}
		decodeJSON(t, rec, &requested)
		if requested.Token == "" {
			t.Fatal("dev reset request did not return a token")
		}

		rec = doRequest(handler, http.MethodPost, "/api/password-reset/confirm",
			fmt.Sprintf(`{"token":%q,"password":"newsecret"}`, requested.Token), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("reset confirm status = %d, body = %s", rec.Code, rec.Body.String())
		}

		rec = doRequest(handler, http.MethodGet, "/api/me", "", oldLogin.cookie, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("old session after reset = %d, want 401", rec.Code)
		}

		rec = doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, employee.Email), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("old password login = %d, want 401", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"newsecret"}`, employee.Email), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("new password login = %d, body = %s", rec.Code, rec.Body.String())
		}

		rec = doRequest(handler, http.MethodPost, "/api/password-reset/confirm",
			fmt.Sprintf(`{"token":%q,"password":"thirdsecret"}`, requested.Token), nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("reused reset token = %d, want 401", rec.Code)
		}

		// Unknown accounts get the same generic answer and no token.
		rec = doRequest(handler, http.MethodPost, "/api/password-reset/request",
			`{"email":"nobody@example.com"}`, nil, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("unknown email status = %d, want 202", rec.Code)
		}
		var unknown struct {
			Token string `json:"token"`
		}
		decodeJSON(t, rec, &unknown)
		if unknown.Token != "" {
			t.Fatal("unknown account leaked a reset token")
		}
	})

	t.Run("admin can issue a reset but employees cannot", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@admin-reset.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@admin-reset.de", "secret", "employee")
		admin := createUserWithPassword(t, queries, tenant.ID, "admin@admin-reset.de", "secret", "platform_admin")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		employeeLogin := loginAs(t, handler, employee.Email, "secret")

		rec := doRequest(handler, http.MethodPost, "/api/users/"+employee.ID.String()+"/password-reset",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusCreated {
			t.Fatalf("admin reset status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var issued struct {
			Token string `json:"token"`
		}
		decodeJSON(t, rec, &issued)
		if issued.Token == "" {
			t.Fatal("admin reset returned no token")
		}

		rec = doRequest(handler, http.MethodPost, "/api/password-reset/confirm",
			fmt.Sprintf(`{"token":%q,"password":"changed"}`, issued.Token), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("confirm admin reset status = %d, body = %s", rec.Code, rec.Body.String())
		}

		// Confirming the reset revoked the employee's old sessions, so the
		// authorization check needs a fresh session with the new password.
		// Regression: this used the revoked session and saw 401, not 403.
		employeeLogin = loginAs(t, handler, employee.Email, "changed")
		rec = doRequest(handler, http.MethodPost, "/api/users/"+chef.ID.String()+"/password-reset",
			"", employeeLogin.cookie, employeeLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("employee reset status = %d, want 403", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/users/"+admin.ID.String()+"/password-reset",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("chef resetting platform admin status = %d, want 403", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/users/"+chef.ID.String()+"/password-reset",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("chef self reset status = %d, want 400", rec.Code)
		}
	})

	t.Run("disable and enable revoke sessions and respect guard rails", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@disable.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@disable.de", "secret", "employee")
		admin := createUserWithPassword(t, queries, tenant.ID, "admin@disable.de", "secret", "platform_admin")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		employeeLogin := loginAs(t, handler, employee.Email, "secret")

		rec := doRequest(handler, http.MethodPost, "/api/users/"+employee.ID.String()+"/disable",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable employee status = %d, body = %s", rec.Code, rec.Body.String())
		}
		rec = doRequest(handler, http.MethodGet, "/api/me", "", employeeLogin.cookie, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("disabled employee session = %d, want 401", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, employee.Email), nil, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("disabled employee login = %d, want 403", rec.Code)
		}

		rec = doRequest(handler, http.MethodPost, "/api/users/"+employee.ID.String()+"/enable",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("enable employee status = %d, body = %s", rec.Code, rec.Body.String())
		}
		rec = doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, employee.Email), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("enabled employee login = %d, body = %s", rec.Code, rec.Body.String())
		}

		rec = doRequest(handler, http.MethodPost, "/api/users/"+chef.ID.String()+"/disable",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("self disable status = %d, want 403", rec.Code)
		}
		rec = doRequest(handler, http.MethodPost, "/api/users/"+admin.ID.String()+"/disable",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("chef disabling platform admin status = %d, want 403", rec.Code)
		}

		// The only active chef cannot be disabled. A platform admin is allowed
		// to manage a chef, so the last-chef guard is what blocks this.
		adminLogin := loginAs(t, handler, admin.Email, "secret")
		rec = doRequest(handler, http.MethodPost, "/api/users/"+chef.ID.String()+"/disable",
			"", adminLogin.cookie, adminLogin.csrf)
		if rec.Code != http.StatusConflict {
			t.Fatalf("last chef disable status = %d, want 409", rec.Code)
		}

		otherChef := createUserWithPassword(t, queries, tenant.ID, "chef2@disable.de", "secret", "chef")
		rec = doRequest(handler, http.MethodPost, "/api/users/"+otherChef.ID.String()+"/disable",
			"", chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable second chef status = %d, body = %s", rec.Code, rec.Body.String())
		}

		var disabled, enabled int
		if err := tx.QueryRow("SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = 'user.disabled'", tenant.ID).Scan(&disabled); err != nil {
			t.Fatalf("count disabled audit: %v", err)
		}
		if err := tx.QueryRow("SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = 'user.enabled'", tenant.ID).Scan(&enabled); err != nil {
			t.Fatalf("count enabled audit: %v", err)
		}
		if disabled < 2 || enabled != 1 {
			t.Fatalf("audit counts disabled=%d enabled=%d, want disabled>=2 enabled=1", disabled, enabled)
		}
	})

	t.Run("role change revokes the old session", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@role.de", "secret", "chef")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@role.de", "secret", "employee")

		chefLogin := loginAs(t, handler, chef.Email, "secret")
		employeeLogin := loginAs(t, handler, employee.Email, "secret")

		rec := doRequest(handler, http.MethodPatch, "/api/users/"+employee.ID.String(),
			`{"role":"chef"}`, chefLogin.cookie, chefLogin.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("role change status = %d, body = %s", rec.Code, rec.Body.String())
		}
		rec = doRequest(handler, http.MethodGet, "/api/me", "", employeeLogin.cookie, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("old role session = %d, want 401", rec.Code)
		}

		// A promoted account has no MFA yet, so login returns a restricted
		// session and the enrollment flag.
		rec = doRequest(handler, http.MethodPost, "/api/login",
			fmt.Sprintf(`{"email":%q,"password":"secret"}`, employee.Email), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("promoted login status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			MFAEnrollmentRequired bool `json:"mfa_enrollment_required"`
		}
		decodeJSON(t, rec, &resp)
		if !resp.MFAEnrollmentRequired {
			t.Fatal("promoted account was not forced to enroll MFA")
		}
	})

	t.Run("session purge removes expired and revoked rows and audits it", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		cfg.metrics = observability.NewRegistry()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@purge.de", "secret", "employee")
		ctx := context.Background()

		expiredToken := mustToken(t)
		if _, err := queries.CreateSession(ctx, db.CreateSessionParams{
			TokenHash: auth.HashToken(expiredToken),
			UserID:    employee.ID,
			TenantID:  tenant.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("create expired session: %v", err)
		}

		revokedToken := mustToken(t)
		if _, err := queries.CreateSession(ctx, db.CreateSessionParams{
			TokenHash: auth.HashToken(revokedToken),
			UserID:    employee.ID,
			TenantID:  tenant.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("create revoked session: %v", err)
		}
		if err := queries.RevokeSession(ctx, auth.HashToken(revokedToken)); err != nil {
			t.Fatalf("revoke session: %v", err)
		}

		activeToken := mustToken(t)
		if _, err := queries.CreateSession(ctx, db.CreateSessionParams{
			TokenHash: auth.HashToken(activeToken),
			UserID:    employee.ID,
			TenantID:  tenant.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("create active session: %v", err)
		}

		deleted, err := cfg.purgeExpiredAuthRows(ctx)
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if deleted < 2 {
			t.Fatalf("purged %d sessions, want at least 2", deleted)
		}

		var remaining int
		if err := tx.QueryRow("SELECT count(*) FROM sessions WHERE user_id = $1", employee.ID).Scan(&remaining); err != nil {
			t.Fatalf("count sessions: %v", err)
		}
		if remaining != 1 {
			t.Fatalf("remaining sessions = %d, want 1", remaining)
		}

		var audits int
		if err := tx.QueryRow("SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = 'auth.sessions_purged'", tenant.ID).Scan(&audits); err != nil {
			t.Fatalf("count purge audit: %v", err)
		}
		if audits < 1 {
			t.Fatal("session purge did not write an audit event")
		}
		if !strings.Contains(cfg.metrics.Render(), "altserv_sessions_purged_total") {
			t.Fatal("session purge metric is missing")
		}
	})
}

func requestMFAChallenge(t *testing.T, handler http.Handler, email string) string {
	t.Helper()
	rec := doRequest(handler, http.MethodPost, "/api/login",
		fmt.Sprintf(`{"email":%q,"password":"secret"}`, email), nil, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	decodeJSON(t, rec, &challenge)
	if challenge.Challenge == "" {
		t.Fatal("login returned no MFA challenge")
	}
	return challenge.Challenge
}

func loginFromRecorder(t *testing.T, rec *httptest.ResponseRecorder) loginResult {
	t.Helper()

	var resp struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, rec, &resp)

	var session *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			session = cookie
			break
		}
	}
	if session == nil {
		t.Fatal("response has no session cookie")
	}
	if resp.CSRFToken == "" {
		t.Fatal("response has no csrf_token")
	}
	return loginResult{cookie: session, csrf: resp.CSRFToken}
}

func mustToken(t *testing.T) string {
	t.Helper()
	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return token
}
