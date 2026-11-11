package breakglass

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/google/uuid"
)

// PatientReader is the narrow read path used after a grant is validated. The
// normal patient service rejects platform_admin, so this path is separate and
// must only be called by ReadPatient.
type PatientReader interface {
	GetForSupport(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (patients.Patient, error)
}

// Service contains the break-glass rules. Every method runs inside the caller's
// tenant-scoped transaction so the grant change and its audit event commit
// together.
type Service struct {
	store    Store
	audit    audit.Recorder
	patients PatientReader
	now      func() time.Time
}

func NewService(store Store, recorder audit.Recorder, patients PatientReader) *Service {
	return &Service{store: store, audit: recorder, patients: patients, now: time.Now}
}

// Request creates a pending grant for one named patient. Only a platform_admin
// may ask. The chef of the patient's tenant decides.
func (s *Service) Request(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string, input RequestInput) (Grant, error) {
	if actor.Role != "platform_admin" {
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.request")
		return Grant{}, ErrForbidden
	}
	if tenantID == uuid.Nil || input.PatientID == uuid.Nil {
		return Grant{}, fmt.Errorf("%w: tenant_id and patient_id are required", ErrInvalid)
	}
	reason, err := validateReason(input.Reason)
	if err != nil {
		return Grant{}, err
	}

	exists, err := s.store.PatientExists(ctx, q, tenantID, input.PatientID)
	if err != nil {
		return Grant{}, err
	}
	if !exists {
		return Grant{}, ErrNotFound
	}

	grant, err := s.store.Create(ctx, q, tenantID, input.PatientID, actor.UserID, reason)
	if err != nil {
		return Grant{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.requested",
		EntityType:  "break_glass_grant",
		EntityID:    grant.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": grant.PatientID.String()},
	}); err != nil {
		return Grant{}, err
	}
	return grant.withEffectiveStatus(s.now()), nil
}

// List returns the grants a chef can decide or a platform_admin has requested.
// The tenant scope is explicit because a platform_admin belongs to a different
// tenant from the patient.
func (s *Service) List(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string) ([]Grant, error) {
	var grants []Grant
	scope := ""
	switch actor.Role {
	case "chef":
		if tenantID != actor.TenantID {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.list")
			return nil, ErrForbidden
		}
		scope = "tenant"
		var err error
		grants, err = s.store.ListByTenant(ctx, q, tenantID)
		if err != nil {
			return nil, err
		}
	case "platform_admin":
		if tenantID == uuid.Nil {
			return nil, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
		}
		scope = "requester"
		var err error
		grants, err = s.store.ListByRequester(ctx, q, tenantID, actor.UserID)
		if err != nil {
			return nil, err
		}
	default:
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.list")
		return nil, ErrForbidden
	}

	now := s.now()
	for i := range grants {
		grants[i] = grants[i].withEffectiveStatus(now)
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.listed",
		EntityType:  "break_glass_grant",
		RequestID:   requestID,
		Metadata:    map[string]any{"count": len(grants), "scope": scope},
	}); err != nil {
		return nil, err
	}
	return grants, nil
}

// Approve grants a short read window. Only a chef of the patient's tenant may
// approve, and a requester cannot approve their own grant.
func (s *Service) Approve(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string, id uuid.UUID, input DecisionInput) (Grant, error) {
	if actor.Role != "chef" || tenantID != actor.TenantID {
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.approve")
		return Grant{}, ErrForbidden
	}
	duration, err := validateDuration(input.DurationMinutes)
	if err != nil {
		return Grant{}, err
	}
	decisionReason, err := validateDecisionReason(input.DecisionReason)
	if err != nil {
		return Grant{}, err
	}

	grant, err := s.store.Get(ctx, q, tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.approve")
		}
		return Grant{}, err
	}
	if grant.RequestedBy == actor.UserID {
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.approve")
		return Grant{}, ErrForbidden
	}
	if grant.Status != "pending" {
		return Grant{}, ErrConflict
	}

	approved, err := s.store.Approve(ctx, q, tenantID, id, actor.UserID, duration, decisionReason)
	if err != nil {
		return Grant{}, err
	}
	metadata := map[string]any{
		"patient_id":       approved.PatientID.String(),
		"duration_minutes": duration,
	}
	if approved.ExpiresAt != nil {
		metadata["expires_at"] = approved.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.approved",
		EntityType:  "break_glass_grant",
		EntityID:    approved.ID,
		RequestID:   requestID,
		Metadata:    metadata,
	}); err != nil {
		return Grant{}, err
	}
	return approved.withEffectiveStatus(s.now()), nil
}

// Deny rejects a pending grant. Only a chef of the patient's tenant may deny.
func (s *Service) Deny(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string, id uuid.UUID, input DecisionInput) (Grant, error) {
	if actor.Role != "chef" || tenantID != actor.TenantID {
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.deny")
		return Grant{}, ErrForbidden
	}
	decisionReason, err := validateDecisionReason(input.DecisionReason)
	if err != nil {
		return Grant{}, err
	}

	grant, err := s.store.Get(ctx, q, tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.deny")
		}
		return Grant{}, err
	}
	if grant.Status != "pending" {
		return Grant{}, ErrConflict
	}

	denied, err := s.store.Deny(ctx, q, tenantID, id, actor.UserID, decisionReason)
	if err != nil {
		return Grant{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.denied",
		EntityType:  "break_glass_grant",
		EntityID:    denied.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": denied.PatientID.String()},
	}); err != nil {
		return Grant{}, err
	}
	return denied.withEffectiveStatus(s.now()), nil
}

// Revoke ends a pending or approved grant. The requester or a chef of the
// patient's tenant may revoke.
func (s *Service) Revoke(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string, id uuid.UUID, input RevokeInput) (Grant, error) {
	var (
		grant Grant
		err   error
	)
	switch actor.Role {
	case "chef":
		if tenantID != actor.TenantID {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.revoke")
			return Grant{}, ErrForbidden
		}
		grant, err = s.store.Get(ctx, q, tenantID, id)
	case "platform_admin":
		if tenantID == uuid.Nil || input.TenantID != tenantID {
			return Grant{}, fmt.Errorf("%w: tenant_id is required", ErrInvalid)
		}
		grant, err = s.store.GetByRequester(ctx, q, tenantID, id, actor.UserID)
	default:
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.revoke")
		return Grant{}, ErrForbidden
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.revoke")
		}
		return Grant{}, err
	}
	if grant.Status != "pending" && grant.Status != "approved" {
		return Grant{}, ErrConflict
	}

	previousStatus := grant.Status
	revoked, err := s.store.Revoke(ctx, q, tenantID, id, strings.TrimSpace(input.Reason))
	if err != nil {
		return Grant{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.revoked",
		EntityType:  "break_glass_grant",
		EntityID:    revoked.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":      revoked.PatientID.String(),
			"previous_status": previousStatus,
		},
	}); err != nil {
		return Grant{}, err
	}
	return revoked.withEffectiveStatus(s.now()), nil
}

// ReadPatient returns one patient through an active grant. It is the only path
// that lets a platform_admin read health data, and every successful read is
// audited in the patient's tenant. The active check uses clock_timestamp() in
// SQL so a long transaction cannot use a stale time.
func (s *Service) ReadPatient(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID string, grantID uuid.UUID) (patients.Patient, error) {
	if actor.Role != "platform_admin" || tenantID == uuid.Nil {
		s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.patient_read")
		return patients.Patient{}, ErrForbidden
	}

	grant, err := s.store.GetActiveForRequester(ctx, q, tenantID, grantID, actor.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, tenantID, actor, requestID, "break_glass.patient_read")
			return patients.Patient{}, ErrForbidden
		}
		return patients.Patient{}, err
	}
	if s.patients == nil {
		return patients.Patient{}, fmt.Errorf("patient reader is not configured")
	}

	patient, err := s.patients.GetForSupport(ctx, q, tenantID, grant.PatientID)
	if err != nil {
		return patients.Patient{}, err
	}
	if _, err := s.store.Touch(ctx, q, tenantID, grant.ID); err != nil {
		return patients.Patient{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "break_glass.patient_read",
		EntityType:  "patient",
		EntityID:    patient.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"grant_id": grant.ID.String()},
	}); err != nil {
		return patients.Patient{}, err
	}
	return patient, nil
}

func validateReason(value string) (string, error) {
	reason := strings.TrimSpace(value)
	if reason == "" {
		return "", fmt.Errorf("%w: reason is required", ErrInvalid)
	}
	if len([]rune(reason)) > maxReasonRunes {
		return "", fmt.Errorf("%w: reason must be at most %d characters", ErrInvalid, maxReasonRunes)
	}
	return reason, nil
}

func validateDecisionReason(value string) (string, error) {
	reason := strings.TrimSpace(value)
	if len([]rune(reason)) > maxReasonRunes {
		return "", fmt.Errorf("%w: decision_reason must be at most %d characters", ErrInvalid, maxReasonRunes)
	}
	return reason, nil
}

func validateDuration(value int) (int, error) {
	if value == 0 {
		return DefaultDurationMinutes, nil
	}
	if value < MinDurationMinutes || value > MaxDurationMinutes {
		return 0, fmt.Errorf("%w: duration_minutes must be between %d and %d", ErrInvalid, MinDurationMinutes, MaxDurationMinutes)
	}
	return value, nil
}

func (s *Service) record(ctx context.Context, q *db.Queries, event audit.Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

func (s *Service) recordDenied(ctx context.Context, q *db.Queries, tenantID uuid.UUID, actor Actor, requestID, attempted string) {
	if tenantID == uuid.Nil {
		return
	}
	// Best effort: a denied attempt must not turn into a 500.
	_ = s.record(ctx, q, audit.Event{
		TenantID:    tenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "break_glass_grant",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": attempted},
	})
}
