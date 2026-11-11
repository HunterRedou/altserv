package billing

import (
	"time"

	"github.com/google/uuid"
)

// BillingRun is one deterministic evaluation of a released report (doc 14). It
// is not an AI result: the same report, contracts and rules always produce the
// same lines and findings. A chef must approve a run before it can be exported.
type BillingRun struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	ReportID    uuid.UUID  `json:"report_id"`
	Status      string     `json:"status"`
	Version     int        `json:"version"`
	GeneratedBy uuid.UUID  `json:"generated_by"`
	GeneratedAt time.Time  `json:"generated_at"`
	ApprovedBy  *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	VoidedBy    *uuid.UUID `json:"voided_by,omitempty"`
	VoidedAt    *time.Time `json:"voided_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Lines    []BillingLine    `json:"lines,omitempty"`
	Findings []BillingFinding `json:"findings,omitempty"`
}

// BillingLine is one suggested billable service for one performed activity. The
// service, contract and payer fields are snapshots, so a later catalog change
// cannot rewrite an approved run.
type BillingLine struct {
	ID                  uuid.UUID `json:"id"`
	RunID               uuid.UUID `json:"run_id"`
	ReportActivityID    uuid.UUID `json:"report_activity_id"`
	ActivityID          uuid.UUID `json:"activity_id"`
	ServiceDefinitionID uuid.UUID `json:"service_definition_id"`
	ContractID          uuid.UUID `json:"contract_id"`
	Quantity            int       `json:"quantity"`
	PerformedAt         time.Time `json:"performed_at"`
	ServiceCode         string    `json:"service_code"`
	ServiceName         string    `json:"service_name"`
	LegalArea           string    `json:"legal_area"`
	PayerName           string    `json:"payer_name"`
	ContractReference   string    `json:"contract_reference"`
	SortOrder           int       `json:"sort_order"`
}

// BillingFinding is one check result. An error blocks approval; a warning or
// info is shown to the chef but does not block.
type BillingFinding struct {
	ID         uuid.UUID  `json:"id"`
	RunID      uuid.UUID  `json:"run_id"`
	Severity   string     `json:"severity"`
	Code       string     `json:"code"`
	Message    string     `json:"message"`
	ActivityID *uuid.UUID `json:"activity_id,omitempty"`
}

// BillingRule is a tenant-specific structural rule. Generic checks (validity,
// billability, report status, double billing) live in the engine. Exclusions,
// prerequisites and combination rules are tenant configuration.
type BillingRule struct {
	ID                         uuid.UUID  `json:"id"`
	TenantID                   uuid.UUID  `json:"tenant_id"`
	Kind                       string     `json:"kind"`
	ServiceDefinitionID        uuid.UUID  `json:"service_definition_id"`
	RelatedServiceDefinitionID uuid.UUID  `json:"related_service_definition_id"`
	LegalArea                  string     `json:"legal_area"`
	Description                string     `json:"description"`
	ValidFrom                  time.Time  `json:"valid_from"`
	ValidUntil                 *time.Time `json:"valid_until,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// CreateRuleInput is the validated input for a tenant billing rule.
type CreateRuleInput struct {
	Kind                       string
	ServiceDefinitionID        uuid.UUID
	RelatedServiceDefinitionID uuid.UUID
	LegalArea                  string
	Description                string
	ValidFrom                  time.Time
	ValidUntil                 *time.Time
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// ExportData is the immutable input for a CSV or PDF export. It contains ids and
// billing data only, no patient names or free text.
type ExportData struct {
	Run       BillingRun
	ReportID  uuid.UUID
	PatientID uuid.UUID
	VisitID   uuid.UUID
	Lines     []BillingLine
}

// Candidate is one report activity with the current service definition and
// contract state. It is the input to the deterministic rule engine.
type Candidate struct {
	ReportActivityID uuid.UUID
	ActivityID       uuid.UUID
	Quantity         int
	PerformedAt      time.Time
	SortOrder        int

	ActivityStatus string

	ServiceDefinitionID *uuid.UUID
	ContractID          *uuid.UUID

	SnapshotServiceCode       string
	SnapshotServiceName       string
	SnapshotLegalArea         string
	SnapshotPayerName         string
	SnapshotContractReference string

	DefinitionFound      bool
	DefinitionCode       string
	DefinitionName       string
	DefinitionLegalArea  string
	DefinitionValidFrom  time.Time
	DefinitionValidUntil *time.Time

	ContractFound      bool
	ContractPayerName  string
	ContractReference  string
	ContractLegalArea  string
	ContractValidFrom  time.Time
	ContractValidUntil *time.Time
}

// Rule is the engine view of a tenant billing rule.
type Rule struct {
	ID                         uuid.UUID
	Kind                       string
	ServiceDefinitionID        uuid.UUID
	RelatedServiceDefinitionID uuid.UUID
	LegalArea                  string
	Description                string
	ValidFrom                  time.Time
	ValidUntil                 *time.Time
}

// Line is a suggested billing line produced by the engine.
type Line struct {
	ReportActivityID    uuid.UUID
	ActivityID          uuid.UUID
	ServiceDefinitionID uuid.UUID
	ContractID          uuid.UUID
	Quantity            int
	PerformedAt         time.Time
	ServiceCode         string
	ServiceName         string
	LegalArea           string
	PayerName           string
	ContractReference   string
	SortOrder           int
}

// Finding is a check result produced by the engine.
type Finding struct {
	Severity   string
	Code       string
	Message    string
	ActivityID *uuid.UUID
}

// EvaluateInput is the complete deterministic input of the rule engine.
type EvaluateInput struct {
	ReportStatus  string
	Candidates    []Candidate
	Rules         []Rule
	AlreadyBilled map[uuid.UUID]uuid.UUID
}

// Evaluation is the deterministic output of the rule engine.
type Evaluation struct {
	Lines    []Line
	Findings []Finding
}
