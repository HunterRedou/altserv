package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Manifest is one verified catalog version and its billable services. It is the
// input of the catalog import tool. The catalog carries a mandatory source note
// so an unverified catalog cannot be loaded (doc 9).
//
// Use either Catalog (create a new version) or CatalogID (add services to an
// existing version), never both.
type Manifest struct {
	Catalog     *ManifestCatalog     `json:"catalog,omitempty"`
	CatalogID   string               `json:"catalog_id,omitempty"`
	Definitions []ManifestDefinition `json:"definitions"`
}

// ManifestCatalog is the catalog header of a manifest.
type ManifestCatalog struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Region     string  `json:"region"`
	SourceNote string  `json:"source_note"`
	ValidFrom  string  `json:"valid_from"`
	ValidUntil *string `json:"valid_until,omitempty"`
}

// ManifestDefinition is one billable service in a manifest.
type ManifestDefinition struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	LegalArea   string  `json:"legal_area"`
	Unit        string  `json:"unit"`
	ValidFrom   string  `json:"valid_from"`
	ValidUntil  *string `json:"valid_until,omitempty"`
}

// ImportPlan is a validated manifest that is ready to write.
type ImportPlan struct {
	Catalog     *CreateCatalogInput
	CatalogID   uuid.UUID
	Definitions []CreateDefinitionInput
}

// ImportResult is the outcome of one import.
type ImportResult struct {
	Catalog     Catalog
	Definitions []Definition
}

// ParseManifest decodes a manifest strictly. Unknown fields are rejected so a
// typo cannot silently drop a service.
func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: unexpected data after the manifest", ErrInvalid)
	}
	return manifest, nil
}

// Plan validates the manifest and turns it into write-ready input. It does not
// touch the database, so the import tool can run a dry run.
func (m Manifest) Plan() (ImportPlan, error) {
	if m.Catalog != nil && strings.TrimSpace(m.CatalogID) != "" {
		return ImportPlan{}, fmt.Errorf("%w: use catalog or catalog_id, not both", ErrInvalid)
	}
	if m.Catalog == nil && strings.TrimSpace(m.CatalogID) == "" {
		return ImportPlan{}, fmt.Errorf("%w: catalog or catalog_id is required", ErrInvalid)
	}
	if len(m.Definitions) == 0 {
		return ImportPlan{}, fmt.Errorf("%w: at least one definition is required", ErrInvalid)
	}

	plan := ImportPlan{}

	if m.Catalog != nil {
		input, err := m.Catalog.input()
		if err != nil {
			return ImportPlan{}, err
		}
		normalized, err := validateCatalogInput(input)
		if err != nil {
			return ImportPlan{}, err
		}
		plan.Catalog = &normalized
	} else {
		catalogID, err := uuid.Parse(strings.TrimSpace(m.CatalogID))
		if err != nil {
			return ImportPlan{}, fmt.Errorf("%w: catalog_id must be a UUID", ErrInvalid)
		}
		plan.CatalogID = catalogID
	}

	seen := make(map[string]struct{}, len(m.Definitions))
	for _, entry := range m.Definitions {
		input, err := entry.input()
		if err != nil {
			return ImportPlan{}, err
		}
		normalized, err := validateDefinitionFields(input)
		if err != nil {
			return ImportPlan{}, fmt.Errorf("definition %q: %w", entry.Code, err)
		}

		key := normalized.Code + "|" + normalized.ValidFrom.UTC().Format(time.RFC3339)
		if _, duplicate := seen[key]; duplicate {
			return ImportPlan{}, fmt.Errorf(
				"%w: definition %q is listed twice for the same valid_from",
				ErrInvalid, normalized.Code,
			)
		}
		seen[key] = struct{}{}

		plan.Definitions = append(plan.Definitions, normalized)
	}

	return plan, nil
}

func (c ManifestCatalog) input() (CreateCatalogInput, error) {
	validFrom, err := parseManifestTime(c.ValidFrom, "catalog valid_from")
	if err != nil {
		return CreateCatalogInput{}, err
	}
	validUntil, err := parseManifestOptionalTime(c.ValidUntil, "catalog valid_until")
	if err != nil {
		return CreateCatalogInput{}, err
	}
	return CreateCatalogInput{
		Code:       c.Code,
		Name:       c.Name,
		Region:     c.Region,
		SourceNote: c.SourceNote,
		ValidFrom:  validFrom,
		ValidUntil: validUntil,
	}, nil
}

func (d ManifestDefinition) input() (CreateDefinitionInput, error) {
	validFrom, err := parseManifestTime(d.ValidFrom, "definition valid_from")
	if err != nil {
		return CreateDefinitionInput{}, err
	}
	validUntil, err := parseManifestOptionalTime(d.ValidUntil, "definition valid_until")
	if err != nil {
		return CreateDefinitionInput{}, err
	}
	return CreateDefinitionInput{
		Code:        d.Code,
		Name:        d.Name,
		Description: d.Description,
		LegalArea:   d.LegalArea,
		Unit:        d.Unit,
		ValidFrom:   validFrom,
		ValidUntil:  validUntil,
	}, nil
}

func parseManifestTime(raw, field string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s must be RFC3339", ErrInvalid, field)
	}
	return parsed, nil
}

func parseManifestOptionalTime(raw *string, field string) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	parsed, err := parseManifestTime(*raw, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// ImportManifest writes one verified catalog version and its services in the
// caller's transaction. It requires a platform admin actor. The whole import is
// one audit event, so a large catalog does not flood the audit log.
func (s *Service) ImportManifest(ctx context.Context, q *db.Queries, actor Actor, requestID string, manifest Manifest) (ImportResult, error) {
	if !canWriteGlobalCatalog(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "service_catalog.import")
		return ImportResult{}, ErrForbidden
	}

	plan, err := manifest.Plan()
	if err != nil {
		return ImportResult{}, err
	}

	var catalog Catalog
	if plan.Catalog != nil {
		catalog, err = s.createCatalog(ctx, q, *plan.Catalog)
	} else {
		catalog, err = s.store.GetCatalog(ctx, q, plan.CatalogID)
	}
	if err != nil {
		return ImportResult{}, err
	}

	definitions := make([]Definition, 0, len(plan.Definitions))
	for _, input := range plan.Definitions {
		input.CatalogID = catalog.ID
		definition, err := s.createDefinition(ctx, q, input)
		if err != nil {
			return ImportResult{}, fmt.Errorf("definition %q: %w", input.Code, err)
		}
		definitions = append(definitions, definition)
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "catalog.imported",
		EntityType:  "service_catalog",
		EntityID:    catalog.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"code":             catalog.Code,
			"region":           catalog.Region,
			"definition_count": len(definitions),
			"created_catalog":  plan.Catalog != nil,
			"valid_from":       catalog.ValidFrom,
			"has_valid_until":  catalog.ValidUntil != nil,
		},
	}); err != nil {
		return ImportResult{}, err
	}

	return ImportResult{Catalog: catalog, Definitions: definitions}, nil
}
