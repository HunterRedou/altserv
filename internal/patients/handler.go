package patients

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

// Handler exposes the patient HTTP API.
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
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	BirthDate string `json:"birth_date"`
	Status    string `json:"status"`
}

// updateRequest uses pointers so an omitted field is left unchanged while an
// explicit empty value is still validated and rejected.
type updateRequest struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	BirthDate *string `json:"birth_date"`
	Status    *string `json:"status"`
}

type patientResponse struct {
	ID              uuid.UUID `json:"id"`
	TenantID        uuid.UUID `json:"tenant_id"`
	FirstName       string    `json:"first_name"`
	LastName        string    `json:"last_name"`
	BirthDate       string    `json:"birth_date"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	StatusChangedAt time.Time `json:"status_changed_at"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	birthDate, err := time.Parse(dateLayout, req.BirthDate)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "birth_date must be YYYY-MM-DD", nil)
		return
	}

	var patient Patient
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		patient, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), CreateInput{
			FirstName: req.FirstName,
			LastName:  req.LastName,
			BirthDate: birthDate,
			Status:    req.Status,
		})
		return err
	})
	if err != nil {
		respondPatientError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toResponse(patient))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	// A non-empty q switches from listing to the protected search index.
	// The term is read from the query string only and never logged.
	query := r.URL.Query().Get("q")

	var patients []Patient
	err := h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		if query != "" {
			patients, err = h.service.Search(r.Context(), q, actor, h.currentRequestID(r.Context()), query)
			return err
		}
		patients, err = h.service.List(r.Context(), q, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondPatientError(w, err)
		return
	}

	response := make([]patientResponse, 0, len(patients))
	for _, patient := range patients {
		response = append(response, toResponse(patient))
	}
	server.RespondWithJSON(w, http.StatusOK, response)
}

// Update handles PATCH /api/patients/{id}. Only a chef may change patient
// records; employees never get write access.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid patient id", nil)
		return
	}

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	input := UpdateInput{
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Status:    req.Status,
	}
	if req.BirthDate != nil {
		birthDate, err := time.Parse(dateLayout, *req.BirthDate)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "birth_date must be YYYY-MM-DD", nil)
			return
		}
		input.BirthDate = &birthDate
	}

	var patient Patient
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		patient, err = h.service.Update(r.Context(), q, actor, h.currentRequestID(r.Context()), id, input)
		return err
	})
	if err != nil {
		respondPatientError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toResponse(patient))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid patient id", nil)
		return
	}

	var patient Patient
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		patient, err = h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondPatientError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toResponse(patient))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondPatientError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access patients", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Patient not found", nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Patient operation failed", err)
	}
}

func toResponse(patient Patient) patientResponse {
	return patientResponse{
		ID:              patient.ID,
		TenantID:        patient.TenantID,
		FirstName:       patient.FirstName,
		LastName:        patient.LastName,
		BirthDate:       patient.BirthDate.Format(dateLayout),
		Status:          patient.Status,
		CreatedAt:       patient.CreatedAt,
		UpdatedAt:       patient.UpdatedAt,
		StatusChangedAt: patient.StatusChangedAt,
	}
}
