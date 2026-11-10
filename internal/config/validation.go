package config

import (
	"bytes"
	"fmt"
	"net/url"
	"time"
)

const (
	// maxSessionDuration keeps a stolen cookie useful for at most 30 days.
	maxSessionDuration = 30 * 24 * time.Hour
)

func (c Config) validate() error {
	if c.DBURL == "" {
		return fmt.Errorf("DB_URL must be set")
	}
	parsed, err := url.Parse(c.DBURL)
	if err != nil {
		return fmt.Errorf("DB_URL is not a valid URL: %w", err)
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("DB_URL scheme must be postgres or postgresql, got %q", parsed.Scheme)
	}

	if _, err := parsePort(c.Port); err != nil {
		return err
	}

	switch c.Platform {
	case "dev", "prod":
	default:
		return fmt.Errorf("PLATFORM must be dev or prod, got %q", c.Platform)
	}

	if c.SessionDuration <= 0 {
		return fmt.Errorf("SESSION_DURATION must be positive, got %s", c.SessionDuration)
	}
	if c.SessionDuration > maxSessionDuration {
		return fmt.Errorf("SESSION_DURATION must not exceed %s, got %s", maxSessionDuration, c.SessionDuration)
	}

	if len(c.PatientEncryptionKey) != 32 {
		return fmt.Errorf("PATIENT_ENCRYPTION_KEY must be 32 bytes after base64 decoding, got %d", len(c.PatientEncryptionKey))
	}
	if c.PatientKeyVersion < 1 {
		return fmt.Errorf("PATIENT_KEY_VERSION must be at least 1, got %d", c.PatientKeyVersion)
	}

	if len(c.PatientSearchKey) != 32 {
		return fmt.Errorf("PATIENT_SEARCH_KEY must be 32 bytes after base64 decoding, got %d", len(c.PatientSearchKey))
	}
	if c.PatientSearchKeyVersion < 1 {
		return fmt.Errorf("PATIENT_SEARCH_KEY_VERSION must be at least 1, got %d", c.PatientSearchKeyVersion)
	}
	if bytes.Equal(c.PatientEncryptionKey, c.PatientSearchKey) {
		return fmt.Errorf("PATIENT_SEARCH_KEY must differ from PATIENT_ENCRYPTION_KEY")
	}

	return nil
}
