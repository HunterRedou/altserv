package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid catalog input")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
)

const (
	// maxDefinitionQueryRunes bounds an ILIKE search term so a pathological
	// query cannot force the database to scan an unbounded pattern (doc 10).
	maxDefinitionQueryRunes = 100
	// maxDefinitionResults keeps the result list small until pg_trgm and a
	// proper relevance index exist (doc 10).
	maxDefinitionResults  = 50
	maxPayerNameRunes     = 200
	maxReferenceRunes     = 200
	maxActivityLabelRunes = 200
)

// Store is the persistence boundary used by the catalog service.
type Store interface {
	ListCatalogs(ctx context.Context, q *db.Queries, includeFuture bool) ([]Catalog, error)
	GetCatalog(ctx context.Context, q *db.Queries, id uuid.UUID) (Catalog, error)
	SearchDefinitions(ctx context.Context, q *db.Queries, filter DefinitionFilter, limit int) ([]Definition, error)
	GetDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (Definition, error)
	CreateContract(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateContractInput) (Contract, error)
	GetContract(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Contract, error)
	ListContracts(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Contract, error)
	CreateRule(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (Rule, error)
	ListRules(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Rule, error)
}

// Service contains the catalog and contract business rules. Global catalogs are
// read-only; only a chef may add a tenant contract.
type Service struct {
	store Store
	audit audit.Recorder
	now   func() time.Time
}

func NewService(store Store, recorder audit.Recorder) *Service {
	return &Service{store: store, audit: recorder, now: time.Now}
}

// ListCatalogs returns the global reference catalogs. Only the currently valid
// versions are returned unless includeFuture is set.
func (s *Service) ListCatalogs(ctx context.Context, q *db.Queries, actor Actor, includeFuture bool) ([]Catalog, error) {
	if !canReadCatalog(actor.Role) {
		return nil, ErrForbidden
	}
	return s.store.ListCatalogs(ctx, q, includeFuture)
}

// GetCatalog returns one catalog version.
func (s *Service) GetCatalog(ctx context.Context, q *db.Queries, actor Actor, id uuid.UUID) (Catalog, error) {
	if !canReadCatalog(actor.Role) {
		return Catalog{}, ErrForbidden
	}
	return s.store.GetCatalog(ctx, q, id)
}

// SearchDefinitions runs the ILIKE service search with relevance ordering.
func (s *Service) SearchDefinitions(ctx context.Context, q *db.Queries, actor Actor, filter DefinitionFilter) ([]Definition, error) {
	if !canReadCatalog(actor.Role) {
		return nil, ErrForbidden
	}

	normalized, err := validateFilter(filter)
	if err != nil {
		return nil, err
	}
	return s.store.SearchDefinitions(ctx, q, normalized, maxDefinitionResults)
}

// GetDefinition returns one billable service.
func (s *Service) GetDefinition(ctx context.Context, q *db.Queries, actor Actor, id uuid.UUID) (Definition, error) {
	if !canReadCatalog(actor.Role) {
		return Definition{}, ErrForbidden
	}
	return s.store.GetDefinition(ctx, q, id)
}

// CreateContract adds a tenant contract version. Only a chef may do this, and
// the contract legal area must match the referenced service definition so SGB XI
// and SGB V never get mixed up (doc 9).
func (s *Service) CreateContract(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateContractInput) (Contract, error) {
	if actor.Role != "chef" {
		s.recordDenied(ctx, q, actor, requestID, "service_contract.create")
		return Contract{}, ErrForbidden
	}

	normalized, err := validateContractInput(input)
	if err != nil {
		return Contract{}, err
	}

	definition, err := s.store.GetDefinition(ctx, q, normalized.ServiceDefinitionID)
	if err != nil {
		return Contract{}, err
	}
	if definition.LegalArea != normalized.LegalArea {
		return Contract{}, fmt.Errorf("%w: legal area does not match the service definition", ErrInvalid)
	}
	if !validAt(definition.ValidFrom, definition.ValidUntil, normalized.ValidFrom) {
		return Contract{}, fmt.Errorf("%w: service definition is not valid at the contract start", ErrInvalid)
	}

	contract, err := s.store.CreateContract(ctx, q, actor.TenantID, normalized)
	if err != nil {
		return Contract{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "service_contract.created",
		EntityType:  "tenant_service_contract",
		EntityID:    contract.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"legal_area":      contract.LegalArea,
			"service_code":    definition.Code,
			"valid_from":      contract.ValidFrom,
			"has_valid_until": contract.ValidUntil != nil,
			"has_payer_name":  contract.PayerName != "",
			"has_reference":   contract.ContractReference != "",
		},
	}); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// GetContract returns one tenant contract.
func (s *Service) GetContract(ctx context.Context, q *db.Queries, actor Actor, id uuid.UUID) (Contract, error) {
	if !canReadCatalog(actor.Role) {
		return Contract{}, ErrForbidden
	}
	return s.store.GetContract(ctx, q, actor.TenantID, id)
}

// ListContracts returns every contract version of the tenant.
func (s *Service) ListContracts(ctx context.Context, q *db.Queries, actor Actor) ([]Contract, error) {
	if !canReadCatalog(actor.Role) {
		return nil, ErrForbidden
	}
	return s.store.ListContracts(ctx, q, actor.TenantID)
}

// CreateRule adds an activity-to-service mapping. Rules are configuration and
// are not applied automatically. Only a chef may create them.
func (s *Service) CreateRule(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateRuleInput) (Rule, error) {
	if actor.Role != "chef" {
		s.recordDenied(ctx, q, actor, requestID, "activity_rule.create")
		return Rule{}, ErrForbidden
	}

	normalized, err := validateRuleInput(input)
	if err != nil {
		return Rule{}, err
	}

	definition, err := s.store.GetDefinition(ctx, q, normalized.ServiceDefinitionID)
	if err != nil {
		return Rule{}, err
	}
	if definition.LegalArea != normalized.LegalArea {
		return Rule{}, fmt.Errorf("%w: legal area does not match the service definition", ErrInvalid)
	}
	if !validAt(definition.ValidFrom, definition.ValidUntil, normalized.ValidFrom) {
		return Rule{}, fmt.Errorf("%w: service definition is not valid at the rule start", ErrInvalid)
	}

	if normalized.ContractID != nil {
		contract, err := s.store.GetContract(ctx, q, actor.TenantID, *normalized.ContractID)
		if err != nil {
			return Rule{}, err
		}
		if contract.ServiceDefinitionID != definition.ID {
			return Rule{}, fmt.Errorf("%w: contract does not cover this service definition", ErrInvalid)
		}
		if contract.LegalArea != definition.LegalArea {
			return Rule{}, fmt.Errorf("%w: contract and service definition legal areas differ", ErrInvalid)
		}
		if !validAt(contract.ValidFrom, contract.ValidUntil, normalized.ValidFrom) {
			return Rule{}, fmt.Errorf("%w: contract is not valid at the rule start", ErrInvalid)
		}
	}

	rule, err := s.store.CreateRule(ctx, q, actor.TenantID, normalized)
	if err != nil {
		return Rule{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "activity_rule.created",
		EntityType:  "activity_service_rule",
		EntityID:    rule.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"legal_area":   rule.LegalArea,
			"service_code": definition.Code,
			"has_contract": rule.ContractID != nil,
			"has_criteria": len(rule.Criteria) > 0 && string(rule.Criteria) != "{}",
		},
	}); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// ListRules returns the tenant activity-service rules.
func (s *Service) ListRules(ctx context.Context, q *db.Queries, actor Actor) ([]Rule, error) {
	if !canReadCatalog(actor.Role) {
		return nil, ErrForbidden
	}
	return s.store.ListRules(ctx, q, actor.TenantID)
}

// canReadCatalog allows the two tenant roles that record or plan care. A
// platform admin gets no automatic tenant data access (doc 4).
func canReadCatalog(role string) bool {
	return role == "chef" || role == "employee"
}

func validateFilter(filter DefinitionFilter) (DefinitionFilter, error) {
	filter.Query = normalizeQuery(filter.Query)
	if len([]rune(filter.Query)) > maxDefinitionQueryRunes {
		return DefinitionFilter{}, fmt.Errorf("%w: search term is too long", ErrInvalid)
	}

	switch filter.LegalArea {
	case "", "sgb_xi", "sgb_v":
	default:
		return DefinitionFilter{}, fmt.Errorf("%w: legal area %q is not allowed", ErrInvalid, filter.LegalArea)
	}

	return filter, nil
}

func validateContractInput(input CreateContractInput) (CreateContractInput, error) {
	input.PayerName = strings.TrimSpace(input.PayerName)
	input.ContractReference = strings.TrimSpace(input.ContractReference)

	if input.ServiceDefinitionID == uuid.Nil {
		return CreateContractInput{}, fmt.Errorf("%w: service definition is required", ErrInvalid)
	}
	if input.PayerName == "" {
		return CreateContractInput{}, fmt.Errorf("%w: payer name is required", ErrInvalid)
	}
	if len([]rune(input.PayerName)) > maxPayerNameRunes {
		return CreateContractInput{}, fmt.Errorf("%w: payer name is too long", ErrInvalid)
	}
	if len([]rune(input.ContractReference)) > maxReferenceRunes {
		return CreateContractInput{}, fmt.Errorf("%w: contract reference is too long", ErrInvalid)
	}

	switch input.LegalArea {
	case "sgb_xi", "sgb_v":
	default:
		return CreateContractInput{}, fmt.Errorf("%w: legal area %q is not allowed", ErrInvalid, input.LegalArea)
	}

	if input.ValidFrom.IsZero() {
		return CreateContractInput{}, fmt.Errorf("%w: valid_from is required", ErrInvalid)
	}
	if input.ValidUntil != nil && !input.ValidUntil.After(input.ValidFrom) {
		return CreateContractInput{}, fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalid)
	}

	return input, nil
}

func validateRuleInput(input CreateRuleInput) (CreateRuleInput, error) {
	input.ActivityLabel = strings.TrimSpace(input.ActivityLabel)

	if input.ServiceDefinitionID == uuid.Nil {
		return CreateRuleInput{}, fmt.Errorf("%w: service definition is required", ErrInvalid)
	}
	if input.ActivityLabel == "" {
		return CreateRuleInput{}, fmt.Errorf("%w: activity label is required", ErrInvalid)
	}
	if len([]rune(input.ActivityLabel)) > maxActivityLabelRunes {
		return CreateRuleInput{}, fmt.Errorf("%w: activity label is too long", ErrInvalid)
	}
	switch input.LegalArea {
	case "sgb_xi", "sgb_v":
	default:
		return CreateRuleInput{}, fmt.Errorf("%w: legal area %q is not allowed", ErrInvalid, input.LegalArea)
	}
	if input.ValidFrom.IsZero() {
		return CreateRuleInput{}, fmt.Errorf("%w: valid_from is required", ErrInvalid)
	}
	if input.ValidUntil != nil && !input.ValidUntil.After(input.ValidFrom) {
		return CreateRuleInput{}, fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalid)
	}
	if len(input.Criteria) > 0 && !json.Valid(input.Criteria) {
		return CreateRuleInput{}, fmt.Errorf("%w: criteria must be valid JSON", ErrInvalid)
	}

	return input, nil
}

// validAt reports whether [from, until) contains at.
func validAt(from time.Time, until *time.Time, at time.Time) bool {
	if at.Before(from) {
		return false
	}
	return until == nil || at.Before(*until)
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
	// Best effort: a denied request must not turn into a 500.
	_ = s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "service_catalog",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}
