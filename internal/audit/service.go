package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

const (
	defaultAuditLimit    = 50
	maxAuditLimit        = 200
	maxAuditFilterRunes  = 100
	auditListAction      = "audit.listed"
	auditDeniedAction    = "access.denied"
	auditReadEntityType  = "audit_event"
	auditReadEntityLabel = "audit.read"
)

// Store is the persistence boundary for audit reads.
type Store interface {
	List(ctx context.Context, q *db.Queries, tenantID uuid.UUID, filter ListFilter) ([]EventRecord, error)
}

// Service exposes the restricted audit read. Writing stays in Recorder so a
// business change and its audit row share one transaction.
type Service struct {
	store Store
	audit Recorder
}

func NewService(store Store, recorder Recorder) *Service {
	return &Service{store: store, audit: recorder}
}

// List returns audit events of the actor's tenant. Only a chef may read the
// log; an employee and a platform admin get no automatic access (doc 4 and the
// platform-admin reminder). The read itself is audited.
func (s *Service) List(ctx context.Context, q *db.Queries, actor Actor, requestID string, filter ListFilter) ([]EventRecord, error) {
	if actor.Role != "chef" {
		s.recordDenied(ctx, q, actor, requestID)
		return nil, ErrForbidden
	}

	normalized, err := validateFilter(filter)
	if err != nil {
		return nil, err
	}

	events, err := s.store.List(ctx, q, actor.TenantID, normalized)
	if err != nil {
		return nil, err
	}

	metadata := map[string]any{"count": len(events)}
	if normalized.Action != "" {
		metadata["action"] = normalized.Action
	}
	if normalized.EntityType != "" {
		metadata["entity_type"] = normalized.EntityType
	}
	if normalized.EntityID != nil {
		metadata["entity_id"] = normalized.EntityID.String()
	}
	if err := s.record(ctx, q, Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      auditListAction,
		EntityType:  auditReadEntityType,
		RequestID:   requestID,
		Metadata:    metadata,
	}); err != nil {
		return nil, err
	}
	return events, nil
}

func validateFilter(filter ListFilter) (ListFilter, error) {
	filter.Action = strings.TrimSpace(filter.Action)
	filter.EntityType = strings.TrimSpace(filter.EntityType)
	if len([]rune(filter.Action)) > maxAuditFilterRunes {
		return ListFilter{}, fmt.Errorf("%w: action filter is too long", ErrInvalid)
	}
	if len([]rune(filter.EntityType)) > maxAuditFilterRunes {
		return ListFilter{}, fmt.Errorf("%w: entity_type filter is too long", ErrInvalid)
	}
	if filter.Limit == 0 {
		filter.Limit = defaultAuditLimit
	}
	if filter.Limit < 1 || filter.Limit > maxAuditLimit {
		return ListFilter{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maxAuditLimit)
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return ListFilter{}, fmt.Errorf("%w: from must be before to", ErrInvalid)
	}
	return filter, nil
}

func (s *Service) record(ctx context.Context, q *db.Queries, event Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

func (s *Service) recordDenied(ctx context.Context, q *db.Queries, actor Actor, requestID string) {
	if actor.TenantID == uuid.Nil {
		return
	}
	// Best effort: a denied read must not turn into a 500.
	_ = s.record(ctx, q, Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      auditDeniedAction,
		EntityType:  auditReadEntityType,
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": auditReadEntityLabel},
	})
}
