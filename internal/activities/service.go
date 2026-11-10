package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid activity input")
	ErrNotFound  = errors.New("activity not found")
)

const (
	// maxActivityResults bounds a list response until paging exists.
	maxActivityResults = 100
	maxNoteRunes       = 5000
	maxFutureSkew      = 5 * time.Minute
)

// Store is the persistence boundary used by the activity service.
type Store interface {
	Create(ctx context.Context, q *db.Queries, tenantID, createdBy uuid.UUID, input CreateInput, snapshot Snapshot) (Activity, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Activity, error)
	ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Activity, error)
	List(ctx context.Context, q *db.Queries, tenantID uuid.UUID, limit int) ([]Activity, error)
	GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error)
}

// AccessChecker answers whether an employee may access a patient right now. It
// is implemented by the assignment service so the rule lives in one place.
type AccessChecker interface {
	CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error)
}

// CatalogReader resolves the global service definition and the tenant contract
// that an activity references.
type CatalogReader interface {
	GetDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (catalog.Definition, error)
	GetContract(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (catalog.Contract, error)
}

// Service contains the activity business rules. It never trusts the client for
// the billable service snapshot: the definition and contract are re-read inside
// the same transaction.
type Service struct {
	store   Store
	catalog CatalogReader
	audit   audit.Recorder
	access  AccessChecker
	now     func() time.Time
}

func NewService(store Store, catalogReader CatalogReader, recorder audit.Recorder, access AccessChecker) *Service {
	return &Service{store: store, catalog: catalogReader, audit: recorder, access: access, now: time.Now}
}

func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateInput) (Activity, error) {
	switch actor.Role {
	case "chef", "employee":
	default:
		s.recordDenied(ctx, q, actor, requestID, "activity.create")
		return Activity{}, ErrForbidden
	}

	normalized, err := validateInput(input, s.now())
	if err != nil {
		return Activity{}, err
	}

	if actor.Role == "employee" {
		// An employee records their own work only, and only for a patient they
		// are currently assigned to.
		if normalized.EmployeeID != uuid.Nil && normalized.EmployeeID != actor.UserID {
			s.recordDenied(ctx, q, actor, requestID, "activity.create")
			return Activity{}, ErrForbidden
		}
		normalized.EmployeeID = actor.UserID

		allowed, err := s.employeeCanAccess(ctx, q, actor, requestID, normalized.PatientID)
		if err != nil {
			return Activity{}, err
		}
		if !allowed {
			return Activity{}, ErrForbidden
		}
	} else {
		if normalized.EmployeeID == uuid.Nil {
			return Activity{}, fmt.Errorf("%w: employee id is required", ErrInvalid)
		}
		role, err := s.store.GetUserRole(ctx, q, actor.TenantID, normalized.EmployeeID)
		if errors.Is(err, ErrNotFound) {
			return Activity{}, fmt.Errorf("%w: employee not found in this tenant", ErrInvalid)
		}
		if err != nil {
			return Activity{}, err
		}
		if role != "employee" {
			return Activity{}, fmt.Errorf("%w: employee_id must reference an employee", ErrInvalid)
		}
	}

	snapshot, err := s.billableSnapshot(ctx, q, actor.TenantID, normalized)
	if err != nil {
		return Activity{}, err
	}

	activity, err := s.store.Create(ctx, q, actor.TenantID, actor.UserID, normalized, snapshot)
	if err != nil {
		return Activity{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "activity.created",
		EntityType:  "activity",
		EntityID:    activity.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":       activity.PatientID,
			"employee_id":      activity.EmployeeID,
			"duration_minutes": activity.DurationMinutes,
			"billable":         activity.ServiceDefinitionID != nil,
			"legal_area":       activity.LegalArea,
			"has_note":         activity.Note != "",
		},
	}); err != nil {
		return Activity{}, err
	}
	return activity, nil
}

func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Activity, error) {
	switch actor.Role {
	case "chef":
	case "employee":
	default:
		s.recordDenied(ctx, q, actor, requestID, "activity.read")
		return Activity{}, ErrForbidden
	}

	activity, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Activity{}, err
	}

	if actor.Role == "employee" {
		allowed, err := s.employeeCanAccess(ctx, q, actor, requestID, activity.PatientID)
		if err != nil {
			return Activity{}, err
		}
		if !allowed {
			// Report as not found so an employee cannot probe patient activity.
			return Activity{}, ErrNotFound
		}
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "activity.read",
		EntityType:  "activity",
		EntityID:    activity.ID,
		RequestID:   requestID,
	}); err != nil {
		return Activity{}, err
	}
	return activity, nil
}

func (s *Service) ListByPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) ([]Activity, error) {
	switch actor.Role {
	case "chef":
	case "employee":
		allowed, err := s.employeeCanAccess(ctx, q, actor, requestID, patientID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrNotFound
		}
	default:
		s.recordDenied(ctx, q, actor, requestID, "activity.list")
		return nil, ErrForbidden
	}

	activities, err := s.store.ListByPatient(ctx, q, actor.TenantID, patientID, maxActivityResults)
	if err != nil {
		return nil, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "activity.listed",
		EntityType:  "activity",
		RequestID:   requestID,
		Metadata:    map[string]any{"patient_id": patientID, "results": len(activities)},
	}); err != nil {
		return nil, err
	}
	return activities, nil
}

// List returns every activity in the tenant. Only a chef may read across
// patients; employees use the per-patient endpoint.
func (s *Service) List(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]Activity, error) {
	if actor.Role != "chef" {
		s.recordDenied(ctx, q, actor, requestID, "activity.list")
		return nil, ErrForbidden
	}

	activities, err := s.store.List(ctx, q, actor.TenantID, maxActivityResults)
	if err != nil {
		return nil, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "activity.listed",
		EntityType:  "activity",
		RequestID:   requestID,
		Metadata:    map[string]any{"results": len(activities)},
	}); err != nil {
		return nil, err
	}
	return activities, nil
}

func (s *Service) billableSnapshot(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateInput) (Snapshot, error) {
	if input.ServiceDefinitionID == nil && input.ContractID == nil {
		return Snapshot{}, nil
	}
	if input.ServiceDefinitionID == nil || input.ContractID == nil {
		return Snapshot{}, fmt.Errorf("%w: service definition and contract must be set together", ErrInvalid)
	}

	definition, err := s.catalog.GetDefinition(ctx, q, *input.ServiceDefinitionID)
	if errors.Is(err, catalog.ErrNotFound) {
		return Snapshot{}, fmt.Errorf("%w: service definition not found", ErrInvalid)
	}
	if err != nil {
		return Snapshot{}, err
	}

	contract, err := s.catalog.GetContract(ctx, q, tenantID, *input.ContractID)
	if errors.Is(err, catalog.ErrNotFound) {
		return Snapshot{}, fmt.Errorf("%w: contract not found in this tenant", ErrInvalid)
	}
	if err != nil {
		return Snapshot{}, err
	}

	if contract.ServiceDefinitionID != definition.ID {
		return Snapshot{}, fmt.Errorf("%w: contract does not cover this service definition", ErrInvalid)
	}
	if contract.LegalArea != definition.LegalArea {
		return Snapshot{}, fmt.Errorf("%w: contract and service definition legal areas differ", ErrInvalid)
	}
	if !validAt(definition.ValidFrom, definition.ValidUntil, input.PerformedAt) {
		return Snapshot{}, fmt.Errorf("%w: service definition was not valid when the activity happened", ErrInvalid)
	}
	if !validAt(contract.ValidFrom, contract.ValidUntil, input.PerformedAt) {
		return Snapshot{}, fmt.Errorf("%w: contract was not valid when the activity happened", ErrInvalid)
	}

	return Snapshot{
		ServiceCode:       definition.Code,
		ServiceName:       definition.Name,
		LegalArea:         definition.LegalArea,
		ContractReference: contract.ContractReference,
		PayerName:         contract.PayerName,
	}, nil
}

func (s *Service) employeeCanAccess(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) (bool, error) {
	if s.access == nil {
		s.recordDenied(ctx, q, actor, requestID, "activity.read")
		return false, nil
	}
	allowed, err := s.access.CanAccessPatient(ctx, q, actor.TenantID, actor.UserID, patientID)
	if err != nil {
		return false, err
	}
	if !allowed {
		s.recordDenied(ctx, q, actor, requestID, "activity.read")
	}
	return allowed, nil
}

func validateInput(input CreateInput, now time.Time) (CreateInput, error) {
	input.Note = strings.TrimSpace(input.Note)

	if input.PatientID == uuid.Nil {
		return CreateInput{}, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}
	if input.PerformedAt.IsZero() {
		return CreateInput{}, fmt.Errorf("%w: performed_at is required", ErrInvalid)
	}
	if input.PerformedAt.After(now.Add(maxFutureSkew)) {
		return CreateInput{}, fmt.Errorf("%w: performed_at is in the future", ErrInvalid)
	}
	if input.DurationMinutes <= 0 || input.DurationMinutes > 1440 {
		return CreateInput{}, fmt.Errorf("%w: duration_minutes must be between 1 and 1440", ErrInvalid)
	}
	if len([]rune(input.Note)) > maxNoteRunes {
		return CreateInput{}, fmt.Errorf("%w: note is too long", ErrInvalid)
	}

	return input, nil
}

// validAt reports whether [from, until) contains at.
func validAt(from time.Time, until *time.Time, at time.Time) bool {
	if at.Before(from) {
		return false
	}
	return until == nil || at.Before(*until)
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
		EntityType:  "activity",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}
