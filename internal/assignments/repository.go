package assignments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Repository implements Store with sqlc queries.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, params CreateParams) (Assignment, error) {
	validUntil := sql.NullTime{}
	if params.ValidUntil != nil {
		validUntil = sql.NullTime{Time: *params.ValidUntil, Valid: true}
	}

	row, err := q.CreateAssignment(ctx, db.CreateAssignmentParams{
		TenantID:       params.TenantID,
		PatientID:      params.PatientID,
		EmployeeID:     params.EmployeeID,
		AssignedBy:     params.AssignedBy,
		AssignmentType: params.AssignmentType,
		ValidFrom:      params.ValidFrom,
		ValidUntil:     validUntil,
	})
	if err != nil {
		return Assignment{}, fmt.Errorf("create assignment: %w", err)
	}
	return toAssignment(row), nil
}

func (r *Repository) Revoke(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Assignment, error) {
	row, err := q.RevokeAssignment(ctx, db.RevokeAssignmentParams{
		ID:       id,
		TenantID: tenantID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, ErrNotFound
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("revoke assignment: %w", err)
	}
	return toAssignment(row), nil
}

func (r *Repository) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]Assignment, error) {
	rows, err := q.ListAssignmentsByPatient(ctx, db.ListAssignmentsByPatientParams{
		TenantID:  tenantID,
		PatientID: patientID,
	})
	if err != nil {
		return nil, fmt.Errorf("list assignments by patient: %w", err)
	}
	return toAssignments(rows), nil
}

func (r *Repository) ListByTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Assignment, error) {
	rows, err := q.ListAssignmentsByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	return toAssignments(rows), nil
}

func (r *Repository) IsAssigned(ctx context.Context, q *db.Queries, tenantID, patientID, employeeID uuid.UUID) (bool, error) {
	assigned, err := q.IsPatientAssignedToEmployee(ctx, db.IsPatientAssignedToEmployeeParams{
		TenantID:   tenantID,
		PatientID:  patientID,
		EmployeeID: employeeID,
	})
	if err != nil {
		return false, fmt.Errorf("check assignment: %w", err)
	}
	return assigned, nil
}

func (r *Repository) GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error) {
	user, err := q.GetUserByID(ctx, db.GetUserByIDParams{
		ID:       userID,
		TenantID: tenantID,
	})
	if err != nil {
		return "", err
	}
	return user.Role, nil
}

func toAssignment(row db.PatientAssignment) Assignment {
	return Assignment{
		ID:             row.ID,
		TenantID:       row.TenantID,
		PatientID:      row.PatientID,
		EmployeeID:     row.EmployeeID,
		AssignedBy:     row.AssignedBy,
		AssignmentType: row.AssignmentType,
		ValidFrom:      row.ValidFrom,
		ValidUntil:     nullTimePtr(row.ValidUntil),
		RevokedAt:      nullTimePtr(row.RevokedAt),
		CreatedAt:      row.CreatedAt,
	}
}

func toAssignments(rows []db.PatientAssignment) []Assignment {
	assignments := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		assignments = append(assignments, toAssignment(row))
	}
	return assignments
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
