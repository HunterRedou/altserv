package main

import (
	"context"
	"log"
	"time"
)

// reviewRetention runs the cross-tenant retention review. The SECURITY DEFINER
// database function writes one retention.reviewed audit event per tenant that
// has a confirmed policy. The job reports candidate counts only; it never
// creates a deletion request and never deletes data.
func (cfg *apiConfig) reviewRetention(ctx context.Context) (int64, error) {
	candidates, err := cfg.retentionService.ReviewAllCandidates(ctx, cfg.db)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, candidate := range candidates {
		total += candidate.Count
	}
	cfg.metrics.IncRetentionReview(int(total))
	return total, nil
}

// runRetentionReview reviews once at startup and then on every interval. It
// stops when ctx is canceled. A failed run is counted and logged; the next run
// retries.
func (cfg *apiConfig) runRetentionReview(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	review := func() {
		candidates, err := cfg.reviewRetention(ctx)
		if err != nil {
			cfg.metrics.IncJobError("retention_review")
			log.Printf("retention review failed: %v", err)
			return
		}
		if candidates > 0 {
			log.Printf("retention review reported %d candidate rows", candidates)
		}
	}

	review()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			review()
		}
	}
}
