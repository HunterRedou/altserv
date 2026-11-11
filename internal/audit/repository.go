package audit

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Repository reads audit rows with sqlc queries. RLS scopes every read to the
// tenant of the surrounding transaction.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) List(ctx context.Context, q *db.Queries, tenantID uuid.UUID, filter ListFilter) ([]EventRecord, error) {
	params := db.ListAuditEventsParams{
		TenantID:   tenantID,
		Action:     filter.Action,
		EntityType: filter.EntityType,
		RowLimit:   int32(filter.Limit),
	}
	if filter.EntityID != nil {
		params.EntityID = uuid.NullUUID{UUID: *filter.EntityID, Valid: true}
	}
	if filter.From != nil {
		params.FromTime = sql.NullTime{Time: *filter.From, Valid: true}
	}
	if filter.To != nil {
		params.ToTime = sql.NullTime{Time: *filter.To, Valid: true}
	}

	rows, err := q.ListAuditEvents(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}

	result := make([]EventRecord, 0, len(rows))
	for _, row := range rows {
		event := EventRecord{
			ID:         row.ID,
			TenantID:   row.TenantID,
			Action:     row.Action,
			EntityType: row.EntityType,
			OccurredAt: row.OccurredAt,
			RequestID:  row.RequestID,
			Metadata:   row.Metadata,
		}
		if row.ActorUserID.Valid {
			value := row.ActorUserID.UUID
			event.ActorUserID = &value
		}
		if row.EntityID.Valid {
			value := row.EntityID.UUID
			event.EntityID = &value
		}
		result = append(result, event)
	}
	return result, nil
}
