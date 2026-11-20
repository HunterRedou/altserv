package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/mail"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestAccountMails covers WP-06: password-reset and MFA-change mails go
// through the Mailer interface, and no mail reveals whether an account exists.
func TestAccountMails(t *testing.T) {
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

	t.Run("reset request sends one message to the account address", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		mailer := &mail.FakeMailer{}
		cfg.mailer = mailer
		cfg.appBaseURL = "https://altserv.example/"
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@mail-reset.de", "secret", "employee")

		rec := doRequest(handler, http.MethodPost, "/api/password-reset/request",
			fmt.Sprintf(`{"email":%q}`, employee.Email), nil, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("reset request status = %d, body = %s", rec.Code, rec.Body.String())
		}

		sent := mailer.Messages()
		if len(sent) != 1 {
			t.Fatalf("sent %d messages, want 1", len(sent))
		}
		if sent[0].To != employee.Email {
			t.Fatalf("message to %q, want %q", sent[0].To, employee.Email)
		}
		const prefix = "https://altserv.example/password-reset#token="
		start := strings.Index(sent[0].Text, prefix)
		if start < 0 {
			t.Fatalf("message body has no reset link: %q", sent[0].Text)
		}
		token := strings.Fields(sent[0].Text[start+len(prefix):])[0]

		// The mailed token must work.
		rec = doRequest(handler, http.MethodPost, "/api/password-reset/confirm",
			fmt.Sprintf(`{"token":%q,"password":"newsecret"}`, token), nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("confirm with mailed token status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown address sends no message and gets the same answer", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		mailer := &mail.FakeMailer{}
		cfg.mailer = mailer
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@mail-unknown.de", "secret", "employee")

		known := doRequest(handler, http.MethodPost, "/api/password-reset/request",
			fmt.Sprintf(`{"email":%q}`, employee.Email), nil, "")
		before := len(mailer.Messages())
		unknown := doRequest(handler, http.MethodPost, "/api/password-reset/request",
			`{"email":"nobody@mail-unknown.de"}`, nil, "")

		if got := len(mailer.Messages()) - before; got != 0 {
			t.Fatalf("unknown address sent %d messages, want 0", got)
		}
		if known.Code != unknown.Code {
			t.Fatalf("status known = %d, unknown = %d", known.Code, unknown.Code)
		}
		if known.Body.String() != unknown.Body.String() {
			t.Fatalf("body known = %q, unknown = %q", known.Body.String(), unknown.Body.String())
		}
	})

	t.Run("log adapter does not log the reset token", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		cfg.platform = "dev"
		var logged bytes.Buffer
		cfg.mailer = mail.LogMailer{Logger: log.New(&logged, "", 0)}
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		employee := createUserWithPassword(t, queries, tenant.ID, "employee@mail-log.de", "secret", "employee")

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
		if !strings.Contains(logged.String(), employee.Email) {
			t.Fatalf("log adapter did not log the recipient: %q", logged.String())
		}
		if strings.Contains(logged.String(), requested.Token) {
			t.Fatalf("log output contains the reset token: %q", logged.String())
		}
	})

	t.Run("enabling mfa sends a notice", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		mailer := &mail.FakeMailer{}
		cfg.mailer = mailer
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithoutMFA(t, queries, tenant.ID, "chef@mail-mfa-on.de", "secret", "chef")
		login := loginAs(t, handler, chef.Email, "secret")

		rec := doRequest(handler, http.MethodPost, "/api/mfa/enroll", "", login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("enroll status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var enroll struct {
			Secret string `json:"secret"`
		}
		decodeJSON(t, rec, &enroll)
		if len(mailer.Messages()) != 0 {
			t.Fatal("enrollment start sent a mail before MFA was enabled")
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

		sent := mailer.Messages()
		if len(sent) != 1 || sent[0].To != chef.Email || sent[0].Subject != mail.MFAEnabledMessage("").Subject {
			t.Fatalf("messages = %+v, want one MFA-enabled notice to %s", sent, chef.Email)
		}
	})

	t.Run("disabling mfa sends a notice", func(t *testing.T) {
		cfg, queries, tx := newSessionTestAPIConfig(t, conn)
		defer tx.Rollback()
		mailer := &mail.FakeMailer{}
		cfg.mailer = mailer
		handler := cfg.routes()

		tenant := createTenant(t, queries, "valid")
		chef := createUserWithPassword(t, queries, tenant.ID, "chef@mail-mfa-off.de", "secret", "chef")
		login := loginAs(t, handler, chef.Email, "secret")
		code, err := auth.TOTPCode(testTOTPSecret(chef.Email), time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}

		rec := doRequest(handler, http.MethodPost, "/api/mfa/disable",
			`{"password":"wrong","code":"000000"}`, login.cookie, login.csrf)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed disable status = %d, want 401", rec.Code)
		}
		if len(mailer.Messages()) != 0 {
			t.Fatal("failed disable sent a mail")
		}

		rec = doRequest(handler, http.MethodPost, "/api/mfa/disable",
			fmt.Sprintf(`{"password":"secret","code":%q}`, code), login.cookie, login.csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable status = %d, body = %s", rec.Code, rec.Body.String())
		}

		sent := mailer.Messages()
		if len(sent) != 1 || sent[0].To != chef.Email || sent[0].Subject != mail.MFADisabledMessage("").Subject {
			t.Fatalf("messages = %+v, want one MFA-disabled notice to %s", sent, chef.Email)
		}
	})
}
