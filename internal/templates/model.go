package templates

import (
	"time"

	"github.com/google/uuid"
)

// Template is a patient-specific, versioned list of suggested activities. It
// is a suggestion only: loading a template never records a performed activity
// (doc 11). Rows are immutable versions; editing creates a new version so a
// historical report can name the exact version it used.
type Template struct {
	ID         uuid.UUID          `json:"id"`
	TenantID   uuid.UUID          `json:"tenant_id"`
	PatientID  uuid.UUID          `json:"patient_id"`
	Name       string             `json:"name"`
	CreatedBy  uuid.UUID          `json:"created_by"`
	Version    int                `json:"version"`
	Status     string             `json:"status"`
	Activities []TemplateActivity `json:"activities"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// TemplateActivity is one suggested activity inside a template. ActivityID
// references a global service definition (the catalog activity), never a
// performed activity row.
type TemplateActivity struct {
	ID              uuid.UUID `json:"id"`
	TemplateID      uuid.UUID `json:"template_id"`
	ActivityID      uuid.UUID `json:"activity_id"`
	DefaultQuantity int       `json:"default_quantity"`
	SortOrder       int       `json:"sort_order"`
	ServiceCode     string    `json:"service_code"`
	ServiceName     string    `json:"service_name"`
	LegalArea       string    `json:"legal_area"`
	Unit            string    `json:"unit"`
}

// CreateInput is the validated input for a new template.
type CreateInput struct {
	PatientID  uuid.UUID
	Name       string
	Activities []ActivityInput
}

// UpdateInput replaces the activity list and creates a new template version.
type UpdateInput struct {
	Activities []ActivityInput
}

// ActivityInput is one suggested activity in a create or update request.
type ActivityInput struct {
	ActivityID      uuid.UUID
	DefaultQuantity int
	SortOrder       int
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
