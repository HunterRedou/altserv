package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Pinger is the subset of *sql.DB the readiness check needs.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Health serves liveness and readiness checks. Liveness never touches the
// database; readiness fails while the process is draining or the database is
// unreachable. Neither response contains internal error details.
type Health struct {
	db       Pinger
	draining atomic.Bool
	timeout  time.Duration
}

// NewHealth creates health handlers for the given database.
func NewHealth(db Pinger) *Health {
	return &Health{db: db, timeout: 2 * time.Second}
}

// SetDraining marks the process as shutting down. Readiness then fails so a
// load balancer stops sending new traffic before the listener closes.
func (h *Health) SetDraining(draining bool) {
	if h == nil {
		return
	}
	h.draining.Store(draining)
}

// Draining reports whether the process is shutting down.
func (h *Health) Draining() bool {
	if h == nil {
		return false
	}
	return h.draining.Load()
}

// Live handles GET /healthz. It only proves that the process is running.
func (h *Health) Live(w http.ResponseWriter, _ *http.Request) {
	writeHealth(w, http.StatusOK, "ok")
}

// Ready handles GET /readyz. It checks the database unless the process is
// already draining.
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.db == nil {
		writeHealth(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if h.draining.Load() {
		writeHealth(w, http.StatusServiceUnavailable, "draining")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if err := h.db.PingContext(ctx); err != nil {
		writeHealth(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeHealth(w, http.StatusOK, "ready")
}

func writeHealth(w http.ResponseWriter, status int, state string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": state})
}

// RefreshBackupAge reads the modification time of a backup marker and records
// its age. It returns an error when the marker cannot be read.
func (r *Registry) RefreshBackupAge(path string) error {
	if r == nil {
		return nil
	}
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		r.IncBackupReadError()
		r.ClearBackupAge()
		return err
	}
	r.SetBackupAge(time.Since(info.ModTime()))
	return nil
}

// WatchBackupMarker refreshes the backup age immediately and then on every
// interval until ctx is canceled. An empty path disables the watcher.
func (r *Registry) WatchBackupMarker(ctx context.Context, path string, interval time.Duration) {
	if r == nil || path == "" {
		return
	}
	_ = r.RefreshBackupAge(path)
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.RefreshBackupAge(path)
			}
		}
	}()
}
