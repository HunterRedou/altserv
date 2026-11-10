package patients

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
	ErrInvalid   = errors.New("invalid patient input")
	ErrNotFound  = errors.New("patient not found")
)

// Store is the persistence boundary used by the service.
type Store interface {
	Create(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateInput) (Patient, error)
	Update(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, input CreateInput) (Patient, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Patient, error)
	List(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Patient, error)
	ListForEmployee(ctx context.Context, q *db.Queries, tenantID, employeeID uuid.UUID) ([]Patient, error)
	ReplaceSearchTokens(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, tokens []SearchToken) error
	Search(ctx context.Context, q *db.Queries, tenantID uuid.UUID, employeeID *uuid.UUID, tokenHashes []string, limit int) ([]Patient, error)
}

// AccessChecker answers the object-level authorization question: may this
// employee access this patient right now? It is implemented by the assignment
// service so the rule lives in one place (doc 4/5).
type AccessChecker interface {
	CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error)
}

// Service contains patient business rules. Database work runs in the caller's
// tenant-scoped transaction so the change and its audit event commit together.
type Service struct {
	store  Store
	audit  audit.Recorder
	access AccessChecker
	search *SearchIndexer
	now    func() time.Time
}

func NewService(store Store, recorder audit.Recorder) *Service {
	return NewServiceWithAccess(store, recorder, nil)
}

func NewServiceWithAccess(store Store, recorder audit.Recorder, access AccessChecker) *Service {
	return &Service{store: store, audit: recorder, access: access, now: time.Now}
}

// NewServiceWithSearch adds the protected search index. Search is optional so
// existing callers and tests that only exercise CRUD keep working.
func NewServiceWithSearch(store Store, recorder audit.Recorder, access AccessChecker, search *SearchIndexer) *Service {
	service := NewServiceWithAccess(store, recorder, access)
	service.search = search
	return service
}

func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateInput) (Patient, error) {
	if !canManagePatients(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "patient.create")
		return Patient{}, ErrForbidden
	}

	normalized, err := validateInput(input, s.now())
	if err != nil {
		return Patient{}, err
	}

	patient, err := s.store.Create(ctx, q, actor.TenantID, normalized)
	if err != nil {
		return Patient{}, err
	}
	if err := s.replaceSearchTokens(ctx, q, actor.TenantID, patient); err != nil {
		return Patient{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "patient.created",
		EntityType:  "patient",
		EntityID:    patient.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"status": patient.Status},
	}); err != nil {
		return Patient{}, err
	}
	return patient, nil
}

func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Patient, error) {
	switch actor.Role {
	case "chef":
		// Chefs see every patient in their own tenant.
	case "employee":
		allowed, err := s.employeeCanAccess(ctx, q, actor, requestID, id)
		if err != nil {
			return Patient{}, err
		}
		if !allowed {
			return Patient{}, ErrNotFound
		}
	default:
		// platform_admin and unknown roles get no automatic patient access.
		s.recordDenied(ctx, q, actor, requestID, "patient.read")
		return Patient{}, ErrForbidden
	}

	patient, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Patient{}, err
	}

	// Archived patients are part of the chef's records, but employees lose
	// access to them even if an old assignment is still active.
	if actor.Role == "employee" && patient.Status == "archived" {
		s.recordDenied(ctx, q, actor, requestID, "patient.read")
		return Patient{}, ErrNotFound
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "patient.read",
		EntityType:  "patient",
		EntityID:    patient.ID,
		RequestID:   requestID,
	}); err != nil {
		return Patient{}, err
	}
	return patient, nil
}

func (s *Service) List(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]Patient, error) {
	switch actor.Role {
	case "chef":
		return s.store.List(ctx, q, actor.TenantID)
	case "employee":
		if s.access == nil {
			s.recordDenied(ctx, q, actor, requestID, "patient.list")
			return nil, ErrForbidden
		}
		// The query only returns patients with a currently valid, non-revoked
		// assignment for this employee.
		return s.store.ListForEmployee(ctx, q, actor.TenantID, actor.UserID)
	default:
		s.recordDenied(ctx, q, actor, requestID, "patient.list")
		return nil, ErrForbidden
	}
}

// Update applies a partial change. The resulting full record is validated as a
// whole, so a PATCH cannot create an invalid patient. PII is re-encrypted with
// fresh nonces by the repository and the search tokens are rebuilt.
func (s *Service) Update(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input UpdateInput) (Patient, error) {
	if !canManagePatients(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "patient.update")
		return Patient{}, ErrForbidden
	}

	existing, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Patient{}, err
	}

	full := CreateInput{
		FirstName: existing.FirstName,
		LastName:  existing.LastName,
		BirthDate: existing.BirthDate,
		Status:    existing.Status,
	}
	changed := make([]string, 0, 4)
	if input.FirstName != nil {
		full.FirstName = *input.FirstName
		changed = append(changed, "first_name")
	}
	if input.LastName != nil {
		full.LastName = *input.LastName
		changed = append(changed, "last_name")
	}
	if input.BirthDate != nil {
		full.BirthDate = *input.BirthDate
		changed = append(changed, "birth_date")
	}
	if input.Status != nil {
		full.Status = *input.Status
		changed = append(changed, "status")
	}
	if len(changed) == 0 {
		return Patient{}, fmt.Errorf("%w: no fields to update", ErrInvalid)
	}

	normalized, err := validateInput(full, s.now())
	if err != nil {
		return Patient{}, err
	}

	patient, err := s.store.Update(ctx, q, actor.TenantID, id, normalized)
	if err != nil {
		return Patient{}, err
	}
	if err := s.replaceSearchTokens(ctx, q, actor.TenantID, patient); err != nil {
		return Patient{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "patient.updated",
		EntityType:  "patient",
		EntityID:    patient.ID,
		RequestID:   requestID,
		Metadata:    map[string]any{"changed_fields": changed, "status": patient.Status},
	}); err != nil {
		return Patient{}, err
	}
	return patient, nil
}

// Search returns only patients the actor may access. Employees are filtered by
// their active assignment before any row is decrypted. The raw search term is
// never written to the audit event or the request log.
func (s *Service) Search(ctx context.Context, q *db.Queries, actor Actor, requestID, query string) ([]Patient, error) {
	switch actor.Role {
	case "chef", "employee":
	default:
		s.recordDenied(ctx, q, actor, requestID, "patient.search")
		return nil, ErrForbidden
	}
	if s.search == nil {
		return nil, fmt.Errorf("patient search is not configured")
	}

	hashes, err := s.search.QueryHashes(actor.TenantID, query)
	if err != nil {
		return nil, err
	}

	var employeeID *uuid.UUID
	if actor.Role == "employee" {
		employeeID = &actor.UserID
	}

	patients, err := s.store.Search(ctx, q, actor.TenantID, employeeID, hashes, maxSearchResults)
	if err != nil {
		return nil, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "patient.searched",
		EntityType:  "patient",
		RequestID:   requestID,
		Metadata:    map[string]any{"terms": len(hashes), "results": len(patients)},
	}); err != nil {
		return nil, err
	}
	return patients, nil
}

func (s *Service) replaceSearchTokens(ctx context.Context, q *db.Queries, tenantID uuid.UUID, patient Patient) error {
	if s.search == nil {
		return nil
	}
	tokens := s.search.Tokens(tenantID, patient.FirstName, patient.LastName)
	return s.store.ReplaceSearchTokens(ctx, q, tenantID, patient.ID, tokens)
}

// employeeCanAccess applies object-level authorization. A missing or expired
// assignment is reported as not found so the API does not leak patient
// existence to employees outside the assignment (doc 4/5).
func (s *Service) employeeCanAccess(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) (bool, error) {
	if s.access == nil {
		s.recordDenied(ctx, q, actor, requestID, "patient.read")
		return false, nil
	}
	allowed, err := s.access.CanAccessPatient(ctx, q, actor.TenantID, actor.UserID, patientID)
	if err != nil {
		return false, err
	}
	if !allowed {
		s.recordDenied(ctx, q, actor, requestID, "patient.read")
	}
	return allowed, nil
}

// canManagePatients is intentionally limited to the chef. A platform admin must
// not get automatic access to health data (doc 4), and employees cannot create
// patients.
func canManagePatients(role string) bool {
	return role == "chef"
}

func validateInput(input CreateInput, now time.Time) (CreateInput, error) {
	input.FirstName = strings.TrimSpace(input.FirstName)
	input.LastName = strings.TrimSpace(input.LastName)

	if input.FirstName == "" {
		return CreateInput{}, fmt.Errorf("%w: first name is required", ErrInvalid)
	}
	if input.LastName == "" {
		return CreateInput{}, fmt.Errorf("%w: last name is required", ErrInvalid)
	}
	if len([]rune(input.FirstName)) > 100 {
		return CreateInput{}, fmt.Errorf("%w: first name is too long", ErrInvalid)
	}
	if len([]rune(input.LastName)) > 100 {
		return CreateInput{}, fmt.Errorf("%w: last name is too long", ErrInvalid)
	}
	if input.BirthDate.IsZero() {
		return CreateInput{}, fmt.Errorf("%w: birth date is required", ErrInvalid)
	}
	if input.BirthDate.After(now) {
		return CreateInput{}, fmt.Errorf("%w: birth date is in the future", ErrInvalid)
	}
	if input.BirthDate.Before(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return CreateInput{}, fmt.Errorf("%w: birth date is too old", ErrInvalid)
	}

	switch input.Status {
	case "":
		input.Status = "active"
	case "active", "inactive", "archived":
	default:
		return CreateInput{}, fmt.Errorf("%w: status %q is not allowed", ErrInvalid, input.Status)
	}

	return input, nil
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
		EntityType:  "patient",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}
