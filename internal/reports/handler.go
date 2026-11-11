package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Handler exposes the report HTTP API.
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
	ActivityID string `json:"activity_id"`
	Quantity   int    `json:"quantity"`
}

type createReportRequest struct {
	Note       string            `json:"note"`
	Activities []activityRequest `json:"activities"`
}

type updateReportRequest struct {
	Version    int                `json:"version"`
	Note       *string            `json:"note"`
	Activities *[]activityRequest `json:"activities"`
}

type versionRequest struct {
	Version int `json:"version"`
}

type returnReportRequest struct {
	Version int    `json:"version"`
	Reason  string `json:"reason"`
}

type reportActivityResponse struct {
	ID          uuid.UUID `json:"id"`
	ReportID    uuid.UUID `json:"report_id"`
	ActivityID  uuid.UUID `json:"activity_id"`
	Quantity    int       `json:"quantity"`
	PerformedAt time.Time `json:"performed_at"`
	SortOrder   int       `json:"sort_order"`
}

type reportResponse struct {
	ID          uuid.UUID                `json:"id"`
	TenantID    uuid.UUID                `json:"tenant_id"`
	VisitID     uuid.UUID                `json:"visit_id"`
	PatientID   uuid.UUID                `json:"patient_id"`
	EmployeeID  uuid.UUID                `json:"employee_id"`
	Status      string                   `json:"status"`
	Note        string                   `json:"note,omitempty"`
	Version     int                      `json:"version"`
	CreatedBy   uuid.UUID                `json:"created_by"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
	SubmittedAt *time.Time               `json:"submitted_at,omitempty"`
	ApprovedAt  *time.Time               `json:"approved_at,omitempty"`
	ApprovedBy  *uuid.UUID               `json:"approved_by,omitempty"`
	Activities  []reportActivityResponse `json:"activities"`
}

type transitionResponse struct {
	ID          uuid.UUID `json:"id"`
	ReportID    uuid.UUID `json:"report_id"`
	FromStatus  string    `json:"from_status"`
	ToStatus    string    `json:"to_status"`
	Kind        string    `json:"kind"`
	ActorUserID uuid.UUID `json:"actor_user_id"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Create handles POST /api/visits/{id}/reports.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	visitID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var req createReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	activities, err := parseActivities(req.Activities)
	if err != nil {
		respondReportError(w, err)
		return
	}

	var report Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		report, err = h.service.Create(r.Context(), q, actor, h.currentRequestID(r.Context()), visitID, CreateInput{
			Note:       req.Note,
			Activities: activities,
		})
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusCreated, toReportResponse(report))
}

// ListByVisit handles GET /api/visits/{id}/reports.
func (h *Handler) ListByVisit(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	visitID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid visit id", nil)
		return
	}

	var reports []Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		reports, err = h.service.ListByVisit(r.Context(), q, actor, h.currentRequestID(r.Context()), visitID)
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponses(reports))
}

// ListByPatient handles GET /api/patients/{id}/reports.
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

	var reports []Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		reports, err = h.service.ListByPatient(r.Context(), q, actor, h.currentRequestID(r.Context()), patientID)
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponses(reports))
}

// Get handles GET /api/reports/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	h.withReport(w, r, func(q *db.Queries, actor Actor, id uuid.UUID) (Report, error) {
		return h.service.Get(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
	})
}

// Update handles PATCH /api/reports/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var req updateReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var activities *[]ActivityInput
	if req.Activities != nil {
		parsed, err := parseActivities(*req.Activities)
		if err != nil {
			respondReportError(w, err)
			return
		}
		activities = &parsed
	}

	var report Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		report, err = h.service.Update(r.Context(), q, actor, h.currentRequestID(r.Context()), id, UpdateInput{
			Version:    req.Version,
			Note:       req.Note,
			Activities: activities,
		})
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponse(report))
}

// Submit handles POST /api/reports/{id}/submit.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID, version int) (Report, error) {
		return h.service.Submit(r.Context(), q, actor, h.currentRequestID(r.Context()), id, version)
	})
}

// Approve handles POST /api/reports/{id}/approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID, version int) (Report, error) {
		return h.service.Approve(r.Context(), q, actor, h.currentRequestID(r.Context()), id, version)
	})
}

// ReadyForBilling handles POST /api/reports/{id}/ready-for-billing.
func (h *Handler) ReadyForBilling(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(q *db.Queries, actor Actor, id uuid.UUID, version int) (Report, error) {
		return h.service.ReadyForBilling(r.Context(), q, actor, h.currentRequestID(r.Context()), id, version)
	})
}

// Return handles POST /api/reports/{id}/return.
func (h *Handler) Return(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var req returnReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var report Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		report, err = h.service.Return(r.Context(), q, actor, h.currentRequestID(r.Context()), id, ReturnInput{
			Version: req.Version,
			Reason:  req.Reason,
		})
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponse(report))
}

// ListTransitions handles GET /api/reports/{id}/transitions.
func (h *Handler) ListTransitions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var transitions []ReportTransition
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		transitions, err = h.service.ListTransitions(r.Context(), q, actor, h.currentRequestID(r.Context()), id)
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toTransitionResponses(transitions))
}

func (h *Handler) withReport(w http.ResponseWriter, r *http.Request, action func(*db.Queries, Actor, uuid.UUID) (Report, error)) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var report Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		report, err = action(q, actor, id)
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponse(report))
}

func (h *Handler) transition(w http.ResponseWriter, r *http.Request, action func(*db.Queries, Actor, uuid.UUID, int) (Report, error)) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Invalid report id", nil)
		return
	}

	var req versionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.RespondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	var report Report
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		report, err = action(q, actor, id, req.Version)
		return err
	})
	if err != nil {
		respondReportError(w, err)
		return
	}

	server.RespondWithJSON(w, http.StatusOK, toReportResponse(report))
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func parseActivities(requests []activityRequest) ([]ActivityInput, error) {
	activities := make([]ActivityInput, 0, len(requests))
	for _, request := range requests {
		activityID, err := uuid.Parse(request.ActivityID)
		if err != nil {
			return nil, fmt.Errorf("%w: activity_id must be a UUID", ErrInvalid)
		}
		activities = append(activities, ActivityInput{ActivityID: activityID, Quantity: request.Quantity})
	}
	return activities, nil
}

func respondReportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to access reports", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		server.RespondWithError(w, http.StatusNotFound, "Report or visit not found", nil)
	case errors.Is(err, ErrConflict):
		server.RespondWithError(w, http.StatusConflict, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Report operation failed", err)
	}
}

func toReportResponse(report Report) reportResponse {
	response := reportResponse{
		ID:          report.ID,
		TenantID:    report.TenantID,
		VisitID:     report.VisitID,
		PatientID:   report.PatientID,
		EmployeeID:  report.EmployeeID,
		Status:      report.Status,
		Note:        report.Note,
		Version:     report.Version,
		CreatedBy:   report.CreatedBy,
		CreatedAt:   report.CreatedAt,
		UpdatedAt:   report.UpdatedAt,
		SubmittedAt: report.SubmittedAt,
		ApprovedAt:  report.ApprovedAt,
		ApprovedBy:  report.ApprovedBy,
		Activities:  make([]reportActivityResponse, 0, len(report.Activities)),
	}
	for _, activity := range report.Activities {
		response.Activities = append(response.Activities, reportActivityResponse{
			ID:          activity.ID,
			ReportID:    activity.ReportID,
			ActivityID:  activity.ActivityID,
			Quantity:    activity.Quantity,
			PerformedAt: activity.PerformedAt,
			SortOrder:   activity.SortOrder,
		})
	}
	return response
}

func toReportResponses(reports []Report) []reportResponse {
	responses := make([]reportResponse, 0, len(reports))
	for _, report := range reports {
		responses = append(responses, toReportResponse(report))
	}
	return responses
}

func toTransitionResponses(transitions []ReportTransition) []transitionResponse {
	responses := make([]transitionResponse, 0, len(transitions))
	for _, transition := range transitions {
		responses = append(responses, transitionResponse{
			ID:          transition.ID,
			ReportID:    transition.ReportID,
			FromStatus:  transition.FromStatus,
			ToStatus:    transition.ToStatus,
			Kind:        transition.Kind,
			ActorUserID: transition.ActorUserID,
			Reason:      transition.Reason,
			CreatedAt:   transition.CreatedAt,
		})
	}
	return responses
}
