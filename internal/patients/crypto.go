package patients

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/google/uuid"
)

// Cipher performs AES-256-GCM encryption for patient PII.
// A fresh random nonce is generated for every field and every write.
type Cipher struct {
	aead       cipher.AEAD
	keyVersion int
}

func NewCipher(key []byte, keyVersion int) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 key must be 32 bytes, got %d", len(key))
	}
	if keyVersion < 1 {
		return nil, fmt.Errorf("key version must be at least 1, got %d", keyVersion)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Cipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *Cipher) KeyVersion() int {
	return c.keyVersion
}

// Encrypt returns nonce||ciphertext||tag. The AAD binds the ciphertext to the
// tenant, patient and field so a swapped value fails authentication.
func (c *Cipher) Encrypt(plaintext string, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	out := make([]byte, 0, len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, []byte(plaintext), aad), nil
}

func (c *Cipher) Decrypt(ciphertext, aad []byte) (string, error) {
	nonceSize := c.aead.NonceSize()
	if len(ciphertext) < nonceSize+c.aead.Overhead() {
		return "", fmt.Errorf("ciphertext is too short")
	}

	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt patient field: %w", err)
	}
	return string(plaintext), nil
}

// FieldAAD builds the additional authenticated data for one encrypted field.
func FieldAAD(tenantID, patientID uuid.UUID, field string, keyVersion int) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|%s", keyVersion, tenantID, patientID, field))
}
