package visits

import (
	"context"
	"encoding/json"
	"errors"
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

// Handler exposes the visit HTTP API.
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

type createVisitRequest struct {
	EmployeeID   string `json:"employee_id"`
	PlannedStart string `json:"planned_start"`
}

type rescheduleVisitRequest struct {
	EmployeeID   *string `json:"employee_id"`
	PlannedStart *string `json:"planned_start"`
}

type correctVisitRequest struct {
	ActualStart *string `json:"actual_start"`
	ActualEnd   *string `json:"actual_end"`
	Reason      string  `json:"reason"`
}

type visitResponse struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	PatientID        uuid.UUID  `json:"patient_id"`
	EmployeeID       uuid.UUID  `json:"employee_id"`
	ActualEmployeeID *uuid.UUID `json:"actual_employee_id,omitempty"`
	PlannedStart     time.Time  `json:"planned_start"`
	ActualStart      *time.Time `json:"actual_start,omitempty"`
	ActualEnd        *time.Time `json:"actual_end,omitempty"`
	Status           string     `json:"status"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Create handles POST /api/patients/{id}/visits.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	patientID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid patient id", nil)
		return
	}

	var req createVisitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	employeeID, err := uuid.Parse(req.EmployeeID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "employee_id must be a UUID", nil)
		return
	}
	plannedStart, err := time.Parse(time.RFC3339, req.PlannedStart)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "planned_start must be RFC3339", nil)
		return
	}

	var visit Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visit, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateInput{
			PatientID:    patientID,
			EmployeeID:   employeeID,
			PlannedStart: plannedStart,
		})
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toVisitResponse(visit))
}

// ListByPatient handles GET /api/patients/{id}/visits.
func (h *Handler) ListByPatient(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	patientID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid patient id", nil)
		return
	}

	var visits []Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visits, err = h.service.ListByPatient(r.Context(), q, actor, h.currentRequestID(r.Context()), patientID)
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toVisitResponses(visits))
}

// Get handles GET /api/visits/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var visit Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visit, err = h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toVisitResponse(visit))
}

// Reschedule handles PATCH /api/visits/{id}.
func (h *Handler) Reschedule(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var req rescheduleVisitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var employeeID *uuid.UUID
	if req.EmployeeID != nil && *req.EmployeeID != "" {
		parsed, err := uuid.Parse(*req.EmployeeID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "employee_id must be a UUID", nil)
			return
		}
		employeeID = &parsed
	}
	plannedStart, err := parseOptionalTime(req.PlannedStart)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "planned_start must be RFC3339", nil)
		return
	}

	var visit Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visit, err = h.service.Reschedule(r.Context(), q, actor, h.currentRequestID(r.Context()), id, ScheduleInput{
			EmployeeID:   employeeID,
			PlannedStart: plannedStart,
		})
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toVisitResponse(visit))
}

// Start handles POST /api/visits/{id}/start.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID) (Visit, error) {
		return h.service.Start(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
	})
}

// Complete handles POST /api/visits/{id}/complete.
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID) (Visit, error) {
		return h.service.Complete(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
	})
}

// Cancel handles POST /api/visits/{id}/cancel.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID) (Visit, error) {
		return h.service.Cancel(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
	})
}

// Correct handles POST /api/visits/{id}/correct.
func (h *Handler) Correct(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var req correctVisitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	actualStart, err := parseOptionalTime(req.ActualStart)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "actual_start must be RFC3339", nil)
		return
	}
	actualEnd, err := parseOptionalTime(req.ActualEnd)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "actual_end must be RFC3339", nil)
		return
	}

	var visit Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visit, err = h.service.Correct(r.Context(), q, actor, h.currentRequestID(r.Context()), id, CorrectInput{
			ActualStart: actualStart,
			ActualEnd:   actualEnd,
			Reason:      req.Reason,
		})
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toVisitResponse(visit))
}

func (h *Handler) transition(w http.ResponseWriter, r *http.Request, action func(*db.Queries, Actor, uuid.UUID) (Visit, error)) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var visit Visit
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		visit, err = action(q, actor, id)
		return err
	})
	if err != nil {
		respondVisitError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toVisitResponse(visit))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func parseOptionalTime(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func respondVisitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access visits", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Visit not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Visit operation failed", err)
	}
}

func toVisitResponse(visit Visit) visitResponse {
	return visitResponse{
		ID:               visit.ID,
		TenantID:         visit.TenantID,
		PatientID:        visit.PatientID,
		EmployeeID:       visit.EmployeeID,
		ActualEmployeeID: visit.ActualEmployeeID,
		PlannedStart:     visit.PlannedStart,
		ActualStart:      visit.ActualStart,
		ActualEnd:        visit.ActualEnd,
		Status:           visit.Status,
		CreatedBy:        visit.CreatedBy,
		CreatedAt:        visit.CreatedAt,
		UpdatedAt:        visit.UpdatedAt,
	}
}

func toVisitResponses(visits []Visit) []visitResponse {
	responses := make([]visitResponse, 0, len(visits))
	for _, visit := range visits {
		responses = append(responses, toVisitResponse(visit))
	}
	return responses
}
