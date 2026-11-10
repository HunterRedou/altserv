package templates

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

// Handler exposes the template HTTP API.
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

type activityRequest struct {
	ActivityID      string `json:"activity_id"`
	DefaultQuantity int    `json:"default_quantity"`
	SortOrder       int    `json:"sort_order"`
}

type createRequest struct {
	Name       string            `json:"name"`
	Activities []activityRequest `json:"activities"`
}

type updateRequest struct {
	Activities []activityRequest `json:"activities"`
}

type activityResponse struct {
	ID              uuid.UUID `json:"id"`
	ActivityID      uuid.UUID `json:"activity_id"`
	DefaultQuantity int       `json:"default_quantity"`
	SortOrder       int       `json:"sort_order"`
	ServiceCode     string    `json:"service_code"`
	ServiceName     string    `json:"service_name"`
	LegalArea       string    `json:"legal_area"`
	Unit            string    `json:"unit"`
}

type templateResponse struct {
	ID         uuid.UUID          `json:"id"`
	TenantID   uuid.UUID          `json:"tenant_id"`
	PatientID  uuid.UUID          `json:"patient_id"`
	Name       string             `json:"name"`
	CreatedBy  uuid.UUID          `json:"created_by"`
	Version    int                `json:"version"`
	Status     string             `json:"status"`
	Activities []activityResponse `json:"activities"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// Create handles POST /api/patients/{id}/templates.
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

	activities, err := parseActivities(req.Activities)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	var template Template
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		template, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateInput{
			PatientID:  patientID,
			Name:       req.Name,
			Activities: activities,
		})
		return err
	})
	if err != nil {
		respondTemplateError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toTemplateResponse(template))
}

// ListByPatient handles GET /api/patients/{id}/templates.
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

	var templates []Template
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		templates, err = h.service.ListByPatient(r.Context(), q, actor, h.currentRequestID(r.Context()), patientID)
		return err
	})
	if err != nil {
		respondTemplateError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toTemplateResponses(templates))
}

// Get handles GET /api/templates/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid template id", nil)
		return
	}

	var template Template
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		template, err = h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondTemplateError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toTemplateResponse(template))
}

// Update handles PATCH /api/templates/{id}. It creates a new version.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid template id", nil)
		return
	}

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	activities, err := parseActivities(req.Activities)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	var template Template
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		template, err = h.service.Update(r.Context(), q, actor, h.currentRequestID(r.Context()), id, UpdateInput{Activities: activities})
		return err
	})
	if err != nil {
		respondTemplateError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toTemplateResponse(template))
}

// Archive handles POST /api/templates/{id}/archive.
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid template id", nil)
		return
	}

	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		return h.service.Archive(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
	})
	if err != nil {
		respondTemplateError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func parseActivities(requests []activityRequest) ([]ActivityInput, error) {
	activities := make([]ActivityInput, 0, len(requests))
	for _, req := range requests {
		id, err := uuid.Parse(req.ActivityID)
		if err != nil {
			return nil, errors.New("activity_id must be a UUID")
		}
		activities = append(activities, ActivityInput{
			ActivityID:      id,
			DefaultQuantity: req.DefaultQuantity,
			SortOrder:       req.SortOrder,
		})
	}
	return activities, nil
}

func respondTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access templates", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Template not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Template operation failed", err)
	}
}

func toTemplateResponse(template Template) templateResponse {
	activities := make([]activityResponse, 0, len(template.Activities))
	for _, activity := range template.Activities {
		activities = append(activities, activityResponse{
			ID:              activity.ID,
			ActivityID:      activity.ActivityID,
			DefaultQuantity: activity.DefaultQuantity,
			SortOrder:       activity.SortOrder,
			ServiceCode:     activity.ServiceCode,
			ServiceName:     activity.ServiceName,
			LegalArea:       activity.LegalArea,
			Unit:            activity.Unit,
		})
	}
	return templateResponse{
		ID:         template.ID,
		TenantID:   template.TenantID,
		PatientID:  template.PatientID,
		Name:       template.Name,
		CreatedBy:  template.CreatedBy,
		Version:    template.Version,
		Status:     template.Status,
		Activities: activities,
		CreatedAt:  template.CreatedAt,
		UpdatedAt:  template.UpdatedAt,
	}
}

func toTemplateResponses(templates []Template) []templateResponse {
	responses := make([]templateResponse, 0, len(templates))
	for _, template := range templates {
		responses = append(responses, toTemplateResponse(template))
	}
	return responses
}
