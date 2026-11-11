package auth

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestSecretBoxRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	box, err := NewSecretBox(key, 1)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}

	tenantID := uuid.New()
	userID := uuid.New()
	aad := MFAFieldAAD(tenantID, userID, 1)

	ciphertext, err := box.Encrypt("JBSWY3DPEHPK3PXP", aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("ciphertext contains the plaintext secret")
	}

	plaintext, err := box.Decrypt(ciphertext, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	// A different tenant or user must not be able to open the value.
	otherAAD := MFAFieldAAD(tenantID, uuid.New(), 1)
	if _, err := box.Decrypt(ciphertext, otherAAD); err == nil {
		t.Fatal("ciphertext was accepted with a different AAD")
	}
}

func TestSecretBoxRejectsBadKeys(t *testing.T) {
	if _, err := NewSecretBox(bytes.Repeat([]byte{1}, 16), 1); err == nil {
		t.Fatal("short key was accepted")
	}
	if _, err := NewSecretBox(bytes.Repeat([]byte{1}, 32), 0); err == nil {
		t.Fatal("invalid key version was accepted")
	}
}

func TestDeriveMFAKeyIsDomainSeparated(t *testing.T) {
	patientKey := bytes.Repeat([]byte{3}, 32)
	derived := DeriveMFAKey(patientKey)
	if len(derived) != 32 {
		t.Fatalf("derived key length = %d, want 32", len(derived))
	}
	if bytes.Equal(derived, patientKey) {
		t.Fatal("derived MFA key equals the patient key")
	}
	if !bytes.Equal(derived, DeriveMFAKey(patientKey)) {
		t.Fatal("derivation is not deterministic")
	}
}
