package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/observability"
)

// metricsHandler serves the Prometheus exposition. When METRICS_TOKEN is set,
// the caller must send it as a bearer token. The endpoint stays public in local
// development, where no token is configured.
func (cfg *apiConfig) metricsHandler() http.HandlerFunc {
	registry := cfg.metrics
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.metricsToken != "" {
			const prefix = "Bearer "
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, prefix) ||
				subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(cfg.metricsToken)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
				return
			}
		}
		if registry == nil {
			respondWithError(w, http.StatusServiceUnavailable, "Metrics unavailable", nil)
			return
		}
		registry.Handler().ServeHTTP(w, r)
	}
}

// runWithDrain marks the process as draining, waits for the load balancer to
// observe the failed readiness check, and then starts the graceful shutdown.
// A zero delay skips the wait but still flips readiness first.
func runWithDrain(
	ctx context.Context,
	health *observability.Health,
	delay time.Duration,
	run func(context.Context) error,
) error {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(runCtx)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		health.SetDraining(true)
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			<-timer.C
		}
		cancel()
		return <-errCh
	}
}
