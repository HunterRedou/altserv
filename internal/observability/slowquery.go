package observability

import (
	"context"
	"database/sql"
	"time"
)

// slowQuerySQL counts statements in this database that have been running longer
// than the threshold. It excludes the watcher's own statement. It uses
// clock_timestamp() so a long transaction does not freeze the elapsed time at
// transaction start.
const slowQuerySQL = `
SELECT count(*)
FROM pg_stat_activity
WHERE datname = current_database()
  AND state = 'active'
  AND query_start IS NOT NULL
  AND clock_timestamp() - query_start > make_interval(secs => $1)
  AND pid <> pg_backend_pid()`

// SlowQueryWatcher periodically samples pg_stat_activity and exports the number
// of running statements above the threshold. It never stores statement text or
// any data from a query, only a count.
type SlowQueryWatcher struct {
	registry  *Registry
	interval  time.Duration
	threshold time.Duration
	count     func(context.Context) (int64, error)
}

// NewSlowQueryWatcher creates a watcher for the given database.
func NewSlowQueryWatcher(db *sql.DB, registry *Registry, threshold, interval time.Duration) *SlowQueryWatcher {
	watcher := &SlowQueryWatcher{
		registry:  registry,
		interval:  interval,
		threshold: threshold,
	}
	watcher.count = func(ctx context.Context) (int64, error) {
		var count int64
		err := db.QueryRowContext(ctx, slowQuerySQL, threshold.Seconds()).Scan(&count)
		return count, err
	}
	return watcher
}

// Check samples once and updates the gauge. A failed sample is counted as a job
// error and leaves the previous gauge value in place.
func (w *SlowQueryWatcher) Check(ctx context.Context) error {
	if w == nil || w.count == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	count, err := w.count(ctx)
	if err != nil {
		w.registry.IncJobError("slow_query_check")
		return err
	}
	w.registry.SetSlowQueries(count)
	return nil
}

// Run samples immediately and then on every interval until ctx is canceled.
func (w *SlowQueryWatcher) Run(ctx context.Context) {
	if w == nil {
		return
	}
	_ = w.Check(ctx)
	interval := w.interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = w.Check(ctx)
			}
		}
	}()
}
