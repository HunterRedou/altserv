package templates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Repository persists templates and their activities with sqlc queries.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

// CreateVersion inserts a new immutable template version and its activities.
// The caller is responsible for superseding the previous active version first.
func (r *Repository) CreateVersion(ctx context.Context, q *db.Queries, tenantID, createdBy uuid.UUID, patientID uuid.UUID, name string, version int, activities []ActivityInput) (Template, error) {
	row, err := q.CreateTemplate(ctx, db.CreateTemplateParams{
		TenantID:  tenantID,
		PatientID: patientID,
		Name:      name,
		CreatedBy: createdBy,
		Version:   int32(version),
		Status:    "active",
	})
	if err != nil {
		return Template{}, fmt.Errorf("create template: %w", err)
	}

	for _, activity := range activities {
		if _, err := q.CreateTemplateActivity(ctx, db.CreateTemplateActivityParams{
			TenantID:        tenantID,
			TemplateID:      row.ID,
			ActivityID:      activity.ActivityID,
			DefaultQuantity: int32(activity.DefaultQuantity),
			SortOrder:       int32(activity.SortOrder),
		}); err != nil {
			return Template{}, fmt.Errorf("create template activity: %w", err)
		}
	}

	template := toTemplate(row)
	template.Activities, err = r.ListActivities(ctx, q, tenantID, row.ID)
	if err != nil {
		return Template{}, err
	}
	return template, nil
}

// Get returns one template version, active or not.
func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Template, error) {
	row, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("get template: %w", err)
	}
	return toTemplate(row), nil
}

// GetActive returns the active version with the given id.
func (r *Repository) GetActive(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Template, error) {
	row, err := q.GetActiveTemplate(ctx, db.GetActiveTemplateParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("get active template: %w", err)
	}
	return toTemplate(row), nil
}

// GetActiveByName returns the active template for a patient and name.
func (r *Repository) GetActiveByName(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, name string) (Template, error) {
	row, err := q.GetActiveTemplateByName(ctx, db.GetActiveTemplateByNameParams{
		TenantID:  tenantID,
		PatientID: patientID,
		Name:      name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("get active template by name: %w", err)
	}
	return toTemplate(row), nil
}

// ListByPatient returns the active templates for a patient.
func (r *Repository) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]Template, error) {
	rows, err := q.ListTemplatesByPatient(ctx, db.ListTemplatesByPatientParams{
		TenantID:  tenantID,
		PatientID: patientID,
	})
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}

	templates := make([]Template, 0, len(rows))
	for _, row := range rows {
		template := toTemplate(row)
		template.Activities, err = r.ListActivities(ctx, q, tenantID, row.ID)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, nil
}

// LatestVersion returns the highest version number for a template name, or 0.
func (r *Repository) LatestVersion(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, name string) (int, error) {
	version, err := q.GetLatestTemplateVersion(ctx, db.GetLatestTemplateVersionParams{
		TenantID:  tenantID,
		PatientID: patientID,
		Name:      name,
	})
	if err != nil {
		return 0, fmt.Errorf("get latest template version: %w", err)
	}
	return int(version), nil
}

// Supersede marks the active version as superseded.
func (r *Repository) Supersede(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) error {
	if err := q.SupersedeTemplateVersion(ctx, db.SupersedeTemplateVersionParams{ID: id, TenantID: tenantID}); err != nil {
		return fmt.Errorf("supersede template: %w", err)
	}
	return nil
}

// Archive marks the active version as archived. It reports whether a row
// changed, so the service can distinguish a stale version from a missing one.
func (r *Repository) Archive(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (bool, error) {
	rows, err := q.ArchiveTemplate(ctx, db.ArchiveTemplateParams{ID: id, TenantID: tenantID})
	if err != nil {
		return false, fmt.Errorf("archive template: %w", err)
	}
	return rows > 0, nil
}

// ListActivities returns the suggested activities with their service details.
func (r *Repository) ListActivities(ctx context.Context, q *db.Queries, tenantID, templateID uuid.UUID) ([]TemplateActivity, error) {
	rows, err := q.ListTemplateActivities(ctx, db.ListTemplateActivitiesParams{
		TenantID:   tenantID,
		TemplateID: templateID,
	})
	if err != nil {
		return nil, fmt.Errorf("list template activities: %w", err)
	}
	activities := make([]TemplateActivity, 0, len(rows))
	for _, row := range rows {
		activities = append(activities, toTemplateActivity(row))
	}
	return activities, nil
}

func toTemplate(row db.Template) Template {
	return Template{
		ID:        row.ID,
		TenantID:  row.TenantID,
		PatientID: row.PatientID,
		Name:      row.Name,
		CreatedBy: row.CreatedBy,
		Version:   int(row.Version),
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func toTemplateActivity(row db.ListTemplateActivitiesRow) TemplateActivity {
	return TemplateActivity{
		ID:              row.ID,
		TemplateID:      row.TemplateID,
		ActivityID:      row.ActivityID,
		DefaultQuantity: int(row.DefaultQuantity),
		SortOrder:       int(row.SortOrder),
		ServiceCode:     row.ServiceCode,
		ServiceName:     row.ServiceName,
		LegalArea:       row.LegalArea,
		Unit:            row.Unit,
	}
}
