package main

import (
	"context"
	"log"
	"net/url"
	"strings"

	"github.com/HunterRedou/altserv/internal/mail"
)

// sendMail delivers msg and only logs a failure. The caller has already
// committed the change, and a public endpoint must give the same answer
// whether or not a mail was sent. The log line never contains the body.
func (cfg *apiConfig) sendMail(ctx context.Context, msg mail.Message) {
	if cfg.mailer == nil {
		return
	}
	if err := cfg.mailer.Send(ctx, msg); err != nil {
		log.Printf("mail: send %q failed: %v", msg.Subject, err)
	}
}

// passwordResetLink puts the token in the fragment, so browsers do not send
// it to the server or in a Referer header.
func (cfg *apiConfig) passwordResetLink(token string) string {
	return strings.TrimRight(cfg.appBaseURL, "/") + "/password-reset#token=" + url.QueryEscape(token)
}
