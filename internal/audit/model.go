package audit

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid audit query")
)

// Actor is the authenticated user reading the audit log.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// ListFilter narrows an audit read. Zero values mean "no filter". Limit is
// clamped by the service; an explicit value above the maximum is rejected.
type ListFilter struct {
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	From       *time.Time
	To         *time.Time
	Limit      int
}

// EventRecord is one audit row returned by the read API. Metadata contains ids,
// statuses and counts only, never free text or health data (doc 15).
type EventRecord struct {
	ID          uuid.UUID       `json:"id"`
	TenantID    uuid.UUID       `json:"tenant_id"`
	ActorUserID *uuid.UUID      `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *uuid.UUID      `json:"entity_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at"`
	RequestID   string          `json:"request_id"`
	Metadata    json.RawMessage `json:"metadata"`
}
