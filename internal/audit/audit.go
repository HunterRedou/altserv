package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Event is one append-only audit record. Metadata must never contain patient
// names, free text or other unnecessary health data (doc 15).
type Event struct {
	TenantID    uuid.UUID
	ActorUserID uuid.UUID
	Action      string
	EntityType  string
	EntityID    uuid.UUID
	RequestID   string
	Metadata    map[string]any
}

// Recorder writes an event inside the caller's transaction so the business
// change and its audit record commit or roll back together.
type Recorder interface {
	Record(ctx context.Context, q *db.Queries, event Event) error
}

// SQLRecorder stores events in audit_events.
type SQLRecorder struct{}

func (SQLRecorder) Record(ctx context.Context, q *db.Queries, event Event) error {
	if event.TenantID == uuid.Nil {
		return fmt.Errorf("audit event requires a tenant id")
	}
	if event.Action == "" {
		return fmt.Errorf("audit event requires an action")
	}
	if event.EntityType == "" {
		return fmt.Errorf("audit event requires an entity type")
	}

	metadata := json.RawMessage(`{}`)
	if len(event.Metadata) > 0 {
		encoded, err := json.Marshal(event.Metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
		metadata = encoded
	}

	actor := uuid.NullUUID{}
	if event.ActorUserID != uuid.Nil {
		actor = uuid.NullUUID{UUID: event.ActorUserID, Valid: true}
	}
	entity := uuid.NullUUID{}
	if event.EntityID != uuid.Nil {
		entity = uuid.NullUUID{UUID: event.EntityID, Valid: true}
	}

	if _, err := q.CreateAuditEvent(ctx, db.CreateAuditEventParams{
		TenantID:    event.TenantID,
		ActorUserID: actor,
		Action:      event.Action,
		EntityType:  event.EntityType,
		EntityID:    entity,
		RequestID:   event.RequestID,
		Metadata:    metadata,
	}); err != nil {
		return fmt.Errorf("create audit event: %w", err)
	}
	return nil
}

// NoopRecorder is used by unit tests that do not care about audit rows.
type NoopRecorder struct{}

func (NoopRecorder) Record(context.Context, *db.Queries, Event) error { return nil }
