package observability

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSlowQueryWatcherCheck(t *testing.T) {
	registry := NewRegistry()
	watcher := &SlowQueryWatcher{
		registry: registry,
		count: func(context.Context) (int64, error) {
			return 3, nil
		},
	}

	if err := watcher.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	output := registry.Render()
	if !strings.Contains(output, "altserv_db_slow_queries 3") {
		t.Fatalf("slow query gauge missing:\n%s", output)
	}
}

func TestSlowQueryWatcherErrorCountsJobError(t *testing.T) {
	registry := NewRegistry()
	watcher := &SlowQueryWatcher{
		registry: registry,
		count: func(context.Context) (int64, error) {
			return 0, errors.New("permission denied")
		},
	}

	if err := watcher.Check(context.Background()); err == nil {
		t.Fatal("Check returned nil error")
	}
	output := registry.Render()
	if !strings.Contains(output, `altserv_job_errors_total{job="slow_query_check"} 1`) {
		t.Fatalf("job error missing:\n%s", output)
	}
	if strings.Contains(output, "altserv_db_slow_queries") {
		t.Fatalf("slow query gauge set after an error:\n%s", output)
	}
}

func TestSlowQueryWatcherNilIsSafe(t *testing.T) {
	var watcher *SlowQueryWatcher
	if err := watcher.Check(context.Background()); err != nil {
		t.Fatalf("nil Check: %v", err)
	}
	watcher.Run(context.Background())
}

func TestSlowQueryWatcherRunSamplesImmediately(t *testing.T) {
	registry := NewRegistry()
	calls := make(chan struct{}, 1)
	watcher := &SlowQueryWatcher{
		registry: registry,
		interval: time.Hour,
		count: func(context.Context) (int64, error) {
			calls <- struct{}{}
			return 1, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher.Run(ctx)

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("Run did not sample immediately")
	}
	if !strings.Contains(registry.Render(), "altserv_db_slow_queries 1") {
		t.Fatalf("gauge not set:\n%s", registry.Render())
	}
}

// TestSlowQueryWatcherDatabase checks the pg_stat_activity query against a real
// database. It is skipped unless TEST_DB_URL is set.
func TestSlowQueryWatcherDatabase(t *testing.T) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping database integration test")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	registry := NewRegistry()
	watcher := NewSlowQueryWatcher(conn, registry, 10*time.Millisecond, time.Hour)

	if err := watcher.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(registry.Render(), "altserv_db_slow_queries ") {
		t.Fatalf("gauge not set:\n%s", registry.Render())
	}

	// Hold a statement open long enough to be counted.
	slowCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = conn.ExecContext(slowCtx, "SELECT pg_sleep(0.4)")
	}()
	time.Sleep(100 * time.Millisecond)

	if err := watcher.Check(context.Background()); err != nil {
		t.Fatalf("Check during slow query: %v", err)
	}
	output := registry.Render()
	if strings.Contains(output, "altserv_db_slow_queries 0") {
		t.Fatalf("slow query not counted:\n%s", output)
	}
	<-done
}
