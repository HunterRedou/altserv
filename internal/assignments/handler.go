package assignments

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

// Handler exposes the assignment HTTP API.
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

type createRequest struct {
	EmployeeID     string  `json:"employee_id"`
	AssignmentType string  `json:"assignment_type"`
	ValidFrom      string  `json:"valid_from"`
	ValidUntil     *string `json:"valid_until"`
}

type assignmentResponse struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	PatientID      uuid.UUID `json:"patient_id"`
	EmployeeID     uuid.UUID `json:"employee_id"`
	AssignedBy     uuid.UUID `json:"assigned_by"`
	AssignmentType string    `json:"assignment_type"`
	ValidFrom      string    `json:"valid_from"`
	ValidUntil     string    `json:"valid_until,omitempty"`
	RevokedAt      string    `json:"revoked_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

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

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	employeeID, err := uuid.Parse(req.EmployeeID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "employee_id must be a UUID", nil)
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

	var assignment Assignment
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		assignment, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), patientID, CreateInput{
			EmployeeID:     employeeID,
			AssignmentType: req.AssignmentType,
			ValidFrom:      validFrom,
			ValidUntil:     validUntil,
		})
		return err
	})
	if err != nil {
		respondAssignmentError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toAssignmentResponse(assignment))
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	assignmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid assignment id", nil)
		return
	}

	var assignment Assignment
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		assignment, err = h.service.Revoke(r.Context(), q, actor, h.currentRequestID(r.Context()), assignmentID)
		return err
	})
	if err != nil {
		respondAssignmentError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toAssignmentResponse(assignment))
}

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

	var assignments []Assignment
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		assignments, err = h.service.ListByPatient(r.Context(), q, actor, patientID)
		return err
	})
	if err != nil {
		respondAssignmentError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toAssignmentResponses(assignments))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var assignments []Assignment
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		assignments, err = h.service.ListByTenant(r.Context(), q, actor)
		return err
	})
	if err != nil {
		respondAssignmentError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toAssignmentResponses(assignments))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondAssignmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to manage assignments", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Assignment not found", nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Assignment operation failed", err)
	}
}

func toAssignmentResponse(assignment Assignment) assignmentResponse {
	response := assignmentResponse{
		ID:             assignment.ID,
		TenantID:       assignment.TenantID,
		PatientID:      assignment.PatientID,
		EmployeeID:     assignment.EmployeeID,
		AssignedBy:     assignment.AssignedBy,
		AssignmentType: assignment.AssignmentType,
		ValidFrom:      assignment.ValidFrom.Format(time.RFC3339),
		CreatedAt:      assignment.CreatedAt,
	}
	if assignment.ValidUntil != nil {
		response.ValidUntil = assignment.ValidUntil.Format(time.RFC3339)
	}
	if assignment.RevokedAt != nil {
		response.RevokedAt = assignment.RevokedAt.Format(time.RFC3339)
	}
	return response
}

func toAssignmentResponses(assignments []Assignment) []assignmentResponse {
	responses := make([]assignmentResponse, 0, len(assignments))
	for _, assignment := range assignments {
		responses = append(responses, toAssignmentResponse(assignment))
	}
	return responses
}
