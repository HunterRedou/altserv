package breakglass

import (
	"context"
	"errors"
	"testing"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// TestRunWithDenialAuditCommitsForbidden is a regression test for a lost audit
// row: a denied request records access.denied inside the tenant transaction,
// but returning ErrForbidden from the callback made db.WithTenant roll the
// transaction back. The handler must commit a forbidden transaction so the
// denial survives, while still returning ErrForbidden to the client.
func TestRunWithDenialAuditCommitsForbidden(t *testing.T) {
	var callbackErr error
	handler := &Handler{
		withTenant: func(_ context.Context, _ uuid.UUID, fn func(*db.Queries) error) error {
			callbackErr = fn(nil)
			return callbackErr
		},
	}

	err := handler.runWithDenialAudit(context.Background(), uuid.New(), func(*db.Queries) error {
		return ErrForbidden
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if callbackErr != nil {
		t.Fatalf("callback error = %v, want nil so the audit row commits", callbackErr)
	}
}

// TestRunWithDenialAuditRollsBackOtherErrors makes sure the commit path is
// narrow. A missing grant must not commit.
func TestRunWithDenialAuditRollsBackOtherErrors(t *testing.T) {
	var callbackErr error
	handler := &Handler{
		withTenant: func(_ context.Context, _ uuid.UUID, fn func(*db.Queries) error) error {
			callbackErr = fn(nil)
			return callbackErr
		},
	}

	err := handler.runWithDenialAudit(context.Background(), uuid.New(), func(*db.Queries) error {
		return ErrNotFound
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
	if !errors.Is(callbackErr, ErrNotFound) {
		t.Fatalf("callback error = %v, want not found so the transaction rolls back", callbackErr)
	}
}
