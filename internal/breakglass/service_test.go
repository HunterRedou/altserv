package breakglass

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/google/uuid"
)

type fakeStore struct {
	patientExists func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error)
	create        func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, string) (Grant, error)
	get           func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error)
	getRequester  func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (Grant, error)
	getActive     func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (Grant, error)
	listTenant    func(context.Context, *db.Queries, uuid.UUID) ([]Grant, error)
	listRequester func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Grant, error)
	approve       func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, int, string) (Grant, error)
	deny          func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, string) (Grant, error)
	revoke        func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, string) (Grant, error)
	touch         func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error)
}

func (f *fakeStore) PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error) {
	return f.patientExists(ctx, q, tenantID, patientID)
}

func (f *fakeStore) Create(ctx context.Context, q *db.Queries, tenantID, patientID, requestedBy uuid.UUID, reason string) (Grant, error) {
	return f.create(ctx, q, tenantID, patientID, requestedBy, reason)
}

func (f *fakeStore) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error) {
	return f.get(ctx, q, tenantID, id)
}

func (f *fakeStore) GetByRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error) {
	return f.getRequester(ctx, q, tenantID, id, requesterID)
}

func (f *fakeStore) GetActiveForRequester(ctx context.Context, q *db.Queries, tenantID, id, requesterID uuid.UUID) (Grant, error) {
	return f.getActive(ctx, q, tenantID, id, requesterID)
}

func (f *fakeStore) ListByTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Grant, error) {
	return f.listTenant(ctx, q, tenantID)
}

func (f *fakeStore) ListByRequester(ctx context.Context, q *db.Queries, tenantID, requesterID uuid.UUID) ([]Grant, error) {
	return f.listRequester(ctx, q, tenantID, requesterID)
}

func (f *fakeStore) Approve(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, durationMinutes int, decisionReason string) (Grant, error) {
	return f.approve(ctx, q, tenantID, id, decidedBy, durationMinutes, decisionReason)
}

func (f *fakeStore) Deny(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, decisionReason string) (Grant, error) {
	return f.deny(ctx, q, tenantID, id, decidedBy, decisionReason)
}

func (f *fakeStore) Revoke(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, reason string) (Grant, error) {
	return f.revoke(ctx, q, tenantID, id, reason)
}

func (f *fakeStore) Touch(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Grant, error) {
	return f.touch(ctx, q, tenantID, id)
}

type recordingRecorder struct {
	events []audit.Event
}

func (r *recordingRecorder) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	r.events = append(r.events, event)
	return nil
}

type fakePatientReader struct {
	patient patients.Patient
	err     error
	calls   int
}

func (f *fakePatientReader) GetForSupport(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (patients.Patient, error) {
	f.calls++
	return f.patient, f.err
}

func baseGrant(tenantID, patientID, requesterID uuid.UUID) Grant {
	return Grant{
		ID:          uuid.New(),
		TenantID:    tenantID,
		PatientID:   patientID,
		RequestedBy: requesterID,
		Status:      "pending",
		Reason:      "support",
		RequestedAt: time.Now(),
	}
}

func TestRequestRequiresPlatformAdmin(t *testing.T) {
	recorder := &recordingRecorder{}
	service := NewService(&fakeStore{}, recorder, nil)
	tenantID := uuid.New()
	actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}

	_, err := service.Request(context.Background(), nil, tenantID, actor, "req-1", RequestInput{PatientID: uuid.New(), Reason: "x"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
		t.Fatalf("events = %+v, want one access.denied", recorder.events)
	}
}

func TestRequestValidatesAndAuditsWithoutReason(t *testing.T) {
	tenantID := uuid.New()
	patientID := uuid.New()
	requester := uuid.New()
	grant := baseGrant(tenantID, patientID, requester)
	recorder := &recordingRecorder{}
	store := &fakeStore{
		patientExists: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error) {
			return true, nil
		},
		create: func(_ context.Context, _ *db.Queries, gotTenant, gotPatient, gotRequester uuid.UUID, reason string) (Grant, error) {
			if gotTenant != tenantID || gotPatient != patientID || gotRequester != requester || reason != "support" {
				t.Fatalf("create args = %s %s %s %q", gotTenant, gotPatient, gotRequester, reason)
			}
			return grant, nil
		},
	}
	service := NewService(store, recorder, nil)
	actor := Actor{UserID: requester, TenantID: uuid.New(), Role: "platform_admin"}

	result, err := service.Request(context.Background(), nil, tenantID, actor, "req-1", RequestInput{PatientID: patientID, Reason: "  support  "})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if result.ID != grant.ID || result.Status != "pending" {
		t.Fatalf("grant = %+v", result)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "break_glass.requested" {
		t.Fatalf("events = %+v", recorder.events)
	}
	if _, ok := recorder.events[0].Metadata["reason"]; ok {
		t.Fatal("audit metadata contains the free-text reason")
	}
}

func TestRequestRejectsMissingReasonAndUnknownPatient(t *testing.T) {
	tenantID := uuid.New()
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "platform_admin"}
	service := NewService(&fakeStore{}, nil, nil)

	if _, err := service.Request(context.Background(), nil, tenantID, actor, "", RequestInput{PatientID: uuid.New(), Reason: "  "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason error = %v, want invalid", err)
	}

	store := &fakeStore{
		patientExists: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		},
	}
	service = NewService(store, nil, nil)
	if _, err := service.Request(context.Background(), nil, tenantID, actor, "", RequestInput{PatientID: uuid.New(), Reason: "support"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown patient error = %v, want not found", err)
	}
}

func TestApproveUsesDefaultAndBounds(t *testing.T) {
	tenantID := uuid.New()
	chefID := uuid.New()
	patientID := uuid.New()
	requesterID := uuid.New()
	grant := baseGrant(tenantID, patientID, requesterID)

	for _, test := range []struct {
		name    string
		input   int
		want    int
		wantErr bool
	}{
		{name: "default", input: 0, want: DefaultDurationMinutes},
		{name: "minimum", input: 1, want: 1},
		{name: "maximum", input: 60, want: 60},
		{name: "too short", input: -1, wantErr: true},
		{name: "too long", input: 61, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotDuration int
			store := &fakeStore{
				get: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error) {
					return grant, nil
				},
				approve: func(_ context.Context, _ *db.Queries, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, duration int, _ string) (Grant, error) {
					gotDuration = duration
					approved := grant
					approved.Status = "approved"
					expires := time.Now().Add(time.Duration(duration) * time.Minute)
					approved.ExpiresAt = &expires
					decidedBy := chefID
					approved.DecidedBy = &decidedBy
					return approved, nil
				},
			}
			service := NewService(store, nil, nil)
			_, err := service.Approve(context.Background(), nil, tenantID, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "", grant.ID, DecisionInput{DurationMinutes: test.input})
			if test.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("error = %v, want invalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Approve: %v", err)
			}
			if gotDuration != test.want {
				t.Fatalf("duration = %d, want %d", gotDuration, test.want)
			}
		})
	}
}

func TestApproveRejectsSelfApprovalAndWrongState(t *testing.T) {
	tenantID := uuid.New()
	chefID := uuid.New()
	patientID := uuid.New()

	service := NewService(&fakeStore{
		get: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error) {
			return baseGrant(tenantID, patientID, chefID), nil
		},
	}, nil, nil)
	if _, err := service.Approve(context.Background(), nil, tenantID, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "", uuid.New(), DecisionInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self approval error = %v, want forbidden", err)
	}

	approved := baseGrant(tenantID, patientID, uuid.New())
	approved.Status = "approved"
	service = NewService(&fakeStore{
		get: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error) {
			return approved, nil
		},
	}, nil, nil)
	if _, err := service.Approve(context.Background(), nil, tenantID, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "", uuid.New(), DecisionInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("approved state error = %v, want conflict", err)
	}
}

func TestReadPatientRequiresActiveGrant(t *testing.T) {
	tenantID := uuid.New()
	patientID := uuid.New()
	requesterID := uuid.New()
	grant := baseGrant(tenantID, patientID, requesterID)
	grant.Status = "approved"
	expires := time.Now().Add(10 * time.Minute)
	grant.ExpiresAt = &expires
	recorder := &recordingRecorder{}
	reader := &fakePatientReader{patient: patients.Patient{ID: patientID, TenantID: tenantID, FirstName: "Anna"}}
	touched := false
	store := &fakeStore{
		getActive: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (Grant, error) {
			return grant, nil
		},
		touch: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Grant, error) {
			touched = true
			return grant, nil
		},
	}
	service := NewService(store, recorder, reader)
	actor := Actor{UserID: requesterID, TenantID: uuid.New(), Role: "platform_admin"}

	patient, err := service.ReadPatient(context.Background(), nil, tenantID, actor, "req-1", grant.ID)
	if err != nil {
		t.Fatalf("ReadPatient: %v", err)
	}
	if patient.FirstName != "Anna" || reader.calls != 1 || !touched {
		t.Fatalf("patient = %+v, reader calls = %d, touched = %v", patient, reader.calls, touched)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "break_glass.patient_read" {
		t.Fatalf("events = %+v", recorder.events)
	}

	// A missing or inactive grant is a forbidden read and must not call the
	// patient reader.
	reader.calls = 0
	store.getActive = func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (Grant, error) {
		return Grant{}, ErrNotFound
	}
	service = NewService(store, recorder, reader)
	if _, err := service.ReadPatient(context.Background(), nil, tenantID, actor, "req-2", grant.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive grant error = %v, want forbidden", err)
	}
	if reader.calls != 0 {
		t.Fatal("patient reader was called without an active grant")
	}
}

func TestListRoleRules(t *testing.T) {
	tenantID := uuid.New()
	patientID := uuid.New()
	requesterID := uuid.New()
	grant := baseGrant(tenantID, patientID, requesterID)

	service := NewService(&fakeStore{
		listTenant: func(context.Context, *db.Queries, uuid.UUID) ([]Grant, error) {
			return []Grant{grant}, nil
		},
		listRequester: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Grant, error) {
			return []Grant{grant}, nil
		},
	}, nil, nil)

	chefGrants, err := service.List(context.Background(), nil, tenantID, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "")
	if err != nil || len(chefGrants) != 1 {
		t.Fatalf("chef list = %+v, err = %v", chefGrants, err)
	}
	if _, err := service.List(context.Background(), nil, tenantID, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant chef error = %v, want forbidden", err)
	}
	requesterGrants, err := service.List(context.Background(), nil, tenantID, Actor{UserID: requesterID, TenantID: uuid.New(), Role: "platform_admin"}, "")
	if err != nil || len(requesterGrants) != 1 {
		t.Fatalf("requester list = %+v, err = %v", requesterGrants, err)
	}
	if _, err := service.List(context.Background(), nil, tenantID, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("employee list error = %v, want forbidden", err)
	}
}
