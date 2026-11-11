package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters. SHA-1, six digits and a 30 second step are the values every
// common authenticator app supports. A one-step skew tolerates clock drift.
const (
	TOTPSecretBytes = 20
	TOTPDigits      = 6
	TOTPStep        = 30 * time.Second
	TOTPSkewSteps   = 1

	recoveryCodeCount  = 10
	recoveryCodeLength = 10
)

// recoveryAlphabet avoids characters that are easy to confuse when a user
// reads a printed recovery code. It has exactly 32 symbols, so the modulo of a
// random byte is unbiased.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a new base32 secret without padding.
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, TOTPSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return base32NoPadding.EncodeToString(b), nil
}

// TOTPCode returns the code for a secret at a point in time. Tests use it to
// drive the login flow; production code only needs ValidateTOTP.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / int64(TOTPStep/time.Second))
	return hotp(key, counter), nil
}

// ValidateTOTP checks a code against the current step and the configured skew.
// The comparison is constant time.
func ValidateTOTP(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return false
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return false
	}

	counter := at.Unix() / int64(TOTPStep/time.Second)
	for offset := -TOTPSkewSteps; offset <= TOTPSkewSteps; offset++ {
		candidate := counter + int64(offset)
		if candidate < 0 {
			continue
		}
		expected := hotp(key, uint64(candidate))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// OTPAuthURI builds the otpauth:// URI that an authenticator app understands.
// The secret is not logged.
func OTPAuthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	return fmt.Sprintf(
		"otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		label,
		url.QueryEscape(secret),
		url.QueryEscape(issuer),
		TOTPDigits,
		int(TOTPStep/time.Second),
	)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32NoPadding.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("decode TOTP secret: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("TOTP secret is empty")
	}
	return key, nil
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", TOTPDigits, value%1_000_000)
}

// GenerateRecoveryCodes returns one-time codes in XXXXX-XXXXX form. The caller
// must show them once and store only HashRecoveryCode values.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		raw := make([]byte, recoveryCodeLength)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}
		var b strings.Builder
		b.Grow(recoveryCodeLength + 1)
		for j, value := range raw {
			if j == recoveryCodeLength/2 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(value)%len(recoveryAlphabet)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// NormalizeRecoveryCode removes the separator and whitespace and upper-cases
// the value so a user can type it with or without the dash.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// HashRecoveryCode returns the SHA-256 fingerprint of a normalized code.
// Recovery codes are high-entropy, so a fast hash is sufficient.
func HashRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return sum[:]
}
