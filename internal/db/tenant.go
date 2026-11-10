package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// tenantRole is the non-superuser, non-BYPASSRLS role used for tenant queries.
// The connection role must be a member of it (migration 012 grants membership).
const tenantRole = "altserv_app"

// WithTenant runs fn inside a transaction that is scoped to one tenant.
// The role switch and the app.tenant_id setting are transaction-local, so the
// connection is returned to the pool in its original state.
func WithTenant(ctx context.Context, conn *sql.DB, tenantID uuid.UUID, fn func(*Queries) error) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("tenant id is required")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tenant transaction: %w", err)
	}
	defer tx.Rollback()

	if err := applyTenantContext(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := fn(New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tenant transaction: %w", err)
	}
	return nil
}

// WithTenantTx is the same as WithTenant but uses an existing transaction.
// It is useful for composing operations and for integration tests that seed
// data and roll everything back.
func WithTenantTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, fn func(*Queries) error) error {
	if err := applyTenantContext(ctx, tx, tenantID); err != nil {
		return err
	}
	// Make the transaction reusable by callers that switch tenants or seed data
	// afterwards. On an aborted transaction this is a no-op error.
	defer tx.ExecContext(context.Background(), "RESET ROLE")

	return fn(New(tx))
}

func applyTenantContext(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("tenant id is required")
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+tenantRole); err != nil {
		return fmt.Errorf("set tenant role: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	return nil
}
