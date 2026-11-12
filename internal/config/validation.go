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
	// maxShutdownDrainDelay bounds how long a deploy can delay a restart.
	maxShutdownDrainDelay = 5 * time.Minute
	// minMetricsTokenLength rejects an obviously weak metrics token.
	minMetricsTokenLength = 16
	// maxSlowQueryThreshold keeps the sample meaningful. A larger value would
	// hide statements that are already too slow.
	maxSlowQueryThreshold = time.Minute
	// minSessionPurgeInterval keeps the maintenance job from spinning.
	minSessionPurgeInterval = time.Minute
	// maxSessionPurgeInterval bounds how stale an expired session can remain.
	maxSessionPurgeInterval = 24 * time.Hour
	// minRetentionReviewInterval keeps the cross-tenant review from spinning.
	minRetentionReviewInterval = time.Hour
	// maxRetentionReviewInterval bounds how stale a candidate count can be.
	maxRetentionReviewInterval = 7 * 24 * time.Hour
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

	if c.DBMaxOpenConns < 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be at least 1, got %d", c.DBMaxOpenConns)
	}
	if c.DBMaxIdleConns < 0 {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must not be negative, got %d", c.DBMaxIdleConns)
	}
	if c.DBMaxIdleConns > c.DBMaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS (%d) must not exceed DB_MAX_OPEN_CONNS (%d)", c.DBMaxIdleConns, c.DBMaxOpenConns)
	}
	if c.DBConnMaxLifetime <= 0 {
		return fmt.Errorf("DB_CONN_MAX_LIFETIME must be positive, got %s", c.DBConnMaxLifetime)
	}
	if c.ShutdownDrainDelay < 0 {
		return fmt.Errorf("SHUTDOWN_DRAIN_DELAY must not be negative, got %s", c.ShutdownDrainDelay)
	}
	if c.ShutdownDrainDelay > maxShutdownDrainDelay {
		return fmt.Errorf("SHUTDOWN_DRAIN_DELAY must not exceed %s, got %s", maxShutdownDrainDelay, c.ShutdownDrainDelay)
	}
	if c.MetricsToken != "" && len(c.MetricsToken) < minMetricsTokenLength {
		return fmt.Errorf("METRICS_TOKEN must be at least %d characters when set", minMetricsTokenLength)
	}
	if c.SlowQueryThreshold <= 0 {
		return fmt.Errorf("SLOW_QUERY_THRESHOLD must be positive, got %s", c.SlowQueryThreshold)
	}
	if c.SlowQueryThreshold > maxSlowQueryThreshold {
		return fmt.Errorf("SLOW_QUERY_THRESHOLD must not exceed %s, got %s", maxSlowQueryThreshold, c.SlowQueryThreshold)
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

	if len(c.MFAEncryptionKey) != 0 && len(c.MFAEncryptionKey) != 32 {
		return fmt.Errorf("MFA_ENCRYPTION_KEY must be 32 bytes after base64 decoding, got %d", len(c.MFAEncryptionKey))
	}
	if c.MFAKeyVersion < 1 {
		return fmt.Errorf("MFA_KEY_VERSION must be at least 1, got %d", c.MFAKeyVersion)
	}
	if len(c.MFAEncryptionKey) != 0 && bytes.Equal(c.MFAEncryptionKey, c.PatientEncryptionKey) {
		return fmt.Errorf("MFA_ENCRYPTION_KEY must differ from PATIENT_ENCRYPTION_KEY")
	}
	if len(c.MFAEncryptionKey) != 0 && bytes.Equal(c.MFAEncryptionKey, c.PatientSearchKey) {
		return fmt.Errorf("MFA_ENCRYPTION_KEY must differ from PATIENT_SEARCH_KEY")
	}
	if c.SessionPurgeInterval < minSessionPurgeInterval {
		return fmt.Errorf("SESSION_PURGE_INTERVAL must be at least %s, got %s", minSessionPurgeInterval, c.SessionPurgeInterval)
	}
	if c.SessionPurgeInterval > maxSessionPurgeInterval {
		return fmt.Errorf("SESSION_PURGE_INTERVAL must not exceed %s, got %s", maxSessionPurgeInterval, c.SessionPurgeInterval)
	}
	if c.RetentionReviewInterval < minRetentionReviewInterval {
		return fmt.Errorf("RETENTION_REVIEW_INTERVAL must be at least %s, got %s", minRetentionReviewInterval, c.RetentionReviewInterval)
	}
	if c.RetentionReviewInterval > maxRetentionReviewInterval {
		return fmt.Errorf("RETENTION_REVIEW_INTERVAL must not exceed %s, got %s", maxRetentionReviewInterval, c.RetentionReviewInterval)
	}
	switch c.BillingTransmissionAdapter {
	case "unconfigured":
	case "test":
		// The synthetic payer must never accept a real claim.
		if c.Platform != "dev" {
			return fmt.Errorf("BILLING_TRANSMISSION_ADAPTER=test is only allowed when PLATFORM=dev")
		}
	default:
		return fmt.Errorf("BILLING_TRANSMISSION_ADAPTER must be unconfigured or test, got %q", c.BillingTransmissionAdapter)
	}

	return nil
}
