package retention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Service contains the retention and deletion rules. Every mutating method runs
// inside the caller's tenant-scoped transaction, so the business change and its
// audit event commit or roll back together.
//
// The service never deletes care data on its own. Deletion needs an explicit
// request by one tenant chef and an approval by a different tenant chef. The
// scheduled review only reports candidate counts.
type Service struct {
	store Store
	audit audit.Recorder
	now   func() time.Time
}

func NewService(store Store, recorder audit.Recorder) *Service {
	return &Service{store: store, audit: recorder, now: time.Now}
}

// CreatePolicy stores a new retention policy. It is disabled until a chef
// confirms it, and it has no effect on the review until then.
func (s *Service) CreatePolicy(ctx context.Context, q *db.Queries, actor Actor, requestID string, input PolicyInput) (Policy, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.policy_create")
		return Policy{}, ErrForbidden
	}
	validated, err := validatePolicy(input)
	if err != nil {
		return Policy{}, err
	}

	policy, err := s.store.CreatePolicy(ctx, q, actor.TenantID, validated, actor.UserID)
	if err != nil {
		return Policy{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.policy_created",
		EntityType:  "retention_policy",
		EntityID:    policy.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"category":      policy.Category,
			"period_months": policy.PeriodMonths,
			"status":        policy.Status,
		},
	}); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// ListPolicies returns every policy of the actor's tenant.
func (s *Service) ListPolicies(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]Policy, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.policy_list")
		return nil, ErrForbidden
	}
	policies, err := s.store.ListPolicies(ctx, q, actor.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.policies_listed",
		EntityType:  "retention_policy",
		RequestID:   requestID,
		Metadata:    map[string]any{"count": len(policies)},
	}); err != nil {
		return nil, err
	}
	return policies, nil
}

// UpdatePolicy changes a period or a note. A changed period is a new decision,
// so the policy returns to disabled and must be confirmed again.
func (s *Service) UpdatePolicy(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input PolicyUpdateInput) (Policy, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.policy_update")
		return Policy{}, ErrForbidden
	}
	if id == uuid.Nil {
		return Policy{}, fmt.Errorf("%w: policy id is required", ErrInvalid)
	}
	period, err := validatePeriod(input.PeriodMonths)
	if err != nil {
		return Policy{}, err
	}
	note, err := validateNote(input.Note)
	if err != nil {
		return Policy{}, err
	}

	policy, err := s.store.UpdatePolicy(ctx, q, actor.TenantID, id, period, note)
	if err != nil {
		return Policy{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.policy_updated",
		EntityType:  "retention_policy",
		EntityID:    policy.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"category":      policy.Category,
			"period_months": policy.PeriodMonths,
			"status":        policy.Status,
		},
	}); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// ConfirmPolicy enables a disabled policy. Confirmation is the controller's
// explicit decision that the period has been professionally reviewed.
func (s *Service) ConfirmPolicy(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Policy, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.policy_confirm")
		return Policy{}, ErrForbidden
	}
	if id == uuid.Nil {
		return Policy{}, fmt.Errorf("%w: policy id is required", ErrInvalid)
	}

	policy, err := s.store.ConfirmPolicy(ctx, q, actor.TenantID, id, actor.UserID)
	if err != nil {
		return Policy{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.policy_confirmed",
		EntityType:  "retention_policy",
		EntityID:    policy.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"category":      policy.Category,
			"period_months": policy.PeriodMonths,
		},
	}); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// DisablePolicy turns a policy off again. The row stays as a record.
func (s *Service) DisablePolicy(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Policy, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.policy_disable")
		return Policy{}, ErrForbidden
	}
	if id == uuid.Nil {
		return Policy{}, fmt.Errorf("%w: policy id is required", ErrInvalid)
	}

	policy, err := s.store.DisablePolicy(ctx, q, actor.TenantID, id)
	if err != nil {
		return Policy{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.policy_disabled",
		EntityType:  "retention_policy",
		EntityID:    policy.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"category": policy.Category},
	}); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// CreateLegalHold declares a tenant-wide or patient-scoped hold. A hold blocks
// deletion while it is active. A chef can declare it without a second chef,
// because a hold restricts processing rather than releasing data.
func (s *Service) CreateLegalHold(ctx context.Context, q *db.Queries, actor Actor, requestID string, input LegalHoldInput) (LegalHold, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.legal_hold_create")
		return LegalHold{}, ErrForbidden
	}
	reason, err := validateReason(input.Reason)
	if err != nil {
		return LegalHold{}, err
	}
	if input.PatientID != nil {
		if *input.PatientID == uuid.Nil {
			return LegalHold{}, fmt.Errorf("%w: patient_id must be a UUID", ErrInvalid)
		}
		exists, err := s.store.PatientExists(ctx, q, actor.TenantID, *input.PatientID)
		if err != nil {
			return LegalHold{}, err
		}
		if !exists {
			return LegalHold{}, ErrNotFound
		}
	}

	reviewDue := input.ReviewDueAt
	if reviewDue == nil {
		value := s.now().AddDate(0, DefaultLegalHoldReviewMonths, 0)
		reviewDue = &value
	} else if !reviewDue.After(s.now()) {
		return LegalHold{}, fmt.Errorf("%w: review_due_at must be in the future", ErrInvalid)
	}

	hold, err := s.store.CreateLegalHold(ctx, q, actor.TenantID, LegalHoldInput{
		PatientID:   input.PatientID,
		Reason:      reason,
		ReviewDueAt: reviewDue,
	}, actor.UserID)
	if err != nil {
		return LegalHold{}, err
	}
	metadata := map[string]any{"scope": "tenant"}
	if hold.PatientID != nil {
		metadata["scope"] = "patient"
		metadata["patient_id"] = hold.PatientID.String()
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.legal_hold_declared",
		EntityType:  "legal_hold",
		EntityID:    hold.ID,
		RequestID:   requestID,
		Metadata:    metadata,
	}); err != nil {
		return LegalHold{}, err
	}
	return hold, nil
}

// ListLegalHolds returns every hold of the actor's tenant.
func (s *Service) ListLegalHolds(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]LegalHold, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.legal_hold_list")
		return nil, ErrForbidden
	}
	holds, err := s.store.ListLegalHolds(ctx, q, actor.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.legal_holds_listed",
		EntityType:  "legal_hold",
		RequestID:   requestID,
		Metadata:    map[string]any{"count": len(holds)},
	}); err != nil {
		return nil, err
	}
	return holds, nil
}

// LiftLegalHold ends an active hold. The lift reason is required so the
// decision is documented.
func (s *Service) LiftLegalHold(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, reason string) (LegalHold, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.legal_hold_lift")
		return LegalHold{}, ErrForbidden
	}
	if id == uuid.Nil {
		return LegalHold{}, fmt.Errorf("%w: hold id is required", ErrInvalid)
	}
	liftReason, err := validateReason(reason)
	if err != nil {
		return LegalHold{}, err
	}

	hold, err := s.store.LiftLegalHold(ctx, q, actor.TenantID, id, actor.UserID, liftReason)
	if err != nil {
		return LegalHold{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "retention.legal_hold_lifted",
		EntityType:  "legal_hold",
		EntityID:    hold.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"scope": holdScope(hold)},
	}); err != nil {
		return LegalHold{}, err
	}
	return hold, nil
}

// CreateDeletionRequest opens a patient-scoped deletion request. Only a tenant
// chef may ask. An active legal hold blocks the request immediately, so a chef
// gets the answer before a second chef spends time on an approval.
func (s *Service) CreateDeletionRequest(ctx context.Context, q *db.Queries, actor Actor, requestID string, input DeletionRequestInput) (DeletionRequest, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.request")
		return DeletionRequest{}, ErrForbidden
	}
	if input.PatientID == uuid.Nil {
		return DeletionRequest{}, fmt.Errorf("%w: patient_id is required", ErrInvalid)
	}
	reason, err := validateReason(input.Reason)
	if err != nil {
		return DeletionRequest{}, err
	}
	exists, err := s.store.PatientExists(ctx, q, actor.TenantID, input.PatientID)
	if err != nil {
		return DeletionRequest{}, err
	}
	if !exists {
		return DeletionRequest{}, ErrNotFound
	}
	held, err := s.store.HasActiveLegalHold(ctx, q, actor.TenantID, input.PatientID)
	if err != nil {
		return DeletionRequest{}, err
	}
	if held {
		return DeletionRequest{}, ErrLegalHold
	}

	request, err := s.store.CreateDeletionRequest(ctx, q, actor.TenantID, DeletionRequestInput{
		PatientID: input.PatientID,
		Reason:    reason,
	}, actor.UserID)
	if err != nil {
		return DeletionRequest{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.requested",
		EntityType:  "deletion_request",
		EntityID:    request.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": request.PatientID.String()},
	}); err != nil {
		return DeletionRequest{}, err
	}
	return request, nil
}

// ListDeletionRequests returns every request of the actor's tenant.
func (s *Service) ListDeletionRequests(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]DeletionRequest, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.list")
		return nil, ErrForbidden
	}
	requests, err := s.store.ListDeletionRequests(ctx, q, actor.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.listed",
		EntityType:  "deletion_request",
		RequestID:   requestID,
		Metadata:    map[string]any{"count": len(requests)},
	}); err != nil {
		return nil, err
	}
	return requests, nil
}

// ApproveDeletionRequest approves a pending request. Four eyes: the approving
// chef must be a different person from the requester. An active legal hold
// blocks the approval.
func (s *Service) ApproveDeletionRequest(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, reason string) (DeletionRequest, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.approve")
		return DeletionRequest{}, ErrForbidden
	}
	if id == uuid.Nil {
		return DeletionRequest{}, fmt.Errorf("%w: request id is required", ErrInvalid)
	}
	decisionReason, err := validateOptionalReason(reason)
	if err != nil {
		return DeletionRequest{}, err
	}

	current, err := s.store.GetDeletionRequest(ctx, q, actor.TenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, actor, requestID, "deletion.approve")
		}
		return DeletionRequest{}, err
	}
	if current.RequestedBy == actor.UserID {
		s.recordDenied(ctx, q, actor, requestID, "deletion.approve")
		return DeletionRequest{}, ErrForbidden
	}
	if current.Status != "pending" {
		return DeletionRequest{}, ErrConflict
	}
	held, err := s.store.HasActiveLegalHold(ctx, q, actor.TenantID, current.PatientID)
	if err != nil {
		return DeletionRequest{}, err
	}
	if held {
		return DeletionRequest{}, ErrLegalHold
	}

	approved, err := s.store.ApproveDeletionRequest(ctx, q, actor.TenantID, id, actor.UserID, decisionReason)
	if err != nil {
		return DeletionRequest{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.approved",
		EntityType:  "deletion_request",
		EntityID:    approved.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": approved.PatientID.String()},
	}); err != nil {
		return DeletionRequest{}, err
	}
	return approved, nil
}

// RejectDeletionRequest rejects a pending request. Four eyes: the deciding chef
// must be a different person from the requester.
func (s *Service) RejectDeletionRequest(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, reason string) (DeletionRequest, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.reject")
		return DeletionRequest{}, ErrForbidden
	}
	if id == uuid.Nil {
		return DeletionRequest{}, fmt.Errorf("%w: request id is required", ErrInvalid)
	}
	decisionReason, err := validateOptionalReason(reason)
	if err != nil {
		return DeletionRequest{}, err
	}

	current, err := s.store.GetDeletionRequest(ctx, q, actor.TenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, actor, requestID, "deletion.reject")
		}
		return DeletionRequest{}, err
	}
	if current.RequestedBy == actor.UserID {
		s.recordDenied(ctx, q, actor, requestID, "deletion.reject")
		return DeletionRequest{}, ErrForbidden
	}
	if current.Status != "pending" {
		return DeletionRequest{}, ErrConflict
	}

	rejected, err := s.store.RejectDeletionRequest(ctx, q, actor.TenantID, id, actor.UserID, decisionReason)
	if err != nil {
		return DeletionRequest{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.rejected",
		EntityType:  "deletion_request",
		EntityID:    rejected.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": rejected.PatientID.String()},
	}); err != nil {
		return DeletionRequest{}, err
	}
	return rejected, nil
}

// CancelDeletionRequest withdraws a pending or approved request. Either chef
// may cancel, including the requester.
func (s *Service) CancelDeletionRequest(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (DeletionRequest, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.cancel")
		return DeletionRequest{}, ErrForbidden
	}
	if id == uuid.Nil {
		return DeletionRequest{}, fmt.Errorf("%w: request id is required", ErrInvalid)
	}

	cancelled, err := s.store.CancelDeletionRequest(ctx, q, actor.TenantID, id, actor.UserID)
	if err != nil {
		return DeletionRequest{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.cancelled",
		EntityType:  "deletion_request",
		EntityID:    cancelled.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": cancelled.PatientID.String()},
	}); err != nil {
		return DeletionRequest{}, err
	}
	return cancelled, nil
}

// ExecuteDeletion performs an approved deletion. Only a tenant chef may run it,
// the executor must differ from the requester, and an active legal hold blocks
// the deletion. The function counts the affected rows, deletes them in
// dependency order, writes the append-only register entry and marks the request
// executed. Everything runs in one transaction.
func (s *Service) ExecuteDeletion(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (DeletionResult, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.execute")
		return DeletionResult{}, ErrForbidden
	}
	if id == uuid.Nil {
		return DeletionResult{}, fmt.Errorf("%w: request id is required", ErrInvalid)
	}

	request, err := s.store.GetDeletionRequest(ctx, q, actor.TenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordDenied(ctx, q, actor, requestID, "deletion.execute")
		}
		return DeletionResult{}, err
	}
	if request.RequestedBy == actor.UserID {
		s.recordDenied(ctx, q, actor, requestID, "deletion.execute")
		return DeletionResult{}, ErrForbidden
	}
	if request.Status != "approved" {
		return DeletionResult{}, ErrConflict
	}
	held, err := s.store.HasActiveLegalHold(ctx, q, actor.TenantID, request.PatientID)
	if err != nil {
		return DeletionResult{}, err
	}
	if held {
		return DeletionResult{}, ErrLegalHold
	}

	rowCounts, err := s.store.CountPatientDeletionRows(ctx, q, actor.TenantID, request.PatientID)
	if err != nil {
		return DeletionResult{}, err
	}
	if err := s.store.DeletePatientData(ctx, q, actor.TenantID, request.PatientID); err != nil {
		return DeletionResult{}, err
	}
	executed, err := s.store.MarkDeletionExecuted(ctx, q, actor.TenantID, id, actor.UserID)
	if err != nil {
		return DeletionResult{}, err
	}
	entry, err := s.store.CreateRegisterEntry(ctx, q, actor.TenantID, id, request.PatientID, actor.UserID, rowCounts)
	if err != nil {
		return DeletionResult{}, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.executed",
		EntityType:  "deletion_request",
		EntityID:    executed.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": request.PatientID.String()},
	}); err != nil {
		return DeletionResult{}, err
	}
	return DeletionResult{Request: executed, Register: entry}, nil
}

// ListRegister returns the append-only deletion evidence of the actor's tenant.
func (s *Service) ListRegister(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]RegisterEntry, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "deletion.register_list")
		return nil, ErrForbidden
	}
	entries, err := s.store.ListRegister(ctx, q, actor.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "deletion.register_listed",
		EntityType:  "deletion_register",
		RequestID:   requestID,
		Metadata:    map[string]any{"count": len(entries)},
	}); err != nil {
		return nil, err
	}
	return entries, nil
}

// ReviewCandidates runs the retention review for one tenant. It reports counts
// only. It never creates a deletion request and never deletes data.
func (s *Service) ReviewCandidates(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]Candidate, error) {
	if !isChef(actor) {
		s.recordDenied(ctx, q, actor, requestID, "retention.review")
		return nil, ErrForbidden
	}
	candidates, err := s.store.ReviewCandidatesForTenant(ctx, q, actor.TenantID)
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

// ReviewAllCandidates runs the cross-tenant review for the scheduled job. The
// database function writes one retention.reviewed audit event per tenant.
func (s *Service) ReviewAllCandidates(ctx context.Context, q *db.Queries) ([]ReviewCandidate, error) {
	return s.store.ReviewAllCandidates(ctx, q)
}

func isChef(actor Actor) bool {
	return actor.Role == "chef" && actor.TenantID != uuid.Nil
}

func holdScope(hold LegalHold) string {
	if hold.PatientID != nil {
		return "patient"
	}
	return "tenant"
}

func validatePolicy(input PolicyInput) (PolicyInput, error) {
	category := strings.TrimSpace(input.Category)
	trigger, ok := categoryTriggers[category]
	if !ok {
		return PolicyInput{}, fmt.Errorf("%w: unknown category %q", ErrInvalid, input.Category)
	}
	if input.TriggerEvent != "" && input.TriggerEvent != trigger {
		return PolicyInput{}, fmt.Errorf("%w: category %s requires trigger %s", ErrInvalid, category, trigger)
	}
	period, err := validatePeriod(input.PeriodMonths)
	if err != nil {
		return PolicyInput{}, err
	}
	note, err := validateNote(input.Note)
	if err != nil {
		return PolicyInput{}, err
	}
	return PolicyInput{
		Category:     category,
		TriggerEvent: trigger,
		PeriodMonths: period,
		Note:         note,
	}, nil
}

func validatePeriod(months int) (int, error) {
	if months < MinPeriodMonths || months > MaxPeriodMonths {
		return 0, fmt.Errorf("%w: period_months must be between %d and %d", ErrInvalid, MinPeriodMonths, MaxPeriodMonths)
	}
	return months, nil
}

func validateNote(value string) (string, error) {
	note := strings.TrimSpace(value)
	if len([]rune(note)) > maxNoteRunes {
		return "", fmt.Errorf("%w: note must be at most %d characters", ErrInvalid, maxNoteRunes)
	}
	return note, nil
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

func validateOptionalReason(value string) (string, error) {
	reason := strings.TrimSpace(value)
	if len([]rune(reason)) > maxReasonRunes {
		return "", fmt.Errorf("%w: reason must be at most %d characters", ErrInvalid, maxReasonRunes)
	}
	return reason, nil
}

func (s *Service) record(ctx context.Context, q *db.Queries, event audit.Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

// recordDenied writes a best-effort access.denied event. The handler commits
// the transaction for ErrForbidden so the row survives.
func (s *Service) recordDenied(ctx context.Context, q *db.Queries, actor Actor, requestID, attempted string) {
	if actor.TenantID == uuid.Nil {
		return
	}
	_ = s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "retention",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": attempted},
	})
}
