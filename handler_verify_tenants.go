package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/vies"
	"github.com/google/uuid"
)

// tenantVerifier is the subset of the database the background verification needs.
// Keeping it small makes the verification logic testable without a real database.
type tenantVerifier interface {
	UpdateTenantStatus(ctx context.Context, arg db.UpdateTenantStatusParams) (db.Tenant, error)
	DeleteTenant(ctx context.Context, id uuid.UUID) error
}

// validUStIDFormat is a cheap, offline sanity check. The real check happens at VIES.
// Keep it in sync with the guard in vies.CheckWithRetry.
func validUStIDFormat(ustID string) bool {
	return len(ustID) >= 3
}

// verifyTenant checks ustID once and persists the outcome.
// It is synchronous so it can be tested; verifyTenantAsync wraps it in a goroutine.
// On error nothing is written, so the tenant stays "pending" for the next sweep.
func verifyTenant(
	ctx context.Context,
	store tenantVerifier,
	tenantID uuid.UUID,
	ustID string,
	check func(context.Context, string) (bool, error),
) error {
	valid, err := check(ctx, ustID)
	if err != nil {
		return fmt.Errorf("check ust id: %w", err)
	}

	if valid {
		_, err := store.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{
			ID:     tenantID,
			Status: "valid",
		})
		if err != nil {
			return fmt.Errorf("mark tenant valid: %w", err)
		}
		return nil
	}

	if err := store.DeleteTenant(ctx, tenantID); err != nil {
		return fmt.Errorf("delete invalid tenant: %w", err)
	}
	return nil
}

// verifyTenantAsync runs verification in the background. The caller does not wait,
// so the result is only visible in tenants.status (or by the row disappearing).
func (cfg *apiConfig) verifyTenantAsync(tenantID uuid.UUID, ustID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 48*time.Hour)
		defer cancel()

		err := verifyTenant(ctx, cfg.db, tenantID, ustID, func(ctx context.Context, ustID string) (bool, error) {
			return vies.CheckWithRetry(ctx, ustID, 4, 4*time.Hour)
		})
		if err != nil {
			cfg.metrics.IncJobError("tenant_verification")
			log.Printf("verify tenant %s: giving up: %v", tenantID, err)
		}
	}()
}

// resumePendingTenantVerification re-runs verification for tenants left pending by
// a previous process. Goroutines do not survive a restart, so without this a tenant
// created just before a deploy would stay "pending" forever.
func (cfg *apiConfig) resumePendingTenantVerification(ctx context.Context) {
	tenants, err := cfg.db.SelectPendingTenants(ctx)
	if err != nil {
		cfg.metrics.IncJobError("tenant_verification")
		log.Printf("resume pending tenant verification: %v", err)
		return
	}

	for _, tenant := range tenants {
		cfg.verifyTenantAsync(tenant.ID, tenant.Ustid)
	}
}
