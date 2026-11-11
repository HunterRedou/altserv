package reports

import (
	"time"

	"github.com/google/uuid"
)

// Report documents the care that happened during one visit (doc 13). A patient,
// a visit and a report are separate entities. The report is created as a draft,
// submitted, approved and finally marked ready for billing.
type Report struct {
	ID         uuid.UUID `json:"id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	VisitID    uuid.UUID `json:"visit_id"`
	PatientID  uuid.UUID `json:"patient_id"`
	EmployeeID uuid.UUID `json:"employee_id"`
	Status     string    `json:"status"`
	Note       string    `json:"note,omitempty"`
	// Version supports optimistic locking. Every write must name the version it
	// read. A stale write is rejected instead of overwriting a newer one.
	Version     int        `json:"version"`
	CreatedBy   uuid.UUID  `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	ApprovedBy  *uuid.UUID `json:"approved_by,omitempty"`

	Activities []ReportActivity `json:"activities,omitempty"`
}

// ReportActivity links a performed activity to a report. It references the
// activity, not a catalog definition, so the report keeps the care that was
// actually done (doc 9 and doc 13).
type ReportActivity struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	ReportID    uuid.UUID `json:"report_id"`
	ActivityID  uuid.UUID `json:"activity_id"`
	Quantity    int       `json:"quantity"`
	PerformedAt time.Time `json:"performed_at"`
	SortOrder   int       `json:"sort_order"`
}

// ReportTransition records a return or correction separately from the report
// row. The reason may contain health data, so it is encrypted and never copied
// into the audit metadata.
type ReportTransition struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	ReportID    uuid.UUID `json:"report_id"`
	FromStatus  string    `json:"from_status"`
	ToStatus    string    `json:"to_status"`
	Kind        string    `json:"kind"`
	ActorUserID uuid.UUID `json:"actor_user_id"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// ActivityInput is one performed activity on a report.
type ActivityInput struct {
	ActivityID uuid.UUID
	Quantity   int
}

// CreateInput creates a draft report for a visit.
type CreateInput struct {
	Note       string
	Activities []ActivityInput
}

// UpdateInput edits a draft report. Nil fields keep their value. A version is
// always required so a stale edit cannot overwrite a newer one.
type UpdateInput struct {
	Version    int
	Note       *string
	Activities *[]ActivityInput
}

// ReturnInput returns a submitted or approved report to draft. A reason is
// required and is stored as a separate, encrypted transition row.
type ReturnInput struct {
	Version int
	Reason  string
}
