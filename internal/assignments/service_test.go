package assignments

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeStore struct {
	createAssignment Assignment
	createErr        error
	createCalls      int
	lastCreate       CreateParams

	revokeAssignment Assignment
	revokeErr        error
	revokeCalls      int

	listByPatient    []Assignment
	listByPatientErr error

	listByTenant    []Assignment
	listByTenantErr error

	assigned      bool
	assignedErr   error
	assignedCalls int

	role      string
	roleErr   error
	roleCalls int
}

func (f *fakeStore) Create(_ context.Context, _ *db.Queries, params CreateParams) (Assignment, error) {
	f.createCalls++
	f.lastCreate = params
	return f.createAssignment, f.createErr
}

func (f *fakeStore) Revoke(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Assignment, error) {
	f.revokeCalls++
	return f.revokeAssignment, f.revokeErr
}

func (f *fakeStore) ListByPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Assignment, error) {
	return f.listByPatient, f.listByPatientErr
}

func (f *fakeStore) ListByTenant(context.Context, *db.Queries, uuid.UUID) ([]Assignment, error) {
	return f.listByTenant, f.listByTenantErr
}

func (f *fakeStore) IsAssigned(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	f.assignedCalls++
	return f.assigned, f.assignedErr
}

func (f *fakeStore) GetUserRole(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (string, error) {
	f.roleCalls++
	return f.role, f.roleErr
}

type fakeAudit struct {
	events []audit.Event
	err    error
}

func (f *fakeAudit) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	f.events = append(f.events, event)
	return f.err
}

func newTestService(store Store, recorder audit.Recorder, now time.Time) *Service {
	service := NewService(store, recorder)
	service.now = func() time.Time { return now }
	return service
}

func TestCreateAssignment(t *testing.T) {
	tenantID, chefID, employeeID, patientID, assignmentID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := now.Add(24 * time.Hour)

	store := &fakeStore{
		role: "employee",
		createAssignment: Assignment{
			ID:             assignmentID,
			TenantID:       tenantID,
			PatientID:      patientID,
			EmployeeID:     employeeID,
			AssignedBy:     chefID,
			AssignmentType: "substitute",
			ValidFrom:      now,
			ValidUntil:     &validUntil,
		},
	}
	recorder := &fakeAudit{}
	service := newTestService(store, recorder, now)

	assignment, err := service.Create(context.Background(), nil, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "req-1", patientID, CreateInput{
		EmployeeID:     employeeID,
		AssignmentType: "substitute",
		ValidFrom:      now,
		ValidUntil:     &validUntil,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if assignment.ID != assignmentID {
		t.Errorf("assignment ID = %s, want %s", assignment.ID, assignmentID)
	}
	if store.createCalls != 1 {
		t.Errorf("store.Create calls = %d, want 1", store.createCalls)
	}
	if store.lastCreate.TenantID != tenantID || store.lastCreate.PatientID != patientID {
		t.Errorf("create params = %+v", store.lastCreate)
	}
	if store.lastCreate.EmployeeID != employeeID || store.lastCreate.AssignedBy != chefID {
		t.Errorf("create params = %+v", store.lastCreate)
	}
	if store.lastCreate.AssignmentType != "substitute" {
		t.Errorf("assignment type = %q, want substitute", store.lastCreate.AssignmentType)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "assignment.created" {
		t.Errorf("action = %q, want assignment.created", event.Action)
	}
	if event.ActorUserID != chefID || event.TenantID != tenantID || event.EntityID != assignmentID {
		t.Errorf("audit event identity is wrong: %+v", event)
	}
	if event.RequestID != "req-1" {
		t.Errorf("request id = %q, want req-1", event.RequestID)
	}
	if event.Metadata["patient_id"] != patientID.String() || event.Metadata["employee_id"] != employeeID.String() {
		t.Errorf("audit metadata = %+v", event.Metadata)
	}
}

func TestCreateAssignmentDefaults(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{role: "employee", createAssignment: Assignment{ID: uuid.New()}}
	service := newTestService(store, &fakeAudit{}, now)

	if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", uuid.New(), CreateInput{
		EmployeeID: uuid.New(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.lastCreate.AssignmentType != "regular" {
		t.Errorf("assignment type = %q, want regular", store.lastCreate.AssignmentType)
	}
	if !store.lastCreate.ValidFrom.Equal(now) {
		t.Errorf("valid_from = %v, want %v", store.lastCreate.ValidFrom, now)
	}
}

func TestCreateAssignmentForbiddenRoles(t *testing.T) {
	for _, role := range []string{"employee", "platform_admin", "unknown"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewService(store, recorder)

			_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: role}, "req", uuid.New(), CreateInput{EmployeeID: uuid.New()})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.createCalls != 0 {
				t.Error("store was called for a forbidden request")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestCreateAssignmentValidation(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntilBefore := now.Add(-time.Hour)

	tests := []struct {
		name      string
		patientID uuid.UUID
		input     CreateInput
	}{
		{name: "missing patient", patientID: uuid.Nil, input: CreateInput{EmployeeID: uuid.New()}},
		{name: "missing employee", patientID: uuid.New(), input: CreateInput{}},
		{name: "invalid type", patientID: uuid.New(), input: CreateInput{EmployeeID: uuid.New(), AssignmentType: "permanent"}},
		{name: "valid until before from", patientID: uuid.New(), input: CreateInput{EmployeeID: uuid.New(), ValidFrom: now, ValidUntil: &validUntilBefore}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{role: "employee"}
			service := newTestService(store, &fakeAudit{}, now)

			_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", tt.patientID, tt.input)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if store.createCalls != 0 {
				t.Error("store was called for invalid input")
			}
		})
	}
}

func TestCreateAssignmentTargetMustBeEmployee(t *testing.T) {
	store := &fakeStore{role: "chef"}
	service := newTestService(store, &fakeAudit{}, time.Now())

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", uuid.New(), CreateInput{EmployeeID: uuid.New()})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called for a non-employee target")
	}
}

func TestCreateAssignmentEmployeeMustBeInTenant(t *testing.T) {
	store := &fakeStore{roleErr: sql.ErrNoRows}
	service := newTestService(store, &fakeAudit{}, time.Now())

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", uuid.New(), CreateInput{EmployeeID: uuid.New()})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called for a foreign employee")
	}
}

func TestCreateAssignmentFailsWhenAuditFails(t *testing.T) {
	store := &fakeStore{role: "employee", createAssignment: Assignment{ID: uuid.New()}}
	service := newTestService(store, &fakeAudit{err: errors.New("audit down")}, time.Now())

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", uuid.New(), CreateInput{EmployeeID: uuid.New()})
	if err == nil {
		t.Fatal("Create returned nil when audit failed")
	}
}

func TestRevokeAssignment(t *testing.T) {
	tenantID, chefID, assignmentID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeStore{revokeAssignment: Assignment{ID: assignmentID, TenantID: tenantID}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	assignment, err := service.Revoke(context.Background(), nil, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "req-revoke", assignmentID)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if assignment.ID != assignmentID {
		t.Errorf("assignment ID = %s, want %s", assignment.ID, assignmentID)
	}
	if store.revokeCalls != 1 {
		t.Errorf("store.Revoke calls = %d, want 1", store.revokeCalls)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "assignment.revoked" {
		t.Fatalf("audit events = %+v, want one assignment.revoked", recorder.events)
	}
}

func TestRevokeAssignmentForbidden(t *testing.T) {
	store := &fakeStore{}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	_, err := service.Revoke(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}, "req", uuid.New())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if store.revokeCalls != 0 {
		t.Error("store was called for a forbidden request")
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
	}
}

func TestRevokeAssignmentNotFound(t *testing.T) {
	store := &fakeStore{revokeErr: ErrNotFound}
	service := NewService(store, &fakeAudit{})

	_, err := service.Revoke(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListAssignmentsForbidden(t *testing.T) {
	service := NewService(&fakeStore{}, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}

	if _, err := service.ListByTenant(context.Background(), nil, actor); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListByTenant err = %v, want ErrForbidden", err)
	}
	if _, err := service.ListByPatient(context.Background(), nil, actor, uuid.New()); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListByPatient err = %v, want ErrForbidden", err)
	}
}

func TestCanAccessPatient(t *testing.T) {
	tenantID, employeeID, patientID := uuid.New(), uuid.New(), uuid.New()

	t.Run("missing ids are denied without a query", func(t *testing.T) {
		store := &fakeStore{assigned: true}
		service := NewService(store, &fakeAudit{})

		allowed, err := service.CanAccessPatient(context.Background(), nil, uuid.Nil, employeeID, patientID)
		if err != nil {
			t.Fatalf("CanAccessPatient: %v", err)
		}
		if allowed {
			t.Error("missing tenant id was allowed")
		}
		if store.assignedCalls != 0 {
			t.Error("store was queried for missing ids")
		}
	})

	t.Run("delegates to the store", func(t *testing.T) {
		store := &fakeStore{assigned: true}
		service := NewService(store, &fakeAudit{})

		allowed, err := service.CanAccessPatient(context.Background(), nil, tenantID, employeeID, patientID)
		if err != nil {
			t.Fatalf("CanAccessPatient: %v", err)
		}
		if !allowed {
			t.Error("assigned patient was denied")
		}
		if store.assignedCalls != 1 {
			t.Errorf("store calls = %d, want 1", store.assignedCalls)
		}
	})
}
