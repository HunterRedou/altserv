package activities

import (
	"time"

	"github.com/google/uuid"
)

// Activity is what was actually done for a patient. It may be billable (linked
// to a service definition and contract) or not. The service fields are a
// snapshot taken when the activity is recorded, so later catalog or contract
// changes cannot rewrite history (doc 9).
type Activity struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	PatientID           uuid.UUID  `json:"patient_id"`
	EmployeeID          uuid.UUID  `json:"employee_id"`
	ServiceDefinitionID *uuid.UUID `json:"service_definition_id,omitempty"`
	ContractID          *uuid.UUID `json:"contract_id,omitempty"`
	PerformedAt         time.Time  `json:"performed_at"`
	DurationMinutes     int        `json:"duration_minutes"`
	Note                string     `json:"note,omitempty"`
	Status              string     `json:"status"`
	ServiceCode         string     `json:"service_code,omitempty"`
	ServiceName         string     `json:"service_name,omitempty"`
	LegalArea           string     `json:"legal_area,omitempty"`
	ContractReference   string     `json:"contract_reference,omitempty"`
	PayerName           string     `json:"payer_name,omitempty"`
	CreatedBy           uuid.UUID  `json:"created_by"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateInput is the validated input for a new activity. ServiceDefinitionID
// and ContractID are either both set (billable) or both nil (not billable).
type CreateInput struct {
	PatientID           uuid.UUID
	EmployeeID          uuid.UUID
	ServiceDefinitionID *uuid.UUID
	ContractID          *uuid.UUID
	PerformedAt         time.Time
	DurationMinutes     int
	Note                string
}

// Snapshot is the billable service state captured at recording time.
type Snapshot struct {
	ServiceCode       string
	ServiceName       string
	LegalArea         string
	ContractReference string
	PayerName         string
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
