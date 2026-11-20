// Package mail sends account mails (password reset, MFA changes) through a
// Mailer interface. A real provider adapter follows in WP-17.
//
// Mail texts must not contain patient names or health data. A body can
// contain a secret such as a reset link, so adapters must never log it.
package mail

import (
	"context"
	"log"
	"sync"
)

const (
	// AdapterNone sends nothing. It is the default and is allowed everywhere.
	AdapterNone = "none"
	// AdapterLog writes recipient and subject to the process log. It is only
	// allowed when PLATFORM=dev.
	AdapterLog = "log"
)

// Message is one plain-text mail.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer delivers a Message.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// NoneMailer drops every message.
type NoneMailer struct{}

func (NoneMailer) Send(context.Context, Message) error { return nil }

// LogMailer is for development only. It logs the recipient and the subject.
// It never logs the body, because the body can contain a reset link.
type LogMailer struct {
	// Logger is the destination. Nil uses the standard logger.
	Logger *log.Logger
}

func (m LogMailer) Send(_ context.Context, msg Message) error {
	logger := m.Logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("mail (log adapter, body not logged): to=%q subject=%q", msg.To, msg.Subject)
	return nil
}

// FakeMailer records every message. It is for tests.
type FakeMailer struct {
	mu       sync.Mutex
	messages []Message
}

func (m *FakeMailer) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}

// Messages returns a copy of the recorded messages.
func (m *FakeMailer) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.messages...)
}

// New returns the Mailer for a validated MAIL_ADAPTER value.
func New(adapter string) Mailer {
	if adapter == AdapterLog {
		return LogMailer{}
	}
	return NoneMailer{}
}
