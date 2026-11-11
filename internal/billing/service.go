package billing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxRuleDescriptionRunes = 500
	maxBillingRules         = 1000
)

// Store is the persistence boundary used by the billing service.
type Store interface {
	GetReport(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Report, error)
	GetActiveRunByReport(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) (BillingRun, error)
	CreateRun(ctx context.Context, q *db.Queries, tenantID, reportID, generatedBy uuid.UUID) (BillingRun, error)
	GetRun(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (BillingRun, error)
	ListRunsByReport(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]BillingRun, error)
	TouchRun(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (BillingRun, error)
	ApproveRun(ctx context.Context, q *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (BillingRun, error)
	VoidRun(ctx context.Context, q *db.Queries, tenantID, id, voidedBy uuid.UUID, version int) (BillingRun, error)
	DeleteLines(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) error
	DeleteFindings(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) error
	InsertLine(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID, line Line) (BillingLine, error)
	InsertFinding(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID, finding Finding) (BillingFinding, error)
	ListLines(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) ([]BillingLine, error)
	ListFindings(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) ([]BillingFinding, error)
	GetLineByActivity(ctx context.Context, q *db.Queries, tenantID, activityID uuid.UUID) (BillingLine, error)
	ListCandidates(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]Candidate, error)
	ListRules(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]BillingRule, error)
	GetServiceDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (db.ServiceDefinition, error)
	CreateRule(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (BillingRule, error)
}

// Service contains the deterministic billing assistant rules (doc 14).
type Service struct {
	store Store
	audit audit.Recorder
}

func NewService(store Store, recorder audit.Recorder) *Service {
	return &Service{store: store, audit: recorder}
}

// GenerateInput controls a generate or regenerate call. Version is 0 for the
// first run of a report and the current run version when regenerating a draft.
type GenerateInput struct {
	Version int
}

// Generate evaluates a report deterministically and stores the suggested lines
// and findings. Only a chef may generate. A report must be ready for billing.
// An approved run must be voided before it can be regenerated.
func (s *Service) Generate(ctx context.Context, q *db.Queries, actor Actor, requestID string, reportID uuid.UUID, input GenerateInput) (BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.generate")
		return BillingRun{}, ErrForbidden
	}
	if reportID == uuid.Nil {
		return BillingRun{}, fmt.Errorf("%w: report id is required", ErrInvalid)
	}

	report, err := s.store.GetReport(ctx, q, actor.TenantID, reportID)
	if err != nil {
		return BillingRun{}, err
	}
	if report.Status != "ready_for_billing" {
		return BillingRun{}, fmt.Errorf("%w: only a report ready for billing can be evaluated", ErrConflict)
	}

	run, err := s.prepareRun(ctx, q, actor, reportID, input.Version)
	if err != nil {
		return BillingRun{}, err
	}

	if err := s.store.DeleteLines(ctx, q, actor.TenantID, run.ID); err != nil {
		return BillingRun{}, err
	}
	if err := s.store.DeleteFindings(ctx, q, actor.TenantID, run.ID); err != nil {
		return BillingRun{}, err
	}

	rules, err := s.store.ListRules(ctx, q, actor.TenantID)
	if err != nil {
		return BillingRun{}, err
	}
	candidates, err := s.store.ListCandidates(ctx, q, actor.TenantID, reportID)
	if err != nil {
		return BillingRun{}, err
	}

	alreadyBilled, err := s.alreadyBilled(ctx, q, actor.TenantID, candidates)
	if err != nil {
		return BillingRun{}, err
	}

	evaluation := Evaluate(EvaluateInput{
		ReportStatus:  report.Status,
		Candidates:    candidates,
		Rules:         engineRules(rules),
		AlreadyBilled: alreadyBilled,
	})

	for _, line := range evaluation.Lines {
		if _, err := s.store.InsertLine(ctx, q, actor.TenantID, run.ID, line); err != nil {
			return BillingRun{}, err
		}
	}
	for _, finding := range evaluation.Findings {
		if _, err := s.store.InsertFinding(ctx, q, actor.TenantID, run.ID, finding); err != nil {
			return BillingRun{}, err
		}
	}

	run, err = s.loadRun(ctx, q, actor.TenantID, run.ID)
	if err != nil {
		return BillingRun{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "billing.run_generated",
		EntityType:  "billing_run",
		EntityID:    run.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"report_id":     run.ReportID,
			"version":       run.Version,
			"line_count":    len(run.Lines),
			"error_count":   countSeverity(run.Findings, SeverityError),
			"warning_count": countSeverity(run.Findings, SeverityWarning),
		},
	}); err != nil {
		return BillingRun{}, err
	}
	return run, nil
}

// prepareRun finds the active run and bumps its version, or creates the first
// run of a report. Regeneration is only allowed while the run is a draft.
func (s *Service) prepareRun(ctx context.Context, q *db.Queries, actor Actor, reportID uuid.UUID, version int) (BillingRun, error) {
	active, err := s.store.GetActiveRunByReport(ctx, q, actor.TenantID, reportID)
	if errors.Is(err, ErrNotFound) {
		if version != 0 {
			return BillingRun{}, fmt.Errorf("%w: the report has no billing run yet", ErrConflict)
		}
		run, err := s.store.CreateRun(ctx, q, actor.TenantID, reportID, actor.UserID)
		if err != nil {
			if isUniqueViolation(err) {
				return BillingRun{}, fmt.Errorf("%w: a billing run already exists", ErrConflict)
			}
			return BillingRun{}, err
		}
		return run, nil
	}
	if err != nil {
		return BillingRun{}, err
	}

	if active.Status != "draft" {
		return BillingRun{}, fmt.Errorf("%w: an approved run must be voided before it is regenerated", ErrConflict)
	}
	if version != active.Version {
		return BillingRun{}, ErrConflict
	}
	run, err := s.store.TouchRun(ctx, q, actor.TenantID, active.ID, version)
	if err != nil {
		return BillingRun{}, err
	}
	return run, nil
}

// alreadyBilled maps an activity to the run that already bills it. The current
// run has no lines at this point, so every hit is a different run.
func (s *Service) alreadyBilled(ctx context.Context, q *db.Queries, tenantID uuid.UUID, candidates []Candidate) (map[uuid.UUID]uuid.UUID, error) {
	result := make(map[uuid.UUID]uuid.UUID)
	for _, candidate := range candidates {
		line, err := s.store.GetLineByActivity(ctx, q, tenantID, candidate.ActivityID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[candidate.ActivityID] = line.RunID
	}
	return result, nil
}

// Get returns one billing run with its lines and findings.
func (s *Service) Get(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) (BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.read")
		return BillingRun{}, ErrForbidden
	}
	return s.loadRun(ctx, q, actor.TenantID, id)
}

// GetActive returns the draft or approved run of a report.
func (s *Service) GetActive(ctx context.Context, q *db.Queries, actor Actor, requestID string, reportID uuid.UUID) (BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.read")
		return BillingRun{}, ErrForbidden
	}
	if _, err := s.store.GetReport(ctx, q, actor.TenantID, reportID); err != nil {
		return BillingRun{}, err
	}
	run, err := s.store.GetActiveRunByReport(ctx, q, actor.TenantID, reportID)
	if err != nil {
		return BillingRun{}, err
	}
	return s.attach(ctx, q, actor.TenantID, run)
}

// ListByReport returns every run of a report, newest first, including voided
// history.
func (s *Service) ListByReport(ctx context.Context, q *db.Queries, actor Actor, requestID string, reportID uuid.UUID) ([]BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.read")
		return nil, ErrForbidden
	}
	if _, err := s.store.GetReport(ctx, q, actor.TenantID, reportID); err != nil {
		return nil, err
	}
	return s.store.ListRunsByReport(ctx, q, actor.TenantID, reportID)
}

// Approve approves a draft run. A run with an error finding or no line cannot
// be approved.
func (s *Service) Approve(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, version int) (BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.approve")
		return BillingRun{}, ErrForbidden
	}
	run, err := s.loadRun(ctx, q, actor.TenantID, id)
	if err != nil {
		return BillingRun{}, err
	}
	if err := ValidateApproval(run); err != nil {
		return BillingRun{}, err
	}
	if run.Version != version {
		return BillingRun{}, ErrConflict
	}

	updated, err := s.store.ApproveRun(ctx, q, actor.TenantID, id, actor.UserID, version)
	if err != nil {
		return BillingRun{}, err
	}
	updated, err = s.attach(ctx, q, actor.TenantID, updated)
	if err != nil {
		return BillingRun{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "billing.run_approved",
		EntityType:  "billing_run",
		EntityID:    updated.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"report_id":  updated.ReportID,
			"version":    updated.Version,
			"line_count": len(updated.Lines),
		},
	}); err != nil {
		return BillingRun{}, err
	}
	return updated, nil
}

// Void voids a draft or approved run. Its lines and findings are deleted so the
// performed activities can be billed again by a corrected run.
func (s *Service) Void(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, version int) (BillingRun, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.void")
		return BillingRun{}, ErrForbidden
	}
	run, err := s.store.GetRun(ctx, q, actor.TenantID, id)
	if err != nil {
		return BillingRun{}, err
	}
	if run.Status == "voided" {
		return BillingRun{}, fmt.Errorf("%w: the billing run is already voided", ErrConflict)
	}
	if run.Version != version {
		return BillingRun{}, ErrConflict
	}

	updated, err := s.store.VoidRun(ctx, q, actor.TenantID, id, actor.UserID, version)
	if err != nil {
		return BillingRun{}, err
	}
	if err := s.store.DeleteLines(ctx, q, actor.TenantID, id); err != nil {
		return BillingRun{}, err
	}
	if err := s.store.DeleteFindings(ctx, q, actor.TenantID, id); err != nil {
		return BillingRun{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "billing.run_voided",
		EntityType:  "billing_run",
		EntityID:    updated.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"report_id": updated.ReportID,
			"version":   updated.Version,
		},
	}); err != nil {
		return BillingRun{}, err
	}
	return updated, nil
}

// ExportCSV returns the internal CSV export of an approved run.
func (s *Service) ExportCSV(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) ([]byte, error) {
	data, err := s.exportData(ctx, q, actor, requestID, id, "csv")
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := WriteCSV(&buffer, data); err != nil {
		return nil, fmt.Errorf("write csv: %w", err)
	}
	return buffer.Bytes(), nil
}

// ExportPDF returns the internal PDF export of an approved run.
func (s *Service) ExportPDF(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID) ([]byte, error) {
	data, err := s.exportData(ctx, q, actor, requestID, id, "pdf")
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := WritePDF(&buffer, data); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *Service) exportData(ctx context.Context, q *db.Queries, actor Actor, requestID string, id uuid.UUID, format string) (ExportData, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.export")
		return ExportData{}, ErrForbidden
	}
	run, err := s.loadRun(ctx, q, actor.TenantID, id)
	if err != nil {
		return ExportData{}, err
	}
	if err := ValidateExport(run); err != nil {
		return ExportData{}, err
	}
	report, err := s.store.GetReport(ctx, q, actor.TenantID, run.ReportID)
	if err != nil {
		return ExportData{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "billing.run_exported",
		EntityType:  "billing_run",
		EntityID:    run.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"report_id":  run.ReportID,
			"version":    run.Version,
			"format":     format,
			"line_count": len(run.Lines),
		},
	}); err != nil {
		return ExportData{}, err
	}

	return ExportData{
		Run:       run,
		ReportID:  run.ReportID,
		PatientID: report.PatientID,
		VisitID:   report.VisitID,
		Lines:     run.Lines,
	}, nil
}

// ListRules returns the tenant billing rules.
func (s *Service) ListRules(ctx context.Context, q *db.Queries, actor Actor, requestID string) ([]BillingRule, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.read")
		return nil, ErrForbidden
	}
	return s.store.ListRules(ctx, q, actor.TenantID)
}

// CreateRule adds a tenant-specific exclusion, prerequisite or combination
// rule. Both service definitions must exist and share the stated legal area.
func (s *Service) CreateRule(ctx context.Context, q *db.Queries, actor Actor, requestID string, input CreateRuleInput) (BillingRule, error) {
	if !canManage(actor.Role) {
		s.recordDenied(ctx, q, actor, requestID, "billing.rule_create")
		return BillingRule{}, ErrForbidden
	}
	normalized, err := validateRuleInput(input)
	if err != nil {
		return BillingRule{}, err
	}

	definition, err := s.store.GetServiceDefinition(ctx, q, normalized.ServiceDefinitionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return BillingRule{}, fmt.Errorf("%w: service definition not found", ErrInvalid)
		}
		return BillingRule{}, err
	}
	related, err := s.store.GetServiceDefinition(ctx, q, normalized.RelatedServiceDefinitionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return BillingRule{}, fmt.Errorf("%w: related service definition not found", ErrInvalid)
		}
		return BillingRule{}, err
	}
	if definition.LegalArea != normalized.LegalArea || related.LegalArea != normalized.LegalArea {
		return BillingRule{}, fmt.Errorf("%w: the rule legal area does not match both service definitions", ErrInvalid)
	}

	rules, err := s.store.ListRules(ctx, q, actor.TenantID)
	if err != nil {
		return BillingRule{}, err
	}
	if len(rules) >= maxBillingRules {
		return BillingRule{}, fmt.Errorf("%w: too many billing rules", ErrInvalid)
	}

	rule, err := s.store.CreateRule(ctx, q, actor.TenantID, normalized)
	if err != nil {
		if isUniqueViolation(err) {
			return BillingRule{}, fmt.Errorf("%w: this rule version already exists", ErrConflict)
		}
		return BillingRule{}, err
	}

	if err := s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "billing.rule_created",
		EntityType:  "billing_rule",
		EntityID:    rule.ID,
		RequestID:   requestID,
		Metadata: map[string]any{
			"kind":                          rule.Kind,
			"service_definition_id":         rule.ServiceDefinitionID,
			"related_service_definition_id": rule.RelatedServiceDefinitionID,
			"legal_area":                    rule.LegalArea,
		},
	}); err != nil {
		return BillingRule{}, err
	}
	return rule, nil
}

func (s *Service) loadRun(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (BillingRun, error) {
	run, err := s.store.GetRun(ctx, q, tenantID, id)
	if err != nil {
		return BillingRun{}, err
	}
	return s.attach(ctx, q, tenantID, run)
}

func (s *Service) attach(ctx context.Context, q *db.Queries, tenantID uuid.UUID, run BillingRun) (BillingRun, error) {
	lines, err := s.store.ListLines(ctx, q, tenantID, run.ID)
	if err != nil {
		return BillingRun{}, err
	}
	findings, err := s.store.ListFindings(ctx, q, tenantID, run.ID)
	if err != nil {
		return BillingRun{}, err
	}
	run.Lines = lines
	run.Findings = findings
	return run, nil
}

func validateRuleInput(input CreateRuleInput) (CreateRuleInput, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	switch input.Kind {
	case RuleKindExclusion, RuleKindPrerequisite, RuleKindCombination:
	default:
		return CreateRuleInput{}, fmt.Errorf("%w: unknown rule kind", ErrInvalid)
	}
	if input.ServiceDefinitionID == uuid.Nil || input.RelatedServiceDefinitionID == uuid.Nil {
		return CreateRuleInput{}, fmt.Errorf("%w: both service definitions are required", ErrInvalid)
	}
	if input.ServiceDefinitionID == input.RelatedServiceDefinitionID {
		return CreateRuleInput{}, fmt.Errorf("%w: a rule needs two different services", ErrInvalid)
	}
	if input.LegalArea != "sgb_xi" && input.LegalArea != "sgb_v" {
		return CreateRuleInput{}, fmt.Errorf("%w: unknown legal area", ErrInvalid)
	}
	if input.ValidFrom.IsZero() {
		return CreateRuleInput{}, fmt.Errorf("%w: valid_from is required", ErrInvalid)
	}
	if input.ValidUntil != nil && !input.ValidUntil.After(input.ValidFrom) {
		return CreateRuleInput{}, fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalid)
	}
	input.Description = strings.TrimSpace(input.Description)
	if len([]rune(input.Description)) > maxRuleDescriptionRunes {
		return CreateRuleInput{}, fmt.Errorf("%w: the description is too long", ErrInvalid)
	}
	return input, nil
}

func engineRules(rules []BillingRule) []Rule {
	result := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, Rule{
			ID:                         rule.ID,
			Kind:                       rule.Kind,
			ServiceDefinitionID:        rule.ServiceDefinitionID,
			RelatedServiceDefinitionID: rule.RelatedServiceDefinitionID,
			LegalArea:                  rule.LegalArea,
			Description:                rule.Description,
			ValidFrom:                  rule.ValidFrom,
			ValidUntil:                 rule.ValidUntil,
		})
	}
	return result
}

func countSeverity(findings []BillingFinding, severity string) int {
	count := 0
	for _, finding := range findings {
		if finding.Severity == severity {
			count++
		}
	}
	return count
}

func (s *Service) record(ctx context.Context, q *db.Queries, event audit.Event) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, q, event)
}

func (s *Service) recordDenied(ctx context.Context, q *db.Queries, actor Actor, requestID, action string) {
	if actor.TenantID == uuid.Nil {
		return
	}
	// Best effort: a denied request must not turn into a 500.
	_ = s.record(ctx, q, audit.Event{
		TenantID:    actor.TenantID,
		ActorUserID: actor.UserID,
		Action:      "access.denied",
		EntityType:  "billing_run",
		RequestID:   requestID,
		Metadata:    map[string]any{"attempted_action": action},
	})
}

// canManage allows only a chef to generate, approve, void or export billing
// data. A platform admin gets no automatic tenant access (doc 4).
func canManage(role string) bool {
	return role == "chef"
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
