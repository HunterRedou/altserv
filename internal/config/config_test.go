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
}
