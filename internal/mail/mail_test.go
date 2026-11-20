package mail

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

func TestLogMailerDoesNotLogBody(t *testing.T) {
	const token = "reset-token-must-not-appear-in-logs"
	var out bytes.Buffer
	mailer := LogMailer{Logger: log.New(&out, "", 0)}

	msg := PasswordResetMessage("user@example.de", "https://altserv.example/password-reset#token="+token, 30*time.Minute)
	if err := mailer.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	logged := out.String()
	if strings.Contains(logged, token) {
		t.Fatalf("log output contains the reset token: %q", logged)
	}
	if !strings.Contains(logged, "user@example.de") || !strings.Contains(logged, msg.Subject) {
		t.Fatalf("log output %q does not name recipient and subject", logged)
	}
}

func TestFakeMailerRecordsMessages(t *testing.T) {
	var mailer FakeMailer
	if err := mailer.Send(context.Background(), MFAEnabledMessage("a@example.de")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := mailer.Send(context.Background(), MFADisabledMessage("b@example.de")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := mailer.Messages()
	if len(got) != 2 || got[0].To != "a@example.de" || got[1].To != "b@example.de" {
		t.Fatalf("messages = %+v", got)
	}
}

func TestNewSelectsAdapter(t *testing.T) {
	if _, ok := New(AdapterLog).(LogMailer); !ok {
		t.Fatal("New(log) is not a LogMailer")
	}
	if _, ok := New(AdapterNone).(NoneMailer); !ok {
		t.Fatal("New(none) is not a NoneMailer")
	}
}

func TestPasswordResetMessageContainsLink(t *testing.T) {
	msg := PasswordResetMessage("user@example.de", "https://altserv.example/password-reset#token=abc", 30*time.Minute)
	if !strings.Contains(msg.Text, "https://altserv.example/password-reset#token=abc") {
		t.Fatalf("body does not contain the link: %q", msg.Text)
	}
	if !strings.Contains(msg.Text, "30 Minuten") {
		t.Fatalf("body does not state the lifetime: %q", msg.Text)
	}
}
