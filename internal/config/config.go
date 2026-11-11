package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort                 = "8080"
	defaultSessionDuration      = 24 * time.Hour
	defaultKeyVersion           = 1
	defaultDBMaxOpenConns       = 20
	defaultDBMaxIdleConns       = 5
	defaultDBConnMaxLifetime    = 30 * time.Minute
	defaultShutdownDrainDelay   = 5 * time.Second
	defaultSlowQueryThreshold   = 1 * time.Second
	defaultSessionPurgeInterval = time.Hour
	// defaultRetentionReviewInterval is daily. The review only reports counts,
	// so a long interval is enough and keeps the cross-tenant query cheap.
	defaultRetentionReviewInterval = 24 * time.Hour
)

// Config is the runtime configuration loaded from environment variables.
// Secrets are only referenced by name/URL here and are never logged.
type Config struct {
	DBURL           string
	Platform        string
	Port            string
	SessionDuration time.Duration

	// Database pool limits. They keep a traffic spike from exhausting the
	// PostgreSQL connection limit (doc 20).
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// ShutdownDrainDelay is the time between failing readiness and closing the
	// listener, so a load balancer can stop sending new requests.
	ShutdownDrainDelay time.Duration

	// MetricsToken protects GET /metrics when set. An empty value leaves the
	// endpoint open, which is only acceptable for local development.
	MetricsToken string

	// BackupMarkerFile is an optional file whose modification time is the last
	// successful backup. The process exports its age as a metric.
	BackupMarkerFile string

	// SlowQueryThreshold is the runtime above which a statement counts as slow
	// in the pg_stat_activity sample.
	SlowQueryThreshold time.Duration

	// PatientEncryptionKey is the AES-256 key for patient PII. It must be
	// supplied by the environment (KMS/secret manager in production).
	PatientEncryptionKey []byte
	PatientKeyVersion    int

	// PatientSearchKey is a separate HMAC-SHA256 key for the blind search
	// index. It must not be the same key as the AES key (doc 7/10).
	PatientSearchKey        []byte
	PatientSearchKeyVersion int

	// MFAEncryptionKey encrypts TOTP seeds at rest. When it is empty the
	// process derives a domain-separated key from PatientEncryptionKey.
	MFAEncryptionKey []byte
	MFAKeyVersion    int

	// SessionPurgeInterval controls the background purge of expired and
	// revoked sessions and expired authentication tokens.
	SessionPurgeInterval time.Duration

	// RetentionReviewInterval controls the background retention review. The
	// job reports candidate counts only; it never deletes data.
	RetentionReviewInterval time.Duration
}

// Load reads the environment, applies defaults and validates the result.
func Load() (Config, error) {
	cfg := Config{
		DBURL:             os.Getenv("DB_URL"),
		Platform:          os.Getenv("PLATFORM"),
		Port:              envOrDefault("PORT", defaultPort),
		SessionDuration:   defaultSessionDuration,
		PatientKeyVersion: defaultKeyVersion,

		DBMaxOpenConns:     defaultDBMaxOpenConns,
		DBMaxIdleConns:     defaultDBMaxIdleConns,
		DBConnMaxLifetime:  defaultDBConnMaxLifetime,
		ShutdownDrainDelay: defaultShutdownDrainDelay,

		MetricsToken:     os.Getenv("METRICS_TOKEN"),
		BackupMarkerFile: os.Getenv("BACKUP_MARKER_FILE"),

		SlowQueryThreshold: defaultSlowQueryThreshold,

		PatientSearchKeyVersion: defaultKeyVersion,
		MFAKeyVersion:           defaultKeyVersion,
		SessionPurgeInterval:    defaultSessionPurgeInterval,
		RetentionReviewInterval: defaultRetentionReviewInterval,
	}

	if raw := os.Getenv("SESSION_DURATION"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_DURATION: %w", err)
		}
		cfg.SessionDuration = duration
	}

	if raw := os.Getenv("DB_MAX_OPEN_CONNS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("DB_MAX_OPEN_CONNS must be a number: %w", err)
		}
		cfg.DBMaxOpenConns = value
	}

	if raw := os.Getenv("DB_MAX_IDLE_CONNS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS must be a number: %w", err)
		}
		cfg.DBMaxIdleConns = value
	}

	if raw := os.Getenv("DB_CONN_MAX_LIFETIME"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("DB_CONN_MAX_LIFETIME: %w", err)
		}
		cfg.DBConnMaxLifetime = duration
	}

	if raw := os.Getenv("SHUTDOWN_DRAIN_DELAY"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SHUTDOWN_DRAIN_DELAY: %w", err)
		}
		cfg.ShutdownDrainDelay = duration
	}

	if raw := os.Getenv("SLOW_QUERY_THRESHOLD"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SLOW_QUERY_THRESHOLD: %w", err)
		}
		cfg.SlowQueryThreshold = duration
	}

	if raw := os.Getenv("PATIENT_ENCRYPTION_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return Config{}, fmt.Errorf("PATIENT_ENCRYPTION_KEY must be base64: %w", err)
		}
		cfg.PatientEncryptionKey = key
	}

	if raw := os.Getenv("PATIENT_KEY_VERSION"); raw != "" {
		version, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("PATIENT_KEY_VERSION must be a number: %w", err)
		}
		cfg.PatientKeyVersion = version
	}

	if raw := os.Getenv("PATIENT_SEARCH_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return Config{}, fmt.Errorf("PATIENT_SEARCH_KEY must be base64: %w", err)
		}
		cfg.PatientSearchKey = key
	}

	if raw := os.Getenv("PATIENT_SEARCH_KEY_VERSION"); raw != "" {
		version, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("PATIENT_SEARCH_KEY_VERSION must be a number: %w", err)
		}
		cfg.PatientSearchKeyVersion = version
	}

	if raw := os.Getenv("MFA_ENCRYPTION_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MFA_ENCRYPTION_KEY must be base64: %w", err)
		}
		cfg.MFAEncryptionKey = key
	}

	if raw := os.Getenv("MFA_KEY_VERSION"); raw != "" {
		version, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MFA_KEY_VERSION must be a number: %w", err)
		}
		cfg.MFAKeyVersion = version
	}

	if raw := os.Getenv("SESSION_PURGE_INTERVAL"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_PURGE_INTERVAL: %w", err)
		}
		cfg.SessionPurgeInterval = duration
	}

	if raw := os.Getenv("RETENTION_REVIEW_INTERVAL"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("RETENTION_REVIEW_INTERVAL: %w", err)
		}
		cfg.RetentionReviewInterval = duration
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Addr is the listen address for the HTTP server.
func (c Config) Addr() string {
	return ":" + c.Port
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parsePort(port string) (int, error) {
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("PORT %q is not a number", port)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("PORT %d is outside 1-65535", n)
	}
	return n, nil
}
