package retention

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid retention input")
	ErrNotFound  = errors.New("retention record not found")
	ErrConflict  = errors.New("retention record is not in a valid state")
	// ErrLegalHold means an active legal hold blocks the requested deletion.
	ErrLegalHold = errors.New("an active legal hold blocks this deletion")
)

const (
	// MinPeriodMonths keeps a policy from deleting almost immediately.
	MinPeriodMonths = 1
	// MaxPeriodMonths bounds a typo. It is not a legal recommendation.
	MaxPeriodMonths = 600

	maxReasonRunes = 1000
	maxNoteRunes   = 1000

	// DefaultLegalHoldReviewMonths is the first review date for a new hold. The
	// retention policy asks for a review at least every six months.
	DefaultLegalHoldReviewMonths = 6
)

// categoryTriggers is the only valid mapping. The database enforces the same
// pairs, so a policy cannot claim an unsupported trigger.
var categoryTriggers = map[string]string{
	"patient_master_data": "end_of_care",
	"care_documentation":  "end_of_care",
	"billing":             "billing_year",
	"audit":               "event_created",
	"assignments":         "revocation",
	"break_glass":         "decision",
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// Policy is a tenant-configured retention period. A new policy is disabled
// until a chef confirms it.
type Policy struct {
	ID           uuid.UUID  `json:"id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	Category     string     `json:"category"`
	TriggerEvent string     `json:"trigger_event"`
	PeriodMonths int        `json:"period_months"`
	Status       string     `json:"status"`
	Note         string     `json:"note"`
	CreatedBy    uuid.UUID  `json:"created_by"`
	ConfirmedBy  *uuid.UUID `json:"confirmed_by,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// PolicyInput is the validated input for a new or updated policy.
type PolicyInput struct {
	Category     string
	TriggerEvent string
	PeriodMonths int
	Note         string
}

// PolicyUpdateInput changes the period and the note of an existing policy. The
// category and trigger are fixed for the lifetime of the row.
type PolicyUpdateInput struct {
	PeriodMonths int
	Note         string
}

// LegalHold suspends deletion for one patient or for the whole tenant.
type LegalHold struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	PatientID   *uuid.UUID `json:"patient_id,omitempty"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	LiftReason  string     `json:"lift_reason,omitempty"`
	DeclaredBy  uuid.UUID  `json:"declared_by"`
	DeclaredAt  time.Time  `json:"declared_at"`
	LiftedBy    *uuid.UUID `json:"lifted_by,omitempty"`
	LiftedAt    *time.Time `json:"lifted_at,omitempty"`
	ReviewDueAt *time.Time `json:"review_due_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// LegalHoldInput is the validated input for a new legal hold. A nil PatientID
// means a tenant-wide hold.
type LegalHoldInput struct {
	PatientID   *uuid.UUID
	Reason      string
	ReviewDueAt *time.Time
}

// DeletionRequest is one patient-scoped deletion request. Status moves from
// pending to approved or rejected, and then to executed or cancelled.
type DeletionRequest struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	RequestedBy    uuid.UUID  `json:"requested_by"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedBy      *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	CancelledBy    *uuid.UUID `json:"cancelled_by,omitempty"`
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
	ExecutedBy     *uuid.UUID `json:"executed_by,omitempty"`
	ExecutedAt     *time.Time `json:"executed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// DeletionRequestInput is the validated input for a new request.
type DeletionRequestInput struct {
	PatientID uuid.UUID
	Reason    string
}

// RegisterEntry is the append-only evidence of an executed deletion. It stores
// counts, not the deleted personal data.
type RegisterEntry struct {
	ID                uuid.UUID       `json:"id"`
	TenantID          uuid.UUID       `json:"tenant_id"`
	DeletionRequestID uuid.UUID       `json:"deletion_request_id"`
	PatientID         uuid.UUID       `json:"patient_id"`
	ExecutedBy        uuid.UUID       `json:"executed_by"`
	ExecutedAt        time.Time       `json:"executed_at"`
	RowCounts         json.RawMessage `json:"row_counts"`
	CreatedAt         time.Time       `json:"created_at"`
}

// Candidate is one retention review line. It is a count only; it never names a
// patient.
type Candidate struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
}

// DeletionResult is returned after a successful deletion.
type DeletionResult struct {
	Request  DeletionRequest `json:"request"`
	Register RegisterEntry   `json:"register"`
}
