package breakglass

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Store is the persistence boundary used by the service.
type Store interface {
	PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error)
	Create(ctx context.Context, q *db.Queries, tenantID, patientID, requestedBy uuid.UUID, reason string) (Grant, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error)
	GetByRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error)
	GetActiveForRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error)
	ListByTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Grant, error)
	ListByRequester(ctx context.Context, q *db.Queries, tenantID, requesterID uuid.UUID) ([]Grant, error)
	Approve(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, durationMinutes int, decisionReason string) (Grant, error)
	Deny(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, decisionReason string) (Grant, error)
	Revoke(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, reason string) (Grant, error)
	Touch(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error)
}

// Repository implements Store with sqlc queries. The surrounding transaction
// is tenant-scoped, so RLS limits every query to the grant's tenant.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error) {
	exists, err := q.PatientExists(ctx, db.PatientExistsParams{ID: patientID, TenantID: tenantID})
	if err != nil {
		return false, fmt.Errorf("check patient existence: %w", err)
	}
	return exists, nil
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, tenantID, patientID, requestedBy uuid.UUID, reason string) (Grant, error) {
	row, err := q.CreateBreakGlassGrant(ctx, db.CreateBreakGlassGrantParams{
		TenantID:    tenantID,
		PatientID:   patientID,
		RequestedBy: requestedBy,
		Reason:      reason,
	})
	if err != nil {
		return Grant{}, fmt.Errorf("create break-glass grant: %w", err)
	}
	return toGrant(row), nil
}

func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error) {
	row, err := q.GetBreakGlassGrant(ctx, db.GetBreakGlassGrantParams{ID: id, TenantID: tenantID})
	if err != nil {
		return Grant{}, mapReadError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) GetByRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error) {
	row, err := q.GetBreakGlassGrantForRequester(ctx, db.GetBreakGlassGrantForRequesterParams{
		ID:          id,
		TenantID:    tenantID,
		RequestedBy: requesterID,
	})
	if err != nil {
		return Grant{}, mapReadError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) GetActiveForRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error) {
	row, err := q.GetActiveBreakGlassGrantForRequester(ctx, db.GetActiveBreakGlassGrantForRequesterParams{
		ID:          id,
		TenantID:    tenantID,
		RequestedBy: requesterID,
	})
	if err != nil {
		return Grant{}, mapReadError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) ListByTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Grant, error) {
	rows, err := q.ListBreakGlassGrantsByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list break-glass grants: %w", err)
	}
	return toGrants(rows), nil
}

func (r *Repository) ListByRequester(ctx context.Context, q *db.Queries, tenantID, requesterID uuid.UUID) ([]Grant, error) {
	rows, err := q.ListBreakGlassGrantsByRequester(ctx, db.ListBreakGlassGrantsByRequesterParams{
		TenantID:    tenantID,
		RequestedBy: requesterID,
	})
	if err != nil {
		return nil, fmt.Errorf("list break-glass grants: %w", err)
	}
	return toGrants(rows), nil
}

func (r *Repository) Approve(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, durationMinutes int, decisionReason string) (Grant, error) {
	row, err := q.ApproveBreakGlassGrant(ctx, db.ApproveBreakGlassGrantParams{
		ID:             id,
		TenantID:       tenantID,
		DecidedBy:      uuid.NullUUID{UUID: decidedBy, Valid: true},
		Column4:        int32(durationMinutes),
		DecisionReason: decisionReason,
	})
	if err != nil {
		return Grant{}, mapWriteError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) Deny(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, decisionReason string) (Grant, error) {
	row, err := q.DenyBreakGlassGrant(ctx, db.DenyBreakGlassGrantParams{
		ID:             id,
		TenantID:       tenantID,
		DecidedBy:      uuid.NullUUID{UUID: decidedBy, Valid: true},
		DecisionReason: decisionReason,
	})
	if err != nil {
		return Grant{}, mapWriteError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) Revoke(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, reason string) (Grant, error) {
	row, err := q.RevokeBreakGlassGrant(ctx, db.RevokeBreakGlassGrantParams{
		ID:             id,
		TenantID:       tenantID,
		DecisionReason: reason,
	})
	if err != nil {
		return Grant{}, mapWriteError(err)
	}
	return toGrant(row), nil
}

func (r *Repository) Touch(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error) {
	row, err := q.TouchBreakGlassGrant(ctx, db.TouchBreakGlassGrantParams{ID: id, TenantID: tenantID})
	if err != nil {
		return Grant{}, mapReadError(err)
	}
	return toGrant(row), nil
}

func mapReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read break-glass grant: %w", err)
}

func mapWriteError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return fmt.Errorf("update break-glass grant: %w", err)
}

func toGrants(rows []db.BreakGlassGrant) []Grant {
	result := make([]Grant, 0, len(rows))
	for _, row := range rows {
		result = append(result, toGrant(row))
	}
	return result
}

func toGrant(row db.BreakGlassGrant) Grant {
	grant := Grant{
		ID:             row.ID,
		TenantID:       row.TenantID,
		PatientID:      row.PatientID,
		RequestedBy:    row.RequestedBy,
		Status:         row.Status,
		Reason:         row.Reason,
		DecisionReason: row.DecisionReason,
		RequestedAt:    row.RequestedAt,
	}
	if row.DecidedBy.Valid {
		value := row.DecidedBy.UUID
		grant.DecidedBy = &value
	}
	if row.DecidedAt.Valid {
		value := row.DecidedAt.Time
		grant.DecidedAt = &value
	}
	if row.ExpiresAt.Valid {
		value := row.ExpiresAt.Time
		grant.ExpiresAt = &value
	}
	if row.RevokedAt.Valid {
		value := row.RevokedAt.Time
		grant.RevokedAt = &value
	}
	if row.LastUsedAt.Valid {
		value := row.LastUsedAt.Time
		grant.LastUsedAt = &value
	}
	return grant
}
