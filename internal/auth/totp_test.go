package auth

import (
	"testing"
	"time"
)

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	code, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if len(code) != TOTPDigits {
		t.Fatalf("code length = %d, want %d", len(code), TOTPDigits)
	}
	if !ValidateTOTP(secret, code, now) {
		t.Fatal("current code was rejected")
	}
	if !ValidateTOTP(secret, code, now.Add(TOTPStep)) {
		t.Fatal("previous step was rejected; skew should accept it")
	}
	if ValidateTOTP(secret, code, now.Add(2*TOTPStep)) {
		t.Fatal("code outside the skew window was accepted")
	}
	if ValidateTOTP(secret, "000000", now) {
		t.Fatal("wrong code was accepted")
	}
	if ValidateTOTP(secret, "12345", now) {
		t.Fatal("short code was accepted")
	}
	if ValidateTOTP("not-base32!", code, now) {
		t.Fatal("invalid secret was accepted")
	}
}

func TestTOTPCodeIsDeterministic(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	at := time.Unix(1_700_000_000, 0)
	first, err := TOTPCode(secret, at)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	second, err := TOTPCode(secret, at)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if first != second {
		t.Fatalf("code changed for the same step: %q != %q", first, second)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("code count = %d, want %d", len(codes), recoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if len(code) != recoveryCodeLength+1 {
			t.Errorf("code %q has length %d, want %d", code, len(code), recoveryCodeLength+1)
		}
		if code[5] != '-' {
			t.Errorf("code %q is not in XXXXX-XXXXX form", code)
		}
		if seen[NormalizeRecoveryCode(code)] {
			t.Errorf("duplicate code %q", code)
		}
		seen[NormalizeRecoveryCode(code)] = true
	}

	normalized := NormalizeRecoveryCode(stringsLower(codes[0]))
	if normalized != NormalizeRecoveryCode(codes[0]) {
		t.Fatal("normalization is not case-insensitive")
	}
	if string(HashRecoveryCode(codes[0])) == string(HashRecoveryCode(codes[1])) {
		t.Fatal("different codes hash to the same value")
	}
	if string(HashRecoveryCode(codes[0])) == codes[0] {
		t.Fatal("recovery code is stored in plaintext")
	}
}

func TestOTPAuthURI(t *testing.T) {
	uri := OTPAuthURI("altserv", "chef@example.com", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{
		"otpauth://totp/",
		"secret=JBSWY3DPEHPK3PXP",
		"issuer=altserv",
		"digits=6",
		"period=30",
	} {
		if !contains(uri, want) {
			t.Errorf("OTPAuthURI() = %q, missing %q", uri, want)
		}
	}
}

func stringsLower(value string) string {
	out := []byte(value)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
