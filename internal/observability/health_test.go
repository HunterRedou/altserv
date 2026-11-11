package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakePinger struct {
	err error
}

func (f fakePinger) PingContext(context.Context) error {
	return f.err
}

func TestLive(t *testing.T) {
	health := NewHealth(fakePinger{})
	rec := httptest.NewRecorder()
	health.Live(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestReady(t *testing.T) {
	tests := []struct {
		name     string
		health   *Health
		draining bool
		want     int
		state    string
	}{
		{name: "database reachable", health: NewHealth(fakePinger{}), want: http.StatusOK, state: "ready"},
		{name: "database down", health: NewHealth(fakePinger{err: errors.New("down")}), want: http.StatusServiceUnavailable, state: "unavailable"},
		{name: "no database", health: NewHealth(nil), want: http.StatusServiceUnavailable, state: "unavailable"},
		{name: "draining", health: NewHealth(fakePinger{}), draining: true, want: http.StatusServiceUnavailable, state: "draining"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.health.SetDraining(tt.draining)
			rec := httptest.NewRecorder()
			tt.health.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if !strings.Contains(rec.Body.String(), `"status":"`+tt.state+`"`) {
				t.Fatalf("body = %s, want state %q", rec.Body.String(), tt.state)
			}
		})
	}
}

func TestDraining(t *testing.T) {
	var health *Health
	health.SetDraining(true)
	if health.Draining() {
		t.Fatal("nil health reports draining")
	}

	health = NewHealth(fakePinger{})
	if health.Draining() {
		t.Fatal("new health is draining")
	}
	health.SetDraining(true)
	if !health.Draining() {
		t.Fatal("health did not record draining")
	}
}

func TestRefreshBackupAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.marker")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	registry := NewRegistry()
	if err := registry.RefreshBackupAge(path); err != nil {
		t.Fatalf("RefreshBackupAge: %v", err)
	}
	output := registry.Render()
	if !strings.Contains(output, "altserv_backup_age_seconds ") {
		t.Fatalf("backup age missing:\n%s", output)
	}
	if strings.Contains(output, "altserv_backup_age_seconds 0") {
		t.Fatalf("backup age is zero:\n%s", output)
	}

	if err := registry.RefreshBackupAge(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("RefreshBackupAge returned nil for a missing marker")
	}
	if !strings.Contains(registry.Render(), "altserv_backup_marker_read_errors_total 1") {
		t.Fatalf("read error not counted:\n%s", registry.Render())
	}
}
