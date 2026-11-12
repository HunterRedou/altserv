package catalog

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Catalog is one versioned reference catalog (for example a Brandenburg
// catalog). Catalogs are global reference data, not tenant data.
type Catalog struct {
	ID         uuid.UUID  `json:"id"`
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Region     string     `json:"region"`
	SourceNote string     `json:"source_note"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Definition is one billable service inside a catalog. The legal area keeps
// SGB XI and SGB V apart; the code is whatever the verified catalog says.
type Definition struct {
	ID          uuid.UUID  `json:"id"`
	CatalogID   uuid.UUID  `json:"catalog_id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	LegalArea   string     `json:"legal_area"`
	Unit        string     `json:"unit"`
	ValidFrom   time.Time  `json:"valid_from"`
	ValidUntil  *time.Time `json:"valid_until,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Contract is a tenant-specific agreement that a billable service can be
// settled with a payer during a validity window.
type Contract struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	ServiceDefinitionID uuid.UUID  `json:"service_definition_id"`
	PayerName           string     `json:"payer_name"`
	ContractReference   string     `json:"contract_reference"`
	LegalArea           string     `json:"legal_area"`
	ValidFrom           time.Time  `json:"valid_from"`
	ValidUntil          *time.Time `json:"valid_until,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateCatalogInput is the validated input for a new catalog version. Global
// catalogs are created by a platform admin through the reviewed SECURITY
// DEFINER functions; the application role has no direct write access.
type CreateCatalogInput struct {
	Code       string
	Name       string
	Region     string
	SourceNote string
	ValidFrom  time.Time
	ValidUntil *time.Time
}

// CreateDefinitionInput is the validated input for a new billable service
// inside a catalog version.
type CreateDefinitionInput struct {
	CatalogID   uuid.UUID
	Code        string
	Name        string
	Description string
	LegalArea   string
	Unit        string
	ValidFrom   time.Time
	ValidUntil  *time.Time
}

// CreateContractInput is the validated input for a new contract version.
type CreateContractInput struct {
	ServiceDefinitionID uuid.UUID
	PayerName           string
	ContractReference   string
	LegalArea           string
	ValidFrom           time.Time
	ValidUntil          *time.Time
}

// Rule maps a free activity label to a billable service and, optionally, a
// contract. Rules are configuration only; they are not applied automatically.
type Rule struct {
	ID                  uuid.UUID       `json:"id"`
	TenantID            uuid.UUID       `json:"tenant_id"`
	ServiceDefinitionID uuid.UUID       `json:"service_definition_id"`
	ContractID          *uuid.UUID      `json:"contract_id,omitempty"`
	ActivityLabel       string          `json:"activity_label"`
	LegalArea           string          `json:"legal_area"`
	Criteria            json.RawMessage `json:"criteria"`
	ValidFrom           time.Time       `json:"valid_from"`
	ValidUntil          *time.Time      `json:"valid_until,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// CreateRuleInput is the validated input for a new activity service rule.
type CreateRuleInput struct {
	ServiceDefinitionID uuid.UUID
	ContractID          *uuid.UUID
	ActivityLabel       string
	LegalArea           string
	Criteria            json.RawMessage
	ValidFrom           time.Time
	ValidUntil          *time.Time
}

// DefinitionFilter narrows a service search.
type DefinitionFilter struct {
	CatalogID *uuid.UUID
	LegalArea string
	Query     string
}

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}
