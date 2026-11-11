package observability

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenderRequestMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveRequest(http.MethodGet, "/api/patients/{id}", http.StatusOK, 10*time.Millisecond)
	registry.ObserveRequest(http.MethodGet, "/api/patients/{id}", http.StatusInternalServerError, 20*time.Millisecond)

	output := registry.Render()

	wantLines := []string{
		`altserv_http_requests_total{method="GET",route="/api/patients/{id}",status="200"} 1`,
		`altserv_http_requests_total{method="GET",route="/api/patients/{id}",status="500"} 1`,
		`altserv_http_request_duration_seconds_count{method="GET",route="/api/patients/{id}"} 2`,
		`altserv_http_in_flight 0`,
	}
	for _, line := range wantLines {
		if !strings.Contains(output, line) {
			t.Fatalf("output does not contain %q:\n%s", line, output)
		}
	}
}

func TestMiddlewareUsesRoutePatternOnly(t *testing.T) {
	registry := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/patients/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := registry.Middleware(mux)
	patientID := "8b4c4c4c-1111-2222-3333-444455556666"
	req := httptest.NewRequest(http.MethodGet, "/api/patients/"+patientID+"?search=Anna", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := registry.Render()
	if !strings.Contains(output, `route="/api/patients/{id}"`) {
		t.Fatalf("route pattern missing:\n%s", output)
	}
	if strings.Contains(output, patientID) {
		t.Fatalf("raw patient id leaked into metrics:\n%s", output)
	}
	if strings.Contains(output, "Anna") || strings.Contains(output, "search=") {
		t.Fatalf("query string leaked into metrics:\n%s", output)
	}
}

func TestMiddlewareUnmatchedRoute(t *testing.T) {
	registry := NewRegistry()
	mux := http.NewServeMux()
	handler := registry.Middleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(registry.Render(), `route="unmatched"`) {
		t.Fatalf("unmatched route missing:\n%s", registry.Render())
	}
}

func TestHistogramBucketsAreCumulative(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveRequest(http.MethodGet, "/api/me", http.StatusOK, 7*time.Millisecond)

	output := registry.Render()
	if !strings.Contains(output, `le="0.005"} 0`) {
		t.Fatalf("0.005 bucket should be 0:\n%s", output)
	}
	if !strings.Contains(output, `le="0.01"} 1`) {
		t.Fatalf("0.01 bucket should be 1:\n%s", output)
	}
	if !strings.Contains(output, `le="+Inf"} 1`) {
		t.Fatalf("+Inf bucket should be 1:\n%s", output)
	}
}

func TestCountersAndBackupAge(t *testing.T) {
	registry := NewRegistry()
	registry.IncLoginFailure()
	registry.IncLoginFailure()
	registry.IncSessionsPurged(3)
	registry.IncSessionsPurged(2)
	registry.IncJobError("tenant_verification")
	registry.SetBackupAge(90 * time.Minute)
	registry.IncBackupReadError()

	output := registry.Render()
	for _, line := range []string{
		"altserv_login_failures_total 2",
		"altserv_sessions_purged_total 5",
		`altserv_job_errors_total{job="tenant_verification"} 1`,
		"altserv_backup_age_seconds 5400",
		"altserv_backup_marker_read_errors_total 1",
		"altserv_process_start_time_seconds ",
	} {
		if !strings.Contains(output, line) {
			t.Fatalf("output does not contain %q:\n%s", line, output)
		}
	}

	registry.ClearBackupAge()
	if strings.Contains(registry.Render(), "altserv_backup_age_seconds") {
		t.Fatal("backup age still present after ClearBackupAge")
	}
}

func TestPoolStats(t *testing.T) {
	registry := NewRegistry()
	registry.SetPoolStats(func() sql.DBStats {
		return sql.DBStats{
			MaxOpenConnections: 20,
			OpenConnections:    7,
			InUse:              3,
			Idle:               4,
			WaitCount:          5,
			WaitDuration:       1500 * time.Millisecond,
		}
	})

	output := registry.Render()
	for _, line := range []string{
		"altserv_db_pool_max_open_connections 20",
		"altserv_db_pool_open_connections 7",
		"altserv_db_pool_in_use 3",
		"altserv_db_pool_idle 4",
		"altserv_db_pool_wait_count_total 5",
		"altserv_db_pool_wait_duration_seconds_total 1.5",
	} {
		if !strings.Contains(output, line) {
			t.Fatalf("output does not contain %q:\n%s", line, output)
		}
	}
}

func TestEscapeLabel(t *testing.T) {
	got := EscapeLabel("a\\b\"c\nd")
	want := `a\\b\"c\nd`
	if got != want {
		t.Fatalf("EscapeLabel = %q, want %q", got, want)
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var registry *Registry
	registry.ObserveRequest(http.MethodGet, "/", 200, time.Millisecond)
	registry.IncLoginFailure()
	registry.IncJobError("job")
	registry.SetBackupAge(time.Minute)
	registry.ClearBackupAge()
	registry.IncBackupReadError()
	registry.SetPoolStats(nil)
	if registry.Render() != "" {
		t.Fatal("nil registry rendered output")
	}
	if registry.Middleware(http.NotFoundHandler()) == nil {
		t.Fatal("nil registry middleware is nil")
	}
	if err := registry.RefreshBackupAge("/tmp/nope"); err != nil {
		t.Fatalf("nil registry RefreshBackupAge = %v", err)
	}
}

func TestConcurrentObservation(t *testing.T) {
	registry := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				registry.ObserveRequest(http.MethodGet, "/api/me", http.StatusOK, time.Millisecond)
				_ = registry.Render()
			}
		}()
	}
	wg.Wait()

	if !strings.Contains(registry.Render(), `altserv_http_requests_total{method="GET",route="/api/me",status="200"} 2000`) {
		t.Fatalf("unexpected count:\n%s", registry.Render())
	}
}
