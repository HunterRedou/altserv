package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// tokenBytes is the entropy of a session or CSRF token. 32 bytes = 256 bits.
const tokenBytes = 32

// GenerateToken returns a cryptographically random, URL-safe token.
// The caller sends it to the client; the server stores only its hash.
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the SHA-256 fingerprint of a token.
// Session tokens are high-entropy, so a fast hash is sufficient; do not use
// this for passwords.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
