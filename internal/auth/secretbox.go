package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/google/uuid"
)

// SecretBox encrypts small secrets at rest, such as a TOTP seed. It uses
// AES-256-GCM. The key version is stored next to the ciphertext so a rotation
// can re-encrypt old rows.
type SecretBox struct {
	aead       cipher.AEAD
	keyVersion int
}

// NewSecretBox creates a box from a 32-byte key.
func NewSecretBox(key []byte, keyVersion int) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret box key must be 32 bytes, got %d", len(key))
	}
	if keyVersion < 1 {
		return nil, fmt.Errorf("secret box key version must be at least 1, got %d", keyVersion)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &SecretBox{aead: aead, keyVersion: keyVersion}, nil
}

// KeyVersion is the version written with a new ciphertext.
func (b *SecretBox) KeyVersion() int {
	return b.keyVersion
}

// DeriveMFAKey derives a separate MFA key from the patient encryption key when
// no dedicated key is configured. Domain separation keeps the two uses apart.
func DeriveMFAKey(patientKey []byte) []byte {
	mac := hmac.New(sha256.New, patientKey)
	mac.Write([]byte("altserv-mfa-secret-v1"))
	return mac.Sum(nil)
}

// Encrypt returns nonce||ciphertext||tag. AAD binds the ciphertext to the
// tenant, user and field so a copied value cannot be moved to another account.
func (b *SecretBox) Encrypt(plaintext string, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, []byte(plaintext), aad), nil
}

// Decrypt opens a value produced by Encrypt.
func (b *SecretBox) Decrypt(ciphertext, aad []byte) (string, error) {
	nonceSize := b.aead.NonceSize()
	if len(ciphertext) < nonceSize+b.aead.Overhead() {
		return "", fmt.Errorf("ciphertext is too short")
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := b.aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plaintext), nil
}

// MFAFieldAAD builds the additional authenticated data for one MFA secret.
func MFAFieldAAD(tenantID, userID uuid.UUID, keyVersion int) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|mfa_secret", keyVersion, tenantID, userID))
}
