package templates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeStore struct {
	created      Template
	createErr    error
	createCalls  int
	lastCreate   CreateVersionCall
	lastActivity []ActivityInput

	get      Template
	getErr   error
	getCalls int

	activeByName    Template
	activeByNameErr error

	listed    []Template
	listedErr error

	latestVersion int
	latestErr     error

	supersedeCalls int
	supersedeErr   error
	lastSuperseded uuid.UUID

	archiveResult bool
	archiveErr    error
	archiveCalls  int
	lastArchived  uuid.UUID

	activities    []TemplateActivity
	activitiesErr error
}

// CreateVersionCall records the arguments of the last CreateVersion call.
type CreateVersionCall struct {
	TenantID  uuid.UUID
	CreatedBy uuid.UUID
	PatientID uuid.UUID
	Name      string
	Version   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{activeByNameErr: ErrNotFound, getErr: ErrNotFound}
}

func (f *fakeStore) CreateVersion(_ context.Context, _ *db.Queries, tenantID, createdBy, patientID uuid.UUID, name string, version int, activities []ActivityInput) (Template, error) {
	f.createCalls++
	f.lastCreate = CreateVersionCall{TenantID: tenantID, CreatedBy: createdBy, PatientID: patientID, Name: name, Version: version}
	f.lastActivity = activities
	return f.created, f.createErr
}

func (f *fakeStore) Get(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Template, error) {
	f.getCalls++
	return f.get, f.getErr
}

func (f *fakeStore) GetActive(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Template, error) {
	return f.get, f.getErr
}

func (f *fakeStore) GetActiveByName(context.Context, *db.Queries, uuid.UUID, uuid.UUID, string) (Template, error) {
	return f.activeByName, f.activeByNameErr
}

func (f *fakeStore) ListByPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Template, error) {
	return f.listed, f.listedErr
}

func (f *fakeStore) LatestVersion(context.Context, *db.Queries, uuid.UUID, uuid.UUID, string) (int, error) {
	return f.latestVersion, f.latestErr
}

func (f *fakeStore) Supersede(_ context.Context, _ *db.Queries, _ uuid.UUID, id uuid.UUID) error {
	f.supersedeCalls++
	f.lastSuperseded = id
	return f.supersedeErr
}

func (f *fakeStore) Archive(_ context.Context, _ *db.Queries, _ uuid.UUID, id uuid.UUID) (bool, error) {
	f.archiveCalls++
	f.lastArchived = id
	return f.archiveResult, f.archiveErr
}

func (f *fakeStore) ListActivities(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]TemplateActivity, error) {
	return f.activities, f.activitiesErr
}

type fakeCatalog struct {
	definitions map[uuid.UUID]catalog.Definition
	err         error
}

func (f *fakeCatalog) GetDefinition(_ context.Context, _ *db.Queries, id uuid.UUID) (catalog.Definition, error) {
	if f.err != nil {
		return catalog.Definition{}, f.err
	}
	definition, ok := f.definitions[id]
	if !ok {
		return catalog.Definition{}, catalog.ErrNotFound
	}
	return definition, nil
}

type fakeAccess struct {
	allowed bool
	err     error
	calls   int
}

func (f *fakeAccess) CanAccessPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	f.calls++
	return f.allowed, f.err
}

type fakeAudit struct {
	events []audit.Event
	err    error
}

func (f *fakeAudit) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	f.events = append(f.events, event)
	return f.err
}

func activeDefinition() catalog.Definition {
	return catalog.Definition{
		ID:        uuid.New(),
		CatalogID: uuid.New(),
		Code:      "LK-001",
		Name:      "Grundpflege",
		LegalArea: "sgb_xi",
		Unit:      "Stunde",
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func validCreateInput(patientID, activityID uuid.UUID) CreateInput {
	return CreateInput{
		PatientID: patientID,
		Name:      "Morgenpflege",
		Activities: []ActivityInput{
			{ActivityID: activityID, DefaultQuantity: 1, SortOrder: 0},
		},
	}
}

func newTemplateService(store *fakeStore, catalogReader CatalogReader, access AccessChecker, recorder audit.Recorder) *Service {
	return NewService(store, catalogReader, recorder, access)
}

func TestServiceCreateRequiresManageRole(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer", ""} {
		t.Run(role, func(t *testing.T) {
			store := newFakeStore()
			recorder := &fakeAudit{}
			service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, recorder)
			tenantID := uuid.New()

			_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}, "req", CreateInput{
				PatientID:  uuid.New(),
				Name:       "Morgenpflege",
				Activities: []ActivityInput{{ActivityID: uuid.New(), DefaultQuantity: 1}},
			})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.createCalls != 0 {
				t.Error("store was called for a forbidden create")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceCreateValidation(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	activityID := definition.ID
	service := newTemplateService(newFakeStore(), &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{activityID: definition}}, &fakeAccess{allowed: true}, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	tests := []struct {
		name  string
		input CreateInput
	}{
		{name: "missing patient", input: CreateInput{Name: "Morgenpflege", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1}}}},
		{name: "missing name", input: CreateInput{PatientID: patientID, Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1}}}},
		{name: "blank name", input: CreateInput{PatientID: patientID, Name: "   ", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1}}}},
		{name: "long name", input: CreateInput{PatientID: patientID, Name: strings.Repeat("a", maxTemplateNameRunes+1), Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1}}}},
		{name: "no activities", input: CreateInput{PatientID: patientID, Name: "Morgenpflege"}},
		{name: "missing activity id", input: CreateInput{PatientID: patientID, Name: "Morgenpflege", Activities: []ActivityInput{{DefaultQuantity: 1}}}},
		{name: "duplicate activity", input: CreateInput{PatientID: patientID, Name: "Morgenpflege", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1}, {ActivityID: activityID, DefaultQuantity: 2}}}},
		{name: "zero quantity", input: CreateInput{PatientID: patientID, Name: "Morgenpflege", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 0}}}},
		{name: "too large quantity", input: CreateInput{PatientID: patientID, Name: "Morgenpflege", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: maxTemplateQuantity + 1}}}},
		{name: "negative sort order", input: CreateInput{PatientID: patientID, Name: "Morgenpflege", Activities: []ActivityInput{{ActivityID: activityID, DefaultQuantity: 1, SortOrder: -1}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.Create(context.Background(), nil, actor, "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestServiceCreateRejectsTooManyActivities(t *testing.T) {
	service := newTemplateService(newFakeStore(), &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})
	activities := make([]ActivityInput, maxTemplateActivities+1)
	for i := range activities {
		activities[i] = ActivityInput{ActivityID: uuid.New(), DefaultQuantity: 1}
	}
	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", CreateInput{
		PatientID:  uuid.New(),
		Name:       "Morgenpflege",
		Activities: activities,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestServiceCreateTrimsName(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	store := newFakeStore()
	store.created = Template{ID: uuid.New(), TenantID: uuid.New(), PatientID: patientID, Name: "Morgenpflege", Version: 1, Status: "active"}
	service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: true}, &fakeAudit{})

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", CreateInput{
		PatientID:  patientID,
		Name:       "  Morgenpflege  ",
		Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.lastCreate.Name != "Morgenpflege" {
		t.Errorf("name = %q, want trimmed name", store.lastCreate.Name)
	}
}

func TestServiceCreateRequiresPatientAccess(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	tenantID := uuid.New()

	t.Run("chef has access without assignment lookup", func(t *testing.T) {
		store := newFakeStore()
		store.created = Template{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Name: "Morgenpflege", Version: 1, Status: "active"}
		access := &fakeAccess{allowed: false}
		service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, access, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", validCreateInput(patientID, definition.ID)); err != nil {
			t.Fatalf("chef create: %v", err)
		}
		if access.calls != 0 {
			t.Error("chef create consulted the assignment checker")
		}
	})

	t.Run("employee without assignment is forbidden", func(t *testing.T) {
		store := newFakeStore()
		recorder := &fakeAudit{}
		service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: false}, recorder)

		_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "req", validCreateInput(patientID, definition.ID))
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.createCalls != 0 {
			t.Error("store was called for an unassigned employee")
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
			t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
		}
	})

	t.Run("employee with assignment may create", func(t *testing.T) {
		store := newFakeStore()
		store.created = Template{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Name: "Morgenpflege", Version: 1, Status: "active"}
		service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", validCreateInput(patientID, definition.ID)); err != nil {
			t.Fatalf("employee create: %v", err)
		}
	})
}

func TestServiceCreateRejectsUnknownDefinition(t *testing.T) {
	store := newFakeStore()
	service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{}}, &fakeAccess{allowed: true}, &fakeAudit{})

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", validCreateInput(uuid.New(), uuid.New()))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called with an unknown service definition")
	}
}

func TestServiceCreateConflictWhenActiveNameExists(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	store := newFakeStore()
	store.activeByName = Template{ID: uuid.New(), PatientID: patientID, Name: "Morgenpflege", Status: "active"}
	store.activeByNameErr = nil
	service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: true}, &fakeAudit{})

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", validCreateInput(patientID, definition.ID))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called despite the active-name conflict")
	}
}

func TestServiceCreateVersioning(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	tenantID := uuid.New()
	userID := uuid.New()
	store := newFakeStore()
	store.latestVersion = 3
	store.created = Template{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Name: "Morgenpflege", Version: 4, Status: "active"}
	recorder := &fakeAudit{}
	service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: true}, recorder)

	template, err := service.Create(context.Background(), nil, Actor{UserID: userID, TenantID: tenantID, Role: "chef"}, "req", validCreateInput(patientID, definition.ID))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.lastCreate.Version != 4 {
		t.Errorf("version = %d, want 4", store.lastCreate.Version)
	}
	if store.lastCreate.CreatedBy != userID || store.lastCreate.TenantID != tenantID {
		t.Errorf("create call = %+v", store.lastCreate)
	}
	if template.Version != 4 {
		t.Errorf("template version = %d, want 4", template.Version)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "template.created" {
		t.Fatalf("audit events = %+v, want one template.created", recorder.events)
	}
	if recorder.events[0].Metadata["version"] != 4 {
		t.Errorf("audit version = %v, want 4", recorder.events[0].Metadata["version"])
	}
}

func TestServiceUpdateCreatesNewVersion(t *testing.T) {
	definition := activeDefinition()
	patientID := uuid.New()
	tenantID := uuid.New()
	userID := uuid.New()
	current := Template{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Name: "Morgenpflege", Version: 1, Status: "active"}

	store := newFakeStore()
	store.get = current
	store.getErr = nil
	store.latestVersion = 1
	store.created = Template{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Name: "Morgenpflege", Version: 2, Status: "active"}
	recorder := &fakeAudit{}
	service := newTemplateService(store, &fakeCatalog{definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition}}, &fakeAccess{allowed: true}, recorder)

	template, err := service.Update(context.Background(), nil, Actor{UserID: userID, TenantID: tenantID, Role: "chef"}, "req", current.ID, UpdateInput{
		Activities: []ActivityInput{{ActivityID: definition.ID, DefaultQuantity: 2, SortOrder: 0}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if store.supersedeCalls != 1 || store.lastSuperseded != current.ID {
		t.Errorf("supersede calls = %d id = %s, want one for %s", store.supersedeCalls, store.lastSuperseded, current.ID)
	}
	if store.lastCreate.Version != 2 {
		t.Errorf("new version = %d, want 2", store.lastCreate.Version)
	}
	if store.lastCreate.Name != current.Name || store.lastCreate.PatientID != current.PatientID {
		t.Errorf("new version kept identity = %+v", store.lastCreate)
	}
	if template.Version != 2 {
		t.Errorf("template version = %d, want 2", template.Version)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "template.updated" {
		t.Fatalf("audit events = %+v, want one template.updated", recorder.events)
	}
	if recorder.events[0].Metadata["previous_version"] != 1 {
		t.Errorf("previous_version = %v, want 1", recorder.events[0].Metadata["previous_version"])
	}
}

func TestServiceUpdateRejectsNonActiveVersion(t *testing.T) {
	current := Template{ID: uuid.New(), TenantID: uuid.New(), PatientID: uuid.New(), Name: "Morgenpflege", Version: 1, Status: "superseded"}
	store := newFakeStore()
	store.get = current
	store.getErr = nil
	service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

	_, err := service.Update(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "chef"}, "", current.ID, UpdateInput{
		Activities: []ActivityInput{{ActivityID: uuid.New(), DefaultQuantity: 1}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if store.supersedeCalls != 0 || store.createCalls != 0 {
		t.Error("store was mutated for a non-active version")
	}
}

func TestServiceUpdateRequiresPatientAccess(t *testing.T) {
	current := Template{ID: uuid.New(), TenantID: uuid.New(), PatientID: uuid.New(), Name: "Morgenpflege", Version: 1, Status: "active"}
	store := newFakeStore()
	store.get = current
	store.getErr = nil
	recorder := &fakeAudit{}
	service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: false}, recorder)

	_, err := service.Update(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "employee"}, "req", current.ID, UpdateInput{
		Activities: []ActivityInput{{ActivityID: uuid.New(), DefaultQuantity: 1}},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if store.supersedeCalls != 0 || store.createCalls != 0 {
		t.Error("store was mutated for an unassigned employee")
	}
}

func TestServiceArchive(t *testing.T) {
	current := Template{ID: uuid.New(), TenantID: uuid.New(), PatientID: uuid.New(), Name: "Morgenpflege", Version: 2, Status: "active"}

	t.Run("chef archives the active version", func(t *testing.T) {
		store := newFakeStore()
		store.get = current
		store.getErr = nil
		store.archiveResult = true
		recorder := &fakeAudit{}
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, recorder)

		if err := service.Archive(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "chef"}, "req", current.ID); err != nil {
			t.Fatalf("Archive: %v", err)
		}
		if store.archiveCalls != 1 || store.lastArchived != current.ID {
			t.Errorf("archive calls = %d id = %s", store.archiveCalls, store.lastArchived)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "template.archived" {
			t.Fatalf("audit events = %+v, want one template.archived", recorder.events)
		}
	})

	t.Run("archiving a non-active version conflicts", func(t *testing.T) {
		superseded := current
		superseded.Status = "superseded"
		store := newFakeStore()
		store.get = superseded
		store.getErr = nil
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		if err := service.Archive(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "chef"}, "", current.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if store.archiveCalls != 0 {
			t.Error("store archive was called for a non-active version")
		}
	})

	t.Run("concurrent archive change conflicts", func(t *testing.T) {
		store := newFakeStore()
		store.get = current
		store.getErr = nil
		store.archiveResult = false
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		if err := service.Archive(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "chef"}, "", current.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("platform admin is forbidden", func(t *testing.T) {
		store := newFakeStore()
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		if err := service.Archive(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "platform_admin"}, "", current.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.archiveCalls != 0 {
			t.Error("store archive was called for a forbidden actor")
		}
	})
}

func TestServiceGetAccess(t *testing.T) {
	current := Template{ID: uuid.New(), TenantID: uuid.New(), PatientID: uuid.New(), Name: "Morgenpflege", Version: 1, Status: "active"}

	t.Run("platform admin is forbidden", func(t *testing.T) {
		store := newFakeStore()
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		_, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "platform_admin"}, "req", current.ID)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("unassigned employee gets not found", func(t *testing.T) {
		store := newFakeStore()
		store.get = current
		store.getErr = nil
		recorder := &fakeAudit{}
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: false}, recorder)

		_, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "employee"}, "req", current.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
			t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
		}
	})

	t.Run("assigned employee gets the template with activities", func(t *testing.T) {
		store := newFakeStore()
		store.get = current
		store.getErr = nil
		store.activities = []TemplateActivity{{ID: uuid.New(), TemplateID: current.ID, ActivityID: uuid.New(), DefaultQuantity: 1}}
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		template, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: current.TenantID, Role: "employee"}, "", current.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(template.Activities) != 1 {
			t.Fatalf("activities = %d, want 1", len(template.Activities))
		}
	})
}

func TestServiceListByPatientAccess(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()

	t.Run("chef lists templates", func(t *testing.T) {
		store := newFakeStore()
		store.listed = []Template{{ID: uuid.New(), PatientID: patientID, Name: "Morgenpflege", Status: "active"}}
		service := newTemplateService(store, &fakeCatalog{}, &fakeAccess{allowed: false}, &fakeAudit{})

		templates, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", patientID)
		if err != nil {
			t.Fatalf("ListByPatient: %v", err)
		}
		if len(templates) != 1 {
			t.Fatalf("templates = %d, want 1", len(templates))
		}
	})

	t.Run("unassigned employee gets not found", func(t *testing.T) {
		service := newTemplateService(newFakeStore(), &fakeCatalog{}, &fakeAccess{allowed: false}, &fakeAudit{})

		if _, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", patientID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("platform admin is forbidden", func(t *testing.T) {
		service := newTemplateService(newFakeStore(), &fakeCatalog{}, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}, "", patientID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}
