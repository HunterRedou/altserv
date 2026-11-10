package activities

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
	activity      Activity
	createErr     error
	createCalls   int
	lastCreatedBy uuid.UUID
	lastInput     CreateInput
	lastSnapshot  Snapshot

	getActivity Activity
	getErr      error

	byPatient    []Activity
	byPatientErr error

	all    []Activity
	allErr error

	roles   map[uuid.UUID]string
	roleErr error
}

func (f *fakeStore) Create(_ context.Context, _ *db.Queries, _ uuid.UUID, createdBy uuid.UUID, input CreateInput, snapshot Snapshot) (Activity, error) {
	f.createCalls++
	f.lastCreatedBy = createdBy
	f.lastInput = input
	f.lastSnapshot = snapshot
	return f.activity, f.createErr
}

func (f *fakeStore) Get(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Activity, error) {
	return f.getActivity, f.getErr
}

func (f *fakeStore) ListByPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) ([]Activity, error) {
	return f.byPatient, f.byPatientErr
}

func (f *fakeStore) List(context.Context, *db.Queries, uuid.UUID, int) ([]Activity, error) {
	return f.all, f.allErr
}

func (f *fakeStore) GetUserRole(_ context.Context, _ *db.Queries, _ uuid.UUID, userID uuid.UUID) (string, error) {
	if f.roleErr != nil {
		return "", f.roleErr
	}
	role, ok := f.roles[userID]
	if !ok {
		return "", ErrNotFound
	}
	return role, nil
}

type fakeCatalog struct {
	definitions map[uuid.UUID]catalog.Definition
	contracts   map[uuid.UUID]catalog.Contract
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

func (f *fakeCatalog) GetContract(_ context.Context, _ *db.Queries, _ uuid.UUID, id uuid.UUID) (catalog.Contract, error) {
	if f.err != nil {
		return catalog.Contract{}, f.err
	}
	contract, ok := f.contracts[id]
	if !ok {
		return catalog.Contract{}, catalog.ErrNotFound
	}
	return contract, nil
}

type fakeAccess struct {
	allowed bool
	err     error
}

func (f *fakeAccess) CanAccessPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return f.allowed, f.err
}

type fakeAudit struct {
	events []audit.Event
}

func (f *fakeAudit) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	f.events = append(f.events, event)
	return nil
}

func validInput(patientID uuid.UUID) CreateInput {
	return CreateInput{
		PatientID:       patientID,
		PerformedAt:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		DurationMinutes: 30,
	}
}

func TestServiceCreateValidation(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	service := NewService(&fakeStore{}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
	service.now = func() time.Time { return now }
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	tests := []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{name: "missing patient", mutate: func(i *CreateInput) { i.PatientID = uuid.Nil }},
		{name: "missing performed at", mutate: func(i *CreateInput) { i.PerformedAt = time.Time{} }},
		{name: "future performed at", mutate: func(i *CreateInput) { i.PerformedAt = now.Add(time.Hour) }},
		{name: "zero duration", mutate: func(i *CreateInput) { i.DurationMinutes = 0 }},
		{name: "duration too long", mutate: func(i *CreateInput) { i.DurationMinutes = 1441 }},
		{name: "note too long", mutate: func(i *CreateInput) { i.Note = strings.Repeat("a", maxNoteRunes+1) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validInput(uuid.New())
			tt.mutate(&input)
			if _, err := service.Create(context.Background(), nil, actor, "", input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestServiceCreateEmployeeRestrictions(t *testing.T) {
	tenantID, employeeID, patientID := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("employee records only their own work", func(t *testing.T) {
		store := &fakeStore{roles: map[uuid.UUID]string{}}
		service := NewService(store, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = uuid.New()
		_, err := service.Create(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", input)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.createCalls != 0 {
			t.Error("store was called for a foreign employee")
		}
	})

	t.Run("employee needs an active assignment", func(t *testing.T) {
		store := &fakeStore{roles: map[uuid.UUID]string{}}
		service := NewService(store, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: false})
		service.now = func() time.Time { return now }

		_, err := service.Create(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", validInput(patientID))
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.createCalls != 0 {
			t.Error("store was called without an assignment")
		}
	})

	t.Run("employee defaults to themselves", func(t *testing.T) {
		store := &fakeStore{
			roles:    map[uuid.UUID]string{},
			activity: Activity{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID},
		}
		service := NewService(store, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
		service.now = func() time.Time { return now }

		if _, err := service.Create(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", validInput(patientID)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if store.lastInput.EmployeeID != employeeID {
			t.Errorf("employee id = %s, want the actor %s", store.lastInput.EmployeeID, employeeID)
		}
	})
}

func TestServiceCreateChefValidatesEmployee(t *testing.T) {
	tenantID, patientID := uuid.New(), uuid.New()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}

	t.Run("missing employee", func(t *testing.T) {
		service := NewService(&fakeStore{roles: map[uuid.UUID]string{}}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		if _, err := service.Create(context.Background(), nil, actor, "", validInput(patientID)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("unknown employee", func(t *testing.T) {
		store := &fakeStore{roles: map[uuid.UUID]string{}}
		service := NewService(store, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = uuid.New()
		if _, err := service.Create(context.Background(), nil, actor, "", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("non-employee user", func(t *testing.T) {
		chefID := uuid.New()
		store := &fakeStore{roles: map[uuid.UUID]string{chefID: "chef"}}
		service := NewService(store, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = chefID
		if _, err := service.Create(context.Background(), nil, actor, "", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestServiceCreateBillableSnapshot(t *testing.T) {
	tenantID, patientID, employeeID := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	performedAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	definition := catalog.Definition{
		ID:        uuid.New(),
		Code:      "LK-001",
		Name:      "Grundpflege",
		LegalArea: "sgb_xi",
		ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	contract := catalog.Contract{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		ServiceDefinitionID: definition.ID,
		PayerName:           "AOK Brandenburg",
		ContractReference:   "V-2026-1",
		LegalArea:           "sgb_xi",
		ValidFrom:           time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	baseCatalog := func() *fakeCatalog {
		return &fakeCatalog{
			definitions: map[uuid.UUID]catalog.Definition{definition.ID: definition},
			contracts:   map[uuid.UUID]catalog.Contract{contract.ID: contract},
		}
	}

	t.Run("billable activity snapshots service and contract", func(t *testing.T) {
		store := &fakeStore{
			roles:    map[uuid.UUID]string{employeeID: "employee"},
			activity: Activity{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID},
		}
		recorder := &fakeAudit{}
		service := NewService(store, baseCatalog(), recorder, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = employeeID
		input.ServiceDefinitionID = &definition.ID
		input.ContractID = &contract.ID
		input.Note = "  Hilfe beim Anziehen  "

		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req", input); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if store.lastSnapshot.ServiceCode != definition.Code || store.lastSnapshot.ServiceName != definition.Name {
			t.Errorf("snapshot = %+v, want the definition code/name", store.lastSnapshot)
		}
		if store.lastSnapshot.PayerName != contract.PayerName || store.lastSnapshot.ContractReference != contract.ContractReference {
			t.Errorf("snapshot = %+v, want the contract data", store.lastSnapshot)
		}
		if store.lastInput.Note != "Hilfe beim Anziehen" {
			t.Errorf("note = %q, want trimmed note", store.lastInput.Note)
		}
		if len(recorder.events) != 1 {
			t.Fatalf("audit events = %d, want 1", len(recorder.events))
		}
		event := recorder.events[0]
		if event.Action != "activity.created" {
			t.Errorf("action = %q, want activity.created", event.Action)
		}
		for _, value := range event.Metadata {
			if text, ok := value.(string); ok && strings.Contains(text, "Anziehen") {
				t.Fatal("audit metadata contains the note text")
			}
		}
	})

	t.Run("only one of service and contract", func(t *testing.T) {
		store := &fakeStore{roles: map[uuid.UUID]string{employeeID: "employee"}}
		service := NewService(store, baseCatalog(), &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = employeeID
		input.ServiceDefinitionID = &definition.ID
		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("contract belongs to another service", func(t *testing.T) {
		other := definition
		other.ID = uuid.New()
		fake := baseCatalog()
		fake.definitions[other.ID] = other
		store := &fakeStore{roles: map[uuid.UUID]string{employeeID: "employee"}}
		service := NewService(store, fake, &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = employeeID
		input.ServiceDefinitionID = &other.ID
		input.ContractID = &contract.ID
		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("contract expired at performed time", func(t *testing.T) {
		expired := contract
		until := performedAt.Add(-time.Hour)
		expired.ValidUntil = &until
		fake := baseCatalog()
		fake.contracts[contract.ID] = expired
		store := &fakeStore{roles: map[uuid.UUID]string{employeeID: "employee"}}
		service := NewService(store, fake, &fakeAudit{}, &fakeAccess{})
		service.now = func() time.Time { return now }

		input := validInput(patientID)
		input.EmployeeID = employeeID
		input.ServiceDefinitionID = &definition.ID
		input.ContractID = &contract.ID
		if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestServiceActivityReadAuthorization(t *testing.T) {
	tenantID, employeeID, patientID := uuid.New(), uuid.New(), uuid.New()
	activity := Activity{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID}

	t.Run("platform admin denied", func(t *testing.T) {
		recorder := &fakeAudit{}
		service := NewService(&fakeStore{}, &fakeCatalog{}, recorder, &fakeAccess{})
		_, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}, "", activity.ID)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
			t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
		}
	})

	t.Run("employee without assignment sees not found", func(t *testing.T) {
		service := NewService(&fakeStore{getActivity: activity}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: false})
		_, err := service.Get(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", activity.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("employee with assignment can read", func(t *testing.T) {
		service := NewService(&fakeStore{getActivity: activity}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
		got, err := service.Get(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", activity.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != activity.ID {
			t.Errorf("id = %s, want %s", got.ID, activity.ID)
		}
	})

	t.Run("employee cannot list all tenant activities", func(t *testing.T) {
		service := NewService(&fakeStore{}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
		if _, err := service.List(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("employee list by patient is assignment scoped", func(t *testing.T) {
		service := NewService(&fakeStore{byPatient: []Activity{activity}}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{allowed: true})
		got, err := service.ListByPatient(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", patientID)
		if err != nil {
			t.Fatalf("ListByPatient: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("activities = %d, want 1", len(got))
		}
	})

	t.Run("chef can list all tenant activities", func(t *testing.T) {
		service := NewService(&fakeStore{all: []Activity{activity}}, &fakeCatalog{}, &fakeAudit{}, &fakeAccess{})
		got, err := service.List(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("activities = %d, want 1", len(got))
		}
	})
}
