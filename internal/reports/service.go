package reports

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid report input")
	ErrNotFound  = errors.New("report not found")
	ErrConflict  = errors.New("report conflict")
)

const (
	maxReportsPerPatient = 200
	maxReportActivities  = 200
	maxReturnReasonRunes = 500
	maxActivityQuantity  = 10000
)

// Store is the persistence boundary used by the report service.
type Store interface {
	GetVisit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Visit, error)
	Create(ctx context.Context, q *db.Queries, tenantID, createdBy, employeeID uuid.UUID, visit db.Visit, input CreateInput) (Report, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Report, error)
	ListByVisit(ctx context.Context, q *db.Queries, tenantID, visitID uuid.UUID) ([]Report, error)
	ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Report, error)
	UpdateDraft(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int, note *string) (Report, error)
	Submit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error)
	Approve(ctx context.Context, q *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (Report, error)
	Return(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error)
	MarkReadyForBilling(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error)
	GetActivity(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Activity, error)
	GetActivityAttachment(ctx context.Context, q *db.Queries, tenantID, activityID uuid.UUID) (uuid.UUID, error)
	ReplaceActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID, activities []storedActivity) ([]ReportActivity, error)
	ListActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportActivity, error)
	CreateTransition(ctx context.Context, q *db.Queries, tenantID, reportID, actorID uuid.UUID, fromStatus, toStatus, kind, reason string) (ReportTransition, error)
	ListTransitions(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportTransition, error)
}

// AccessChecker answers whether an employee may access a patient right now. It
// is implemented by the assignment service so the rule lives in one place.
type AccessChecker interface {
	CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error)
}

// Service contains the report and approval rules (doc 13).
type Service struct {
	store  Store
	audit  audit.Recorder
	access AccessChecker
}

func NewService(store Store, recorder audit.Recorder, access AccessChecker) *Service {
	return &Service{store: store, audit: recorder, access: access}
}

// Create creates a draft report for a completed visit. The report, its
// activities and the audit event are written in the caller's transaction.
func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, visitID uuid.UUID, input CreateInput) (Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.create")
		return Report{}, ErrForbidden
	}
	if visitID == uuid.Nil {
		return Report{}, fmt.Errorf("%w: visit id is required", ErrInvalid)
	}

	visit, err := s.store.GetVisit(ctx, q, actor.TenantID, visitID)
	if err != nil {
		return Report{}, err
	}
	if visit.Status != "completed" {
		return Report{}, fmt.Errorf("%w: only a completed visit can be reported", ErrConflict)
	}

	allowed, err := s.canWriteVisit(ctx, q, actor, requestID, visit)
	if err != nil {
		return Report{}, err
	}
	if !allowed {
		return Report{}, ErrForbidden
	}

	existing, err := s.store.ListByVisit(ctx, q, actor.TenantID, visit.ID)
	if err != nil {
		return Report{}, err
	}
	if len(existing) > 0 {
		return Report{}, fmt.Errorf("%w: this visit already has a report", ErrConflict)
	}

	activities, err := s.resolveActivities(ctx, q, actor.TenantID, visit.PatientID, uuid.Nil, input.Activities, false)
	if err != nil {
		return Report{}, err
	}

	employeeID := visit.EmployeeID
	if visit.ActualEmployeeID.Valid {
		employeeID = visit.ActualEmployeeID.UUID
	}

	report, err := s.store.Create(ctx, q, actor.TenantID, actor.UserID, employeeID, visit, input)
	if err != nil {
		if isUniqueViolation(err) {
			return Report{}, fmt.Errorf("%w: this visit already has a report", ErrConflict)
		}
		return Report{}, err
	}

	report.Activities, err = s.store.ReplaceActivities(ctx, q, actor.TenantID, report.ID, activities)
	if err != nil {
		if isUniqueViolation(err) {
			return Report{}, fmt.Errorf("%w: an activity is already attached to another report", ErrConflict)
		}
		if isForeignKeyViolation(err) {
			return Report{}, fmt.Errorf("%w: an approved billing run references this report; void the billing run first", ErrConflict)
		}
		return Report{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.created",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"visit_id":       report.VisitID,
			"patient_id":     report.PatientID,
			"employee_id":    report.EmployeeID,
			"activity_count": len(report.Activities),
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// ListByVisit returns the reports for one visit. There is at most one.
func (s *Service) ListByVisit(ctx context.Context, q *db.Queries, actor Actor, requestID string, visitID uuid.UUID) ([]Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.list")
		return nil, ErrForbidden
	}

	visit, err := s.store.GetVisit(ctx, q, actor.TenantID, visitID)
	if err != nil {
		return nil, err
	}
	allowed, err := s.canReadPatient(ctx, q, actor, requestID, visit.PatientID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrNotFound
	}
	return s.store.ListByVisit(ctx, q, actor.TenantID, visitID)
}

// ListByPatient returns the reports for a patient, newest first.
func (s *Service) ListByPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) ([]Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.list")
		return nil, ErrForbidden
	}
	if patientID == uuid.Nil {
		return nil, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}

	allowed, err := s.canReadPatient(ctx, q, actor, requestID, patientID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrNotFound
	}
	return s.store.ListByPatient(ctx, q, actor.TenantID, patientID, maxReportsPerPatient)
}

// Get returns one report with its activities. An unassigned employee cannot
// see that the report exists.
func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.read")
		return Report{}, ErrForbidden
	}

	report, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	allowed, err := s.canReadPatient(ctx, q, actor, requestID, report.PatientID)
	if err != nil {
		return Report{}, err
	}
	if !allowed {
		return Report{}, ErrNotFound
	}

	report.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, report.ID)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

// Update edits a draft report. After submission there is no silent change: the
// caller must return the report to draft first.
func (s *Service) Update(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input UpdateInput) (Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.update")
		return Report{}, ErrForbidden
	}
	if input.Note == nil && input.Activities == nil {
		return Report{}, fmt.Errorf("%w: nothing to update", ErrInvalid)
	}
	if input.Version < 1 {
		return Report{}, fmt.Errorf("%w: version is required", ErrInvalid)
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	if current.Status != "draft" {
		return Report{}, fmt.Errorf("%w: only a draft report can be edited; return it first", ErrConflict)
	}
	allowed, err := s.canWriteReport(ctx, q, actor, requestID, current)
	if err != nil {
		return Report{}, err
	}
	if !allowed {
		return Report{}, ErrForbidden
	}
	if current.Version != input.Version {
		return Report{}, ErrConflict
	}

	var activities []storedActivity
	if input.Activities != nil {
		activities, err = s.resolveActivities(ctx, q, actor.TenantID, current.PatientID, current.ID, *input.Activities, false)
		if err != nil {
			return Report{}, err
		}
	}

	report, err := s.store.UpdateDraft(ctx, q, actor.TenantID, id, input.Version, input.Note)
	if err != nil {
		return Report{}, err
	}
	if input.Activities != nil {
		report.Activities, err = s.store.ReplaceActivities(ctx, q, actor.TenantID, report.ID, activities)
		if err != nil {
			if isUniqueViolation(err) {
				return Report{}, fmt.Errorf("%w: an activity is already attached to another report", ErrConflict)
			}
			if isForeignKeyViolation(err) {
				return Report{}, fmt.Errorf("%w: an approved billing run references this report; void the billing run first", ErrConflict)
			}
			return Report{}, err
		}
	} else {
		report.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, report.ID)
		if err != nil {
			return Report{}, err
		}
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.updated",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":     report.PatientID,
			"version":        report.Version,
			"note_changed":   input.Note != nil,
			"activity_count": len(report.Activities),
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// Submit moves a draft report to submitted. It requires at least one performed
// activity so an empty report cannot enter the approval flow.
func (s *Service) Submit(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, version int) (Report, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.submit")
		return Report{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	if current.Status != "draft" {
		return Report{}, fmt.Errorf("%w: only a draft report can be submitted", ErrConflict)
	}
	allowed, err := s.canWriteReport(ctx, q, actor, requestID, current)
	if err != nil {
		return Report{}, err
	}
	if !allowed {
		return Report{}, ErrForbidden
	}
	if current.Version != version {
		return Report{}, ErrConflict
	}

	activities, err := s.store.ListActivities(ctx, q, actor.TenantID, current.ID)
	if err != nil {
		return Report{}, err
	}
	if len(activities) == 0 {
		return Report{}, fmt.Errorf("%w: a report needs at least one activity", ErrInvalid)
	}

	report, err := s.store.Submit(ctx, q, actor.TenantID, id, version)
	if err != nil {
		return Report{}, err
	}
	report.Activities = activities

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.submitted",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":     report.PatientID,
			"version":        report.Version,
			"activity_count": len(activities),
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// Approve approves a submitted report. Only a chef may approve.
func (s *Service) Approve(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, version int) (Report, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.approve")
		return Report{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	if current.Status != "submitted" {
		return Report{}, fmt.Errorf("%w: only a submitted report can be approved", ErrConflict)
	}
	if current.Version != version {
		return Report{}, ErrConflict
	}

	report, err := s.store.Approve(ctx, q, actor.TenantID, id, actor.UserID, version)
	if err != nil {
		return Report{}, err
	}
	report.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, report.ID)
	if err != nil {
		return Report{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.approved",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id": report.PatientID,
			"version":    report.Version,
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// Return moves a submitted or approved report back to draft. A reason is
// required and is stored in a separate transition row. Only a chef may return.
func (s *Service) Return(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input ReturnInput) (Report, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.return")
		return Report{}, ErrForbidden
	}

	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return Report{}, fmt.Errorf("%w: a return reason is required", ErrInvalid)
	}
	if len([]rune(reason)) > maxReturnReasonRunes {
		return Report{}, fmt.Errorf("%w: the return reason is too long", ErrInvalid)
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	if current.Status != "submitted" && current.Status != "approved" {
		return Report{}, fmt.Errorf("%w: only a submitted or approved report can be returned", ErrConflict)
	}
	if current.Version != input.Version {
		return Report{}, ErrConflict
	}

	report, err := s.store.Return(ctx, q, actor.TenantID, id, input.Version)
	if err != nil {
		return Report{}, err
	}

	transition, err := s.store.CreateTransition(ctx, q, actor.TenantID, report.ID, actor.UserID, current.Status, report.Status, transitionKind(current.Status), reason)
	if err != nil {
		return Report{}, err
	}

	report.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, report.ID)
	if err != nil {
		return Report{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.returned",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":    report.PatientID,
			"from_status":   transition.FromStatus,
			"to_status":     transition.ToStatus,
			"kind":          transition.Kind,
			"transition_id": transition.ID,
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// ReadyForBilling marks an approved report as ready for billing. Only a chef
// may do it.
func (s *Service) ReadyForBilling(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, version int) (Report, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.ready_for_billing")
		return Report{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Report{}, err
	}
	if current.Status != "approved" {
		return Report{}, fmt.Errorf("%w: only an approved report can be marked ready for billing", ErrConflict)
	}
	if current.Version != version {
		return Report{}, ErrConflict
	}

	report, err := s.store.MarkReadyForBilling(ctx, q, actor.TenantID, id, version)
	if err != nil {
		return Report{}, err
	}
	report.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, report.ID)
	if err != nil {
		return Report{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "report.ready_for_billing",
		EntityType:  "report",
		EntityID:    report.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id": report.PatientID,
			"version":    report.Version,
		},
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

// ListTransitions returns the returns and corrections of a report.
func (s *Service) ListTransitions(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) ([]ReportTransition, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "report.read")
		return nil, ErrForbidden
	}

	report, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return nil, err
	}
	allowed, err := s.canReadPatient(ctx, q, actor, requestID, report.PatientID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrNotFound
	}
	return s.store.ListTransitions(ctx, q, actor.TenantID, report.ID)
}

// resolveActivities validates each performed activity against the patient and
// the report. required rejects an empty list.
func (s *Service) resolveActivities(ctx context.Context, q *db.Queries, tenantID, patientID, reportID uuid.UUID, inputs []ActivityInput, required bool) ([]storedActivity, error) {
	if len(inputs) == 0 {
		if required {
			return nil, fmt.Errorf("%w: a report needs at least one activity", ErrInvalid)
		}
		return nil, nil
	}
	if len(inputs) > maxReportActivities {
		return nil, fmt.Errorf("%w: too many activities", ErrInvalid)
	}

	seen := make(map[uuid.UUID]struct{}, len(inputs))
	result := make([]storedActivity, 0, len(inputs))
	for _, input := range inputs {
		if input.ActivityID == uuid.Nil {
			return nil, fmt.Errorf("%w: activity id is required", ErrInvalid)
		}
		if input.Quantity < 1 || input.Quantity > maxActivityQuantity {
			return nil, fmt.Errorf("%w: quantity must be between 1 and %d", ErrInvalid, maxActivityQuantity)
		}
		if _, duplicate := seen[input.ActivityID]; duplicate {
			return nil, fmt.Errorf("%w: an activity is listed twice", ErrInvalid)
		}
		seen[input.ActivityID] = struct{}{}

		activity, err := s.store.GetActivity(ctx, q, tenantID, input.ActivityID)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: activity not found in this tenant", ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		if activity.PatientID != patientID {
			return nil, fmt.Errorf("%w: activity belongs to another patient", ErrInvalid)
		}
		if activity.Status != "recorded" {
			return nil, fmt.Errorf("%w: only a recorded activity can be reported", ErrInvalid)
		}

		attachment, err := s.store.GetActivityAttachment(ctx, q, tenantID, input.ActivityID)
		if err == nil && attachment != reportID {
			return nil, fmt.Errorf("%w: an activity is already attached to another report", ErrConflict)
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}

		result = append(result, storedActivity{
			ActivityID:  activity.ID,
			Quantity:    input.Quantity,
			PerformedAt: activity.PerformedAt,
		})
	}
	return result, nil
}

func (s *Service) canWriteVisit(ctx context.Context, q *db.Queries, actor Actor, requestID string, visit db.Visit) (bool, error) {
	if actor.Role == "chef" {
		return true, nil
	}
	employeeID := visit.EmployeeID
	if visit.ActualEmployeeID.Valid {
		employeeID = visit.ActualEmployeeID.UUID
	}
	if employeeID != actor.UserID {
		s.recordDenied(ctx, q, actor, requestID, "report.create")
		return false, nil
	}
	return s.canReadPatient(ctx, q, actor, requestID, visit.PatientID)
}

func (s *Service) canWriteReport(ctx context.Context, q *db.Queries, actor Actor, requestID string, report Report) (bool, error) {
	if actor.Role == "chef" {
		return true, nil
	}
	if report.EmployeeID != actor.UserID {
		s.recordDenied(ctx, q, actor, requestID, "report.write")
		return false, nil
	}
	return s.canReadPatient(ctx, q, actor, requestID, report.PatientID)
}

func (s *Service) canReadPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) (bool, error) {
	if actor.Role == "chef" {
		return true, nil
	}
	if s.access == nil {
		s.recordDenied(ctx, q, actor, requestID, "report.access")
		return false, nil
	}
	allowed, err := s.access.CanAccessPatient(ctx, q, actor.TenantID, actor.UserID, patientID)
	if err != nil {
		return false, err
	}
	if !allowed {
		s.recordDenied(ctx, q, actor, requestID, "report.access")
	}
	return allowed, nil
}

// transitionKind names why a report was reopened: a submitted report is
// returned, an approved report is corrected.
func transitionKind(fromStatus string) string {
	if fromStatus == "approved" {
		return "correction"
	}
	return "return"
}

// canRead allows the two tenant roles that deliver care. A platform admin gets
// no automatic tenant data access (doc 4).
func canRead(role string) bool {
	return role == "chef" || role == "employee"
}

// canManage allows only a chef to approve, return or release a report.
func canManage(role string) bool {
	return role == "chef"
}

func (s *Service) record(ctx context.Context, q *db.Queries, event audit.Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

func (s *Service) recordDenied(ctx context.Context, q *db.Queries, actor Actor, requestID, action string) {
	if actor.TenantID == uuid.Nil {
		return
	}
	// Best effort: a denied request must not turn into a 500.
	_ = s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "report",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}

// isUniqueViolation detects a PostgreSQL unique constraint violation. It is
// used to turn a concurrent duplicate into a conflict instead of a 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation detects a PostgreSQL foreign key violation. A report
// activity that is still referenced by an approved billing run cannot be
// replaced until the run is voided.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
