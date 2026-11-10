package assignments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid assignment input")
	ErrNotFound  = errors.New("assignment not found")
)

// CreateParams is the persistence input for a new assignment.
type CreateParams struct {
	TenantID       uuid.UUID
	PatientID      uuid.UUID
	EmployeeID     uuid.UUID
	AssignedBy     uuid.UUID
	AssignmentType string
	ValidFrom      time.Time
	ValidUntil     *time.Time
}

// Store is the persistence boundary used by the service.
type Store interface {
	Create(ctx context.Context, q *db.Queries, params CreateParams) (Assignment, error)
	Revoke(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Assignment, error)
	ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]Assignment, error)
	ListByTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Assignment, error)
	IsAssigned(ctx context.Context, q *db.Queries, tenantID, patientID, employeeID uuid.UUID) (bool, error)
	GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error)
}

// Service contains assignment business rules. Database work runs in the
// caller's tenant-scoped transaction so the change and its audit event commit
// together.
type Service struct {
	store Store
	audit audit.Recorder
	now   func() time.Time
}

func NewService(store Store, recorder audit.Recorder) *Service {
	return &Service{store: store, audit: recorder, now: time.Now}
}

// CanAccessPatient reports whether the employee has a currently valid,
// non-revoked assignment for the patient.
func (s *Service) CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error) {
	if tenantID == uuid.Nil || employeeID == uuid.Nil || patientID == uuid.Nil {
		return false, nil
	}
	return s.store.IsAssigned(ctx, q, tenantID, patientID, employeeID)
}

func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID, input CreateInput) (Assignment, error) {
	if !canManageAssignments(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "assignment.create")
		return Assignment{}, ErrForbidden
	}
	if patientID == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}

	normalized, err := validateInput(input, s.now())
	if err != nil {
		return Assignment{}, err
	}

	role, err := s.store.GetUserRole(ctx, q, actor.TenantID, normalized.EmployeeID)
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, fmt.Errorf("%w: employee not found in this tenant", ErrInvalid)
	}
	if err != nil {
		return Assignment{}, err
	}
	if role != "employee" {
		return Assignment{}, fmt.Errorf("%w: employee_id must reference an employee", ErrInvalid)
	}

	assignment, err := s.store.Create(ctx, q, CreateParams{
		TenantID:       actor.TenantID,
		PatientID:      patientID,
		EmployeeID:     normalized.EmployeeID,
		AssignedBy:     actor.UserID,
		AssignmentType: normalized.AssignmentType,
		ValidFrom:      normalized.ValidFrom,
		ValidUntil:     normalized.ValidUntil,
	})
	if err != nil {
		return Assignment{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "assignment.created",
		EntityType:  "patient_assignment",
		EntityID:    assignment.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":      assignment.PatientID.String(),
			"employee_id":     assignment.EmployeeID.String(),
			"assignment_type": assignment.AssignmentType,
			"valid_from":      assignment.ValidFrom.Format(time.RFC3339),
			"valid_until":     formatOptionalTime(assignment.ValidUntil),
		},
	}); err != nil {
		return Assignment{}, err
	}
	return assignment, nil
}

func (s *Service) Revoke(ctx context.Context, q *db.Queries, actor Actor, requestID string, assignmentID uuid.UUID) (Assignment, error) {
	if !canManageAssignments(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "assignment.revoke")
		return Assignment{}, ErrForbidden
	}
	if assignmentID == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: assignment id is required", ErrInvalid)
	}

	assignment, err := s.store.Revoke(ctx, q, actor.TenantID, assignmentID)
	if err != nil {
		return Assignment{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "assignment.revoked",
		EntityType:  "patient_assignment",
		EntityID:    assignment.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":      assignment.PatientID.String(),
			"employee_id":     assignment.EmployeeID.String(),
			"assignment_type": assignment.AssignmentType,
		},
	}); err != nil {
		return Assignment{}, err
	}
	return assignment, nil
}

func (s *Service) ListByPatient(ctx context.Context, q *db.Queries, actor Actor, patientID uuid.UUID) ([]Assignment, error) {
	if !canManageAssignments(actor.Role) {
		return nil, ErrForbidden
	}
	if patientID == uuid.Nil {
		return nil, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}
	return s.store.ListByPatient(ctx, q, actor.TenantID, patientID)
}

func (s *Service) ListByTenant(ctx context.Context, q *db.Queries, actor Actor) ([]Assignment, error) {
	if !canManageAssignments(actor.Role) {
		return nil, ErrForbidden
	}
	return s.store.ListByTenant(ctx, q, actor.TenantID)
}

// canManageAssignments is intentionally limited to the chef. A platform admin
// must not get patient data or assignment control automatically (doc 4).
func canManageAssignments(role string) bool {
	return role == "chef"
}

func validateInput(input CreateInput, now time.Time) (CreateInput, error) {
	if input.EmployeeID == uuid.Nil {
		return CreateInput{}, fmt.Errorf("%w: employee_id is required", ErrInvalid)
	}

	switch input.AssignmentType {
	case "":
		input.AssignmentType = "regular"
	case "regular", "substitute":
	default:
		return CreateInput{}, fmt.Errorf("%w: assignment_type %q is not allowed", ErrInvalid, input.AssignmentType)
	}

	if input.ValidFrom.IsZero() {
		input.ValidFrom = now
	}
	if input.ValidUntil != nil && !input.ValidUntil.After(input.ValidFrom) {
		return CreateInput{}, fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalid)
	}

	return input, nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}

func (s *Service) record(ctx context.Context, q *db.Queries, event audit.Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

func (s *Service) recordDenied(ctx context.Context, q *db.Queries, actor Actor, requestID, action string) {
	if actor.TenantID == uuid.Nil {
		return
	}
	_ = s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "patient_assignment",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}
