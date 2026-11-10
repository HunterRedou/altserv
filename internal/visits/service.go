package visits

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

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid visit input")
	ErrNotFound  = errors.New("visit not found")
	ErrConflict  = errors.New("visit conflict")
)

const (
	maxVisitsPerPatient      = 200
	maxCorrectionReasonRunes = 500
)

// Store is the persistence boundary used by the visit service.
type Store interface {
	PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error)
	Create(ctx context.Context, q *db.Queries, tenantID, createdBy uuid.UUID, input CreateInput) (Visit, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error)
	ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Visit, error)
	UpdateSchedule(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, employeeID uuid.UUID, plannedStart time.Time) (Visit, error)
	Start(ctx context.Context, q *db.Queries, tenantID, id, actualEmployeeID uuid.UUID) (Visit, error)
	Complete(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error)
	Cancel(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Visit, error)
	Correct(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, actualStart, actualEnd *time.Time) (Visit, error)
	GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error)
}

// AccessChecker answers whether an employee may access a patient right now. It
// is implemented by the assignment service so the rule lives in one place.
type AccessChecker interface {
	CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error)
}

// Service contains the visit business rules. A visit schedules a deployment;
// it does not contain a report or performed activities (doc 12).
type Service struct {
	store  Store
	audit  audit.Recorder
	access AccessChecker
	now    func() time.Time
}

func NewService(store Store, recorder audit.Recorder, access AccessChecker) *Service {
	return &Service{store: store, audit: recorder, access: access, now: time.Now}
}

// Create schedules a planned visit. Only a chef may plan a visit.
func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateInput) (Visit, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.create")
		return Visit{}, ErrForbidden
	}
	if input.PatientID == uuid.Nil {
		return Visit{}, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}
	if input.EmployeeID == uuid.Nil {
		return Visit{}, fmt.Errorf("%w: planned employee is required", ErrInvalid)
	}
	if input.PlannedStart.IsZero() {
		return Visit{}, fmt.Errorf("%w: planned start is required", ErrInvalid)
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, input.PatientID)
	if err != nil {
		return Visit{}, err
	}
	if !allowed {
		return Visit{}, ErrForbidden
	}

	exists, err := s.store.PatientExists(ctx, q, actor.TenantID, input.PatientID)
	if err != nil {
		return Visit{}, err
	}
	if !exists {
		return Visit{}, fmt.Errorf("%w: patient not found in this tenant", ErrInvalid)
	}

	if err := s.validatePlannedEmployee(ctx, q, actor.TenantID, input.PatientID, input.EmployeeID); err != nil {
		return Visit{}, err
	}

	visit, err := s.store.Create(ctx, q, actor.TenantID, actor.UserID, input)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.created",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":    visit.PatientID,
			"employee_id":   visit.EmployeeID,
			"planned_start": visit.PlannedStart.UTC().Format(time.RFC3339),
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

// Get returns one visit. Employees may only see visits for assigned patients.
func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Visit, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.read")
		return Visit{}, ErrForbidden
	}

	visit, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, visit.PatientID)
	if err != nil {
		return Visit{}, err
	}
	if !allowed {
		// Hide the visit's existence from an unassigned employee.
		return Visit{}, ErrNotFound
	}
	return visit, nil
}

// ListByPatient returns the visits for a patient, newest planned start first.
func (s *Service) ListByPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) ([]Visit, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.list")
		return nil, ErrForbidden
	}
	if patientID == uuid.Nil {
		return nil, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, patientID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrNotFound
	}

	return s.store.ListByPatient(ctx, q, actor.TenantID, patientID, maxVisitsPerPatient)
}

// Reschedule changes the planned employee and/or planned start of a visit that
// has not started yet. Only a chef may reschedule.
func (s *Service) Reschedule(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input ScheduleInput) (Visit, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.reschedule")
		return Visit{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}
	if current.Status != "planned" {
		return Visit{}, fmt.Errorf("%w: only a planned visit can be rescheduled", ErrConflict)
	}

	employeeID := current.EmployeeID
	if input.EmployeeID != nil {
		employeeID = *input.EmployeeID
	}
	plannedStart := current.PlannedStart
	if input.PlannedStart != nil {
		plannedStart = *input.PlannedStart
	}
	if employeeID == uuid.Nil {
		return Visit{}, fmt.Errorf("%w: planned employee is required", ErrInvalid)
	}
	if plannedStart.IsZero() {
		return Visit{}, fmt.Errorf("%w: planned start is required", ErrInvalid)
	}
	if err := s.validatePlannedEmployee(ctx, q, actor.TenantID, current.PatientID, employeeID); err != nil {
		return Visit{}, err
	}

	visit, err := s.store.UpdateSchedule(ctx, q, actor.TenantID, id, employeeID, plannedStart)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.rescheduled",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"previous_employee_id":   current.EmployeeID,
			"employee_id":            visit.EmployeeID,
			"previous_planned_start": current.PlannedStart.UTC().Format(time.RFC3339),
			"planned_start":          visit.PlannedStart.UTC().Format(time.RFC3339),
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

// Start begins a planned visit and records the employee who actually performs
// it. An assigned employee may take over a visit planned for someone else; that
// substitution is recorded in the visit and in the audit log.
func (s *Service) Start(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Visit, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.start")
		return Visit{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}
	if current.Status != "planned" {
		return Visit{}, fmt.Errorf("%w: only a planned visit can be started", ErrConflict)
	}

	if actor.Role != "chef" {
		allowed, err := s.canAccessPatient(ctx, q, actor, requestID, current.PatientID)
		if err != nil {
			return Visit{}, err
		}
		if !allowed {
			return Visit{}, ErrForbidden
		}
	}

	visit, err := s.store.Start(ctx, q, actor.TenantID, id, actor.UserID)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.started",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":          current.PatientID,
			"planned_employee_id": current.EmployeeID,
			"actual_employee_id":  actor.UserID,
			"substitution":        current.EmployeeID != actor.UserID,
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

// Complete ends an in-progress visit. The actual employee or a chef may do it.
func (s *Service) Complete(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Visit, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.complete")
		return Visit{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}
	if current.Status != "in_progress" {
		return Visit{}, fmt.Errorf("%w: only an in-progress visit can be completed", ErrConflict)
	}

	if actor.Role != "chef" {
		if current.ActualEmployeeID == nil || *current.ActualEmployeeID != actor.UserID {
			s.recordDenied(ctx, q, actor, requestID, "visit.complete")
			return Visit{}, ErrForbidden
		}
		allowed, err := s.canAccessPatient(ctx, q, actor, requestID, current.PatientID)
		if err != nil {
			return Visit{}, err
		}
		if !allowed {
			return Visit{}, ErrForbidden
		}
	}

	visit, err := s.store.Complete(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.completed",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":   visit.PatientID,
			"actual_start": timeOrNil(visit.ActualStart),
			"actual_end":   timeOrNil(visit.ActualEnd),
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

// Cancel cancels a planned or in-progress visit. Only a chef may cancel.
func (s *Service) Cancel(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Visit, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.cancel")
		return Visit{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}
	if current.Status != "planned" && current.Status != "in_progress" {
		return Visit{}, fmt.Errorf("%w: only a planned or in-progress visit can be cancelled", ErrConflict)
	}

	visit, err := s.store.Cancel(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.cancelled",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id": visit.PatientID,
			"status":     visit.Status,
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

// Correct changes the actual times of a started visit. A reason is required so
// every correction is traceable. Only a chef may correct.
func (s *Service) Correct(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input CorrectInput) (Visit, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "visit.correct")
		return Visit{}, ErrForbidden
	}

	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return Visit{}, fmt.Errorf("%w: a correction reason is required", ErrInvalid)
	}
	if len([]rune(reason)) > maxCorrectionReasonRunes {
		return Visit{}, fmt.Errorf("%w: the correction reason is too long", ErrInvalid)
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Visit{}, err
	}
	if current.Status != "in_progress" && current.Status != "completed" {
		return Visit{}, fmt.Errorf("%w: only a started visit can be corrected", ErrConflict)
	}

	actualStart := current.ActualStart
	if input.ActualStart != nil {
		actualStart = input.ActualStart
	}
	actualEnd := current.ActualEnd
	if input.ActualEnd != nil {
		actualEnd = input.ActualEnd
	}
	if actualStart == nil {
		return Visit{}, fmt.Errorf("%w: actual start is required", ErrInvalid)
	}
	if current.Status == "in_progress" {
		if actualEnd != nil {
			return Visit{}, fmt.Errorf("%w: complete the visit instead of setting an end time", ErrInvalid)
		}
	} else if actualEnd == nil {
		return Visit{}, fmt.Errorf("%w: a completed visit requires an end time", ErrInvalid)
	}
	if actualEnd != nil && actualEnd.Before(*actualStart) {
		return Visit{}, fmt.Errorf("%w: actual end must not be before actual start", ErrInvalid)
	}
	now := s.now()
	if actualStart.After(now) {
		return Visit{}, fmt.Errorf("%w: actual start must not be in the future", ErrInvalid)
	}
	if actualEnd != nil && actualEnd.After(now) {
		return Visit{}, fmt.Errorf("%w: actual end must not be in the future", ErrInvalid)
	}

	visit, err := s.store.Correct(ctx, q, actor.TenantID, id, actualStart, actualEnd)
	if err != nil {
		return Visit{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "visit.corrected",
		EntityType:  "visit",
		EntityID:    visit.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":            visit.PatientID,
			"reason":                reason,
			"previous_actual_start": timeOrNil(current.ActualStart),
			"previous_actual_end":   timeOrNil(current.ActualEnd),
			"actual_start":          timeOrNil(visit.ActualStart),
			"actual_end":            timeOrNil(visit.ActualEnd),
		},
	}); err != nil {
		return Visit{}, err
	}
	return visit, nil
}

func (s *Service) validatePlannedEmployee(ctx context.Context, q *db.Queries, tenantID, patientID, employeeID uuid.UUID) error {
	role, err := s.store.GetUserRole(ctx, q, tenantID, employeeID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: planned employee is not a member of this tenant", ErrInvalid)
	}
	if err != nil {
		return err
	}

	switch role {
	case "chef":
		return nil
	case "employee":
		if s.access == nil {
			return fmt.Errorf("%w: planned employee is not assigned to this patient", ErrInvalid)
		}
		allowed, err := s.access.CanAccessPatient(ctx, q, tenantID, employeeID, patientID)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: planned employee is not assigned to this patient", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: planned employee must be a care worker", ErrInvalid)
	}
}

func (s *Service) canAccessPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) (bool, error) {
	if actor.Role == "chef" {
		return true, nil
	}
	if s.access == nil {
		s.recordDenied(ctx, q, actor, requestID, "visit.access")
		return false, nil
	}
	allowed, err := s.access.CanAccessPatient(ctx, q, actor.TenantID, actor.UserID, patientID)
	if err != nil {
		return false, err
	}
	if !allowed {
		s.recordDenied(ctx, q, actor, requestID, "visit.access")
	}
	return allowed, nil
}

// canRead allows the two tenant roles that deliver care. A platform admin gets
// no automatic tenant data access (doc 4).
func canRead(role string) bool {
	return role == "chef" || role == "employee"
}

// canManage allows only a chef to plan, reschedule, cancel or correct visits.
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
		EntityType:  "visit",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}

func timeOrNil(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
