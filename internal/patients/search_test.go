package patients

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestIndexer(t *testing.T) *SearchIndexer {
	t.Helper()

	indexer, err := NewSearchIndexer(bytes.Repeat([]byte{3}, 32), 1)
	if err != nil {
		t.Fatalf("NewSearchIndexer: %v", err)
	}
	return indexer
}

func TestSearchIndexerRejectsInvalidKey(t *testing.T) {
	if _, err := NewSearchIndexer([]byte("short"), 1); err == nil {
		t.Fatal("NewSearchIndexer accepted a short key")
	}
	if _, err := NewSearchIndexer(bytes.Repeat([]byte{1}, 32), 0); err == nil {
		t.Fatal("NewSearchIndexer accepted key version 0")
	}
}

func TestSearchIndexerTokens(t *testing.T) {
	indexer := newTestIndexer(t)
	tenantID := uuid.New()

	tokens := indexer.Tokens(tenantID, "Anna Maria", "Muster")
	if len(tokens) != 3 {
		t.Fatalf("tokens = %d, want 3 (two first-name words, one last-name word)", len(tokens))
	}
	for _, token := range tokens {
		if token.KeyVersion != 1 {
			t.Errorf("token key version = %d, want 1", token.KeyVersion)
		}
		if len(token.Hash) != 32 {
			t.Errorf("token hash length = %d, want 32", len(token.Hash))
		}
	}
	if tokens[0].Type != "first_name" || tokens[2].Type != "last_name" {
		t.Errorf("token types = %q, %q, %q", tokens[0].Type, tokens[1].Type, tokens[2].Type)
	}
}

func TestSearchIndexerTenantIsolation(t *testing.T) {
	indexer := newTestIndexer(t)
	tenantA, tenantB := uuid.New(), uuid.New()

	hashA := indexer.HashString(tenantA, "muster")
	hashB := indexer.HashString(tenantB, "muster")
	if hashA == hashB {
		t.Fatal("the same name produced the same hash in two tenants")
	}
}

func TestSearchIndexerQueryHashes(t *testing.T) {
	indexer := newTestIndexer(t)
	tenantID := uuid.New()

	hashes, err := indexer.QueryHashes(tenantID, "  Anna   Muster ")
	if err != nil {
		t.Fatalf("QueryHashes: %v", err)
	}
	if len(hashes) != 2 {
		t.Fatalf("hashes = %d, want 2", len(hashes))
	}
	if hashes[0] != indexer.HashString(tenantID, "anna") {
		t.Errorf("first hash does not match the normalized term")
	}
	if hashes[1] != indexer.HashString(tenantID, "muster") {
		t.Errorf("second hash does not match the normalized term")
	}

	empty, err := indexer.QueryHashes(tenantID, "   ")
	if err != nil {
		t.Fatalf("QueryHashes(empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty query hashes = %d, want 0", len(empty))
	}
}

func TestSearchIndexerQueryLimits(t *testing.T) {
	indexer := newTestIndexer(t)
	tenantID := uuid.New()

	if _, err := indexer.QueryHashes(tenantID, strings.Repeat("a", maxSearchQueryRunes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long query err = %v, want ErrInvalid", err)
	}

	tooMany := strings.Repeat("a ", maxSearchTerms+1)
	if _, err := indexer.QueryHashes(tenantID, tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many terms err = %v, want ErrInvalid", err)
	}
}
