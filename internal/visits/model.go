package visits

import (
	"time"

	"github.com/google/uuid"
)

// Visit is the planned or actual deployment of one employee to one patient
// (doc 12). Patient, visit and report are separate entities: the visit only
// schedules and records the deployment, never the care report itself.
type Visit struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	PatientID uuid.UUID `json:"patient_id"`
	// EmployeeID is the planned employee. ActualEmployeeID is set when the
	// visit starts, so a substitution is explicit.
	EmployeeID       uuid.UUID  `json:"employee_id"`
	ActualEmployeeID *uuid.UUID `json:"actual_employee_id,omitempty"`
	PlannedStart     time.Time  `json:"planned_start"`
	ActualStart      *time.Time `json:"actual_start,omitempty"`
	ActualEnd        *time.Time `json:"actual_end,omitempty"`
	Status           string     `json:"status"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// CreateInput is the validated input for a planned visit.
type CreateInput struct {
	PatientID    uuid.UUID
	EmployeeID   uuid.UUID
	PlannedStart time.Time
}

// ScheduleInput reschedules a planned visit. Nil fields keep their value.
type ScheduleInput struct {
	EmployeeID   *uuid.UUID
	PlannedStart *time.Time
}

// CorrectInput corrects the actual times of a started visit. A non-empty
// reason is required so every correction is traceable in the audit log.
type CorrectInput struct {
	ActualStart *time.Time
	ActualEnd   *time.Time
	Reason      string
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
