package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/observability"
)

func TestMetricsHandlerWithoutToken(t *testing.T) {
	registry := observability.NewRegistry()
	cfg := &apiConfig{metrics: registry}

	rec := httptest.NewRecorder()
	cfg.metricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "altserv_http_requests_total") {
		t.Fatalf("metrics body missing counter:\n%s", rec.Body.String())
	}
}

func TestMetricsHandlerRequiresBearerToken(t *testing.T) {
	cfg := &apiConfig{metrics: observability.NewRegistry(), metricsToken: "0123456789abcdef"}

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "no header", want: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer nope", want: http.StatusUnauthorized},
		{name: "basic auth", header: "Basic 0123456789abcdef", want: http.StatusUnauthorized},
		{name: "correct token", header: "Bearer 0123456789abcdef", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			cfg.metricsHandler()(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestMetricsHandlerNilRegistry(t *testing.T) {
	cfg := &apiConfig{}
	rec := httptest.NewRecorder()
	cfg.metricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestRunWithDrainMarksDrainingBeforeShutdown(t *testing.T) {
	health := observability.NewHealth(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runCalled := make(chan struct{})
	run := func(runCtx context.Context) error {
		close(runCalled)
		<-runCtx.Done()
		if !health.Draining() {
			return errors.New("shutdown started before readiness failed")
		}
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithDrain(ctx, health, 0, run)
	}()

	<-runCalled
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("runWithDrain: %v", err)
	}
	if !health.Draining() {
		t.Fatal("health is not draining after shutdown")
	}
}

func TestRunWithDrainWaitsForDelay(t *testing.T) {
	health := observability.NewHealth(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan time.Time, 1)
	run := func(runCtx context.Context) error {
		<-runCtx.Done()
		started <- time.Now()
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithDrain(ctx, health, 50*time.Millisecond, run)
	}()

	signalled := time.Now()
	cancel()
	finished := <-started
	if finished.Sub(signalled) < 40*time.Millisecond {
		t.Fatalf("shutdown started after %s, want at least the drain delay", finished.Sub(signalled))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("runWithDrain: %v", err)
	}
}

func TestRunWithDrainReturnsRunError(t *testing.T) {
	wantErr := errors.New("listen failed")
	got := runWithDrain(context.Background(), observability.NewHealth(nil), time.Second, func(context.Context) error {
		return wantErr
	})
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want %v", got, wantErr)
	}
}

func TestRoutesRecordHealthCheckByPattern(t *testing.T) {
	registry := observability.NewRegistry()
	cfg := &apiConfig{metrics: registry, health: observability.NewHealth(nil)}

	rec := httptest.NewRecorder()
	cfg.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	output := registry.Render()
	if !strings.Contains(output, `altserv_http_requests_total{method="GET",route="/healthz",status="200"} 1`) {
		t.Fatalf("health request not recorded by route pattern:\n%s", output)
	}
}
