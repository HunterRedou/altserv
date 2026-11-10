package assignments

import (
	"time"

	"github.com/google/uuid"
)

// Assignment links one employee to one patient for a limited time.
type Assignment struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	EmployeeID     uuid.UUID  `json:"employee_id"`
	AssignedBy     uuid.UUID  `json:"assigned_by"`
	AssignmentType string     `json:"assignment_type"`
	ValidFrom      time.Time  `json:"valid_from"`
	ValidUntil     *time.Time `json:"valid_until,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CreateInput is the validated input for a new assignment.
type CreateInput struct {
	EmployeeID     uuid.UUID
	AssignmentType string
	ValidFrom      time.Time
	ValidUntil     *time.Time
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
