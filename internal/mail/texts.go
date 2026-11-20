package mail

import (
	"fmt"
	"time"
)

// The texts name no patient, no tenant data and no health data. They only
// address the account owner.

const textFooter = "\n\nDiese E-Mail wurde automatisch von altserv gesendet. Bitte antworten Sie nicht darauf.\n"

// PasswordResetMessage is sent after a self-service reset request. link
// contains the single-use reset token.
func PasswordResetMessage(to, link string, ttl time.Duration) Message {
	return Message{
		To:      to,
		Subject: "altserv: Passwort zurücksetzen",
		Text: fmt.Sprintf(
			"Guten Tag,\n\n"+
				"für Ihr altserv-Konto wurde das Zurücksetzen des Passworts angefordert.\n\n"+
				"Öffnen Sie diesen Link, um ein neues Passwort festzulegen:\n%s\n\n"+
				"Der Link ist %d Minuten gültig und kann nur einmal verwendet werden.\n\n"+
				"Wenn Sie das nicht angefordert haben, ignorieren Sie diese E-Mail. Ihr Passwort bleibt unverändert."+
				textFooter,
			link, int(ttl.Minutes()),
		),
	}
}

// MFAEnabledMessage is sent after two-factor authentication was enabled.
func MFAEnabledMessage(to string) Message {
	return Message{
		To:      to,
		Subject: "altserv: Zwei-Faktor-Anmeldung aktiviert",
		Text: "Guten Tag,\n\n" +
			"für Ihr altserv-Konto wurde die Zwei-Faktor-Anmeldung aktiviert.\n\n" +
			"Wenn Sie das nicht selbst waren, wenden Sie sich sofort an Ihre Pflegedienstleitung." +
			textFooter,
	}
}

// MFADisabledMessage is sent after two-factor authentication was disabled.
func MFADisabledMessage(to string) Message {
	return Message{
		To:      to,
		Subject: "altserv: Zwei-Faktor-Anmeldung deaktiviert",
		Text: "Guten Tag,\n\n" +
			"für Ihr altserv-Konto wurde die Zwei-Faktor-Anmeldung deaktiviert.\n\n" +
			"Wenn Sie das nicht selbst waren, ändern Sie sofort Ihr Passwort und wenden Sie sich an Ihre Pflegedienstleitung." +
			textFooter,
	}
}
