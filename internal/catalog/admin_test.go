package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func platformActor() Actor {
	return Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "platform_admin"}
}

func validCatalogInput() CreateCatalogInput {
	return CreateCatalogInput{
		Code:       "  BB-2026 ",
		Name:       " Brandenburger Leistungskatalog ",
		Region:     "",
		SourceNote: "  Fachlich geprueft, Quelle: Ministerium  ",
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func validDefinitionInput() CreateDefinitionInput {
	return CreateDefinitionInput{
		CatalogID:   uuid.New(),
		Code:        " LK-001 ",
		Name:        " Grundpflege ",
		Description: "  Hilfe bei der Koerperpflege  ",
		LegalArea:   "sgb_xi",
		Unit:        " Einsatz ",
		ValidFrom:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestServiceCreateCatalogRequiresPlatformAdmin(t *testing.T) {
	for _, role := range []string{"chef", "employee", "unknown"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewService(store, recorder)

			_, err := service.CreateCatalog(context.Background(), nil, Actor{
				UserID:   uuid.New(),
				TenantID: uuid.New(),
				Role:     role,
			}, "req", validCatalogInput())
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.createCatalogCalls != 0 {
				t.Error("store was called for a forbidden catalog create")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceCreateCatalogValidatesInput(t *testing.T) {
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := validFrom.Add(-time.Hour)

	tests := []struct {
		name  string
		input CreateCatalogInput
	}{
		{name: "missing code", input: CreateCatalogInput{Name: "Katalog", SourceNote: "Quelle", ValidFrom: validFrom}},
		{name: "long code", input: CreateCatalogInput{Code: strings.Repeat("a", maxCatalogCodeRunes+1), Name: "Katalog", SourceNote: "Quelle", ValidFrom: validFrom}},
		{name: "missing name", input: CreateCatalogInput{Code: "BB", SourceNote: "Quelle", ValidFrom: validFrom}},
		{name: "long name", input: CreateCatalogInput{Code: "BB", Name: strings.Repeat("a", maxCatalogNameRunes+1), SourceNote: "Quelle", ValidFrom: validFrom}},
		{name: "long region", input: CreateCatalogInput{Code: "BB", Name: "Katalog", Region: strings.Repeat("a", maxRegionRunes+1), SourceNote: "Quelle", ValidFrom: validFrom}},
		{name: "missing source note", input: CreateCatalogInput{Code: "BB", Name: "Katalog", ValidFrom: validFrom}},
		{name: "long source note", input: CreateCatalogInput{Code: "BB", Name: "Katalog", SourceNote: strings.Repeat("a", maxSourceNoteRunes+1), ValidFrom: validFrom}},
		{name: "missing valid from", input: CreateCatalogInput{Code: "BB", Name: "Katalog", SourceNote: "Quelle"}},
		{name: "valid until before valid from", input: CreateCatalogInput{Code: "BB", Name: "Katalog", SourceNote: "Quelle", ValidFrom: validFrom, ValidUntil: &validUntil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			service := NewService(store, &fakeAudit{})
			if _, err := service.CreateCatalog(context.Background(), nil, platformActor(), "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if store.createCatalogCalls != 0 {
				t.Error("store was called for invalid input")
			}
		})
	}
}

func TestServiceCreateCatalogNormalizesAndAudits(t *testing.T) {
	catalogID := uuid.New()
	store := &fakeStore{catalog: Catalog{
		ID:         catalogID,
		Code:       "BB-2026",
		Name:       "Brandenburger Leistungskatalog",
		Region:     "brandenburg",
		SourceNote: "Fachlich geprueft, Quelle: Ministerium",
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)
	actor := platformActor()

	catalog, err := service.CreateCatalog(context.Background(), nil, actor, "req-1", validCatalogInput())
	if err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if catalog.ID != catalogID {
		t.Errorf("catalog id = %s, want %s", catalog.ID, catalogID)
	}
	if store.lastCatalogInput.Code != "BB-2026" {
		t.Errorf("code = %q, want trimmed", store.lastCatalogInput.Code)
	}
	if store.lastCatalogInput.Name != "Brandenburger Leistungskatalog" {
		t.Errorf("name = %q, want trimmed", store.lastCatalogInput.Name)
	}
	if store.lastCatalogInput.Region != "brandenburg" {
		t.Errorf("region = %q, want default brandenburg", store.lastCatalogInput.Region)
	}
	if store.lastCatalogInput.SourceNote != "Fachlich geprueft, Quelle: Ministerium" {
		t.Errorf("source note = %q, want trimmed", store.lastCatalogInput.SourceNote)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "service_catalog.created" {
		t.Errorf("action = %q", event.Action)
	}
	if event.TenantID != actor.TenantID || event.ActorUserID != actor.UserID {
		t.Errorf("event actor = %s/%s, want %s/%s", event.TenantID, event.ActorUserID, actor.TenantID, actor.UserID)
	}
	if event.Metadata["code"] != "BB-2026" {
		t.Errorf("metadata code = %v", event.Metadata["code"])
	}
	if event.Metadata["has_source_note"] != true {
		t.Errorf("metadata has_source_note = %v, want true", event.Metadata["has_source_note"])
	}
	// Audit metadata must never contain the free-text source note itself.
	for key, value := range event.Metadata {
		if text, ok := value.(string); ok && strings.Contains(text, "Ministerium") {
			t.Errorf("metadata %q leaks the source note: %q", key, text)
		}
	}
}

func TestServiceCreateDefinitionRequiresPlatformAdmin(t *testing.T) {
	for _, role := range []string{"chef", "employee", "unknown"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewService(store, recorder)

			_, err := service.CreateDefinition(context.Background(), nil, Actor{
				UserID:   uuid.New(),
				TenantID: uuid.New(),
				Role:     role,
			}, "req", validDefinitionInput())
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.createDefinitionCalls != 0 {
				t.Error("store was called for a forbidden definition create")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceCreateDefinitionValidatesInput(t *testing.T) {
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := validFrom.Add(-time.Hour)

	tests := []struct {
		name  string
		input CreateDefinitionInput
	}{
		{name: "missing catalog", input: CreateDefinitionInput{Code: "LK", Name: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "missing code", input: CreateDefinitionInput{CatalogID: uuid.New(), Name: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "long code", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: strings.Repeat("a", maxDefinitionCodeRunes+1), Name: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "missing name", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "long name", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: strings.Repeat("a", maxDefinitionNameRunes+1), LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "long description", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: "Pflege", Description: strings.Repeat("a", maxDefinitionDescRunes+1), LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "invalid legal area", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: "Pflege", LegalArea: "sgb_xii", ValidFrom: validFrom}},
		{name: "long unit", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: "Pflege", LegalArea: "sgb_xi", Unit: strings.Repeat("a", maxDefinitionUnitRunes+1), ValidFrom: validFrom}},
		{name: "missing valid from", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: "Pflege", LegalArea: "sgb_xi"}},
		{name: "valid until before valid from", input: CreateDefinitionInput{CatalogID: uuid.New(), Code: "LK", Name: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom, ValidUntil: &validUntil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			service := NewService(store, &fakeAudit{})
			if _, err := service.CreateDefinition(context.Background(), nil, platformActor(), "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if store.createDefinitionCalls != 0 {
				t.Error("store was called for invalid input")
			}
		})
	}
}

func TestServiceCreateDefinitionNormalizesAndAudits(t *testing.T) {
	catalogID := uuid.New()
	definitionID := uuid.New()
	store := &fakeStore{definition: Definition{
		ID:        definitionID,
		CatalogID: catalogID,
		Code:      "LK-001",
		Name:      "Grundpflege",
		LegalArea: "sgb_xi",
		ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)
	actor := platformActor()

	input := validDefinitionInput()
	input.CatalogID = catalogID

	definition, err := service.CreateDefinition(context.Background(), nil, actor, "req-2", input)
	if err != nil {
		t.Fatalf("CreateDefinition: %v", err)
	}
	if definition.ID != definitionID {
		t.Errorf("definition id = %s, want %s", definition.ID, definitionID)
	}
	if store.lastDefinitionInput.Code != "LK-001" {
		t.Errorf("code = %q, want trimmed", store.lastDefinitionInput.Code)
	}
	if store.lastDefinitionInput.Description != "Hilfe bei der Koerperpflege" {
		t.Errorf("description = %q, want trimmed", store.lastDefinitionInput.Description)
	}
	if store.lastDefinitionInput.Unit != "Einsatz" {
		t.Errorf("unit = %q, want trimmed", store.lastDefinitionInput.Unit)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "service_definition.created" {
		t.Errorf("action = %q", event.Action)
	}
	if event.Metadata["catalog_id"] != catalogID {
		t.Errorf("metadata catalog_id = %v, want %s", event.Metadata["catalog_id"], catalogID)
	}
	if event.Metadata["legal_area"] != "sgb_xi" {
		t.Errorf("metadata legal_area = %v", event.Metadata["legal_area"])
	}
}

func TestParseManifest(t *testing.T) {
	valid := `{
		"catalog": {
			"code": "BB-2026",
			"name": "Brandenburger Leistungskatalog",
			"region": "brandenburg",
			"source_note": "Fachlich geprueft",
			"valid_from": "2026-01-01T00:00:00Z",
			"valid_until": "2027-01-01T00:00:00Z"
		},
		"definitions": [
			{
				"code": "LK-001",
				"name": "Grundpflege",
				"legal_area": "sgb_xi",
				"valid_from": "2026-01-01T00:00:00Z"
			}
		]
	}`

	manifest, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Catalog == nil || manifest.Catalog.Code != "BB-2026" {
		t.Fatalf("manifest catalog = %+v", manifest.Catalog)
	}
	if len(manifest.Definitions) != 1 {
		t.Fatalf("definitions = %d, want 1", len(manifest.Definitions))
	}

	tests := []struct {
		name string
		data string
	}{
		{name: "invalid json", data: `{`},
		{name: "unknown field", data: `{"definitions":[],"extra":true}`},
		{name: "trailing data", data: valid + `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tt.data)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestManifestPlanValidates(t *testing.T) {
	validDefinition := ManifestDefinition{
		Code:      "LK-001",
		Name:      "Grundpflege",
		LegalArea: "sgb_xi",
		ValidFrom: "2026-01-01T00:00:00Z",
	}

	tests := []struct {
		name     string
		manifest Manifest
	}{
		{name: "no catalog and no id", manifest: Manifest{Definitions: []ManifestDefinition{validDefinition}}},
		{
			name: "catalog and id",
			manifest: Manifest{
				Catalog:     &ManifestCatalog{Code: "BB", Name: "Katalog", SourceNote: "Quelle", ValidFrom: "2026-01-01T00:00:00Z"},
				CatalogID:   uuid.NewString(),
				Definitions: []ManifestDefinition{validDefinition},
			},
		},
		{name: "no definitions", manifest: Manifest{CatalogID: uuid.NewString()}},
		{name: "bad catalog id", manifest: Manifest{CatalogID: "not-a-uuid", Definitions: []ManifestDefinition{validDefinition}}},
		{
			name: "catalog without source note",
			manifest: Manifest{
				Catalog:     &ManifestCatalog{Code: "BB", Name: "Katalog", ValidFrom: "2026-01-01T00:00:00Z"},
				Definitions: []ManifestDefinition{validDefinition},
			},
		},
		{
			name: "definition without valid_from",
			manifest: Manifest{
				CatalogID: uuid.NewString(),
				Definitions: []ManifestDefinition{{
					Code: "LK-001", Name: "Grundpflege", LegalArea: "sgb_xi",
				}},
			},
		},
		{
			name: "definition bad legal area",
			manifest: Manifest{
				CatalogID: uuid.NewString(),
				Definitions: []ManifestDefinition{{
					Code: "LK-001", Name: "Grundpflege", LegalArea: "sgb_xii", ValidFrom: "2026-01-01T00:00:00Z",
				}},
			},
		},
		{
			name: "duplicate definition",
			manifest: Manifest{
				CatalogID:   uuid.NewString(),
				Definitions: []ManifestDefinition{validDefinition, validDefinition},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.manifest.Plan(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestManifestPlanBuildsInput(t *testing.T) {
	manifest := Manifest{
		Catalog: &ManifestCatalog{
			Code:       " BB-2026 ",
			Name:       " Katalog ",
			Region:     "",
			SourceNote: " Quelle ",
			ValidFrom:  "2026-01-01T00:00:00Z",
			ValidUntil: strPtr("2027-01-01T00:00:00Z"),
		},
		Definitions: []ManifestDefinition{
			{
				Code:      " LK-001 ",
				Name:      " Grundpflege ",
				LegalArea: "sgb_xi",
				ValidFrom: "2026-01-01T00:00:00Z",
			},
			{
				Code:      "LK-001",
				Name:      "Grundpflege (neue Fassung)",
				LegalArea: "sgb_xi",
				ValidFrom: "2026-07-01T00:00:00Z",
			},
		},
	}

	plan, err := manifest.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Catalog == nil {
		t.Fatal("plan catalog is nil")
	}
	if plan.Catalog.Code != "BB-2026" || plan.Catalog.Region != "brandenburg" || plan.Catalog.SourceNote != "Quelle" {
		t.Errorf("plan catalog = %+v, want normalized", plan.Catalog)
	}
	if len(plan.Definitions) != 2 {
		t.Fatalf("definitions = %d, want 2", len(plan.Definitions))
	}
	if plan.Definitions[0].Code != "LK-001" {
		t.Errorf("definition code = %q, want trimmed", plan.Definitions[0].Code)
	}
}

func TestImportManifestRequiresPlatformAdmin(t *testing.T) {
	store := &fakeStore{}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)
	manifest := Manifest{
		CatalogID: uuid.NewString(),
		Definitions: []ManifestDefinition{{
			Code: "LK-001", Name: "Grundpflege", LegalArea: "sgb_xi", ValidFrom: "2026-01-01T00:00:00Z",
		}},
	}

	_, err := service.ImportManifest(context.Background(), nil, Actor{
		UserID:   uuid.New(),
		TenantID: uuid.New(),
		Role:     "chef",
	}, "req", manifest)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
	}
	if store.createDefinitionCalls != 0 || store.createCatalogCalls != 0 {
		t.Error("store was called for a forbidden import")
	}
}

func TestImportManifestCreatesCatalogAndDefinitions(t *testing.T) {
	catalogID := uuid.New()
	store := &fakeStore{catalog: Catalog{
		ID:         catalogID,
		Code:       "BB-2026",
		Name:       "Brandenburger Leistungskatalog",
		Region:     "brandenburg",
		SourceNote: "Fachlich geprueft",
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)
	actor := platformActor()

	manifest := Manifest{
		Catalog: &ManifestCatalog{
			Code:       "BB-2026",
			Name:       "Brandenburger Leistungskatalog",
			SourceNote: "Fachlich geprueft",
			ValidFrom:  "2026-01-01T00:00:00Z",
		},
		Definitions: []ManifestDefinition{
			{Code: "LK-001", Name: "Grundpflege", LegalArea: "sgb_xi", ValidFrom: "2026-01-01T00:00:00Z"},
			{Code: "LK-002", Name: "Hauswirtschaft", LegalArea: "sgb_xi", ValidFrom: "2026-01-01T00:00:00Z"},
		},
	}

	result, err := service.ImportManifest(context.Background(), nil, actor, "req", manifest)
	if err != nil {
		t.Fatalf("ImportManifest: %v", err)
	}
	if result.Catalog.ID != catalogID {
		t.Errorf("catalog id = %s, want %s", result.Catalog.ID, catalogID)
	}
	if len(result.Definitions) != 2 {
		t.Fatalf("definitions = %d, want 2", len(result.Definitions))
	}
	if store.createCatalogCalls != 1 {
		t.Errorf("catalog creates = %d, want 1", store.createCatalogCalls)
	}
	if store.createDefinitionCalls != 2 {
		t.Errorf("definition creates = %d, want 2", store.createDefinitionCalls)
	}
	if store.lastDefinitionInput.CatalogID != catalogID {
		t.Errorf("definition catalog id = %s, want %s", store.lastDefinitionInput.CatalogID, catalogID)
	}

	// One import is one audit event, not one per definition.
	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "catalog.imported" {
		t.Errorf("action = %q, want catalog.imported", event.Action)
	}
	if event.Metadata["definition_count"] != 2 {
		t.Errorf("definition_count = %v, want 2", event.Metadata["definition_count"])
	}
	if event.Metadata["created_catalog"] != true {
		t.Errorf("created_catalog = %v, want true", event.Metadata["created_catalog"])
	}
}

func TestImportManifestAddsToExistingCatalog(t *testing.T) {
	catalogID := uuid.New()
	store := &fakeStore{catalog: Catalog{ID: catalogID, Code: "BB-2026"}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	manifest := Manifest{
		CatalogID: catalogID.String(),
		Definitions: []ManifestDefinition{
			{Code: "LK-003", Name: "Beratung", LegalArea: "sgb_xi", ValidFrom: "2026-01-01T00:00:00Z"},
		},
	}

	result, err := service.ImportManifest(context.Background(), nil, platformActor(), "req", manifest)
	if err != nil {
		t.Fatalf("ImportManifest: %v", err)
	}
	if result.Catalog.ID != catalogID {
		t.Errorf("catalog id = %s, want %s", result.Catalog.ID, catalogID)
	}
	if store.createCatalogCalls != 0 {
		t.Errorf("catalog creates = %d, want 0", store.createCatalogCalls)
	}
	if store.createDefinitionCalls != 1 {
		t.Errorf("definition creates = %d, want 1", store.createDefinitionCalls)
	}
	if len(recorder.events) != 1 || recorder.events[0].Metadata["created_catalog"] != false {
		t.Fatalf("audit events = %+v, want one import with created_catalog=false", recorder.events)
	}
}

func strPtr(value string) *string {
	return &value
}
