package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort            = "8080"
	defaultSessionDuration = 24 * time.Hour
	defaultKeyVersion      = 1
)

// Config is the runtime configuration loaded from environment variables.
// Secrets are only referenced by name/URL here and are never logged.
type Config struct {
	DBURL           string
	Platform        string
	Port            string
	SessionDuration time.Duration

	// PatientEncryptionKey is the AES-256 key for patient PII. It must be
	// supplied by the environment (KMS/secret manager in production).
	PatientEncryptionKey []byte
	PatientKeyVersion    int

	// PatientSearchKey is a separate HMAC-SHA256 key for the blind search
	// index. It must not be the same key as the AES key (doc 7/10).
	PatientSearchKey        []byte
	PatientSearchKeyVersion int
}

// Load reads the environment, applies defaults and validates the result.
func Load() (Config, error) {
	cfg := Config{
		DBURL:             os.Getenv("DB_URL"),
		Platform:          os.Getenv("PLATFORM"),
		Port:              envOrDefault("PORT", defaultPort),
		SessionDuration:   defaultSessionDuration,
		PatientKeyVersion: defaultKeyVersion,

		PatientSearchKeyVersion: defaultKeyVersion,
	}

	if raw := os.Getenv("SESSION_DURATION"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_DURATION: %w", err)
		}
		cfg.SessionDuration = duration
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
