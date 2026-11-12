package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/server"
	"github.com/google/uuid"
)

// ActorFromContext extracts the authenticated user from the request context.
type ActorFromContext func(context.Context) (Actor, bool)

// RequestIDFromContext returns the request id used for audit correlation.
type RequestIDFromContext func(context.Context) string

// TenantRunner runs a function inside a tenant-scoped transaction.
type TenantRunner func(context.Context, uuid.UUID, func(*db.Queries) error) error

// Handler exposes the catalog HTTP API.
type Handler struct {
	service          *Service
	withTenant       TenantRunner
	actorFromContext ActorFromContext
	requestID        RequestIDFromContext
}

func NewHandler(service *Service, withTenant TenantRunner, actorFromContext ActorFromContext, requestID RequestIDFromContext) *Handler {
	return &Handler{
		service:          service,
		withTenant:       withTenant,
		actorFromContext: actorFromContext,
		requestID:        requestID,
	}
}

type createContractRequest struct {
	ServiceDefinitionID string  `json:"service_definition_id"`
	PayerName           string  `json:"payer_name"`
	ContractReference   string  `json:"contract_reference"`
	LegalArea           string  `json:"legal_area"`
	ValidFrom           string  `json:"valid_from"`
	ValidUntil          *string `json:"valid_until"`
}

// ListCatalogs handles GET /api/service-catalogs.
func (h *Handler) ListCatalogs(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	includeFuture, _ := strconv.ParseBool(r.URL.Query().Get("include_future"))

	var catalogs []Catalog
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		catalogs, err = h.service.ListCatalogs(r.Context(), q, actor, includeFuture)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toCatalogResponses(catalogs))
}

// GetCatalog handles GET /api/service-catalogs/{id}.
func (h *Handler) GetCatalog(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid catalog id", nil)
		return
	}

	var catalog Catalog
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		catalog, err = h.service.GetCatalog(r.Context(), q, actor, id)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toCatalogResponse(catalog))
}

type createCatalogRequest struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Region     string  `json:"region"`
	SourceNote string  `json:"source_note"`
	ValidFrom  string  `json:"valid_from"`
	ValidUntil *string `json:"valid_until"`
}

// CreateCatalog handles POST /api/service-catalogs. Only a platform admin may
// create a global catalog version.
func (h *Handler) CreateCatalog(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req createCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	validFrom, validUntil, ok := parseValidityWindow(w, req.ValidFrom, req.ValidUntil)
	if !ok {
		return
	}

	var catalog Catalog
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		catalog, err = h.service.CreateCatalog(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateCatalogInput{
			Code:       req.Code,
			Name:       req.Name,
			Region:     req.Region,
			SourceNote: req.SourceNote,
			ValidFrom:  validFrom,
			ValidUntil: validUntil,
		})
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toCatalogResponse(catalog))
}

// SearchDefinitions handles GET /api/service-definitions. The q term is read
// from the query string only and never logged (doc 10).
func (h *Handler) SearchDefinitions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	filter := DefinitionFilter{
		LegalArea: r.URL.Query().Get("legal_area"),
		Query:     r.URL.Query().Get("q"),
	}
	if raw := r.URL.Query().Get("catalog_id"); raw != "" {
		catalogID, err := uuid.Parse(raw)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "catalog_id must be a UUID", nil)
			return
		}
		filter.CatalogID = &catalogID
	}

	var definitions []Definition
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		definitions, err = h.service.SearchDefinitions(r.Context(), q, actor, filter)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toDefinitionResponses(definitions))
}

// GetDefinition handles GET /api/service-definitions/{id}.
func (h *Handler) GetDefinition(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid service definition id", nil)
		return
	}

	var definition Definition
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		definition, err = h.service.GetDefinition(r.Context(), q, actor, id)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toDefinitionResponse(definition))
}

type createDefinitionRequest struct {
	CatalogID   string  `json:"catalog_id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	LegalArea   string  `json:"legal_area"`
	Unit        string  `json:"unit"`
	ValidFrom   string  `json:"valid_from"`
	ValidUntil  *string `json:"valid_until"`
}

// CreateDefinition handles POST /api/service-definitions. Only a platform admin
// may add a billable service to a global catalog version.
func (h *Handler) CreateDefinition(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req createDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	catalogID, err := uuid.Parse(req.CatalogID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "catalog_id must be a UUID", nil)
		return
	}

	validFrom, validUntil, ok := parseValidityWindow(w, req.ValidFrom, req.ValidUntil)
	if !ok {
		return
	}

	var definition Definition
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		definition, err = h.service.CreateDefinition(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateDefinitionInput{
			CatalogID:   catalogID,
			Code:        req.Code,
			Name:        req.Name,
			Description: req.Description,
			LegalArea:   req.LegalArea,
			Unit:        req.Unit,
			ValidFrom:   validFrom,
			ValidUntil:  validUntil,
		})
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toDefinitionResponse(definition))
}

// parseValidityWindow parses the RFC3339 validity window shared by the catalog
// and definition create handlers. It writes a 400 response on a bad value.
func parseValidityWindow(w http.ResponseWriter, rawFrom string, rawUntil *string) (time.Time, *time.Time, bool) {
	var validFrom time.Time
	if rawFrom != "" {
		parsed, err := time.Parse(time.RFC3339, rawFrom)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_from must be RFC3339", nil)
			return time.Time{}, nil, false
		}
		validFrom = parsed
	}

	var validUntil *time.Time
	if rawUntil != nil && *rawUntil != "" {
		parsed, err := time.Parse(time.RFC3339, *rawUntil)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_until must be RFC3339", nil)
			return time.Time{}, nil, false
		}
		validUntil = &parsed
	}

	return validFrom, validUntil, true
}

// ListContracts handles GET /api/service-contracts.
func (h *Handler) ListContracts(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var contracts []Contract
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		contracts, err = h.service.ListContracts(r.Context(), q, actor)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toContractResponses(contracts))
}

// GetContract handles GET /api/service-contracts/{id}.
func (h *Handler) GetContract(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid contract id", nil)
		return
	}

	var contract Contract
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		contract, err = h.service.GetContract(r.Context(), q, actor, id)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toContractResponse(contract))
}

// CreateContract handles POST /api/service-contracts.
func (h *Handler) CreateContract(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req createContractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	definitionID, err := uuid.Parse(req.ServiceDefinitionID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "service_definition_id must be a UUID", nil)
		return
	}

	var validFrom time.Time
	if req.ValidFrom != "" {
		validFrom, err = time.Parse(time.RFC3339, req.ValidFrom)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_from must be RFC3339", nil)
			return
		}
	}

	var validUntil *time.Time
	if req.ValidUntil != nil && *req.ValidUntil != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ValidUntil)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_until must be RFC3339", nil)
			return
		}
		validUntil = &parsed
	}

	var contract Contract
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		contract, err = h.service.CreateContract(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateContractInput{
			ServiceDefinitionID: definitionID,
			PayerName:           req.PayerName,
			ContractReference:   req.ContractReference,
			LegalArea:           req.LegalArea,
			ValidFrom:           validFrom,
			ValidUntil:          validUntil,
		})
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toContractResponse(contract))
}

type createRuleRequest struct {
	ServiceDefinitionID string          `json:"service_definition_id"`
	ContractID          string          `json:"contract_id"`
	ActivityLabel       string          `json:"activity_label"`
	LegalArea           string          `json:"legal_area"`
	Criteria            json.RawMessage `json:"criteria"`
	ValidFrom           string          `json:"valid_from"`
	ValidUntil          *string         `json:"valid_until"`
}

// ListRules handles GET /api/activity-rules.
func (h *Handler) ListRules(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var rules []Rule
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		rules, err = h.service.ListRules(r.Context(), q, actor)
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toRuleResponses(rules))
}

// CreateRule handles POST /api/activity-rules.
func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req createRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	definitionID, err := uuid.Parse(req.ServiceDefinitionID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "service_definition_id must be a UUID", nil)
		return
	}

	var contractID *uuid.UUID
	if req.ContractID != "" {
		id, err := uuid.Parse(req.ContractID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "contract_id must be a UUID", nil)
			return
		}
		contractID = &id
	}

	var validFrom time.Time
	if req.ValidFrom != "" {
		validFrom, err = time.Parse(time.RFC3339, req.ValidFrom)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_from must be RFC3339", nil)
			return
		}
	}

	var validUntil *time.Time
	if req.ValidUntil != nil && *req.ValidUntil != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ValidUntil)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "valid_until must be RFC3339", nil)
			return
		}
		validUntil = &parsed
	}

	var rule Rule
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		rule, err = h.service.CreateRule(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateRuleInput{
			ServiceDefinitionID: definitionID,
			ContractID:          contractID,
			ActivityLabel:       req.ActivityLabel,
			LegalArea:           req.LegalArea,
			Criteria:            req.Criteria,
			ValidFrom:           validFrom,
			ValidUntil:          validUntil,
		})
		return err
	})
	if err != nil {
		respondCatalogError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toRuleResponse(rule))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access services", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, "Version already exists", nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Service operation failed", err)
	}
}

type catalogResponse struct {
	ID         uuid.UUID `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Region     string    `json:"region"`
	SourceNote string    `json:"source_note"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type definitionResponse struct {
	ID          uuid.UUID `json:"id"`
	CatalogID   uuid.UUID `json:"catalog_id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	LegalArea   string    `json:"legal_area"`
	Unit        string    `json:"unit"`
	ValidFrom   time.Time `json:"valid_from"`
	ValidUntil  time.Time `json:"valid_until,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type contractResponse struct {
	ID                  uuid.UUID `json:"id"`
	TenantID            uuid.UUID `json:"tenant_id"`
	ServiceDefinitionID uuid.UUID `json:"service_definition_id"`
	PayerName           string    `json:"payer_name"`
	ContractReference   string    `json:"contract_reference"`
	LegalArea           string    `json:"legal_area"`
	ValidFrom           time.Time `json:"valid_from"`
	ValidUntil          time.Time `json:"valid_until,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func toCatalogResponse(catalog Catalog) catalogResponse {
	response := catalogResponse{
		ID:         catalog.ID,
		Code:       catalog.Code,
		Name:       catalog.Name,
		Region:     catalog.Region,
		SourceNote: catalog.SourceNote,
		ValidFrom:  catalog.ValidFrom,
		CreatedAt:  catalog.CreatedAt,
		UpdatedAt:  catalog.UpdatedAt,
	}
	if catalog.ValidUntil != nil {
		response.ValidUntil = *catalog.ValidUntil
	}
	return response
}

func toCatalogResponses(catalogs []Catalog) []catalogResponse {
	responses := make([]catalogResponse, 0, len(catalogs))
	for _, catalog := range catalogs {
		responses = append(responses, toCatalogResponse(catalog))
	}
	return responses
}

func toDefinitionResponse(definition Definition) definitionResponse {
	response := definitionResponse{
		ID:          definition.ID,
		CatalogID:   definition.CatalogID,
		Code:        definition.Code,
		Name:        definition.Name,
		Description: definition.Description,
		LegalArea:   definition.LegalArea,
		Unit:        definition.Unit,
		ValidFrom:   definition.ValidFrom,
		CreatedAt:   definition.CreatedAt,
		UpdatedAt:   definition.UpdatedAt,
	}
	if definition.ValidUntil != nil {
		response.ValidUntil = *definition.ValidUntil
	}
	return response
}

func toDefinitionResponses(definitions []Definition) []definitionResponse {
	responses := make([]definitionResponse, 0, len(definitions))
	for _, definition := range definitions {
		responses = append(responses, toDefinitionResponse(definition))
	}
	return responses
}

func toContractResponse(contract Contract) contractResponse {
	response := contractResponse{
		ID:                  contract.ID,
		TenantID:            contract.TenantID,
		ServiceDefinitionID: contract.ServiceDefinitionID,
		PayerName:           contract.PayerName,
		ContractReference:   contract.ContractReference,
		LegalArea:           contract.LegalArea,
		ValidFrom:           contract.ValidFrom,
		CreatedAt:           contract.CreatedAt,
		UpdatedAt:           contract.UpdatedAt,
	}
	if contract.ValidUntil != nil {
		response.ValidUntil = *contract.ValidUntil
	}
	return response
}

func toContractResponses(contracts []Contract) []contractResponse {
	responses := make([]contractResponse, 0, len(contracts))
	for _, contract := range contracts {
		responses = append(responses, toContractResponse(contract))
	}
	return responses
}

type ruleResponse struct {
	ID                  uuid.UUID       `json:"id"`
	TenantID            uuid.UUID       `json:"tenant_id"`
	ServiceDefinitionID uuid.UUID       `json:"service_definition_id"`
	ContractID          *uuid.UUID      `json:"contract_id,omitempty"`
	ActivityLabel       string          `json:"activity_label"`
	LegalArea           string          `json:"legal_area"`
	Criteria            json.RawMessage `json:"criteria"`
	ValidFrom           time.Time       `json:"valid_from"`
	ValidUntil          time.Time       `json:"valid_until,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func toRuleResponse(rule Rule) ruleResponse {
	response := ruleResponse{
		ID:                  rule.ID,
		TenantID:            rule.TenantID,
		ServiceDefinitionID: rule.ServiceDefinitionID,
		ContractID:          rule.ContractID,
		ActivityLabel:       rule.ActivityLabel,
		LegalArea:           rule.LegalArea,
		Criteria:            rule.Criteria,
		ValidFrom:           rule.ValidFrom,
		CreatedAt:           rule.CreatedAt,
		UpdatedAt:           rule.UpdatedAt,
	}
	if rule.ValidUntil != nil {
		response.ValidUntil = *rule.ValidUntil
	}
	return response
}

func toRuleResponses(rules []Rule) []ruleResponse {
	responses := make([]ruleResponse, 0, len(rules))
	for _, rule := range rules {
		responses = append(responses, toRuleResponse(rule))
	}
	return responses
}
