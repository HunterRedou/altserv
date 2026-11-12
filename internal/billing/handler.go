package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// Handler exposes the billing HTTP API.
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

type generateRequest struct {
	Version int `json:"version"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type createRuleRequest struct {
	Kind                       string     `json:"kind"`
	ServiceDefinitionID        string     `json:"service_definition_id"`
	RelatedServiceDefinitionID string     `json:"related_service_definition_id"`
	LegalArea                  string     `json:"legal_area"`
	Description                string     `json:"description"`
	ValidFrom                  time.Time  `json:"valid_from"`
	ValidUntil                 *time.Time `json:"valid_until"`
}

type transmissionResponse struct {
	Status     string                  `json:"status"`
	Adapter    string                  `json:"adapter"`
	Procedures []TransmissionProcedure `json:"procedures"`
}

type transmitRequest struct {
	LegalArea      string `json:"legal_area"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Generate handles POST /api/reports/{id}/billing.
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	reportID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var run BillingRun
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		run, err = h.service.Generate(r.Context(), q, actor, h.currentRequestID(r.Context()), reportID, GenerateInput{Version: req.Version})
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, run)
}

// GetActive handles GET /api/reports/{id}/billing.
func (h *Handler) GetActive(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	reportID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var run BillingRun
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		run, err = h.service.GetActive(r.Context(), q, actor, h.currentRequestID(r.Context()), reportID)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, run)
}

// ListByReport handles GET /api/reports/{id}/billing-runs.
func (h *Handler) ListByReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	reportID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var runs []BillingRun
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		runs, err = h.service.ListByReport(r.Context(), q, actor, h.currentRequestID(r.Context()), reportID)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	if runs == nil {
		runs = []BillingRun{}
	}
	server.RespondWithJSON(w, http.StatusOK, runs)
}

// Get handles GET /api/billing-runs/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid billing run id", nil)
		return
	}

	var run BillingRun
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		run, err = h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, run)
}

// Approve handles POST /api/billing-runs/{id}/approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	h.versionAction(w, r, func(q *db.Queries, actor Actor, id uuid.UUID, version int) (BillingRun, error) {
		return h.service.Approve(r.Context(), q, actor, h.currentRequestID(r.Context()), id, version)
	})
}

// Void handles POST /api/billing-runs/{id}/void.
func (h *Handler) Void(w http.ResponseWriter, r *http.Request) {
	h.versionAction(w, r, func(q *db.Queries, actor Actor, id uuid.UUID, version int) (BillingRun, error) {
		return h.service.Void(r.Context(), q, actor, h.currentRequestID(r.Context()), id, version)
	})
}

// ExportCSV handles GET /api/billing-runs/{id}/export.csv.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	h.export(w, r, "csv", "text/csv; charset=utf-8", h.service.ExportCSV)
}

// ExportPDF handles GET /api/billing-runs/{id}/export.pdf.
func (h *Handler) ExportPDF(w http.ResponseWriter, r *http.Request) {
	h.export(w, r, "pdf", "application/pdf", h.service.ExportPDF)
}

// ListRules handles GET /api/billing-rules.
func (h *Handler) ListRules(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var rules []BillingRule
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		rules, err = h.service.ListRules(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	if rules == nil {
		rules = []BillingRule{}
	}
	server.RespondWithJSON(w, http.StatusOK, rules)
}

// CreateRule handles POST /api/billing-rules.
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

	serviceID, err := uuid.Parse(req.ServiceDefinitionID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "service_definition_id must be a UUID", nil)
		return
	}
	relatedID, err := uuid.Parse(req.RelatedServiceDefinitionID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "related_service_definition_id must be a UUID", nil)
		return
	}

	var rule BillingRule
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		rule, err = h.service.CreateRule(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateRuleInput{
			Kind:                       req.Kind,
			ServiceDefinitionID:        serviceID,
			RelatedServiceDefinitionID: relatedID,
			LegalArea:                  req.LegalArea,
			Description:                req.Description,
			ValidFrom:                  req.ValidFrom,
			ValidUntil:                 req.ValidUntil,
		})
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, rule)
}

// Transmission handles GET /api/billing-transmission. It describes the legal
// submission procedures and the wired adapter, and never sends data.
func (h *Handler) Transmission(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorFromContext(r.Context()); !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, transmissionResponse{
		Status:     TransmissionStatusNotConfigured,
		Adapter:    h.service.TransmissionAdapter(),
		Procedures: TransmissionProcedures(),
	})
}

// Transmit handles POST /api/billing-runs/{id}/transmit. Only a chef may submit
// an approved run. The idempotency key comes from the Idempotency-Key header or
// the JSON body.
func (h *Handler) Transmit(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid billing run id", nil)
		return
	}

	var req transmitRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
			return
		}
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	}

	var outcome TransmissionOutcome
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		outcome, err = h.service.Transmit(r.Context(), q, actor, h.currentRequestID(r.Context()), TransmitInput{
			RunID:          id,
			LegalArea:      req.LegalArea,
			IdempotencyKey: idempotencyKey,
		})
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}

	switch outcome.Transmission.Status {
	case TransmissionStatusAccepted:
		status := http.StatusCreated
		if !outcome.Created {
			status = http.StatusOK
		}
		server.RespondWithJSON(w, status, outcome.Transmission)
	// A rejected or failed attempt is recorded; the body carries its id and
	// response code so the chef can correct the run and retry with a new key.
	case TransmissionStatusRejected:
		server.RespondWithJSON(w, http.StatusUnprocessableEntity, outcome.Transmission)
	case TransmissionStatusFailed:
		server.RespondWithJSON(w, http.StatusBadGateway, outcome.Transmission)
	case TransmissionStatusNotConfigured:
		server.RespondWithError(w, http.StatusServiceUnavailable, "Billing transmission is not configured", nil)
	default:
		server.RespondWithJSON(w, http.StatusAccepted, outcome.Transmission)
	}
}

// ListTransmissions handles GET /api/billing-runs/{id}/transmissions.
func (h *Handler) ListTransmissions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid billing run id", nil)
		return
	}

	var transmissions []Transmission
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		transmissions, err = h.service.ListTransmissions(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	if transmissions == nil {
		transmissions = []Transmission{}
	}
	server.RespondWithJSON(w, http.StatusOK, transmissions)
}

func (h *Handler) versionAction(w http.ResponseWriter, r *http.Request, action func(*db.Queries, Actor, uuid.UUID, int) (BillingRun, error)) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid billing run id", nil)
		return
	}

	var req versionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var run BillingRun
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		run, err = action(q, actor, id, req.Version)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, run)
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request, format, contentType string, action func(context.Context, *db.Queries, Actor, string, uuid.UUID) ([]byte, error)) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid billing run id", nil)
		return
	}

	var data []byte
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		data, err = action(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondBillingError(w, err)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "billing-"+id.String()+"."+format))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondBillingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access billing data", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Report or billing run not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Billing operation failed", err)
	}
}
