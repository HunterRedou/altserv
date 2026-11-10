package patients

import (
	"time"

	"github.com/google/uuid"
)

// Patient is the decrypted domain model. Only the API layer sees plaintext.
type Patient struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	BirthDate time.Time `json:"birth_date"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateInput is the validated input for a new patient.
type CreateInput struct {
	FirstName string
	LastName  string
	BirthDate time.Time
	Status    string
}

// UpdateInput is a partial patient update. Nil fields are left unchanged.
type UpdateInput struct {
	FirstName *string
	LastName  *string
	BirthDate *time.Time
	Status    *string
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
