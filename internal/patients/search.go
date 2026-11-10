package patients

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	maxSearchQueryRunes = 100
	maxSearchTerms      = 5
	maxSearchResults    = 50
)

// SearchToken is one blind-index entry for a patient name word. KeyVersion
// records which HMAC key produced the hash so the index can be rotated.
type SearchToken struct {
	Hash       []byte
	Type       string
	KeyVersion int
}

// SearchIndexer builds a protected search index. The HMAC key is separate from
// the AES key, so a leaked encryption key does not reveal search tokens and
// vice versa (doc 7/10).
type SearchIndexer struct {
	key        []byte
	keyVersion int
}

func NewSearchIndexer(key []byte, keyVersion int) (*SearchIndexer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("search HMAC key must be 32 bytes, got %d", len(key))
	}
	if keyVersion < 1 {
		return nil, fmt.Errorf("search key version must be at least 1, got %d", keyVersion)
	}
	return &SearchIndexer{key: key, keyVersion: keyVersion}, nil
}

func (s *SearchIndexer) KeyVersion() int {
	return s.keyVersion
}

// Tokens indexes every word of the first and last name. The tenant id is part
// of the HMAC input so the same name in two tenants produces different hashes.
func (s *SearchIndexer) Tokens(tenantID uuid.UUID, firstName, lastName string) []SearchToken {
	tokens := make([]SearchToken, 0, 8)
	for _, term := range nameTerms(firstName) {
		tokens = append(tokens, SearchToken{Hash: s.hash(tenantID, term), Type: "first_name", KeyVersion: s.keyVersion})
	}
	for _, term := range nameTerms(lastName) {
		tokens = append(tokens, SearchToken{Hash: s.hash(tenantID, term), Type: "last_name", KeyVersion: s.keyVersion})
	}
	return tokens
}

// QueryHashes validates and hashes a search query. Every whitespace-separated
// term must match (AND semantics).
func (s *SearchIndexer) QueryHashes(tenantID uuid.UUID, query string) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []string{}, nil
	}
	if len([]rune(query)) > maxSearchQueryRunes {
		return nil, fmt.Errorf("%w: search query is too long", ErrInvalid)
	}

	terms := nameTerms(query)
	if len(terms) > maxSearchTerms {
		return nil, fmt.Errorf("%w: search query has too many terms", ErrInvalid)
	}

	hashes := make([]string, 0, len(terms))
	for _, term := range terms {
		hashes = append(hashes, hex.EncodeToString(s.hash(tenantID, term)))
	}
	return hashes, nil
}

// HashString returns the hex-encoded token for one normalized term. It is used
// by tests and by callers that need to check a single value.
func (s *SearchIndexer) HashString(tenantID uuid.UUID, term string) string {
	return hex.EncodeToString(s.hash(tenantID, normalizeTerm(term)))
}

func (s *SearchIndexer) hash(tenantID uuid.UUID, term string) []byte {
	mac := hmac.New(sha256.New, s.key)
	fmt.Fprintf(mac, "v%d|%s|%s", s.keyVersion, tenantID, term)
	return mac.Sum(nil)
}

func nameTerms(value string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		term := normalizeTerm(field)
		if term != "" {
			terms = append(terms, term)
		}
	}
	return terms
}

func normalizeTerm(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			return r
		}
		return -1
	}, strings.ToLower(strings.TrimSpace(value)))
}
