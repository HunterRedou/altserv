package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func validKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func validSearchKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
}

func validMFAKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
}

func setValidBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_URL", "postgres://user:pass@localhost:5432/altserv?sslmode=disable")
	t.Setenv("PLATFORM", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("SESSION_DURATION", "24h")
	t.Setenv("PATIENT_ENCRYPTION_KEY", validKey())
	t.Setenv("PATIENT_KEY_VERSION", "1")
	t.Setenv("PATIENT_SEARCH_KEY", validSearchKey())
	t.Setenv("PATIENT_SEARCH_KEY_VERSION", "1")
}

func TestLoadObservabilityAndPoolSettings(t *testing.T) {
	t.Run("applies defaults", func(t *testing.T) {
		setValidBaseEnv(t)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.DBMaxOpenConns != 20 || cfg.DBMaxIdleConns != 5 {
			t.Errorf("pool = %d/%d, want 20/5", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
		}
		if cfg.DBConnMaxLifetime != 30*time.Minute {
			t.Errorf("lifetime = %s, want 30m", cfg.DBConnMaxLifetime)
		}
		if cfg.ShutdownDrainDelay != 5*time.Second {
			t.Errorf("drain = %s, want 5s", cfg.ShutdownDrainDelay)
		}
		if cfg.MetricsToken != "" || cfg.BackupMarkerFile != "" {
			t.Errorf("metrics token or backup marker unexpectedly set")
		}
		if cfg.SlowQueryThreshold != time.Second {
			t.Errorf("slow query threshold = %s, want 1s", cfg.SlowQueryThreshold)
		}
	})

	t.Run("reads overrides", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("DB_MAX_OPEN_CONNS", "40")
		t.Setenv("DB_MAX_IDLE_CONNS", "10")
		t.Setenv("DB_CONN_MAX_LIFETIME", "10m")
		t.Setenv("SHUTDOWN_DRAIN_DELAY", "30s")
		t.Setenv("METRICS_TOKEN", "0123456789abcdef")
		t.Setenv("BACKUP_MARKER_FILE", "/var/lib/altserv/last-backup")
		t.Setenv("SLOW_QUERY_THRESHOLD", "250ms")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.DBMaxOpenConns != 40 || cfg.DBMaxIdleConns != 10 {
			t.Errorf("pool = %d/%d, want 40/10", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
		}
		if cfg.DBConnMaxLifetime != 10*time.Minute {
			t.Errorf("lifetime = %s, want 10m", cfg.DBConnMaxLifetime)
		}
		if cfg.ShutdownDrainDelay != 30*time.Second {
			t.Errorf("drain = %s, want 30s", cfg.ShutdownDrainDelay)
		}
		if cfg.MetricsToken != "0123456789abcdef" {
			t.Errorf("metrics token = %q", cfg.MetricsToken)
		}
		if cfg.BackupMarkerFile != "/var/lib/altserv/last-backup" {
			t.Errorf("backup marker = %q", cfg.BackupMarkerFile)
		}
		if cfg.SlowQueryThreshold != 250*time.Millisecond {
			t.Errorf("slow query threshold = %s, want 250ms", cfg.SlowQueryThreshold)
		}
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		cases := []struct {
			key   string
			value string
		}{
			{key: "DB_MAX_OPEN_CONNS", value: "0"},
			{key: "DB_MAX_OPEN_CONNS", value: "abc"},
			{key: "DB_MAX_IDLE_CONNS", value: "-1"},
			{key: "DB_MAX_IDLE_CONNS", value: "99"},
			{key: "DB_CONN_MAX_LIFETIME", value: "0s"},
			{key: "DB_CONN_MAX_LIFETIME", value: "soon"},
			{key: "SHUTDOWN_DRAIN_DELAY", value: "-1s"},
			{key: "SHUTDOWN_DRAIN_DELAY", value: "10m"},
			{key: "METRICS_TOKEN", value: "short"},
			{key: "SLOW_QUERY_THRESHOLD", value: "0s"},
			{key: "SLOW_QUERY_THRESHOLD", value: "2m"},
			{key: "SLOW_QUERY_THRESHOLD", value: "fast"},
		}
		for _, tt := range cases {
			setValidBaseEnv(t)
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for %s=%q", tt.key, tt.value)
			}
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("loads valid configuration", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PORT", "9090")
		t.Setenv("SESSION_DURATION", "2h")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Addr() != ":9090" {
			t.Errorf("Addr() = %q, want %q", cfg.Addr(), ":9090")
		}
		if cfg.SessionDuration != 2*time.Hour {
			t.Errorf("SessionDuration = %s, want 2h", cfg.SessionDuration)
		}
		if cfg.Platform != "dev" {
			t.Errorf("Platform = %q, want dev", cfg.Platform)
		}
		if len(cfg.PatientEncryptionKey) != 32 {
			t.Errorf("PatientEncryptionKey length = %d, want 32", len(cfg.PatientEncryptionKey))
		}
		if cfg.PatientKeyVersion != 1 {
			t.Errorf("PatientKeyVersion = %d, want 1", cfg.PatientKeyVersion)
		}
		if len(cfg.PatientSearchKey) != 32 {
			t.Errorf("PatientSearchKey length = %d, want 32", len(cfg.PatientSearchKey))
		}
		if cfg.PatientSearchKeyVersion != 1 {
			t.Errorf("PatientSearchKeyVersion = %d, want 1", cfg.PatientSearchKeyVersion)
		}
	})

	t.Run("applies defaults", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PORT", "")
		t.Setenv("SESSION_DURATION", "")
		t.Setenv("PATIENT_KEY_VERSION", "")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Port != "8080" {
			t.Errorf("Port = %q, want 8080", cfg.Port)
		}
		if cfg.SessionDuration != 24*time.Hour {
			t.Errorf("SessionDuration = %s, want 24h", cfg.SessionDuration)
		}
		if cfg.PatientKeyVersion != 1 {
			t.Errorf("PatientKeyVersion = %d, want 1", cfg.PatientKeyVersion)
		}
		if cfg.PatientSearchKeyVersion != 1 {
			t.Errorf("PatientSearchKeyVersion = %d, want 1", cfg.PatientSearchKeyVersion)
		}
	})

	t.Run("rejects missing database url", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("DB_URL", "")

		if _, err := Load(); err == nil {
			t.Fatal("Load returned nil error for empty DB_URL")
		}
	})

	t.Run("rejects unsupported database scheme", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("DB_URL", "mysql://localhost/altserv")

		if _, err := Load(); err == nil {
			t.Fatal("Load returned nil error for a mysql URL")
		}
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		for _, port := range []string{"abc", "0", "70000"} {
			setValidBaseEnv(t)
			t.Setenv("PORT", port)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for PORT=%q", port)
			}
		}
	})

	t.Run("rejects unknown platform", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PLATFORM", "staging")

		if _, err := Load(); err == nil {
			t.Fatal("Load returned nil error for unknown platform")
		}
	})

	t.Run("rejects invalid session duration", func(t *testing.T) {
		for _, duration := range []string{"soon", "0s", "-1h", "744h"} {
			setValidBaseEnv(t)
			t.Setenv("SESSION_DURATION", duration)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for SESSION_DURATION=%q", duration)
			}
		}
	})

	t.Run("rejects missing encryption key", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PATIENT_ENCRYPTION_KEY", "")

		if _, err := Load(); err == nil {
			t.Fatal("Load returned nil error without PATIENT_ENCRYPTION_KEY")
		}
	})

	t.Run("rejects malformed or short encryption key", func(t *testing.T) {
		for _, key := range []string{"not-base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
			setValidBaseEnv(t)
			t.Setenv("PATIENT_ENCRYPTION_KEY", key)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for key %q", key)
			}
		}
	})

	t.Run("rejects invalid key version", func(t *testing.T) {
		for _, version := range []string{"0", "-1", "abc"} {
			setValidBaseEnv(t)
			t.Setenv("PATIENT_KEY_VERSION", version)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for PATIENT_KEY_VERSION=%q", version)
			}
		}
	})

	t.Run("rejects missing search key", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PATIENT_SEARCH_KEY", "")

		if _, err := Load(); err == nil {
			t.Fatal("Load returned nil error without PATIENT_SEARCH_KEY")
		}
	})

	t.Run("rejects malformed or short search key", func(t *testing.T) {
		for _, key := range []string{"not-base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
			setValidBaseEnv(t)
			t.Setenv("PATIENT_SEARCH_KEY", key)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for search key %q", key)
			}
		}
	})

	t.Run("rejects reusing the encryption key for search", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("PATIENT_SEARCH_KEY", validKey())

		if _, err := Load(); err == nil {
			t.Fatal("Load accepted the same key for encryption and search")
		}
	})

	t.Run("rejects invalid search key version", func(t *testing.T) {
		for _, version := range []string{"0", "-1", "abc"} {
			setValidBaseEnv(t)
			t.Setenv("PATIENT_SEARCH_KEY_VERSION", version)

			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for PATIENT_SEARCH_KEY_VERSION=%q", version)
			}
		}
	})

	t.Run("loads authentication hardening settings", func(t *testing.T) {
		setValidBaseEnv(t)
		t.Setenv("MFA_ENCRYPTION_KEY", validMFAKey())
		t.Setenv("MFA_KEY_VERSION", "2")
		t.Setenv("SESSION_PURGE_INTERVAL", "15m")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.MFAEncryptionKey) != 32 {
			t.Errorf("MFAEncryptionKey length = %d, want 32", len(cfg.MFAEncryptionKey))
		}
		if cfg.MFAKeyVersion != 2 {
			t.Errorf("MFAKeyVersion = %d, want 2", cfg.MFAKeyVersion)
		}
		if cfg.SessionPurgeInterval != 15*time.Minute {
			t.Errorf("SessionPurgeInterval = %s, want 15m", cfg.SessionPurgeInterval)
		}
	})

	t.Run("applies authentication hardening defaults", func(t *testing.T) {
		setValidBaseEnv(t)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.MFAEncryptionKey) != 0 {
			t.Errorf("MFAEncryptionKey = %d bytes, want derived default", len(cfg.MFAEncryptionKey))
		}
		if cfg.MFAKeyVersion != 1 {
			t.Errorf("MFAKeyVersion = %d, want 1", cfg.MFAKeyVersion)
		}
		if cfg.SessionPurgeInterval != time.Hour {
			t.Errorf("SessionPurgeInterval = %s, want 1h", cfg.SessionPurgeInterval)
		}
	})

	t.Run("rejects invalid authentication hardening settings", func(t *testing.T) {
		cases := []struct {
			key   string
			value string
		}{
			{key: "MFA_ENCRYPTION_KEY", value: "not-base64!"},
			{key: "MFA_ENCRYPTION_KEY", value: base64.StdEncoding.EncodeToString([]byte("short"))},
			{key: "MFA_ENCRYPTION_KEY", value: validKey()},
			{key: "MFA_ENCRYPTION_KEY", value: validSearchKey()},
			{key: "MFA_KEY_VERSION", value: "0"},
			{key: "MFA_KEY_VERSION", value: "abc"},
			{key: "SESSION_PURGE_INTERVAL", value: "1s"},
			{key: "SESSION_PURGE_INTERVAL", value: "48h"},
			{key: "SESSION_PURGE_INTERVAL", value: "soon"},
		}
		for _, tt := range cases {
			setValidBaseEnv(t)
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load returned nil error for %s=%q", tt.key, tt.value)
			}
		}
	})
}
