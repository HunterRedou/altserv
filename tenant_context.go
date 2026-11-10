package main

import (
	"context"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// tenantRunner lets tests run tenant-scoped work inside an existing transaction
// so seeded data can be rolled back.
type tenantRunner func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error

// withTenant runs fn with Row Level Security scoped to tenantID.
func (cfg *apiConfig) withTenant(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
	if cfg.tenantRunner != nil {
		return cfg.tenantRunner(ctx, tenantID, fn)
	}
	return db.WithTenant(ctx, cfg.dbConn, tenantID, fn)
}

// recordAudit writes an audit event inside the caller's transaction.
func (cfg *apiConfig) recordAudit(ctx context.Context, q *db.Queries, event audit.Event) error {
	if cfg.auditRecorder == nil {
		return nil
	}
	return cfg.auditRecorder.Record(ctx, q, event)
}
