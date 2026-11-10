package patients

import (
	"bytes"
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
	createPatient Patient
	createErr     error
	createCalls   int
	lastTenant    uuid.UUID
	lastInput     CreateInput

	getPatient Patient
	getErr     error

	listPatients []Patient
	listErr      error

	listForEmployeePatients []Patient
	listForEmployeeErr      error
	lastEmployeeID          uuid.UUID

	updatePatient Patient
	updateErr     error
	updateCalls   int
	lastUpdateID  uuid.UUID
	lastUpdate    CreateInput

	searchPatients     []Patient
	searchErr          error
	searchCalls        int
	lastSearchEmployee *uuid.UUID
	lastSearchHashes   []string
	lastSearchLimit    int

	tokenCalls       int
	lastTokenTenant  uuid.UUID
	lastTokenPatient uuid.UUID
	lastTokens       []SearchToken
}

func (f *fakeStore) Create(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input CreateInput) (Patient, error) {
	f.createCalls++
	f.lastTenant = tenantID
	f.lastInput = input
	return f.createPatient, f.createErr
}

func (f *fakeStore) Update(_ context.Context, _ *db.Queries, _ uuid.UUID, id uuid.UUID, input CreateInput) (Patient, error) {
	f.updateCalls++
	f.lastUpdateID = id
	f.lastUpdate = input
	return f.updatePatient, f.updateErr
}

func (f *fakeStore) Get(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Patient, error) {
	return f.getPatient, f.getErr
}

func (f *fakeStore) List(context.Context, *db.Queries, uuid.UUID) ([]Patient, error) {
	return f.listPatients, f.listErr
}

func (f *fakeStore) ListForEmployee(_ context.Context, _ *db.Queries, _ uuid.UUID, employeeID uuid.UUID) ([]Patient, error) {
	f.lastEmployeeID = employeeID
	return f.listForEmployeePatients, f.listForEmployeeErr
}

func (f *fakeStore) ReplaceSearchTokens(_ context.Context, _ *db.Queries, tenantID, patientID uuid.UUID, tokens []SearchToken) error {
	f.tokenCalls++
	f.lastTokenTenant = tenantID
	f.lastTokenPatient = patientID
	f.lastTokens = tokens
	return nil
}

func (f *fakeStore) Search(_ context.Context, _ *db.Queries, _ uuid.UUID, employeeID *uuid.UUID, tokenHashes []string, limit int) ([]Patient, error) {
	f.searchCalls++
	f.lastSearchEmployee = employeeID
	f.lastSearchHashes = tokenHashes
	f.lastSearchLimit = limit
	return f.searchPatients, f.searchErr
}

type fakeAccess struct {
	allowed bool
	err     error
	calls   int
	last    uuid.UUID
}

func (f *fakeAccess) CanAccessPatient(_ context.Context, _ *db.Queries, _, _, patientID uuid.UUID) (bool, error) {
	f.calls++
	f.last = patientID
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

func TestValidateInput(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := CreateInput{
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name    string
		mutate  func(*CreateInput)
		wantErr bool
	}{
		{name: "valid", mutate: func(*CreateInput) {}},
		{name: "defaults status", mutate: func(i *CreateInput) { i.Status = "" }},
		{name: "trims names", mutate: func(i *CreateInput) { i.FirstName = "  Anna  "; i.LastName = " Muster " }},
		{name: "empty first name", mutate: func(i *CreateInput) { i.FirstName = "  " }, wantErr: true},
		{name: "empty last name", mutate: func(i *CreateInput) { i.LastName = "" }, wantErr: true},
		{name: "first name too long", mutate: func(i *CreateInput) { i.FirstName = strings.Repeat("a", 101) }, wantErr: true},
		{name: "last name too long", mutate: func(i *CreateInput) { i.LastName = strings.Repeat("a", 101) }, wantErr: true},
		{name: "missing birth date", mutate: func(i *CreateInput) { i.BirthDate = time.Time{} }, wantErr: true},
		{name: "future birth date", mutate: func(i *CreateInput) { i.BirthDate = now.Add(24 * time.Hour) }, wantErr: true},
		{name: "too old birth date", mutate: func(i *CreateInput) { i.BirthDate = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC) }, wantErr: true},
		{name: "invalid status", mutate: func(i *CreateInput) { i.Status = "deleted" }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			got, err := validateInput(input, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if got.Status == "" {
				t.Error("status was not defaulted")
			}
			if strings.TrimSpace(got.FirstName) != got.FirstName {
				t.Errorf("first name %q was not trimmed", got.FirstName)
			}
		})
	}
}

func TestServiceCreate(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	patientID := uuid.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	store := &fakeStore{createPatient: Patient{ID: patientID, TenantID: tenantID, Status: "active"}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)
	service.now = func() time.Time { return now }

	input := CreateInput{
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	actor := Actor{UserID: userID, TenantID: tenantID, Role: "chef"}

	patient, err := service.Create(context.Background(), nil, actor, "req-1", input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if patient.ID != patientID {
		t.Errorf("patient ID = %s, want %s", patient.ID, patientID)
	}
	if store.createCalls != 1 {
		t.Errorf("store.Create calls = %d, want 1", store.createCalls)
	}
	if store.lastTenant != tenantID {
		t.Errorf("tenant = %s, want %s", store.lastTenant, tenantID)
	}
	if store.lastInput.Status != "active" {
		t.Errorf("status = %q, want active", store.lastInput.Status)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "patient.created" {
		t.Errorf("action = %q, want patient.created", event.Action)
	}
	if event.ActorUserID != userID || event.TenantID != tenantID || event.EntityID != patientID {
		t.Errorf("audit event identity is wrong: %+v", event)
	}
	if event.RequestID != "req-1" {
		t.Errorf("request id = %q, want req-1", event.RequestID)
	}
	if _, ok := event.Metadata["first_name"]; ok {
		t.Error("audit metadata contains a patient name")
	}
}

func TestServiceCreateForbiddenRecordsDeniedAccess(t *testing.T) {
	store := &fakeStore{}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}
	_, err := service.Create(context.Background(), nil, actor, "req-2", CreateInput{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called for a forbidden request")
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
	}
}

func TestServiceCreateFailsWhenAuditFails(t *testing.T) {
	store := &fakeStore{createPatient: Patient{ID: uuid.New(), Status: "active"}}
	recorder := &fakeAudit{err: errors.New("audit down")}
	service := NewService(store, recorder)
	service.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	_, err := service.Create(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "chef"}, "", CreateInput{
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Create returned nil when audit failed")
	}
}

func TestServiceGet(t *testing.T) {
	patientID := uuid.New()
	store := &fakeStore{getPatient: Patient{ID: patientID}}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	patient, err := service.Get(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "chef"}, "req-3", patientID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if patient.ID != patientID {
		t.Errorf("patient ID = %s, want %s", patient.ID, patientID)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "patient.read" {
		t.Fatalf("audit events = %+v, want one patient.read", recorder.events)
	}
}

func TestServiceGetNotFound(t *testing.T) {
	store := &fakeStore{getErr: ErrNotFound}
	service := NewService(store, &fakeAudit{})

	_, err := service.Get(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "chef"}, "", uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestServiceListForbidden(t *testing.T) {
	service := NewService(&fakeStore{}, &fakeAudit{})

	_, err := service.List(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "platform_admin"}, "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestServiceCreateForbiddenForPlatformAdmin(t *testing.T) {
	store := &fakeStore{}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	_, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "platform_admin"}, "req-admin", CreateInput{
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if store.createCalls != 0 {
		t.Error("store was called for a platform admin")
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
	}
}

func TestServiceGetForbiddenForPlatformAdmin(t *testing.T) {
	store := &fakeStore{}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	_, err := service.Get(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "platform_admin"}, "req-admin", uuid.New())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
	}
}

func TestServiceEmployeeListUsesAssignments(t *testing.T) {
	tenantID, employeeID := uuid.New(), uuid.New()
	assigned := Patient{ID: uuid.New(), TenantID: tenantID}
	store := &fakeStore{listForEmployeePatients: []Patient{assigned}}
	service := NewServiceWithAccess(store, &fakeAudit{}, &fakeAccess{allowed: true})

	patients, err := service.List(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "req-list")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(patients) != 1 || patients[0].ID != assigned.ID {
		t.Fatalf("patients = %+v, want only the assigned patient", patients)
	}
	if store.lastEmployeeID != employeeID {
		t.Errorf("employee id = %s, want %s", store.lastEmployeeID, employeeID)
	}
}

func TestServiceEmployeeListRequiresAccessChecker(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, &fakeAudit{})

	_, err := service.List(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}, "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestServiceEmployeeGetChecksAssignment(t *testing.T) {
	tenantID, employeeID, patientID := uuid.New(), uuid.New(), uuid.New()

	t.Run("assigned patient is readable", func(t *testing.T) {
		store := &fakeStore{getPatient: Patient{ID: patientID, TenantID: tenantID}}
		access := &fakeAccess{allowed: true}
		service := NewServiceWithAccess(store, &fakeAudit{}, access)

		patient, err := service.Get(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "req-get", patientID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if patient.ID != patientID {
			t.Errorf("patient ID = %s, want %s", patient.ID, patientID)
		}
		if access.calls != 1 || access.last != patientID {
			t.Errorf("access checker calls = %d, last = %s", access.calls, access.last)
		}
	})

	t.Run("unassigned patient is not found", func(t *testing.T) {
		store := &fakeStore{}
		recorder := &fakeAudit{}
		service := NewServiceWithAccess(store, recorder, &fakeAccess{allowed: false})

		_, err := service.Get(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "req-get", patientID)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
			t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
		}
	})

	t.Run("assignment lookup errors are not hidden", func(t *testing.T) {
		service := NewServiceWithAccess(&fakeStore{}, &fakeAudit{}, &fakeAccess{err: errors.New("db down")})

		if _, err := service.Get(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "req-get", patientID); err == nil {
			t.Fatal("Get hid the assignment lookup error")
		}
	})
}

func TestNewCipherRejectsInvalidKey(t *testing.T) {
	if _, err := NewCipher([]byte("short"), 1); err == nil {
		t.Fatal("NewCipher accepted a short key")
	}
	if _, err := NewCipher(bytes.Repeat([]byte{1}, 32), 0); err == nil {
		t.Fatal("NewCipher accepted key version 0")
	}
}

func TestCipherRoundTrip(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	tenantID, patientID := uuid.New(), uuid.New()
	aad := FieldAAD(tenantID, patientID, "first_name", 1)

	encrypted, err := cipher.Encrypt("Anna", aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(encrypted, []byte("Anna")) {
		t.Fatal("ciphertext contains the plaintext")
	}

	decrypted, err := cipher.Decrypt(encrypted, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if decrypted != "Anna" {
		t.Errorf("decrypted = %q, want Anna", decrypted)
	}
}

func TestCipherRejectsTampering(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	aad := FieldAAD(uuid.New(), uuid.New(), "first_name", 1)

	encrypted, err := cipher.Encrypt("Anna", aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	encrypted[len(encrypted)-1] ^= 0xff

	if _, err := cipher.Decrypt(encrypted, aad); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}

func TestCipherRejectsWrongAAD(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	tenantID, patientID := uuid.New(), uuid.New()
	encrypted, err := cipher.Encrypt("Anna", FieldAAD(tenantID, patientID, "first_name", 1))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Same ciphertext, different field binding.
	if _, err := cipher.Decrypt(encrypted, FieldAAD(tenantID, patientID, "last_name", 1)); err == nil {
		t.Fatal("Decrypt accepted ciphertext with the wrong AAD")
	}
}

func TestCipherUsesFreshNonce(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32), 1)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	aad := FieldAAD(uuid.New(), uuid.New(), "first_name", 1)

	first, err := cipher.Encrypt("Anna", aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := cipher.Encrypt("Anna", aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same plaintext are identical")
	}
}
