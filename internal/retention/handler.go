package retention

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// Handler exposes the retention and deletion HTTP API. Every endpoint is
// chef-only. A platform_admin is denied and cannot observe patient data here.
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

type policyRequest struct {
	Category     string `json:"category"`
	TriggerEvent string `json:"trigger_event"`
	PeriodMonths int    `json:"period_months"`
	Note         string `json:"note"`
}

type policyUpdateRequest struct {
	PeriodMonths int    `json:"period_months"`
	Note         string `json:"note"`
}

type legalHoldRequest struct {
	PatientID   string `json:"patient_id"`
	Reason      string `json:"reason"`
	ReviewDueAt string `json:"review_due_at"`
}

type liftRequest struct {
	Reason string `json:"reason"`
}

type deletionRequest struct {
	PatientID string `json:"patient_id"`
	Reason    string `json:"reason"`
}

type decisionRequest struct {
	Reason string `json:"reason"`
}

// ListPolicies handles GET /api/retention/policies.
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var policies []Policy
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		policies, err = h.service.ListPolicies(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	if policies == nil {
		policies = []Policy{}
	}
	server.RespondWithJSON(w, http.StatusOK, policies)
}

// CreatePolicy handles POST /api/retention/policies. The new policy is disabled
// until a chef confirms it.
func (h *Handler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var req policyRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	var policy Policy
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		policy, err = h.service.CreatePolicy(r.Context(), q, actor, h.currentRequestID(r.Context()), PolicyInput{
			Category:     req.Category,
			TriggerEvent: req.TriggerEvent,
			PeriodMonths: req.PeriodMonths,
			Note:         req.Note,
		})
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, policy)
}

// UpdatePolicy handles PATCH /api/retention/policies/{id}.
func (h *Handler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid policy id", nil)
		return
	}
	var req policyUpdateRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	var policy Policy
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		policy, err = h.service.UpdatePolicy(r.Context(), q, actor, h.currentRequestID(r.Context()), id, PolicyUpdateInput{
			PeriodMonths: req.PeriodMonths,
			Note:         req.Note,
		})
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, policy)
}

// ConfirmPolicy handles POST /api/retention/policies/{id}/confirm.
func (h *Handler) ConfirmPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid policy id", nil)
		return
	}
	var policy Policy
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		policy, err = h.service.ConfirmPolicy(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, policy)
}

// DisablePolicy handles POST /api/retention/policies/{id}/disable.
func (h *Handler) DisablePolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid policy id", nil)
		return
	}
	var policy Policy
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		policy, err = h.service.DisablePolicy(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, policy)
}

// Review handles POST /api/retention/review. It reports candidate counts for the
// actor's tenant only and never creates a deletion request.
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var candidates []Candidate
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		candidates, err = h.service.ReviewCandidates(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	if candidates == nil {
		candidates = []Candidate{}
	}
	server.RespondWithJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// ListLegalHolds handles GET /api/legal-holds.
func (h *Handler) ListLegalHolds(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var holds []LegalHold
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		holds, err = h.service.ListLegalHolds(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	if holds == nil {
		holds = []LegalHold{}
	}
	server.RespondWithJSON(w, http.StatusOK, holds)
}

// CreateLegalHold handles POST /api/legal-holds. An empty patient_id declares a
// tenant-wide hold.
func (h *Handler) CreateLegalHold(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var req legalHoldRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var patientID *uuid.UUID
	if req.PatientID != "" {
		parsed, err := uuid.Parse(req.PatientID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "patient_id must be a UUID", nil)
			return
		}
		patientID = &parsed
	}
	var reviewDue *time.Time
	if req.ReviewDueAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ReviewDueAt)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "review_due_at must be RFC3339", nil)
			return
		}
		reviewDue = &parsed
	}

	var hold LegalHold
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		hold, err = h.service.CreateLegalHold(r.Context(), q, actor, h.currentRequestID(r.Context()), LegalHoldInput{
			PatientID:   patientID,
			Reason:      req.Reason,
			ReviewDueAt: reviewDue,
		})
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, hold)
}

// LiftLegalHold handles POST /api/legal-holds/{id}/lift.
func (h *Handler) LiftLegalHold(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid legal hold id", nil)
		return
	}
	var req liftRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	var hold LegalHold
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		hold, err = h.service.LiftLegalHold(r.Context(), q, actor, h.currentRequestID(r.Context()), id, req.Reason)
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, hold)
}

// ListDeletionRequests handles GET /api/deletion-requests.
func (h *Handler) ListDeletionRequests(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var requests []DeletionRequest
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		requests, err = h.service.ListDeletionRequests(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	if requests == nil {
		requests = []DeletionRequest{}
	}
	server.RespondWithJSON(w, http.StatusOK, requests)
}

// CreateDeletionRequest handles POST /api/deletion-requests.
func (h *Handler) CreateDeletionRequest(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var req deletionRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	patientID, err := uuid.Parse(req.PatientID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "patient_id must be a UUID", nil)
		return
	}
	var request DeletionRequest
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		request, err = h.service.CreateDeletionRequest(r.Context(), q, actor, h.currentRequestID(r.Context()), DeletionRequestInput{
			PatientID: patientID,
			Reason:    req.Reason,
		})
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, request)
}

// ApproveDeletionRequest handles POST /api/deletion-requests/{id}/approve.
func (h *Handler) ApproveDeletionRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, "approve")
}

// RejectDeletionRequest handles POST /api/deletion-requests/{id}/reject.
func (h *Handler) RejectDeletionRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, "reject")
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, decision string) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid deletion request id", nil)
		return
	}
	var req decisionRequest
	if err := decodeJSONBody(r, &req, true); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	var request DeletionRequest
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		if decision == "approve" {
			request, err = h.service.ApproveDeletionRequest(r.Context(), q, actor, h.currentRequestID(r.Context()), id, req.Reason)
		} else {
			request, err = h.service.RejectDeletionRequest(r.Context(), q, actor, h.currentRequestID(r.Context()), id, req.Reason)
		}
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, request)
}

// CancelDeletionRequest handles POST /api/deletion-requests/{id}/cancel.
func (h *Handler) CancelDeletionRequest(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid deletion request id", nil)
		return
	}
	var request DeletionRequest
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		request, err = h.service.CancelDeletionRequest(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, request)
}

// ExecuteDeletion handles POST /api/deletion-requests/{id}/execute.
func (h *Handler) ExecuteDeletion(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid deletion request id", nil)
		return
	}
	var result DeletionResult
	err = h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		result, err = h.service.ExecuteDeletion(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, result)
}

// ListRegister handles GET /api/deletion-register.
func (h *Handler) ListRegister(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(r)
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	var entries []RegisterEntry
	err := h.run(r.Context(), actor, func(q *db.Queries) error {
		var err error
		entries, err = h.service.ListRegister(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondRetentionError(w, err)
		return
	}
	if entries == nil {
		entries = []RegisterEntry{}
	}
	server.RespondWithJSON(w, http.StatusOK, entries)
}

func (h *Handler) actor(r *http.Request) (Actor, bool) {
	return h.actorFromContext(r.Context())
}

// run executes fn in a tenant transaction. A forbidden request commits so the
// access.denied audit row survives. Every role check runs before a business
// write, so a forbidden request has nothing else to commit.
func (h *Handler) run(ctx context.Context, actor Actor, fn func(*db.Queries) error) error {
	var opErr error
	err := h.withTenant(ctx, actor.TenantID, func(q *db.Queries) error {
		opErr = fn(q)
		if errors.Is(opErr, ErrForbidden) {
			return nil
		}
		return opErr
	})
	if err != nil {
		return err
	}
	return opErr
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func decodeJSONBody(r *http.Request, target any, allowEmpty bool) error {
	err := json.NewDecoder(r.Body).Decode(target)
	if allowEmpty && errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func respondRetentionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Retention operation denied", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Retention record or patient not found", nil)
	case errors.Is(err, ErrLegalHold):
		server.RespondWithError(w, http.StatusConflict, "An active legal hold blocks this deletion", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Retention operation failed", err)
	}
}
