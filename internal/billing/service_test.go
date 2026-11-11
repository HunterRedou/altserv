package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeAudit struct {
	events []audit.Event
	err    error
}

func (f *fakeAudit) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeStore struct {
	report    db.Report
	reportErr error

	run     BillingRun
	runErr  error
	active  *BillingRun
	runs    []BillingRun
	runList []BillingRun

	candidates  []Candidate
	rules       []BillingRule
	definitions map[uuid.UUID]db.ServiceDefinition

	billed map[uuid.UUID]BillingLine

	insertedLines    []Line
	insertedFindings []Finding
	deletedLines     int
	deletedFindings  int
	touchedVersions  []int
	approveCalls     int
	voidCalls        int
	createRuleCalls  int
}

func (f *fakeStore) GetReport(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (db.Report, error) {
	if f.reportErr != nil {
		return db.Report{}, f.reportErr
	}
	return f.report, nil
}

func (f *fakeStore) GetActiveRunByReport(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (BillingRun, error) {
	if f.active == nil {
		return BillingRun{}, ErrNotFound
	}
	return *f.active, nil
}

func (f *fakeStore) CreateRun(_ context.Context, _ *db.Queries, tenantID, reportID, generatedBy uuid.UUID) (BillingRun, error) {
	run := BillingRun{
		ID:          uuid.New(),
		TenantID:    tenantID,
		ReportID:    reportID,
		Status:      "draft",
		Version:     1,
		GeneratedBy: generatedBy,
		GeneratedAt: time.Now().UTC(),
	}
	f.run = run
	f.active = &run
	return run, nil
}

func (f *fakeStore) GetRun(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (BillingRun, error) {
	if f.runErr != nil {
		return BillingRun{}, f.runErr
	}
	return f.run, nil
}

func (f *fakeStore) ListRunsByReport(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]BillingRun, error) {
	return f.runList, nil
}

func (f *fakeStore) TouchRun(_ context.Context, _ *db.Queries, tenantID, id uuid.UUID, version int) (BillingRun, error) {
	f.touchedVersions = append(f.touchedVersions, version)
	run := BillingRun{ID: id, TenantID: tenantID, ReportID: f.report.ID, Status: "draft", Version: version + 1}
	f.run = run
	f.active = &run
	return run, nil
}

func (f *fakeStore) ApproveRun(_ context.Context, _ *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (BillingRun, error) {
	f.approveCalls++
	now := time.Now().UTC()
	run := f.run
	run.ID = id
	run.TenantID = tenantID
	run.Status = "approved"
	run.Version = version + 1
	run.ApprovedBy = &approvedBy
	run.ApprovedAt = &now
	f.run = run
	return run, nil
}

func (f *fakeStore) VoidRun(_ context.Context, _ *db.Queries, tenantID, id, voidedBy uuid.UUID, version int) (BillingRun, error) {
	f.voidCalls++
	now := time.Now().UTC()
	run := f.run
	run.ID = id
	run.TenantID = tenantID
	run.Status = "voided"
	run.Version = version + 1
	run.VoidedBy = &voidedBy
	run.VoidedAt = &now
	f.run = run
	return run, nil
}

func (f *fakeStore) DeleteLines(context.Context, *db.Queries, uuid.UUID, uuid.UUID) error {
	f.deletedLines++
	f.insertedLines = nil
	return nil
}

func (f *fakeStore) DeleteFindings(context.Context, *db.Queries, uuid.UUID, uuid.UUID) error {
	f.deletedFindings++
	f.insertedFindings = nil
	return nil
}

func (f *fakeStore) InsertLine(_ context.Context, _ *db.Queries, _ uuid.UUID, runID uuid.UUID, line Line) (BillingLine, error) {
	f.insertedLines = append(f.insertedLines, line)
	return BillingLine{
		ID:                  uuid.New(),
		RunID:               runID,
		ReportActivityID:    line.ReportActivityID,
		ActivityID:          line.ActivityID,
		ServiceDefinitionID: line.ServiceDefinitionID,
		ContractID:          line.ContractID,
		Quantity:            line.Quantity,
		PerformedAt:         line.PerformedAt,
		ServiceCode:         line.ServiceCode,
		ServiceName:         line.ServiceName,
		LegalArea:           line.LegalArea,
		PayerName:           line.PayerName,
		ContractReference:   line.ContractReference,
		SortOrder:           line.SortOrder,
	}, nil
}

func (f *fakeStore) InsertFinding(_ context.Context, _ *db.Queries, _ uuid.UUID, runID uuid.UUID, finding Finding) (BillingFinding, error) {
	f.insertedFindings = append(f.insertedFindings, finding)
	return BillingFinding{
		ID:         uuid.New(),
		RunID:      runID,
		Severity:   finding.Severity,
		Code:       finding.Code,
		Message:    finding.Message,
		ActivityID: finding.ActivityID,
	}, nil
}

func (f *fakeStore) ListLines(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]BillingLine, error) {
	if len(f.insertedLines) == 0 && len(f.run.Lines) > 0 {
		return f.run.Lines, nil
	}
	result := make([]BillingLine, 0, len(f.insertedLines))
	for _, line := range f.insertedLines {
		result = append(result, BillingLine{
			ID:                  uuid.New(),
			RunID:               f.run.ID,
			ReportActivityID:    line.ReportActivityID,
			ActivityID:          line.ActivityID,
			ServiceDefinitionID: line.ServiceDefinitionID,
			ContractID:          line.ContractID,
			Quantity:            line.Quantity,
			PerformedAt:         line.PerformedAt,
			ServiceCode:         line.ServiceCode,
			ServiceName:         line.ServiceName,
			LegalArea:           line.LegalArea,
			PayerName:           line.PayerName,
			ContractReference:   line.ContractReference,
			SortOrder:           line.SortOrder,
		})
	}
	return result, nil
}

func (f *fakeStore) ListFindings(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]BillingFinding, error) {
	if len(f.insertedFindings) == 0 && len(f.run.Findings) > 0 {
		return f.run.Findings, nil
	}
	result := make([]BillingFinding, 0, len(f.insertedFindings))
	for _, finding := range f.insertedFindings {
		result = append(result, BillingFinding{
			ID:         uuid.New(),
			RunID:      f.run.ID,
			Severity:   finding.Severity,
			Code:       finding.Code,
			Message:    finding.Message,
			ActivityID: finding.ActivityID,
		})
	}
	return result, nil
}

func (f *fakeStore) GetLineByActivity(_ context.Context, _ *db.Queries, _ uuid.UUID, activityID uuid.UUID) (BillingLine, error) {
	line, ok := f.billed[activityID]
	if !ok {
		return BillingLine{}, ErrNotFound
	}
	return line, nil
}

func (f *fakeStore) ListCandidates(context.Context, *db.Queries, uuid.UUID, uuid.UUID) ([]Candidate, error) {
	return f.candidates, nil
}

func (f *fakeStore) ListRules(context.Context, *db.Queries, uuid.UUID) ([]BillingRule, error) {
	return f.rules, nil
}

func (f *fakeStore) GetServiceDefinition(_ context.Context, _ *db.Queries, id uuid.UUID) (db.ServiceDefinition, error) {
	definition, ok := f.definitions[id]
	if !ok {
		return db.ServiceDefinition{}, ErrNotFound
	}
	return definition, nil
}

func (f *fakeStore) CreateRule(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (BillingRule, error) {
	f.createRuleCalls++
	return BillingRule{
		ID:                         uuid.New(),
		TenantID:                   tenantID,
		Kind:                       input.Kind,
		ServiceDefinitionID:        input.ServiceDefinitionID,
		RelatedServiceDefinitionID: input.RelatedServiceDefinitionID,
		LegalArea:                  input.LegalArea,
		Description:                input.Description,
		ValidFrom:                  input.ValidFrom,
		ValidUntil:                 input.ValidUntil,
	}, nil
}

func readyReport() db.Report {
	return db.Report{ID: uuid.New(), Status: "ready_for_billing", PatientID: uuid.New(), VisitID: uuid.New()}
}

func newBillingService(store Store, recorder audit.Recorder) *Service {
	return NewService(store, recorder)
}

func hasBillingFinding(findings []BillingFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestGenerateDeniesNonChef(t *testing.T) {
	store := &fakeStore{report: readyReport()}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}

	if _, err := service.Generate(context.Background(), nil, actor, "req", store.report.ID, GenerateInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if _, err := service.Generate(context.Background(), nil, Actor{Role: "platform_admin", TenantID: uuid.New()}, "req", store.report.ID, GenerateInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("platform admin err = %v, want ErrForbidden", err)
	}
}

func TestGenerateRequiresReadyForBillingReport(t *testing.T) {
	store := &fakeStore{report: db.Report{ID: uuid.New(), Status: "approved"}}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	if _, err := service.Generate(context.Background(), nil, actor, "req", store.report.ID, GenerateInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestGenerateCreatesFirstRunAndStoresFindings(t *testing.T) {
	report := readyReport()
	performedAt := time.Now().UTC()
	serviceID := uuid.New()
	contractID := uuid.New()
	candidate := testCandidate(uuid.New(), serviceID, contractID, performedAt)
	store := &fakeStore{
		report:     report,
		candidates: []Candidate{candidate},
		billed:     map[uuid.UUID]BillingLine{},
	}
	recorder := &fakeAudit{}
	service := newBillingService(store, recorder)
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	run, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{Version: 0})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if run.Status != "draft" || run.Version != 1 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Lines) != 1 || len(store.insertedLines) != 1 {
		t.Fatalf("lines = %+v", run.Lines)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "billing.run_generated" {
		t.Fatalf("audit events = %+v", recorder.events)
	}
}

func TestGenerateRejectsWrongVersionWhenNoRunExists(t *testing.T) {
	report := readyReport()
	store := &fakeStore{report: report, billed: map[uuid.UUID]BillingLine{}}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	if _, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{Version: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestGenerateRegeneratesDraftOnlyWithCurrentVersion(t *testing.T) {
	report := readyReport()
	active := BillingRun{ID: uuid.New(), ReportID: report.ID, Status: "draft", Version: 3}
	store := &fakeStore{report: report, active: &active, billed: map[uuid.UUID]BillingLine{}}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	if _, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{Version: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale err = %v, want ErrConflict", err)
	}
	run, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{Version: 3})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if run.Version != 4 || len(store.touchedVersions) != 1 || store.touchedVersions[0] != 3 {
		t.Fatalf("run = %+v, touched = %v", run, store.touchedVersions)
	}
	if store.deletedLines != 1 || store.deletedFindings != 1 {
		t.Fatalf("old lines/findings were not replaced: %d %d", store.deletedLines, store.deletedFindings)
	}
}

func TestGenerateRejectsApprovedRun(t *testing.T) {
	report := readyReport()
	active := BillingRun{ID: uuid.New(), ReportID: report.ID, Status: "approved", Version: 2}
	store := &fakeStore{report: report, active: &active, billed: map[uuid.UUID]BillingLine{}}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	if _, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{Version: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestGenerateDetectsAlreadyBilledActivity(t *testing.T) {
	report := readyReport()
	performedAt := time.Now().UTC()
	activityID := uuid.New()
	candidate := testCandidate(activityID, uuid.New(), uuid.New(), performedAt)
	otherRun := uuid.New()
	store := &fakeStore{
		report:     report,
		candidates: []Candidate{candidate},
		billed:     map[uuid.UUID]BillingLine{activityID: {ID: uuid.New(), RunID: otherRun, ActivityID: activityID}},
	}
	service := newBillingService(store, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	run, err := service.Generate(context.Background(), nil, actor, "req", report.ID, GenerateInput{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(run.Lines) != 0 {
		t.Fatalf("lines = %+v, want none", run.Lines)
	}
	if !hasBillingFinding(run.Findings, CodeBillingDuplicate) {
		t.Fatalf("findings = %+v, want billing duplicate", run.Findings)
	}
}

func TestApproveRules(t *testing.T) {
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	t.Run("only chef", func(t *testing.T) {
		store := &fakeStore{run: BillingRun{ID: uuid.New(), Status: "draft", Version: 1}}
		service := newBillingService(store, &fakeAudit{})
		if _, err := service.Approve(context.Background(), nil, Actor{Role: "employee", TenantID: uuid.New()}, "req", store.run.ID, 1); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("error finding blocks", func(t *testing.T) {
		store := &fakeStore{
			run: BillingRun{
				ID:       uuid.New(),
				Status:   "draft",
				Version:  1,
				Lines:    []BillingLine{{ID: uuid.New()}},
				Findings: []BillingFinding{{Severity: SeverityError, Code: CodeServiceMissing}},
			},
		}
		service := newBillingService(store, &fakeAudit{})
		if _, err := service.Approve(context.Background(), nil, actor, "req", store.run.ID, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("approves a valid draft", func(t *testing.T) {
		store := &fakeStore{
			run: BillingRun{
				ID:      uuid.New(),
				Status:  "draft",
				Version: 1,
				Lines:   []BillingLine{{ID: uuid.New()}},
			},
			billed: map[uuid.UUID]BillingLine{},
		}
		recorder := &fakeAudit{}
		service := newBillingService(store, recorder)
		run, err := service.Approve(context.Background(), nil, actor, "req", store.run.ID, 1)
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if run.Status != "approved" || store.approveCalls != 1 {
			t.Fatalf("run = %+v calls = %d", run, store.approveCalls)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "billing.run_approved" {
			t.Fatalf("audit events = %+v", recorder.events)
		}
	})

	t.Run("stale version", func(t *testing.T) {
		store := &fakeStore{run: BillingRun{ID: uuid.New(), Status: "draft", Version: 2, Lines: []BillingLine{{ID: uuid.New()}}}}
		service := newBillingService(store, &fakeAudit{})
		if _, err := service.Approve(context.Background(), nil, actor, "req", store.run.ID, 1); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestVoidDeletesLinesAndFindings(t *testing.T) {
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}
	store := &fakeStore{
		run: BillingRun{ID: uuid.New(), Status: "approved", Version: 2},
	}
	recorder := &fakeAudit{}
	service := newBillingService(store, recorder)

	run, err := service.Void(context.Background(), nil, actor, "req", store.run.ID, 2)
	if err != nil {
		t.Fatalf("Void: %v", err)
	}
	if run.Status != "voided" || store.voidCalls != 1 {
		t.Fatalf("run = %+v calls = %d", run, store.voidCalls)
	}
	if store.deletedLines != 1 || store.deletedFindings != 1 {
		t.Fatalf("lines/findings not deleted: %d %d", store.deletedLines, store.deletedFindings)
	}
	if len(recorder.events) != 1 || recorder.events[0].Action != "billing.run_voided" {
		t.Fatalf("audit events = %+v", recorder.events)
	}

	if _, err := service.Void(context.Background(), nil, actor, "req", store.run.ID, 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("void again err = %v, want ErrConflict", err)
	}
}

func TestExportRequiresApprovedRun(t *testing.T) {
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}
	store := &fakeStore{
		report: readyReport(),
		run:    BillingRun{ID: uuid.New(), Status: "draft", Version: 1, Lines: []BillingLine{{ID: uuid.New()}}},
	}
	service := newBillingService(store, &fakeAudit{})

	if _, err := service.ExportCSV(context.Background(), nil, actor, "req", store.run.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft export err = %v, want ErrConflict", err)
	}

	store.run.Status = "approved"
	data, err := service.ExportCSV(context.Background(), nil, actor, "req", store.run.ID)
	if err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty CSV export")
	}
}

func TestCreateRuleValidatesDefinitions(t *testing.T) {
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}
	serviceID := uuid.New()
	relatedID := uuid.New()
	validFrom := time.Now().UTC()

	t.Run("non-chef denied", func(t *testing.T) {
		service := newBillingService(&fakeStore{}, &fakeAudit{})
		if _, err := service.CreateRule(context.Background(), nil, Actor{Role: "employee", TenantID: uuid.New()}, "req", CreateRuleInput{}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		service := newBillingService(&fakeStore{}, &fakeAudit{})
		_, err := service.CreateRule(context.Background(), nil, actor, "req", CreateRuleInput{
			Kind:                       "magic",
			ServiceDefinitionID:        serviceID,
			RelatedServiceDefinitionID: relatedID,
			LegalArea:                  "sgb_xi",
			ValidFrom:                  validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("definition missing", func(t *testing.T) {
		service := newBillingService(&fakeStore{definitions: map[uuid.UUID]db.ServiceDefinition{}}, &fakeAudit{})
		_, err := service.CreateRule(context.Background(), nil, actor, "req", CreateRuleInput{
			Kind:                       RuleKindExclusion,
			ServiceDefinitionID:        serviceID,
			RelatedServiceDefinitionID: relatedID,
			LegalArea:                  "sgb_xi",
			ValidFrom:                  validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("legal area mismatch", func(t *testing.T) {
		store := &fakeStore{definitions: map[uuid.UUID]db.ServiceDefinition{
			serviceID: {ID: serviceID, LegalArea: "sgb_xi"},
			relatedID: {ID: relatedID, LegalArea: "sgb_v"},
		}}
		service := newBillingService(store, &fakeAudit{})
		_, err := service.CreateRule(context.Background(), nil, actor, "req", CreateRuleInput{
			Kind:                       RuleKindExclusion,
			ServiceDefinitionID:        serviceID,
			RelatedServiceDefinitionID: relatedID,
			LegalArea:                  "sgb_xi",
			ValidFrom:                  validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("creates a valid rule", func(t *testing.T) {
		store := &fakeStore{definitions: map[uuid.UUID]db.ServiceDefinition{
			serviceID: {ID: serviceID, LegalArea: "sgb_xi"},
			relatedID: {ID: relatedID, LegalArea: "sgb_xi"},
		}}
		recorder := &fakeAudit{}
		service := newBillingService(store, recorder)
		rule, err := service.CreateRule(context.Background(), nil, actor, "req", CreateRuleInput{
			Kind:                       RuleKindCombination,
			ServiceDefinitionID:        serviceID,
			RelatedServiceDefinitionID: relatedID,
			LegalArea:                  "sgb_xi",
			Description:                "Gemeinsam abrechnen",
			ValidFrom:                  validFrom,
		})
		if err != nil {
			t.Fatalf("CreateRule: %v", err)
		}
		if rule.Kind != RuleKindCombination || store.createRuleCalls != 1 {
			t.Fatalf("rule = %+v calls = %d", rule, store.createRuleCalls)
		}
		if len(recorder.events) != 1 || recorder.events[0].Action != "billing.rule_created" {
			t.Fatalf("audit events = %+v", recorder.events)
		}
	})
}

func TestReadOperationsAreChefOnly(t *testing.T) {
	store := &fakeStore{run: BillingRun{ID: uuid.New(), Status: "draft"}, report: readyReport()}
	service := newBillingService(store, &fakeAudit{})
	employee := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "employee"}
	admin := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "platform_admin"}

	for _, actor := range []Actor{employee, admin} {
		if _, err := service.Get(context.Background(), nil, actor, "req", store.run.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s Get err = %v, want ErrForbidden", actor.Role, err)
		}
		if _, err := service.ListRules(context.Background(), nil, actor, "req"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s ListRules err = %v, want ErrForbidden", actor.Role, err)
		}
	}
}

func TestTransmissionProceduresAreNotConfigured(t *testing.T) {
	procedures := TransmissionProcedures()
	if len(procedures) != 2 {
		t.Fatalf("procedures = %+v, want two", procedures)
	}
	for _, procedure := range procedures {
		if procedure.Status != TransmissionStatusNotConfigured {
			t.Fatalf("status = %q, want %q", procedure.Status, TransmissionStatusNotConfigured)
		}
	}
	procedures[0].Status = "sent"
	if TransmissionProcedures()[0].Status != TransmissionStatusNotConfigured {
		t.Fatal("TransmissionProcedures exposes mutable package state")
	}
	if err := (UnconfiguredTransmitter{}).Submit(context.Background(), BillingRun{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Submit err = %v, want ErrNotConfigured", err)
	}
}
