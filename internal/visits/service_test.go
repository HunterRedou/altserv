package visits

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeStore struct {
	patientExists bool
	patientErr    error
	patientCalls  int

	created       Visit
	createErr     error
	createCalls   int
	lastCreate    CreateInput
	lastCreatedBy uuid.UUID

	get      Visit
	getErr   error
	getCalls int

	listed    []Visit
	listedErr error
	lastLimit int

	updated          Visit
	updateErr        error
	updateCalls      int
	lastEmployee     uuid.UUID
	lastPlannedStart time.Time

	started            Visit
	startErr           error
	startCalls         int
	lastActualEmployee uuid.UUID

	completed     Visit
	completeErr   error
	completeCalls int

	cancelled   Visit
	cancelErr   error
	cancelCalls int

	corrected        Visit
	correctErr       error
	correctCalls     int
	lastCorrectStart *time.Time
	lastCorrectEnd   *time.Time

	roles   map[uuid.UUID]string
	roleErr error
}

func (f *fakeStore) PatientExists(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error) {
	f.patientCalls++
	return f.patientExists, f.patientErr
}

func (f *fakeStore) Create(_ context.Context, _ *db.Queries, _ uuid.UUID, createdBy uuid.UUID, input CreateInput) (Visit, error) {
	f.createCalls++
	f.lastCreatedBy = createdBy
	f.lastCreate = input
	return f.created, f.createErr
}

func (f *fakeStore) Get(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Visit, error) {
	f.getCalls++
	return f.get, f.getErr
}

func (f *fakeStore) ListByPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) ([]Visit, error) {
	f.lastLimit = maxVisitsPerPatient
	return f.listed, f.listedErr
}

func (f *fakeStore) UpdateSchedule(_ context.Context, _ *db.Queries, _ uuid.UUID, _ uuid.UUID, employeeID uuid.UUID, plannedStart time.Time) (Visit, error) {
	f.updateCalls++
	f.lastEmployee = employeeID
	f.lastPlannedStart = plannedStart
	return f.updated, f.updateErr
}

func (f *fakeStore) Start(_ context.Context, _ *db.Queries, _ uuid.UUID, _ uuid.UUID, actualEmployeeID uuid.UUID) (Visit, error) {
	f.startCalls++
	f.lastActualEmployee = actualEmployeeID
	return f.started, f.startErr
}

func (f *fakeStore) Complete(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Visit, error) {
	f.completeCalls++
	return f.completed, f.completeErr
}

func (f *fakeStore) Cancel(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Visit, error) {
	f.cancelCalls++
	return f.cancelled, f.cancelErr
}

func (f *fakeStore) Correct(_ context.Context, _ *db.Queries, _ uuid.UUID, _ uuid.UUID, actualStart, actualEnd *time.Time) (Visit, error) {
	f.correctCalls++
	f.lastCorrectStart = actualStart
	f.lastCorrectEnd = actualEnd
	return f.corrected, f.correctErr
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

func newVisitService(store *fakeStore, access AccessChecker, recorder audit.Recorder) *Service {
	return NewService(store, recorder, access)
}

func validCreate(patientID, employeeID uuid.UUID) CreateInput {
	return CreateInput{
		PatientID:    patientID,
		EmployeeID:   employeeID,
		PlannedStart: time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC),
	}
}

func TestServiceCreateRequiresChef(t *testing.T) {
	for _, role := range []string{"employee", "platform_admin", ""} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := newVisitService(store, &fakeAccess{allowed: true}, recorder)
			tenantID := uuid.New()

			_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}, "req", validCreate(uuid.New(), uuid.New()))
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
	patientID := uuid.New()
	employeeID := uuid.New()
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	tests := []struct {
		name  string
		input CreateInput
	}{
		{name: "missing patient", input: CreateInput{EmployeeID: employeeID, PlannedStart: time.Now()}},
		{name: "missing employee", input: CreateInput{PatientID: patientID, PlannedStart: time.Now()}},
		{name: "missing planned start", input: CreateInput{PatientID: patientID, EmployeeID: employeeID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{patientExists: true}
			service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})
			if _, err := service.Create(context.Background(), nil, actor, "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if store.createCalls != 0 {
				t.Error("store was called for invalid input")
			}
		})
	}
}

func TestServiceCreateRejectsMissingPatient(t *testing.T) {
	store := &fakeStore{patientExists: false}
	service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", validCreate(uuid.New(), uuid.New()))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called for a missing patient")
	}
}

func TestServiceCreateValidatesPlannedEmployee(t *testing.T) {
	patientID := uuid.New()
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	t.Run("unknown employee", func(t *testing.T) {
		store := &fakeStore{patientExists: true, roles: map[uuid.UUID]string{}}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, actor, "", validCreate(patientID, uuid.New())); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("non care worker", func(t *testing.T) {
		employeeID := uuid.New()
		store := &fakeStore{patientExists: true, roles: map[uuid.UUID]string{employeeID: "platform_admin"}}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, actor, "", validCreate(patientID, employeeID)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("employee not assigned", func(t *testing.T) {
		employeeID := uuid.New()
		store := &fakeStore{patientExists: true, roles: map[uuid.UUID]string{employeeID: "employee"}}
		service := newVisitService(store, &fakeAccess{allowed: false}, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, actor, "", validCreate(patientID, employeeID)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.createCalls != 0 {
			t.Error("store was called for an unassigned planned employee")
		}
	})

	t.Run("chef needs no assignment", func(t *testing.T) {
		chefID := uuid.New()
		store := &fakeStore{
			patientExists: true,
			roles:         map[uuid.UUID]string{chefID: "chef"},
			created:       Visit{ID: uuid.New(), PatientID: patientID, EmployeeID: chefID, Status: "planned"},
		}
		access := &fakeAccess{allowed: false}
		service := newVisitService(store, access, &fakeAudit{})

		if _, err := service.Create(context.Background(), nil, actor, "", validCreate(patientID, chefID)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if access.calls != 0 {
			t.Error("chef planned employee consulted the assignment checker")
		}
	})
}

func TestServiceCreateRecordsAudit(t *testing.T) {
	patientID := uuid.New()
	employeeID := uuid.New()
	tenantID := uuid.New()
	userID := uuid.New()
	planned := time.Date(2026, 2, 3, 7, 30, 0, 0, time.UTC)

	store := &fakeStore{
		patientExists: true,
		roles:         map[uuid.UUID]string{employeeID: "employee"},
		created:       Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID, PlannedStart: planned, Status: "planned"},
	}
	recorder := &fakeAudit{}
	service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

	visit, err := service.Create(context.Background(), nil, Actor{UserID: userID, TenantID: tenantID, Role: "chef"}, "req", validCreate(patientID, employeeID))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if visit.ID == uuid.Nil {
		t.Fatal("created visit has no id")
	}
	if store.lastCreatedBy != userID {
		t.Errorf("created by = %s, want %s", store.lastCreatedBy, userID)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "visit.created" {
		t.Fatalf("audit events = %+v, want one visit.created", recorder.events)
	}
	if recorder.events[0].Metadata["employee_id"] != employeeID {
		t.Errorf("audit employee_id = %v, want %s", recorder.events[0].Metadata["employee_id"], employeeID)
	}
}

func TestServiceGetAccess(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()
	current := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Status: "planned"}

	t.Run("platform admin is forbidden", func(t *testing.T) {
		service := newVisitService(&fakeStore{get: current}, &fakeAccess{allowed: true}, &fakeAudit{})
		if _, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}, "req", current.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("unassigned employee gets not found", func(t *testing.T) {
		recorder := &fakeAudit{}
		service := newVisitService(&fakeStore{get: current}, &fakeAccess{allowed: false}, recorder)
		if _, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "req", current.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
			t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
		}
	})

	t.Run("assigned employee gets the visit", func(t *testing.T) {
		service := newVisitService(&fakeStore{get: current}, &fakeAccess{allowed: true}, &fakeAudit{})
		visit, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", current.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if visit.ID != current.ID {
			t.Errorf("visit id = %s, want %s", visit.ID, current.ID)
		}
	})
}

func TestServiceListByPatient(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()

	t.Run("chef lists", func(t *testing.T) {
		store := &fakeStore{listed: []Visit{{ID: uuid.New(), PatientID: patientID}}}
		service := newVisitService(store, &fakeAccess{allowed: false}, &fakeAudit{})
		visits, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", patientID)
		if err != nil {
			t.Fatalf("ListByPatient: %v", err)
		}
		if len(visits) != 1 || store.lastLimit != maxVisitsPerPatient {
			t.Errorf("visits = %d limit = %d", len(visits), store.lastLimit)
		}
	})

	t.Run("unassigned employee gets not found", func(t *testing.T) {
		service := newVisitService(&fakeStore{}, &fakeAccess{allowed: false}, &fakeAudit{})
		if _, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", patientID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("platform admin is forbidden", func(t *testing.T) {
		service := newVisitService(&fakeStore{}, &fakeAccess{allowed: true}, &fakeAudit{})
		if _, err := service.ListByPatient(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}, "", patientID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

func TestServiceReschedule(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()
	employeeID := uuid.New()
	current := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID, PlannedStart: time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC), Status: "planned"}

	t.Run("chef moves a planned visit", func(t *testing.T) {
		newStart := time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)
		store := &fakeStore{
			get:     current,
			roles:   map[uuid.UUID]string{employeeID: "employee"},
			updated: Visit{ID: current.ID, EmployeeID: employeeID, PlannedStart: newStart, Status: "planned"},
		}
		recorder := &fakeAudit{}
		service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

		visit, err := service.Reschedule(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req", current.ID, ScheduleInput{PlannedStart: &newStart})
		if err != nil {
			t.Fatalf("Reschedule: %v", err)
		}
		if !store.lastPlannedStart.Equal(newStart) || store.lastEmployee != employeeID {
			t.Errorf("schedule = %v employee %s", store.lastPlannedStart, store.lastEmployee)
		}
		if visit.PlannedStart != newStart {
			t.Errorf("visit planned start = %v, want %v", visit.PlannedStart, newStart)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "visit.rescheduled" {
			t.Fatalf("audit events = %+v, want one visit.rescheduled", recorder.events)
		}
	})

	t.Run("only planned visits can move", func(t *testing.T) {
		started := current
		started.Status = "in_progress"
		store := &fakeStore{get: started}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Reschedule(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", current.ID, ScheduleInput{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if store.updateCalls != 0 {
			t.Error("store update was called for a started visit")
		}
	})

	t.Run("employee cannot reschedule", func(t *testing.T) {
		store := &fakeStore{get: current}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})
		if _, err := service.Reschedule(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", current.ID, ScheduleInput{}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

func TestServiceStart(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()
	plannedEmployee := uuid.New()
	current := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, EmployeeID: plannedEmployee, Status: "planned"}

	t.Run("planned employee starts their visit", func(t *testing.T) {
		actorID := plannedEmployee
		store := &fakeStore{get: current, started: Visit{ID: current.ID, Status: "in_progress"}}
		recorder := &fakeAudit{}
		service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

		if _, err := service.Start(context.Background(), nil, Actor{UserID: actorID, TenantID: tenantID, Role: "employee"}, "req", current.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if store.lastActualEmployee != actorID {
			t.Errorf("actual employee = %s, want %s", store.lastActualEmployee, actorID)
		}
		if recorder.events[0].Metadata["substitution"] != false {
			t.Errorf("substitution = %v, want false", recorder.events[0].Metadata["substitution"])
		}
	})

	t.Run("assigned substitute starts and is recorded", func(t *testing.T) {
		substitute := uuid.New()
		store := &fakeStore{get: current, started: Visit{ID: current.ID, Status: "in_progress"}}
		recorder := &fakeAudit{}
		service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

		if _, err := service.Start(context.Background(), nil, Actor{UserID: substitute, TenantID: tenantID, Role: "employee"}, "req", current.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if store.lastActualEmployee != substitute {
			t.Errorf("actual employee = %s, want the substitute %s", store.lastActualEmployee, substitute)
		}
		if recorder.events[0].Metadata["substitution"] != true {
			t.Errorf("substitution = %v, want true", recorder.events[0].Metadata["substitution"])
		}
	})

	t.Run("unassigned employee is forbidden", func(t *testing.T) {
		store := &fakeStore{get: current}
		service := newVisitService(store, &fakeAccess{allowed: false}, &fakeAudit{})

		if _, err := service.Start(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", current.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.startCalls != 0 {
			t.Error("store start was called for an unassigned employee")
		}
	})

	t.Run("only planned visits can start", func(t *testing.T) {
		completed := current
		completed.Status = "completed"
		store := &fakeStore{get: completed}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Start(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", current.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("chef starts without an assignment", func(t *testing.T) {
		store := &fakeStore{get: current, started: Visit{ID: current.ID, Status: "in_progress"}}
		access := &fakeAccess{allowed: false}
		service := newVisitService(store, access, &fakeAudit{})
		chefID := uuid.New()

		if _, err := service.Start(context.Background(), nil, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "", current.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if store.lastActualEmployee != chefID || access.calls != 0 {
			t.Errorf("actual employee = %s access calls = %d", store.lastActualEmployee, access.calls)
		}
	})
}

func TestServiceComplete(t *testing.T) {
	patientID := uuid.New()
	tenantID := uuid.New()
	actual := uuid.New()
	current := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, ActualEmployeeID: &actual, Status: "in_progress"}

	t.Run("actual employee completes", func(t *testing.T) {
		store := &fakeStore{get: current, completed: Visit{ID: current.ID, Status: "completed"}}
		recorder := &fakeAudit{}
		service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

		if _, err := service.Complete(context.Background(), nil, Actor{UserID: actual, TenantID: tenantID, Role: "employee"}, "req", current.ID); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if store.completeCalls != 1 {
			t.Errorf("complete calls = %d, want 1", store.completeCalls)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "visit.completed" {
			t.Fatalf("audit events = %+v, want one visit.completed", recorder.events)
		}
	})

	t.Run("another employee is forbidden", func(t *testing.T) {
		store := &fakeStore{get: current}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Complete(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", current.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if store.completeCalls != 0 {
			t.Error("store complete was called for the wrong employee")
		}
	})

	t.Run("chef may complete", func(t *testing.T) {
		store := &fakeStore{get: current, completed: Visit{ID: current.ID, Status: "completed"}}
		service := newVisitService(store, &fakeAccess{allowed: false}, &fakeAudit{})

		if _, err := service.Complete(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", current.ID); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	})

	t.Run("only in-progress visits complete", func(t *testing.T) {
		planned := current
		planned.Status = "planned"
		store := &fakeStore{get: planned}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Complete(context.Background(), nil, Actor{UserID: actual, TenantID: tenantID, Role: "employee"}, "", current.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestServiceCancel(t *testing.T) {
	tenantID := uuid.New()

	for _, status := range []string{"planned", "in_progress"} {
		t.Run("chef cancels "+status, func(t *testing.T) {
			current := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: uuid.New(), Status: status}
			store := &fakeStore{get: current, cancelled: Visit{ID: current.ID, Status: "cancelled"}}
			recorder := &fakeAudit{}
			service := newVisitService(store, &fakeAccess{allowed: true}, recorder)

			if _, err := service.Cancel(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req", current.ID); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "visit.cancelled" {
				t.Fatalf("audit events = %+v, want one visit.cancelled", recorder.events)
			}
		})
	}

	t.Run("completed visits cannot be cancelled", func(t *testing.T) {
		current := Visit{ID: uuid.New(), TenantID: tenantID, Status: "completed"}
		store := &fakeStore{get: current}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Cancel(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", current.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if store.cancelCalls != 0 {
			t.Error("store cancel was called for a completed visit")
		}
	})

	t.Run("employee cannot cancel", func(t *testing.T) {
		current := Visit{ID: uuid.New(), TenantID: tenantID, Status: "planned"}
		service := newVisitService(&fakeStore{get: current}, &fakeAccess{allowed: true}, &fakeAudit{})

		if _, err := service.Cancel(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", current.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}

func TestServiceCorrect(t *testing.T) {
	tenantID := uuid.New()
	patientID := uuid.New()
	start := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	end := start.Add(45 * time.Minute)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}

	completed := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Status: "completed", ActualStart: &start, ActualEnd: &end}
	inProgress := Visit{ID: uuid.New(), TenantID: tenantID, PatientID: patientID, Status: "in_progress", ActualStart: &start}

	t.Run("chef corrects a completed visit with a reason", func(t *testing.T) {
		newEnd := start.Add(30 * time.Minute)
		store := &fakeStore{
			get:       completed,
			corrected: Visit{ID: completed.ID, Status: "completed", ActualStart: &start, ActualEnd: &newEnd},
		}
		recorder := &fakeAudit{}
		service := newVisitService(store, &fakeAccess{allowed: true}, recorder)
		service.now = func() time.Time { return now }

		visit, err := service.Correct(context.Background(), nil, actor, "req", completed.ID, CorrectInput{ActualEnd: &newEnd, Reason: "  end time typo  "})
		if err != nil {
			t.Fatalf("Correct: %v", err)
		}
		if store.correctCalls != 1 {
			t.Errorf("correct calls = %d, want 1", store.correctCalls)
		}
		if store.lastCorrectEnd == nil || !store.lastCorrectEnd.Equal(newEnd) {
			t.Errorf("corrected end = %v, want %v", store.lastCorrectEnd, newEnd)
		}
		if visit.ActualEnd == nil || !visit.ActualEnd.Equal(newEnd) {
			t.Errorf("visit end = %v, want %v", visit.ActualEnd, newEnd)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "visit.corrected" {
			t.Fatalf("audit events = %+v, want one visit.corrected", recorder.events)
		}
		if recorder.events[0].Metadata["reason"] != "end time typo" {
			t.Errorf("reason = %v, want the trimmed reason", recorder.events[0].Metadata["reason"])
		}
	})

	t.Run("reason is required", func(t *testing.T) {
		store := &fakeStore{get: completed}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})
		if _, err := service.Correct(context.Background(), nil, actor, "", completed.ID, CorrectInput{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.correctCalls != 0 {
			t.Error("store correct was called without a reason")
		}
	})

	t.Run("reason must not be too long", func(t *testing.T) {
		service := newVisitService(&fakeStore{get: completed}, &fakeAccess{allowed: true}, &fakeAudit{})
		_, err := service.Correct(context.Background(), nil, actor, "", completed.ID, CorrectInput{Reason: strings.Repeat("a", maxCorrectionReasonRunes+1)})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("only a started visit can be corrected", func(t *testing.T) {
		planned := Visit{ID: uuid.New(), TenantID: tenantID, Status: "planned"}
		store := &fakeStore{get: planned}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})
		if _, err := service.Correct(context.Background(), nil, actor, "", planned.ID, CorrectInput{Reason: "x"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("in-progress visits cannot set an end time", func(t *testing.T) {
		service := newVisitService(&fakeStore{get: inProgress}, &fakeAccess{allowed: true}, &fakeAudit{})
		service.now = func() time.Time { return now }
		if _, err := service.Correct(context.Background(), nil, actor, "", inProgress.ID, CorrectInput{ActualEnd: &end, Reason: "x"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("in-progress visits can correct the start", func(t *testing.T) {
		newStart := start.Add(-10 * time.Minute)
		store := &fakeStore{get: inProgress, corrected: Visit{ID: inProgress.ID, Status: "in_progress", ActualStart: &newStart}}
		service := newVisitService(store, &fakeAccess{allowed: true}, &fakeAudit{})
		service.now = func() time.Time { return now }
		if _, err := service.Correct(context.Background(), nil, actor, "", inProgress.ID, CorrectInput{ActualStart: &newStart, Reason: "started earlier"}); err != nil {
			t.Fatalf("Correct: %v", err)
		}
	})

	t.Run("end before start is invalid", func(t *testing.T) {
		early := start.Add(-time.Minute)
		service := newVisitService(&fakeStore{get: completed}, &fakeAccess{allowed: true}, &fakeAudit{})
		service.now = func() time.Time { return now }
		if _, err := service.Correct(context.Background(), nil, actor, "", completed.ID, CorrectInput{ActualEnd: &early, Reason: "x"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("future times are invalid", func(t *testing.T) {
		future := now.Add(time.Hour)
		service := newVisitService(&fakeStore{get: inProgress}, &fakeAccess{allowed: true}, &fakeAudit{})
		service.now = func() time.Time { return now }
		if _, err := service.Correct(context.Background(), nil, actor, "", inProgress.ID, CorrectInput{ActualStart: &future, Reason: "x"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("employee cannot correct", func(t *testing.T) {
		service := newVisitService(&fakeStore{get: completed}, &fakeAccess{allowed: true}, &fakeAudit{})
		_, err := service.Correct(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", completed.ID, CorrectInput{Reason: "x"})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}
