// Package tenantadmin changes the status of one tenant on behalf of an
// operator. It replaces the VIES gate for manual activation (WP-05) and never
// deletes a tenant or its data.
package tenantadmin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Action is an operator request for one tenant.
type Action string

const (
	ActionActivate Action = "activate"
	ActionSuspend  Action = "suspend"
)

const (
	StatusValid     = "valid"
	StatusSuspended = "suspended"

	auditEntityType = "tenant"
)

var (
	// ErrInvalidAction is returned for an action other than activate or suspend.
	ErrInvalidAction = errors.New("action must be activate or suspend")
	// ErrTenantNotFound is returned when the tenant row does not exist. A
	// terminated tenant has no row (terminate_tenant deletes it), so it can
	// never be activated again.
	ErrTenantNotFound = errors.New("tenant not found or terminated")
)

// TenantRunner runs fn inside a transaction scoped to tenantID.
type TenantRunner func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error

// Result describes the outcome. Changed is false when the tenant already had
// the target status; then no audit event is written.
type Result struct {
	TenantID  uuid.UUID
	OldStatus string
	NewStatus string
	Changed   bool
}

// ParseAction validates the action flag.
func ParseAction(value string) (Action, error) {
	switch Action(value) {
	case ActionActivate, ActionSuspend:
		return Action(value), nil
	}
	return "", ErrInvalidAction
}

func (a Action) target() (status, auditAction string, err error) {
	switch a {
	case ActionActivate:
		return StatusValid, "tenant.activated", nil
	case ActionSuspend:
		return StatusSuspended, "tenant.suspended", nil
	}
	return "", "", ErrInvalidAction
}

// Apply changes the tenant status and writes one audit event in the same
// transaction. The metadata holds only the old and the new status.
func Apply(ctx context.Context, run TenantRunner, recorder audit.Recorder, tenantID uuid.UUID, action Action, requestID string) (Result, error) {
	newStatus, auditAction, err := action.target()
	if err != nil {
		return Result{}, err
	}
	if tenantID == uuid.Nil {
		return Result{}, fmt.Errorf("tenant id is required")
	}

	result := Result{TenantID: tenantID, NewStatus: newStatus}
	err = run(ctx, tenantID, func(q *db.Queries) error {
		oldStatus, err := q.GetTenantStatusForUpdate(ctx, tenantID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTenantNotFound
		}
		if err != nil {
			return fmt.Errorf("read tenant status: %w", err)
		}
		result.OldStatus = oldStatus
		if oldStatus == newStatus {
			return nil
		}

		if _, err := q.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{ID: tenantID, Status: newStatus}); err != nil {
			return fmt.Errorf("update tenant status: %w", err)
		}
		if err := recorder.Record(ctx, q, audit.Event{
			TenantID:   tenantID,
			Action:     auditAction,
			EntityType: auditEntityType,
			EntityID:   tenantID,
			RequestID:  requestID,
			Metadata: map[string]any{
				"old_status": oldStatus,
				"new_status": newStatus,
			},
		}); err != nil {
			return err
		}
		result.Changed = true
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
