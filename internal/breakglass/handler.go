package breakglass

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/server"
	"github.com/google/uuid"
)

// ActorFromContext extracts the authenticated user from the request context.
type ActorFromContext func(context.Context) (Actor, bool)

// RequestIDFromContext returns the request id used for audit correlation.
type RequestIDFromContext func(context.Context) string

// TenantRunner runs a function inside a tenant-scoped transaction.
type TenantRunner func(context.Context, uuid.UUID, func(*db.Queries) error) error

// Handler exposes the break-glass HTTP API.
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

type requestRequest struct {
	TenantID  string `json:"tenant_id"`
	PatientID string `json:"patient_id"`
	Reason    string `json:"reason"`
}

type decisionRequest struct {
	DurationMinutes int    `json:"duration_minutes"`
	DecisionReason  string `json:"decision_reason"`
}

type revokeRequest struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason"`
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

// Request handles POST /api/break-glass/grants. Only a platform_admin may ask.
func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	// A non-platform_admin gets a denied audit entry in the own tenant. The
	// service checks the role before it validates any input.
	if actor.Role != "platform_admin" {
		err := h.runWithDenialAudit(r.Context(), actor.TenantID, func(q *db.Queries) error {
			_, err := h.service.Request(r.Context(), q, actor.TenantID, actor, h.currentRequestID(r.Context()), RequestInput{})
			return err
		})
		respondBreakGlassError(w, err)
		return
	}

	var req requestRequest
	if err := decodeJSONBody(r, &req, false); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "tenant_id must be a UUID", nil)
		return
	}
	patientID, err := uuid.Parse(req.PatientID)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "patient_id must be a UUID", nil)
		return
	}

	var grant Grant
	err = h.runWithDenialAudit(r.Context(), tenantID, func(q *db.Queries) error {
		var err error
		grant, err = h.service.Request(r.Context(), q, tenantID, actor, h.currentRequestID(r.Context()), RequestInput{
			PatientID: patientID,
			Reason:    req.Reason,
		})
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusCreated, grant)
}

// List handles GET /api/break-glass/grants. A chef sees the grants of the own
// tenant. A platform_admin sees only the own requests and must name the tenant.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	tenantID := actor.TenantID
	if actor.Role == "platform_admin" {
		raw := r.URL.Query().Get("tenant_id")
		if raw == "" {
			server.RespondWithError(w, http.StatusBadRequest, "tenant_id is required", nil)
			return
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "tenant_id must be a UUID", nil)
			return
		}
		tenantID = parsed
	}

	var grants []Grant
	err := h.runWithDenialAudit(r.Context(), tenantID, func(q *db.Queries) error {
		var err error
		grants, err = h.service.List(r.Context(), q, tenantID, actor, h.currentRequestID(r.Context()))
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	if grants == nil {
		grants = []Grant{}
	}
	server.RespondWithJSON(w, http.StatusOK, grants)
}

// Approve handles POST /api/break-glass/grants/{id}/approve. Only a chef of the
// patient's tenant may approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid break-glass grant id", nil)
		return
	}
	var req decisionRequest
	if err := decodeJSONBody(r, &req, true); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var grant Grant
	err = h.runWithDenialAudit(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		grant, err = h.service.Approve(r.Context(), q, actor.TenantID, actor, h.currentRequestID(r.Context()), id, DecisionInput{
			DurationMinutes: req.DurationMinutes,
			DecisionReason:  req.DecisionReason,
		})
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, grant)
}

// Deny handles POST /api/break-glass/grants/{id}/deny. Only a chef of the
// patient's tenant may deny.
func (h *Handler) Deny(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid break-glass grant id", nil)
		return
	}
	var req decisionRequest
	if err := decodeJSONBody(r, &req, true); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var grant Grant
	err = h.runWithDenialAudit(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		grant, err = h.service.Deny(r.Context(), q, actor.TenantID, actor, h.currentRequestID(r.Context()), id, DecisionInput{
			DecisionReason: req.DecisionReason,
		})
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, grant)
}

// Revoke handles POST /api/break-glass/grants/{id}/revoke. The requester or a
// chef of the patient's tenant may revoke. A platform_admin must name the
// tenant in the body.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid break-glass grant id", nil)
		return
	}
	var req revokeRequest
	if err := decodeJSONBody(r, &req, true); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	tenantID := actor.TenantID
	if actor.Role == "platform_admin" {
		parsed, err := uuid.Parse(req.TenantID)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "tenant_id must be a UUID", nil)
			return
		}
		tenantID = parsed
	}

	var grant Grant
	err = h.runWithDenialAudit(r.Context(), tenantID, func(q *db.Queries) error {
		var err error
		grant, err = h.service.Revoke(r.Context(), q, tenantID, actor, h.currentRequestID(r.Context()), id, RevokeInput{
			TenantID: tenantID,
			Reason:   req.Reason,
		})
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, grant)
}

// Patient handles GET /api/break-glass/grants/{id}/patient. Only the requesting
// platform_admin may use it, and only while the grant is active.
func (h *Handler) Patient(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid break-glass grant id", nil)
		return
	}

	tenantID := actor.TenantID
	if actor.Role == "platform_admin" {
		raw := r.URL.Query().Get("tenant_id")
		if raw == "" {
			server.RespondWithError(w, http.StatusBadRequest, "tenant_id is required", nil)
			return
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			server.RespondWithError(w, http.StatusBadRequest, "tenant_id must be a UUID", nil)
			return
		}
		tenantID = parsed
	}

	var patient patients.Patient
	err = h.runWithDenialAudit(r.Context(), tenantID, func(q *db.Queries) error {
		var err error
		patient, err = h.service.ReadPatient(r.Context(), q, tenantID, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondBreakGlassError(w, err)
		return
	}
	server.RespondWithJSON(w, http.StatusOK, toPatientResponse(patient))
}

// runWithDenialAudit runs fn inside a tenant transaction. A forbidden request
// is rejected, but the access.denied row that the service wrote must survive.
// The transaction therefore commits for ErrForbidden and the error is returned
// to the caller afterwards. All break-glass checks run before a business write,
// so a forbidden request has nothing else to commit. For every other error the
// transaction rolls back as usual.
func (h *Handler) runWithDenialAudit(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
	var opErr error
	err := h.withTenant(ctx, tenantID, func(q *db.Queries) error {
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

func toPatientResponse(patient patients.Patient) patientResponse {
	return patientResponse{
		ID:              patient.ID,
		TenantID:        patient.TenantID,
		FirstName:       patient.FirstName,
		LastName:        patient.LastName,
		BirthDate:       patient.BirthDate.Format("2006-01-02"),
		Status:          patient.Status,
		CreatedAt:       patient.CreatedAt,
		UpdatedAt:       patient.UpdatedAt,
		StatusChangedAt: patient.StatusChangedAt,
	}
}

func respondBreakGlassError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Break-glass access denied", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Patient or break-glass grant not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Break-glass operation failed", err)
	}
}
