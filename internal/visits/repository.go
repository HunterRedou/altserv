package visits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Repository persists visits with sqlc queries.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error) {
	exists, err := q.PatientExists(ctx, db.PatientExistsParams{ID: patientID, TenantID: tenantID})
	if err != nil {
		return false, fmt.Errorf("check patient: %w", err)
	}
	return exists, nil
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, tenantID, createdBy uuid.UUID, input CreateInput) (Visit, error) {
	row, err := q.CreateVisit(ctx, db.CreateVisitParams{
		TenantID:     tenantID,
		PatientID:    input.PatientID,
		EmployeeID:   input.EmployeeID,
		PlannedStart: input.PlannedStart.UTC(),
		CreatedBy:    createdBy,
	})
	if err != nil {
		return Visit{}, fmt.Errorf("create visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error) {
	row, err := q.GetVisit(ctx, db.GetVisitParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrNotFound
	}
	if err != nil {
		return Visit{}, fmt.Errorf("get visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Visit, error) {
	rows, err := q.ListVisitsByPatient(ctx, db.ListVisitsByPatientParams{
		TenantID:   tenantID,
		PatientID:  patientID,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list visits: %w", err)
	}
	visits := make([]Visit, 0, len(rows))
	for _, row := range rows {
		visits = append(visits, toVisit(row))
	}
	return visits, nil
}

func (r *Repository) UpdateSchedule(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, employeeID uuid.UUID, plannedStart time.Time) (Visit, error) {
	row, err := q.UpdateVisitSchedule(ctx, db.UpdateVisitScheduleParams{
		ID:           id,
		TenantID:     tenantID,
		EmployeeID:   employeeID,
		PlannedStart: plannedStart.UTC(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrConflict
	}
	if err != nil {
		return Visit{}, fmt.Errorf("reschedule visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) Start(ctx context.Context, q *db.Queries, tenantID, id, actualEmployeeID uuid.UUID) (Visit, error) {
	row, err := q.StartVisit(ctx, db.StartVisitParams{
		ID:               id,
		TenantID:         tenantID,
		ActualEmployeeID: uuid.NullUUID{UUID: actualEmployeeID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrConflict
	}
	if err != nil {
		return Visit{}, fmt.Errorf("start visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) Complete(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error) {
	row, err := q.CompleteVisit(ctx, db.CompleteVisitParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrConflict
	}
	if err != nil {
		return Visit{}, fmt.Errorf("complete visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) Cancel(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error) {
	row, err := q.CancelVisit(ctx, db.CancelVisitParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrConflict
	}
	if err != nil {
		return Visit{}, fmt.Errorf("cancel visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) Correct(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, actualStart, actualEnd *time.Time) (Visit, error) {
	row, err := q.CorrectVisit(ctx, db.CorrectVisitParams{
		ID:          id,
		TenantID:    tenantID,
		ActualStart: nullableTime(actualStart),
		ActualEnd:   nullableTime(actualEnd),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrConflict
	}
	if err != nil {
		return Visit{}, fmt.Errorf("correct visit: %w", err)
	}
	return toVisit(row), nil
}

func (r *Repository) GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error) {
	user, err := q.GetUserByID(ctx, db.GetUserByIDParams{ID: userID, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get user role: %w", err)
	}
	return user.Role, nil
}

func toVisit(row db.Visit) Visit {
	visit := Visit{
		ID:           row.ID,
		TenantID:     row.TenantID,
		PatientID:    row.PatientID,
		EmployeeID:   row.EmployeeID,
		PlannedStart: row.PlannedStart,
		Status:       row.Status,
		CreatedBy:    row.CreatedBy,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if row.ActualEmployeeID.Valid {
		id := row.ActualEmployeeID.UUID
		visit.ActualEmployeeID = &id
	}
	if row.ActualStart.Valid {
		start := row.ActualStart.Time
		visit.ActualStart = &start
	}
	if row.ActualEnd.Valid {
		end := row.ActualEnd.Time
		visit.ActualEnd = &end
	}
	return visit
}

func nullableTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}
