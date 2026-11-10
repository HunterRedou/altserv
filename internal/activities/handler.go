package activities

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

// Handler exposes the activity HTTP API.
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
	EmployeeID          string `json:"employee_id"`
	ServiceDefinitionID string `json:"service_definition_id"`
	ContractID          string `json:"contract_id"`
	PerformedAt         string `json:"performed_at"`
	DurationMinutes     int    `json:"duration_minutes"`
	Note                string `json:"note"`
}

type activityResponse struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	PatientID           uuid.UUID  `json:"patient_id"`
	EmployeeID          uuid.UUID  `json:"employee_id"`
	ServiceDefinitionID *uuid.UUID `json:"service_definition_id,omitempty"`
	ContractID          *uuid.UUID `json:"contract_id,omitempty"`
	PerformedAt         time.Time  `json:"performed_at"`
	DurationMinutes     int        `json:"duration_minutes"`
	Note                string     `json:"note,omitempty"`
	Status              string     `json:"status"`
	ServiceCode         string     `json:"service_code,omitempty"`
	ServiceName         string     `json:"service_name,omitempty"`
	LegalArea           string     `json:"legal_area,omitempty"`
	ContractReference   string     `json:"contract_reference,omitempty"`
	PayerName           string     `json:"payer_name,omitempty"`
	CreatedBy           uuid.UUID  `json:"created_by"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// Create handles POST /api/patients/{id}/activities.
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

	input := CreateInput{
		PatientID:       patientID,
		DurationMinutes: req.DurationMinutes,
		Note:            req.Note,
	}
	if req.EmployeeID != "" {
		employeeID, err := uuid.Parse(req.EmployeeID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "employee_id must be a UUID", nil)
			return
		}
		input.EmployeeID = employeeID
	}
	if req.ServiceDefinitionID != "" {
		id, err := uuid.Parse(req.ServiceDefinitionID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "service_definition_id must be a UUID", nil)
			return
		}
		input.ServiceDefinitionID = &id
	}
	if req.ContractID != "" {
		id, err := uuid.Parse(req.ContractID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "contract_id must be a UUID", nil)
			return
		}
		input.ContractID = &id
	}
	if req.PerformedAt != "" {
		performedAt, err := time.Parse(time.RFC3339, req.PerformedAt)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "performed_at must be RFC3339", nil)
			return
		}
		input.PerformedAt = performedAt
	}

	var activity Activity
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		activity, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), input)
		return err
	})
	if err != nil {
		respondActivityError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toActivityResponse(activity))
}

// Get handles GET /api/activities/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid activity id", nil)
		return
	}

	var activity Activity
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		activity, err = h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondActivityError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toActivityResponse(activity))
}

// ListByPatient handles GET /api/patients/{id}/activities.
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

	var activities []Activity
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		activities, err = h.service.ListByPatient(r.Context(), q, actor, h.currentRequestID(r.Context()), patientID)
		return err
	})
	if err != nil {
		respondActivityError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toActivityResponses(activities))
}

// List handles GET /api/activities. Only a chef may read across patients.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var activities []Activity
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		activities, err = h.service.List(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondActivityError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toActivityResponses(activities))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondActivityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access activities", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Activity not found", nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Activity operation failed", err)
	}
}

func toActivityResponse(activity Activity) activityResponse {
	return activityResponse{
		ID:                  activity.ID,
		TenantID:            activity.TenantID,
		PatientID:           activity.PatientID,
		EmployeeID:          activity.EmployeeID,
		ServiceDefinitionID: activity.ServiceDefinitionID,
		ContractID:          activity.ContractID,
		PerformedAt:         activity.PerformedAt,
		DurationMinutes:     activity.DurationMinutes,
		Note:                activity.Note,
		Status:              activity.Status,
		ServiceCode:         activity.ServiceCode,
		ServiceName:         activity.ServiceName,
		LegalArea:           activity.LegalArea,
		ContractReference:   activity.ContractReference,
		PayerName:           activity.PayerName,
		CreatedBy:           activity.CreatedBy,
		CreatedAt:           activity.CreatedAt,
		UpdatedAt:           activity.UpdatedAt,
	}
}

func toActivityResponses(activities []Activity) []activityResponse {
	responses := make([]activityResponse, 0, len(activities))
	for _, activity := range activities {
		responses = append(responses, toActivityResponse(activity))
	}
	return responses
}
