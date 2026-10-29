package main

import (
	"context"
	"time"

	"github.com/HunterRedou/altserv/internal/vies"
	"github.com/google/uuid"
)

func (cfg *apiConfig) verifyFirmsAsync(firmID uuid.UUID, ustID string) {
	go func() {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, 48*time.Hour)
		defer cancel()

		valid, err := vies.CheckWithRetry(ctx, ustID, 6, 4*time.Hour)
		switch {
		case err != nil:
			cfg.verifyFirmsAsync(firmID, ustID)
			return
		case valid:
			// cfg.db.update
		default:
			// cfg.db.delete
		}

	}()
}
