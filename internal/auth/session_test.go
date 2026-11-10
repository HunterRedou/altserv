package auth

import (
	"bytes"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	// 32 bytes, base64 RawURLEncoding, no padding.
	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43", len(token))
	}

	other, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == other {
		t.Fatal("two generated tokens are equal")
	}
}

func TestHashToken(t *testing.T) {
	token := "a-fixed-token"

	first := HashToken(token)
	second := HashToken(token)

	if len(first) != 32 {
		t.Fatalf("hash length = %d, want 32", len(first))
	}
	if !bytes.Equal(first, second) {
		t.Fatal("HashToken is not deterministic")
	}
	if bytes.Equal(first, []byte(token)) {
		t.Fatal("HashToken returned the token itself")
	}
	if bytes.Equal(first, HashToken(token+"x")) {
		t.Fatal("different tokens produced the same hash")
	}
}
