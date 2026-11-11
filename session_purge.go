package main

import (
	"context"
	"log"
	"time"
)

// purgeExpiredAuthRows removes expired and revoked sessions plus expired
// authentication tokens. The SECURITY DEFINER database function writes one
// auth.sessions_purged audit event per tenant, so the count and the audit row
// commit together.
func (cfg *apiConfig) purgeExpiredAuthRows(ctx context.Context) (int64, error) {
	rows, err := cfg.db.PurgeExpiredAuthRows(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, row := range rows {
		total += row.DeletedCount
	}
	cfg.metrics.IncSessionsPurged(int(total))
	return total, nil
}

// runSessionPurge purges once at startup and then on every interval. It stops
// when ctx is canceled. A failed run is counted and logged; the next run
// retries.
func (cfg *apiConfig) runSessionPurge(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}

	purge := func() {
		deleted, err := cfg.purgeExpiredAuthRows(ctx)
		if err != nil {
			cfg.metrics.IncJobError("session_purge")
			log.Printf("session purge failed: %v", err)
			return
		}
		if deleted > 0 {
			log.Printf("session purge removed %d expired or revoked sessions", deleted)
		}
	}

	purge()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}
