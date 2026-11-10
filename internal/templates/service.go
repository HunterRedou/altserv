package templates

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid template input")
	ErrNotFound  = errors.New("template not found")
	ErrConflict  = errors.New("template conflict")
)

const (
	maxTemplateNameRunes   = 120
	maxTemplateActivities  = 100
	maxTemplateQuantity    = 10000
	maxTemplatesPerPatient = 100
)

// Store is the persistence boundary used by the template service.
type Store interface {
	CreateVersion(ctx context.Context, q *db.Queries, tenantID, createdBy, patientID uuid.UUID, name string, version int, activities []ActivityInput) (Template, error)
	Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Template, error)
	GetActive(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Template, error)
	GetActiveByName(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, name string) (Template, error)
	ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]Template, error)
	LatestVersion(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, name string) (int, error)
	Supersede(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) error
	Archive(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (bool, error)
	ListActivities(ctx context.Context, q *db.Queries, tenantID, templateID uuid.UUID) ([]TemplateActivity, error)
}

// AccessChecker answers whether an employee may access a patient right now. It
// is implemented by the assignment service so the rule lives in one place.
type AccessChecker interface {
	CanAccessPatient(ctx context.Context, q *db.Queries, tenantID, employeeID, patientID uuid.UUID) (bool, error)
}

// CatalogReader resolves the global service definitions referenced by a
// template. Templates reference services, never performed activities.
type CatalogReader interface {
	GetDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (catalog.Definition, error)
}

// Service contains the template business rules. A template is a suggestion and
// never records a performed activity. Only the active version can be edited;
// editing creates a new immutable version (doc 11).
type Service struct {
	store   Store
	catalog CatalogReader
	audit   audit.Recorder
	access  AccessChecker
}

func NewService(store Store, catalogReader CatalogReader, recorder audit.Recorder, access AccessChecker) *Service {
	return &Service{store: store, catalog: catalogReader, audit: recorder, access: access}
}

// Create adds a new template version 1 for a patient.
func (s *Service) Create(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateInput) (Template, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "template.create")
		return Template{}, ErrForbidden
	}

	normalized, err := validateCreate(input)
	if err != nil {
		return Template{}, err
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, normalized.PatientID)
	if err != nil {
		return Template{}, err
	}
	if !allowed {
		return Template{}, ErrForbidden
	}

	if _, err := s.store.GetActiveByName(ctx, q, actor.TenantID, normalized.PatientID, normalized.Name); err == nil {
		return Template{}, fmt.Errorf("%w: an active template with this name already exists", ErrConflict)
	} else if !errors.Is(err, ErrNotFound) {
		return Template{}, err
	}

	if err := s.validateDefinitions(ctx, q, normalized.Activities); err != nil {
		return Template{}, err
	}

	version, err := s.store.LatestVersion(ctx, q, actor.TenantID, normalized.PatientID, normalized.Name)
	if err != nil {
		return Template{}, err
	}

	template, err := s.store.CreateVersion(ctx, q, actor.TenantID, actor.UserID, normalized.PatientID, normalized.Name, version+1, normalized.Activities)
	if err != nil {
		return Template{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "template.created",
		EntityType:  "template",
		EntityID:    template.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":     template.PatientID,
			"version":        template.Version,
			"activity_count": len(template.Activities),
		},
	}); err != nil {
		return Template{}, err
	}
	return template, nil
}

// Update supersedes the active version and creates the next version with a new
// activity list. The name and patient stay the same; use Create for a new name.
func (s *Service) Update(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, input UpdateInput) (Template, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "template.update")
		return Template{}, ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Template{}, err
	}
	if current.Status != "active" {
		return Template{}, fmt.Errorf("%w: only the active version can be edited", ErrConflict)
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, current.PatientID)
	if err != nil {
		return Template{}, err
	}
	if !allowed {
		return Template{}, ErrForbidden
	}

	normalized, err := validateActivities(input.Activities)
	if err != nil {
		return Template{}, err
	}
	if err := s.validateDefinitions(ctx, q, normalized); err != nil {
		return Template{}, err
	}

	version, err := s.store.LatestVersion(ctx, q, actor.TenantID, current.PatientID, current.Name)
	if err != nil {
		return Template{}, err
	}
	if err := s.store.Supersede(ctx, q, actor.TenantID, current.ID); err != nil {
		return Template{}, err
	}

	template, err := s.store.CreateVersion(ctx, q, actor.TenantID, actor.UserID, current.PatientID, current.Name, version+1, normalized)
	if err != nil {
		return Template{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "template.updated",
		EntityType:  "template",
		EntityID:    template.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id":       template.PatientID,
			"version":          template.Version,
			"previous_version": current.Version,
			"activity_count":   len(template.Activities),
		},
	}); err != nil {
		return Template{}, err
	}
	return template, nil
}

// Archive retires the active version. Historical versions stay readable.
func (s *Service) Archive(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) error {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "template.archive")
		return ErrForbidden
	}

	current, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return err
	}
	if current.Status != "active" {
		return fmt.Errorf("%w: only the active version can be archived", ErrConflict)
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, current.PatientID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}

	archived, err := s.store.Archive(ctx, q, actor.TenantID, current.ID)
	if err != nil {
		return err
	}
	if !archived {
		return fmt.Errorf("%w: template version changed concurrently", ErrConflict)
	}

	return s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "template.archived",
		EntityType:  "template",
		EntityID:    current.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"patient_id": current.PatientID,
			"version":    current.Version,
		},
	})
}

// Get returns one template version with its suggested activities.
func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (Template, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "template.read")
		return Template{}, ErrForbidden
	}

	template, err := s.store.Get(ctx, q, actor.TenantID, id)
	if err != nil {
		return Template{}, err
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, template.PatientID)
	if err != nil {
		return Template{}, err
	}
	if !allowed {
		// Hide the template's existence from an unassigned employee.
		return Template{}, ErrNotFound
	}

	template.Activities, err = s.store.ListActivities(ctx, q, actor.TenantID, template.ID)
	if err != nil {
		return Template{}, err
	}
	return template, nil
}

// ListByPatient returns the active templates for a patient.
func (s *Service) ListByPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) ([]Template, error) {
	if !canRead(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "template.list")
		return nil, ErrForbidden
	}

	allowed, err := s.canAccessPatient(ctx, q, actor, requestID, patientID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrNotFound
	}

	templates, err := s.store.ListByPatient(ctx, q, actor.TenantID, patientID)
	if err != nil {
		return nil, err
	}
	if len(templates) > maxTemplatesPerPatient {
		templates = templates[:maxTemplatesPerPatient]
	}
	return templates, nil
}

func (s *Service) canAccessPatient(ctx context.Context, q *db.Queries, actor Actor, requestID string, patientID uuid.UUID) (bool, error) {
	if actor.Role == "chef" {
		return true, nil
	}
	if s.access == nil {
		s.recordDenied(ctx, q, actor, requestID, "template.access")
		return false, nil
	}
	allowed, err := s.access.CanAccessPatient(ctx, q, actor.TenantID, actor.UserID, patientID)
	if err != nil {
		return false, err
	}
	if !allowed {
		s.recordDenied(ctx, q, actor, requestID, "template.access")
	}
	return allowed, nil
}

func (s *Service) validateDefinitions(ctx context.Context, q *db.Queries, activities []ActivityInput) error {
	for _, activity := range activities {
		if _, err := s.catalog.GetDefinition(ctx, q, activity.ActivityID); err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				return fmt.Errorf("%w: activity %s is not a known service definition", ErrInvalid, activity.ActivityID)
			}
			return err
		}
	}
	return nil
}

// canRead allows the two tenant roles that record or plan care. A platform
// admin gets no automatic tenant data access (doc 4).
func canRead(role string) bool {
	return role == "chef" || role == "employee"
}

// canManage allows employees to maintain templates for patients they are
// assigned to; the assignment check happens separately.
func canManage(role string) bool {
	return role == "chef" || role == "employee"
}

func validateCreate(input CreateInput) (CreateInput, error) {
	input.Name = strings.TrimSpace(input.Name)

	if input.PatientID == uuid.Nil {
		return CreateInput{}, fmt.Errorf("%w: patient id is required", ErrInvalid)
	}
	if input.Name == "" {
		return CreateInput{}, fmt.Errorf("%w: template name is required", ErrInvalid)
	}
	if len([]rune(input.Name)) > maxTemplateNameRunes {
		return CreateInput{}, fmt.Errorf("%w: template name is too long", ErrInvalid)
	}

	activities, err := validateActivities(input.Activities)
	if err != nil {
		return CreateInput{}, err
	}
	input.Activities = activities
	return input, nil
}

func validateActivities(activities []ActivityInput) ([]ActivityInput, error) {
	if len(activities) == 0 {
		return nil, fmt.Errorf("%w: at least one activity is required", ErrInvalid)
	}
	if len(activities) > maxTemplateActivities {
		return nil, fmt.Errorf("%w: too many activities", ErrInvalid)
	}

	seen := make(map[uuid.UUID]struct{}, len(activities))
	for _, activity := range activities {
		if activity.ActivityID == uuid.Nil {
			return nil, fmt.Errorf("%w: activity id is required", ErrInvalid)
		}
		if _, ok := seen[activity.ActivityID]; ok {
			return nil, fmt.Errorf("%w: duplicate activity %s", ErrInvalid, activity.ActivityID)
		}
		seen[activity.ActivityID] = struct{}{}
		if activity.DefaultQuantity <= 0 || activity.DefaultQuantity > maxTemplateQuantity {
			return nil, fmt.Errorf("%w: default_quantity must be between 1 and %d", ErrInvalid, maxTemplateQuantity)
		}
		if activity.SortOrder < 0 {
			return nil, fmt.Errorf("%w: sort_order must not be negative", ErrInvalid)
		}
	}
	return activities, nil
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
		EntityType:  "template",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}
