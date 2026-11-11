package reports

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeStore implements Store with function hooks so each test can set only the
// behavior it needs.
type fakeStore struct {
	getVisitFn          func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Visit, error)
	createFn            func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, db.Visit, CreateInput) (Report, error)
	getFn               func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error)
	listByVisitFn       func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Report, error)
	listByPatientFn     func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) ([]Report, error)
	updateDraftFn       func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int, *string) (Report, error)
	submitFn            func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) (Report, error)
	approveFn           func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, int) (Report, error)
	returnFn            func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) (Report, error)
	markReadyFn         func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int) (Report, error)
	getActivityFn       func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Activity, error)
	getAttachmentFn     func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (uuid.UUID, error)
	replaceActivitiesFn func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, []storedActivity) ([]ReportActivity, error)
	listActivitiesFn    func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]ReportActivity, error)
	createTransitionFn  func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string, string) (ReportTransition, error)
	listTransitionsFn   func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]ReportTransition, error)
}

func (f *fakeStore) GetVisit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Visit, error) {
	if f.getVisitFn == nil {
		return db.Visit{}, ErrNotFound
	}
	return f.getVisitFn(ctx, q, tenantID, id)
}

func (f *fakeStore) Create(ctx context.Context, q *db.Queries, tenantID, createdBy, employeeID uuid.UUID, visit db.Visit, input CreateInput) (Report, error) {
	if f.createFn == nil {
		return Report{}, errors.New("unexpected Create call")
	}
	return f.createFn(ctx, q, tenantID, createdBy, employeeID, visit, input)
}

func (f *fakeStore) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Report, error) {
	if f.getFn == nil {
		return Report{}, ErrNotFound
	}
	return f.getFn(ctx, q, tenantID, id)
}

func (f *fakeStore) ListByVisit(ctx context.Context, q *db.Queries, tenantID, visitID uuid.UUID) ([]Report, error) {
	if f.listByVisitFn == nil {
		return nil, nil
	}
	return f.listByVisitFn(ctx, q, tenantID, visitID)
}

func (f *fakeStore) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Report, error) {
	if f.listByPatientFn == nil {
		return nil, nil
	}
	return f.listByPatientFn(ctx, q, tenantID, patientID, limit)
}

func (f *fakeStore) UpdateDraft(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int, note *string) (Report, error) {
	if f.updateDraftFn == nil {
		return Report{}, ErrConflict
	}
	return f.updateDraftFn(ctx, q, tenantID, id, version, note)
}

func (f *fakeStore) Submit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	if f.submitFn == nil {
		return Report{}, ErrConflict
	}
	return f.submitFn(ctx, q, tenantID, id, version)
}

func (f *fakeStore) Approve(ctx context.Context, q *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (Report, error) {
	if f.approveFn == nil {
		return Report{}, ErrConflict
	}
	return f.approveFn(ctx, q, tenantID, id, approvedBy, version)
}

func (f *fakeStore) Return(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	if f.returnFn == nil {
		return Report{}, ErrConflict
	}
	return f.returnFn(ctx, q, tenantID, id, version)
}

func (f *fakeStore) MarkReadyForBilling(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	if f.markReadyFn == nil {
		return Report{}, ErrConflict
	}
	return f.markReadyFn(ctx, q, tenantID, id, version)
}

func (f *fakeStore) GetActivity(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Activity, error) {
	if f.getActivityFn == nil {
		return db.Activity{}, ErrNotFound
	}
	return f.getActivityFn(ctx, q, tenantID, id)
}

func (f *fakeStore) GetActivityAttachment(ctx context.Context, q *db.Queries, tenantID, activityID uuid.UUID) (uuid.UUID, error) {
	if f.getAttachmentFn == nil {
		return uuid.Nil, ErrNotFound
	}
	return f.getAttachmentFn(ctx, q, tenantID, activityID)
}

func (f *fakeStore) ReplaceActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID, activities []storedActivity) ([]ReportActivity, error) {
	if f.replaceActivitiesFn == nil {
		result := make([]ReportActivity, 0, len(activities))
		for _, activity := range activities {
			result = append(result, ReportActivity{ReportID: reportID, ActivityID: activity.ActivityID, Quantity: activity.Quantity})
		}
		return result, nil
	}
	return f.replaceActivitiesFn(ctx, q, tenantID, reportID, activities)
}

func (f *fakeStore) ListActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportActivity, error) {
	if f.listActivitiesFn == nil {
		return nil, nil
	}
	return f.listActivitiesFn(ctx, q, tenantID, reportID)
}

func (f *fakeStore) CreateTransition(ctx context.Context, q *db.Queries, tenantID, reportID, actorID uuid.UUID, fromStatus, toStatus, kind, reason string) (ReportTransition, error) {
	if f.createTransitionFn == nil {
		return ReportTransition{ReportID: reportID, FromStatus: fromStatus, ToStatus: toStatus, Kind: kind, Reason: reason}, nil
	}
	return f.createTransitionFn(ctx, q, tenantID, reportID, actorID, fromStatus, toStatus, kind, reason)
}

func (f *fakeStore) ListTransitions(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportTransition, error) {
	if f.listTransitionsFn == nil {
		return nil, nil
	}
	return f.listTransitionsFn(ctx, q, tenantID, reportID)
}

type fakeAccess struct {
	allowed bool
	err     error
}

func (f fakeAccess) CanAccessPatient(context.Context, *db.Queries, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return f.allowed, f.err
}

func newServiceForTest(store Store, access AccessChecker) *Service {
	return NewService(store, audit.NoopRecorder{}, access)
}

func TestCreateReportRules(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	patientID := uuid.New()
	employeeID := uuid.New()
	visitID := uuid.New()
	activityID := uuid.New()
	now := time.Now()

	completedVisit := db.Visit{
		ID:               visitID,
		TenantID:         tenantID,
		PatientID:        patientID,
		EmployeeID:       employeeID,
		ActualEmployeeID: uuid.NullUUID{UUID: employeeID, Valid: true},
		Status:           "completed",
	}

	t.Run("employee creates a draft with one activity", func(t *testing.T) {
		store := &fakeStore{
			getVisitFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Visit, error) {
				return completedVisit, nil
			},
			createFn: func(_ context.Context, _ *db.Queries, tenant, createdBy, employee uuid.UUID, visit db.Visit, input CreateInput) (Report, error) {
				if input.Note != "Pflegebericht" {
					t.Errorf("note = %q", input.Note)
				}
				return Report{ID: uuid.New(), TenantID: tenant, VisitID: visit.ID, PatientID: visit.PatientID, EmployeeID: employee, Status: "draft", Version: 1}, nil
			},
			getActivityFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Activity, error) {
				return db.Activity{ID: activityID, PatientID: patientID, Status: "recorded", PerformedAt: now}, nil
			},
			getAttachmentFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (uuid.UUID, error) {
				return uuid.Nil, ErrNotFound
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})

		report, err := service.Create(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", visitID, CreateInput{
			Note:       "Pflegebericht",
			Activities: []ActivityInput{{ActivityID: activityID, Quantity: 2}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if report.Status != "draft" || report.Version != 1 || len(report.Activities) != 1 {
			t.Fatalf("report = %+v", report)
		}
	})

	t.Run("platform admin is denied", func(t *testing.T) {
		service := newServiceForTest(&fakeStore{}, nil)
		_, err := service.Create(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}, "", visitID, CreateInput{})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("a visit must be completed", func(t *testing.T) {
		store := &fakeStore{getVisitFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Visit, error) {
			visit := completedVisit
			visit.Status = "in_progress"
			return visit, nil
		}}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		_, err := service.Create(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", visitID, CreateInput{})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("an employee who did not perform the visit is denied", func(t *testing.T) {
		store := &fakeStore{getVisitFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Visit, error) {
			return completedVisit, nil
		}}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		_, err := service.Create(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "employee"}, "", visitID, CreateInput{})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("an activity from another patient is rejected", func(t *testing.T) {
		store := &fakeStore{
			getVisitFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Visit, error) {
				return completedVisit, nil
			},
			getActivityFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Activity, error) {
				return db.Activity{ID: activityID, PatientID: uuid.New(), Status: "recorded"}, nil
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		_, err := service.Create(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", visitID, CreateInput{
			Activities: []ActivityInput{{ActivityID: activityID, Quantity: 1}},
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestReportEditAndApprovalRules(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	patientID := uuid.New()
	employeeID := uuid.New()
	reportID := uuid.New()

	draft := Report{ID: reportID, TenantID: tenantID, PatientID: patientID, EmployeeID: employeeID, Status: "draft", Version: 2}
	submitted := draft
	submitted.Status = "submitted"
	submitted.Version = 3

	t.Run("a submitted report cannot be edited", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
			return submitted, nil
		}}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		note := "changed"
		_, err := service.Update(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", reportID, UpdateInput{Version: 3, Note: &note})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("a stale version is rejected", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
			return draft, nil
		}}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		note := "changed"
		_, err := service.Update(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", reportID, UpdateInput{Version: 1, Note: &note})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("an activity referenced by an approved billing run is protected", func(t *testing.T) {
		activityID := uuid.New()
		store := &fakeStore{
			getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
				return draft, nil
			},
			getActivityFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Activity, error) {
				return db.Activity{ID: activityID, PatientID: patientID, Status: "recorded", PerformedAt: time.Now().UTC()}, nil
			},
			updateDraftFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, int, *string) (Report, error) {
				updated := draft
				updated.Version = 3
				return updated, nil
			},
			replaceActivitiesFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID, []storedActivity) ([]ReportActivity, error) {
				// The database rejects the replacement because a billing line
				// of an approved run still references the old report activity.
				return nil, &pgconn.PgError{Code: "23503", ConstraintName: "billing_lines_report_activity_fk"}
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		activities := []ActivityInput{{ActivityID: activityID, Quantity: 1}}
		_, err := service.Update(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", reportID, UpdateInput{Version: 2, Activities: &activities})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if !strings.Contains(err.Error(), "void the billing run first") {
			t.Fatalf("err = %v, want the billing run hint", err)
		}
	})

	t.Run("submit needs at least one activity", func(t *testing.T) {
		store := &fakeStore{
			getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
				return draft, nil
			},
			listActivitiesFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]ReportActivity, error) {
				return nil, nil
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		_, err := service.Submit(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", reportID, 2)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("only a chef approves", func(t *testing.T) {
		service := newServiceForTest(&fakeStore{}, nil)
		_, err := service.Approve(ctx, nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "", reportID, 3)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("return needs a reason", func(t *testing.T) {
		service := newServiceForTest(&fakeStore{}, nil)
		_, err := service.Return(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", reportID, ReturnInput{Version: 3})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("return stores a separate transition", func(t *testing.T) {
		approved := draft
		approved.Status = "approved"
		approved.Version = 4
		var gotFrom, gotTo, gotKind, gotReason string
		store := &fakeStore{
			getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
				return approved, nil
			},
			returnFn: func(_ context.Context, _ *db.Queries, tenant, id uuid.UUID, version int) (Report, error) {
				report := approved
				report.Status = "draft"
				report.Version = 5
				return report, nil
			},
			createTransitionFn: func(_ context.Context, _ *db.Queries, tenant, report, actor uuid.UUID, from, to, kind, reason string) (ReportTransition, error) {
				gotFrom, gotTo, gotKind, gotReason = from, to, kind, reason
				return ReportTransition{ID: uuid.New(), ReportID: report, FromStatus: from, ToStatus: to, Kind: kind, Reason: reason}, nil
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		report, err := service.Return(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", reportID, ReturnInput{Version: 4, Reason: "  Menge fehlt  "})
		if err != nil {
			t.Fatalf("return: %v", err)
		}
		if gotFrom != "approved" || gotTo != "draft" || gotKind != "correction" || gotReason != "Menge fehlt" {
			t.Fatalf("transition = %q -> %q (%q, %q)", gotFrom, gotTo, gotKind, gotReason)
		}
		if report.Status != "draft" || report.Version != 5 {
			t.Fatalf("report = %+v", report)
		}
	})

	t.Run("a return from submitted is a return", func(t *testing.T) {
		var gotKind string
		store := &fakeStore{
			getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
				return submitted, nil
			},
			returnFn: func(_ context.Context, _ *db.Queries, tenant, id uuid.UUID, version int) (Report, error) {
				report := submitted
				report.Status = "draft"
				report.Version = 4
				return report, nil
			},
			createTransitionFn: func(_ context.Context, _ *db.Queries, tenant, report, actor uuid.UUID, from, to, kind, reason string) (ReportTransition, error) {
				gotKind = kind
				return ReportTransition{ID: uuid.New(), ReportID: report, FromStatus: from, ToStatus: to, Kind: kind, Reason: reason}, nil
			},
		}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		if _, err := service.Return(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", reportID, ReturnInput{Version: 3, Reason: "bitte ergaenzen"}); err != nil {
			t.Fatalf("return: %v", err)
		}
		if gotKind != "return" {
			t.Fatalf("kind = %q, want return", gotKind)
		}
	})

	t.Run("ready for billing needs an approved report", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Report, error) {
			return submitted, nil
		}}
		service := newServiceForTest(store, fakeAccess{allowed: true})
		_, err := service.ReadyForBilling(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", reportID, 3)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestCipherAADIsBoundToTheReport(t *testing.T) {
	tenantID := uuid.New()
	reportA := uuid.New()
	reportB := uuid.New()
	if string(noteAAD(tenantID, reportA, 1)) == string(noteAAD(tenantID, reportB, 1)) {
		t.Fatal("note AAD is not bound to the report id")
	}
	if string(noteAAD(tenantID, reportA, 1)) == string(noteAAD(uuid.New(), reportA, 1)) {
		t.Fatal("note AAD is not bound to the tenant id")
	}
	if string(reasonAAD(tenantID, reportA, 1)) == string(noteAAD(tenantID, reportA, 1)) {
		t.Fatal("note and reason AAD must differ")
	}
}
