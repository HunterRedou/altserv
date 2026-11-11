package audit

import (
	"context"
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

// Handler exposes the restricted audit read API.
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

// List handles GET /api/audit-events. Filters: action, entity_type, entity_id,
// from, to and limit. Times are RFC 3339.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorFromContext(r.Context())
	if !ok {
		server.RespondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	filter, err := parseListFilter(r)
	if err != nil {
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	var events []EventRecord
	err = h.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		events, err = h.service.List(r.Context(), q, actor, h.currentRequestID(r.Context()), filter)
		return err
	})
	if err != nil {
		respondAuditError(w, err)
		return
	}
	if events == nil {
		events = []EventRecord{}
	}
	server.RespondWithJSON(w, http.StatusOK, events)
}

func parseListFilter(r *http.Request) (ListFilter, error) {
	query := r.URL.Query()
	filter := ListFilter{
		Action:     query.Get("action"),
		EntityType: query.Get("entity_type"),
	}

	if value := query.Get("entity_id"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			return ListFilter{}, errors.New("entity_id must be a UUID")
		}
		filter.EntityID = &id
	}
	if value := query.Get("from"); value != "" {
		from, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return ListFilter{}, errors.New("from must be an RFC 3339 timestamp")
		}
		filter.From = &from
	}
	if value := query.Get("to"); value != "" {
		to, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return ListFilter{}, errors.New("to must be an RFC 3339 timestamp")
		}
		filter.To = &to
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return ListFilter{}, errors.New("limit must be a number")
		}
		filter.Limit = limit
	}
	return filter, nil
}

func (h *Handler) currentRequestID(ctx context.Context) string {
	if h.requestID == nil {
		return ""
	}
	return h.requestID(ctx)
}

func respondAuditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		server.RespondWithError(w, http.StatusForbidden, "Not allowed to read the audit log", nil)
	case errors.Is(err, ErrInvalid):
		server.RespondWithError(w, http.StatusBadRequest, err.Error(), nil)
	default:
		server.RespondWithError(w, http.StatusInternalServerError, "Audit read failed", err)
	}
}
