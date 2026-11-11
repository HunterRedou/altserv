package retention

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// fakeStore embeds the Store interface so a test only implements the methods it
// exercises. Calling an unimplemented method panics, which is the signal that
// the test needs a more complete fake.
type fakeStore struct {
	Store

	patientExists bool
	held          bool

	createdPolicy   *Policy
	updatedPolicy   *Policy
	confirmedPolicy *Policy

	createdHold *LegalHold
	liftedHold  *LegalHold

	createdRequest *DeletionRequest
	currentRequest *DeletionRequest
	approved       *DeletionRequest
	rejected       *DeletionRequest
	cancelled      *DeletionRequest
	executed       *DeletionRequest

	deleted         bool
	registerCreated bool
	markedExecuted  bool

	candidates []Candidate
}

func (f *fakeStore) PatientExists(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error) {
	return f.patientExists, nil
}

func (f *fakeStore) CreatePolicy(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input PolicyInput, createdBy uuid.UUID) (Policy, error) {
	policy := Policy{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Category:     input.Category,
		TriggerEvent: input.TriggerEvent,
		PeriodMonths: input.PeriodMonths,
		Status:       "disabled",
		Note:         input.Note,
		CreatedBy:    createdBy,
	}
	f.createdPolicy = &policy
	return policy, nil
}

func (f *fakeStore) UpdatePolicy(_ context.Context, _ *db.Queries, tenantID, id uuid.UUID, periodMonths int, note string) (Policy, error) {
	policy := Policy{
		ID:           id,
		TenantID:     tenantID,
		Category:     "patient_master_data",
		TriggerEvent: "end_of_care",
		PeriodMonths: periodMonths,
		Status:       "disabled",
		Note:         note,
	}
	f.updatedPolicy = &policy
	return policy, nil
}

func (f *fakeStore) ConfirmPolicy(_ context.Context, _ *db.Queries, tenantID, id, confirmedBy uuid.UUID) (Policy, error) {
	policy := Policy{
		ID:           id,
		TenantID:     tenantID,
		Category:     "patient_master_data",
		PeriodMonths: 120,
		Status:       "confirmed",
	}
	f.confirmedPolicy = &policy
	return policy, nil
}

func (f *fakeStore) CreateLegalHold(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input LegalHoldInput, declaredBy uuid.UUID) (LegalHold, error) {
	hold := LegalHold{
		ID:         uuid.New(),
		TenantID:   tenantID,
		PatientID:  input.PatientID,
		Status:     "active",
		Reason:     input.Reason,
		DeclaredBy: declaredBy,
	}
	f.createdHold = &hold
	return hold, nil
}

func (f *fakeStore) LiftLegalHold(_ context.Context, _ *db.Queries, tenantID, id, liftedBy uuid.UUID, reason string) (LegalHold, error) {
	hold := LegalHold{
		ID:         id,
		TenantID:   tenantID,
		Status:     "lifted",
		LiftedBy:   &liftedBy,
		LiftReason: reason,
	}
	f.liftedHold = &hold
	return hold, nil
}

func (f *fakeStore) HasActiveLegalHold(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (bool, error) {
	return f.held, nil
}

func (f *fakeStore) CreateDeletionRequest(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input DeletionRequestInput, requestedBy uuid.UUID) (DeletionRequest, error) {
	request := DeletionRequest{
		ID:          uuid.New(),
		TenantID:    tenantID,
		PatientID:   input.PatientID,
		Status:      "pending",
		Reason:      input.Reason,
		RequestedBy: requestedBy,
		RequestedAt: time.Now(),
	}
	f.createdRequest = &request
	return request, nil
}

func (f *fakeStore) GetDeletionRequest(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (DeletionRequest, error) {
	if f.currentRequest == nil {
		return DeletionRequest{}, ErrNotFound
	}
	return *f.currentRequest, nil
}

func (f *fakeStore) ApproveDeletionRequest(_ context.Context, _ *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error) {
	request := *f.currentRequest
	request.Status = "approved"
	request.DecidedBy = &decidedBy
	request.DecisionReason = reason
	f.approved = &request
	return request, nil
}

func (f *fakeStore) RejectDeletionRequest(_ context.Context, _ *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error) {
	request := *f.currentRequest
	request.Status = "rejected"
	request.DecidedBy = &decidedBy
	request.DecisionReason = reason
	f.rejected = &request
	return request, nil
}

func (f *fakeStore) CancelDeletionRequest(_ context.Context, _ *db.Queries, tenantID, id, cancelledBy uuid.UUID) (DeletionRequest, error) {
	request := *f.currentRequest
	request.Status = "cancelled"
	request.CancelledBy = &cancelledBy
	f.cancelled = &request
	return request, nil
}

func (f *fakeStore) MarkDeletionExecuted(_ context.Context, _ *db.Queries, tenantID, id, executedBy uuid.UUID) (DeletionRequest, error) {
	request := *f.currentRequest
	request.Status = "executed"
	request.ExecutedBy = &executedBy
	f.executed = &request
	f.markedExecuted = true
	return request, nil
}

func (f *fakeStore) CountPatientDeletionRows(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (json.RawMessage, error) {
	return json.RawMessage(`{"patients":1}`), nil
}

func (f *fakeStore) DeletePatientData(context.Context, *db.Queries, uuid.UUID, uuid.UUID) error {
	f.deleted = true
	return nil
}

func (f *fakeStore) CreateRegisterEntry(_ context.Context, _ *db.Queries, tenantID, requestID, patientID, executedBy uuid.UUID, rowCounts json.RawMessage) (RegisterEntry, error) {
	f.registerCreated = true
	return RegisterEntry{
		ID:                uuid.New(),
		TenantID:          tenantID,
		DeletionRequestID: requestID,
		PatientID:         patientID,
		ExecutedBy:        executedBy,
		RowCounts:         rowCounts,
	}, nil
}

func (f *fakeStore) ReviewCandidatesForTenant(context.Context, *db.Queries, uuid.UUID) ([]Candidate, error) {
	return f.candidates, nil
}

func chefActor(tenantID uuid.UUID) Actor {
	return Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}
}

func TestValidatePolicy(t *testing.T) {
	tests := []struct {
		name    string
		input   PolicyInput
		wantErr bool
	}{
		{
			name:  "valid category fills the trigger",
			input: PolicyInput{Category: "patient_master_data", PeriodMonths: 120},
		},
		{
			name:    "unknown category",
			input:   PolicyInput{Category: "everything", PeriodMonths: 12},
			wantErr: true,
		},
		{
			name:    "trigger does not match category",
			input:   PolicyInput{Category: "billing", TriggerEvent: "end_of_care", PeriodMonths: 12},
			wantErr: true,
		},
		{
			name:    "period is zero",
			input:   PolicyInput{Category: "audit", PeriodMonths: 0},
			wantErr: true,
		},
		{
			name:    "period is too large",
			input:   PolicyInput{Category: "audit", PeriodMonths: MaxPeriodMonths + 1},
			wantErr: true,
		},
		{
			name:    "note is too long",
			input:   PolicyInput{Category: "audit", PeriodMonths: 12, Note: strings.Repeat("x", maxNoteRunes+1)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validatePolicy(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("validatePolicy error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validatePolicy error = %v", err)
			}
			if got.TriggerEvent != categoryTriggers[got.Category] {
				t.Fatalf("trigger = %q, want %q", got.TriggerEvent, categoryTriggers[got.Category])
			}
		})
	}
}

func TestNonChefIsDenied(t *testing.T) {
	tenantID := uuid.New()
	for _, role := range []string{"platform_admin", "employee", ""} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			service := NewService(store, nil)
			actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}

			if _, err := service.CreatePolicy(context.Background(), nil, actor, "", PolicyInput{Category: "audit", PeriodMonths: 12}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("CreatePolicy error = %v, want ErrForbidden", err)
			}
			if _, err := service.ReviewCandidates(context.Background(), nil, actor, ""); !errors.Is(err, ErrForbidden) {
				t.Fatalf("ReviewCandidates error = %v, want ErrForbidden", err)
			}
			if _, err := service.CreateDeletionRequest(context.Background(), nil, actor, "", DeletionRequestInput{PatientID: uuid.New(), Reason: "request"}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("CreateDeletionRequest error = %v, want ErrForbidden", err)
			}
			if store.createdPolicy != nil || store.createdRequest != nil {
				t.Fatal("a denied actor wrote a row")
			}
		})
	}
}

func TestChefWithNoTenantIsDenied(t *testing.T) {
	service := NewService(&fakeStore{}, nil)
	actor := Actor{UserID: uuid.New(), Role: "chef"}
	if _, err := service.CreatePolicy(context.Background(), nil, actor, "", PolicyInput{Category: "audit", PeriodMonths: 12}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreatePolicy error = %v, want ErrForbidden", err)
	}
}

func TestCreatePolicyKeepsItDisabled(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	policy, err := service.CreatePolicy(context.Background(), nil, actor, "", PolicyInput{
		Category:     "care_documentation",
		PeriodMonths: 120,
	})
	if err != nil {
		t.Fatalf("CreatePolicy error = %v", err)
	}
	if policy.Status != "disabled" {
		t.Fatalf("new policy status = %q, want disabled", policy.Status)
	}
	if policy.TriggerEvent != "end_of_care" {
		t.Fatalf("new policy trigger = %q, want end_of_care", policy.TriggerEvent)
	}
}

func TestCreateDeletionRequestBlockedByLegalHold(t *testing.T) {
	store := &fakeStore{patientExists: true, held: true}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	_, err := service.CreateDeletionRequest(context.Background(), nil, actor, "", DeletionRequestInput{
		PatientID: uuid.New(),
		Reason:    "Patient asked for erasure",
	})
	if !errors.Is(err, ErrLegalHold) {
		t.Fatalf("CreateDeletionRequest error = %v, want ErrLegalHold", err)
	}
	if store.createdRequest != nil {
		t.Fatal("a blocked request was still created")
	}
}

func TestCreateDeletionRequestTrimsReason(t *testing.T) {
	store := &fakeStore{patientExists: true}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	_, err := service.CreateDeletionRequest(context.Background(), nil, actor, "", DeletionRequestInput{
		PatientID: uuid.New(),
		Reason:    "  Patient asked for erasure  ",
	})
	if err != nil {
		t.Fatalf("CreateDeletionRequest error = %v", err)
	}
	if store.createdRequest.Reason != "Patient asked for erasure" {
		t.Fatalf("reason = %q", store.createdRequest.Reason)
	}
}

func TestApproveDeletionRequestRequiresSecondChef(t *testing.T) {
	requester := chefActor(uuid.New())
	otherChef := chefActor(requester.TenantID)
	request := DeletionRequest{
		ID:          uuid.New(),
		TenantID:    requester.TenantID,
		PatientID:   uuid.New(),
		Status:      "pending",
		RequestedBy: requester.UserID,
	}

	t.Run("requester cannot approve the own request", func(t *testing.T) {
		store := &fakeStore{currentRequest: &request}
		service := NewService(store, nil)
		if _, err := service.ApproveDeletionRequest(context.Background(), nil, requester, "", request.ID, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("ApproveDeletionRequest error = %v, want ErrForbidden", err)
		}
		if store.approved != nil {
			t.Fatal("the requester approved the own request")
		}
	})

	t.Run("a second chef can approve", func(t *testing.T) {
		store := &fakeStore{currentRequest: &request}
		service := NewService(store, nil)
		approved, err := service.ApproveDeletionRequest(context.Background(), nil, otherChef, "", request.ID, "")
		if err != nil {
			t.Fatalf("ApproveDeletionRequest error = %v", err)
		}
		if approved.Status != "approved" {
			t.Fatalf("status = %q, want approved", approved.Status)
		}
		if approved.DecidedBy == nil || *approved.DecidedBy != otherChef.UserID {
			t.Fatal("decider is not the second chef")
		}
	})

	t.Run("a decided request cannot be approved again", func(t *testing.T) {
		decided := request
		decided.Status = "approved"
		store := &fakeStore{currentRequest: &decided}
		service := NewService(store, nil)
		if _, err := service.ApproveDeletionRequest(context.Background(), nil, otherChef, "", request.ID, ""); !errors.Is(err, ErrConflict) {
			t.Fatalf("ApproveDeletionRequest error = %v, want ErrConflict", err)
		}
	})
}

func TestApproveDeletionRequestBlockedByLegalHold(t *testing.T) {
	requester := chefActor(uuid.New())
	otherChef := chefActor(requester.TenantID)
	request := DeletionRequest{
		ID:          uuid.New(),
		TenantID:    requester.TenantID,
		PatientID:   uuid.New(),
		Status:      "pending",
		RequestedBy: requester.UserID,
	}
	store := &fakeStore{currentRequest: &request, held: true}
	service := NewService(store, nil)

	if _, err := service.ApproveDeletionRequest(context.Background(), nil, otherChef, "", request.ID, ""); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("ApproveDeletionRequest error = %v, want ErrLegalHold", err)
	}
	if store.approved != nil {
		t.Fatal("a blocked request was approved")
	}
}

func TestExecuteDeletion(t *testing.T) {
	requester := chefActor(uuid.New())
	otherChef := chefActor(requester.TenantID)
	approved := DeletionRequest{
		ID:          uuid.New(),
		TenantID:    requester.TenantID,
		PatientID:   uuid.New(),
		Status:      "approved",
		RequestedBy: requester.UserID,
	}

	t.Run("a pending request cannot be executed", func(t *testing.T) {
		pending := approved
		pending.Status = "pending"
		store := &fakeStore{currentRequest: &pending}
		service := NewService(store, nil)
		if _, err := service.ExecuteDeletion(context.Background(), nil, otherChef, "", pending.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("ExecuteDeletion error = %v, want ErrConflict", err)
		}
		if store.deleted {
			t.Fatal("a pending request deleted data")
		}
	})

	t.Run("the requester cannot execute the own request", func(t *testing.T) {
		store := &fakeStore{currentRequest: &approved}
		service := NewService(store, nil)
		if _, err := service.ExecuteDeletion(context.Background(), nil, requester, "", approved.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("ExecuteDeletion error = %v, want ErrForbidden", err)
		}
		if store.deleted {
			t.Fatal("the requester deleted data")
		}
	})

	t.Run("a legal hold blocks execution", func(t *testing.T) {
		store := &fakeStore{currentRequest: &approved, held: true}
		service := NewService(store, nil)
		if _, err := service.ExecuteDeletion(context.Background(), nil, otherChef, "", approved.ID); !errors.Is(err, ErrLegalHold) {
			t.Fatalf("ExecuteDeletion error = %v, want ErrLegalHold", err)
		}
		if store.deleted {
			t.Fatal("a held patient was deleted")
		}
	})

	t.Run("a second chef executes the deletion", func(t *testing.T) {
		store := &fakeStore{currentRequest: &approved}
		service := NewService(store, nil)
		result, err := service.ExecuteDeletion(context.Background(), nil, otherChef, "", approved.ID)
		if err != nil {
			t.Fatalf("ExecuteDeletion error = %v", err)
		}
		if !store.deleted {
			t.Fatal("patient data was not deleted")
		}
		if !store.markedExecuted || !store.registerCreated {
			t.Fatalf("executed=%v register=%v, want both true", store.markedExecuted, store.registerCreated)
		}
		if result.Request.Status != "executed" {
			t.Fatalf("request status = %q, want executed", result.Request.Status)
		}
		if result.Register.PatientID != approved.PatientID {
			t.Fatalf("register patient = %s, want %s", result.Register.PatientID, approved.PatientID)
		}
	})
}

func TestReviewOnlyReportsCounts(t *testing.T) {
	store := &fakeStore{candidates: []Candidate{{Category: "audit", Count: 7}}}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	candidates, err := service.ReviewCandidates(context.Background(), nil, actor, "")
	if err != nil {
		t.Fatalf("ReviewCandidates error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].Count != 7 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if store.createdRequest != nil || store.deleted {
		t.Fatal("the review created a request or deleted data")
	}
}

func TestLegalHoldRequiresReason(t *testing.T) {
	store := &fakeStore{patientExists: true}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	if _, err := service.CreateLegalHold(context.Background(), nil, actor, "", LegalHoldInput{Reason: "   "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateLegalHold error = %v, want ErrInvalid", err)
	}
	if store.createdHold != nil {
		t.Fatal("a hold without a reason was created")
	}
}

func TestLegalHoldDefaultsReviewDate(t *testing.T) {
	store := &fakeStore{patientExists: true}
	service := NewService(store, nil)
	actor := chefActor(uuid.New())

	hold, err := service.CreateLegalHold(context.Background(), nil, actor, "", LegalHoldInput{Reason: "Tax audit"})
	if err != nil {
		t.Fatalf("CreateLegalHold error = %v", err)
	}
	if hold.PatientID != nil {
		t.Fatalf("a tenant-wide hold got a patient id: %s", *hold.PatientID)
	}
	if store.createdHold == nil {
		t.Fatal("hold was not stored")
	}
}
