package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Repository persists billing runs, lines, findings and tenant rules with sqlc
// queries.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) GetReport(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Report, error) {
	row, err := q.GetReportForBilling(ctx, db.GetReportForBillingParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Report{}, ErrNotFound
	}
	if err != nil {
		return db.Report{}, fmt.Errorf("get report for billing: %w", err)
	}
	return row, nil
}

func (r *Repository) GetActiveRunByReport(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) (BillingRun, error) {
	row, err := q.GetActiveBillingRunByReport(ctx, db.GetActiveBillingRunByReportParams{
		TenantID: tenantID,
		ReportID: reportID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingRun{}, ErrNotFound
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("get active billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) CreateRun(ctx context.Context, q *db.Queries, tenantID, reportID, generatedBy uuid.UUID) (BillingRun, error) {
	row, err := q.CreateBillingRun(ctx, db.CreateBillingRunParams{
		ID:          uuid.New(),
		TenantID:    tenantID,
		ReportID:    reportID,
		GeneratedBy: generatedBy,
	})
	if err != nil {
		return BillingRun{}, fmt.Errorf("create billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) GetRun(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (BillingRun, error) {
	row, err := q.GetBillingRun(ctx, db.GetBillingRunParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingRun{}, ErrNotFound
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("get billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) ListRunsByReport(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]BillingRun, error) {
	rows, err := q.ListBillingRunsByReport(ctx, db.ListBillingRunsByReportParams{
		TenantID: tenantID,
		ReportID: reportID,
	})
	if err != nil {
		return nil, fmt.Errorf("list billing runs: %w", err)
	}
	result := make([]BillingRun, 0, len(rows))
	for _, row := range rows {
		result = append(result, toBillingRun(row))
	}
	return result, nil
}

func (r *Repository) TouchRun(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (BillingRun, error) {
	row, err := q.TouchBillingRun(ctx, db.TouchBillingRunParams{
		ID:       id,
		TenantID: tenantID,
		Version:  int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingRun{}, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("touch billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) ApproveRun(ctx context.Context, q *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (BillingRun, error) {
	row, err := q.ApproveBillingRun(ctx, db.ApproveBillingRunParams{
		ID:         id,
		TenantID:   tenantID,
		ApprovedBy: uuid.NullUUID{UUID: approvedBy, Valid: true},
		Version:    int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingRun{}, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("approve billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) VoidRun(ctx context.Context, q *db.Queries, tenantID, id, voidedBy uuid.UUID, version int) (BillingRun, error) {
	row, err := q.VoidBillingRun(ctx, db.VoidBillingRunParams{
		ID:       id,
		TenantID: tenantID,
		VoidedBy: uuid.NullUUID{UUID: voidedBy, Valid: true},
		Version:  int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingRun{}, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("void billing run: %w", err)
	}
	return toBillingRun(row), nil
}

func (r *Repository) DeleteLines(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) error {
	if err := q.DeleteBillingLines(ctx, db.DeleteBillingLinesParams{RunID: runID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("delete billing lines: %w", err)
	}
	return nil
}

func (r *Repository) DeleteFindings(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) error {
	if err := q.DeleteBillingFindings(ctx, db.DeleteBillingFindingsParams{RunID: runID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("delete billing findings: %w", err)
	}
	return nil
}

func (r *Repository) InsertLine(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID, line Line) (BillingLine, error) {
	row, err := q.InsertBillingLine(ctx, db.InsertBillingLineParams{
		TenantID:            tenantID,
		RunID:               runID,
		ReportActivityID:    line.ReportActivityID,
		ActivityID:          line.ActivityID,
		ServiceDefinitionID: line.ServiceDefinitionID,
		ContractID:          line.ContractID,
		Quantity:            int32(line.Quantity),
		PerformedAt:         line.PerformedAt,
		ServiceCode:         line.ServiceCode,
		ServiceName:         line.ServiceName,
		LegalArea:           line.LegalArea,
		PayerName:           line.PayerName,
		ContractReference:   line.ContractReference,
		SortOrder:           int32(line.SortOrder),
	})
	if err != nil {
		return BillingLine{}, fmt.Errorf("insert billing line: %w", err)
	}
	return toBillingLine(row), nil
}

func (r *Repository) InsertFinding(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID, finding Finding) (BillingFinding, error) {
	activityID := uuid.NullUUID{}
	if finding.ActivityID != nil {
		activityID = uuid.NullUUID{UUID: *finding.ActivityID, Valid: true}
	}
	row, err := q.InsertBillingFinding(ctx, db.InsertBillingFindingParams{
		TenantID:   tenantID,
		RunID:      runID,
		Severity:   finding.Severity,
		Code:       finding.Code,
		Message:    finding.Message,
		ActivityID: activityID,
	})
	if err != nil {
		return BillingFinding{}, fmt.Errorf("insert billing finding: %w", err)
	}
	return toBillingFinding(row), nil
}

func (r *Repository) ListLines(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) ([]BillingLine, error) {
	rows, err := q.ListBillingLines(ctx, db.ListBillingLinesParams{RunID: runID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list billing lines: %w", err)
	}
	result := make([]BillingLine, 0, len(rows))
	for _, row := range rows {
		result = append(result, toBillingLine(row))
	}
	return result, nil
}

func (r *Repository) ListFindings(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) ([]BillingFinding, error) {
	rows, err := q.ListBillingFindings(ctx, db.ListBillingFindingsParams{RunID: runID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list billing findings: %w", err)
	}
	result := make([]BillingFinding, 0, len(rows))
	for _, row := range rows {
		result = append(result, toBillingFinding(row))
	}
	return result, nil
}

func (r *Repository) GetLineByActivity(ctx context.Context, q *db.Queries, tenantID, activityID uuid.UUID) (BillingLine, error) {
	row, err := q.GetBillingLineByActivity(ctx, db.GetBillingLineByActivityParams{
		TenantID:   tenantID,
		ActivityID: activityID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BillingLine{}, ErrNotFound
	}
	if err != nil {
		return BillingLine{}, fmt.Errorf("get billing line by activity: %w", err)
	}
	return toBillingLine(row), nil
}

func (r *Repository) ListCandidates(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]Candidate, error) {
	rows, err := q.ListBillingCandidates(ctx, db.ListBillingCandidatesParams{
		ReportID: reportID,
		TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("list billing candidates: %w", err)
	}
	result := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, toCandidate(row))
	}
	return result, nil
}

func (r *Repository) ListRules(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]BillingRule, error) {
	rows, err := q.ListBillingRules(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list billing rules: %w", err)
	}
	result := make([]BillingRule, 0, len(rows))
	for _, row := range rows {
		result = append(result, toBillingRule(row))
	}
	return result, nil
}

func (r *Repository) GetServiceDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (db.ServiceDefinition, error) {
	row, err := q.GetBillingServiceDefinition(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ServiceDefinition{}, ErrNotFound
	}
	if err != nil {
		return db.ServiceDefinition{}, fmt.Errorf("get service definition: %w", err)
	}
	return row, nil
}

func (r *Repository) CreateRule(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (BillingRule, error) {
	validUntil := sql.NullTime{}
	if input.ValidUntil != nil {
		validUntil = sql.NullTime{Time: *input.ValidUntil, Valid: true}
	}
	row, err := q.CreateBillingRule(ctx, db.CreateBillingRuleParams{
		TenantID:                   tenantID,
		Kind:                       input.Kind,
		ServiceDefinitionID:        input.ServiceDefinitionID,
		RelatedServiceDefinitionID: input.RelatedServiceDefinitionID,
		LegalArea:                  input.LegalArea,
		Description:                input.Description,
		ValidFrom:                  input.ValidFrom,
		ValidUntil:                 validUntil,
	})
	if err != nil {
		return BillingRule{}, fmt.Errorf("create billing rule: %w", err)
	}
	return toBillingRule(row), nil
}

func (r *Repository) CreateTransmission(ctx context.Context, q *db.Queries, params CreateTransmissionParams) (Transmission, error) {
	row, err := q.CreateBillingTransmission(ctx, db.CreateBillingTransmissionParams{
		TenantID:       params.TenantID,
		RunID:          params.RunID,
		LegalArea:      params.LegalArea,
		Adapter:        params.Adapter,
		IdempotencyKey: params.IdempotencyKey,
		PayloadSha256:  params.PayloadSHA256,
		Attempt:        int32(params.Attempt),
		SubmittedBy:    params.SubmittedBy,
	})
	if err != nil {
		return Transmission{}, fmt.Errorf("create billing transmission: %w", err)
	}
	return toTransmission(row), nil
}

func (r *Repository) GetTransmissionByKey(ctx context.Context, q *db.Queries, tenantID uuid.UUID, key string) (Transmission, error) {
	row, err := q.GetBillingTransmissionByKey(ctx, db.GetBillingTransmissionByKeyParams{
		TenantID:       tenantID,
		IdempotencyKey: key,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Transmission{}, ErrNotFound
	}
	if err != nil {
		return Transmission{}, fmt.Errorf("get billing transmission by key: %w", err)
	}
	return toTransmission(row), nil
}

func (r *Repository) GetActiveTransmissionByRun(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) (Transmission, error) {
	row, err := q.GetActiveBillingTransmissionByRun(ctx, db.GetActiveBillingTransmissionByRunParams{
		TenantID: tenantID,
		RunID:    runID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Transmission{}, ErrNotFound
	}
	if err != nil {
		return Transmission{}, fmt.Errorf("get active billing transmission: %w", err)
	}
	return toTransmission(row), nil
}

func (r *Repository) CountTransmissionsByRun(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) (int, error) {
	count, err := q.CountBillingTransmissionsByRun(ctx, db.CountBillingTransmissionsByRunParams{
		TenantID: tenantID,
		RunID:    runID,
	})
	if err != nil {
		return 0, fmt.Errorf("count billing transmissions: %w", err)
	}
	return int(count), nil
}

func (r *Repository) CompleteTransmission(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, result TransmissionResult) (Transmission, error) {
	row, err := q.CompleteBillingTransmission(ctx, db.CompleteBillingTransmissionParams{
		ID:                id,
		TenantID:          tenantID,
		Status:            result.Status,
		ExternalReference: result.ExternalReference,
		ResponseCode:      result.ResponseCode,
		ResponseMessage:   result.ResponseMessage,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Transmission{}, ErrConflict
	}
	if err != nil {
		return Transmission{}, fmt.Errorf("complete billing transmission: %w", err)
	}
	return toTransmission(row), nil
}

func (r *Repository) ListTransmissionsByRun(ctx context.Context, q *db.Queries, tenantID, runID uuid.UUID) ([]Transmission, error) {
	rows, err := q.ListBillingTransmissionsByRun(ctx, db.ListBillingTransmissionsByRunParams{
		TenantID: tenantID,
		RunID:    runID,
	})
	if err != nil {
		return nil, fmt.Errorf("list billing transmissions: %w", err)
	}
	result := make([]Transmission, 0, len(rows))
	for _, row := range rows {
		result = append(result, toTransmission(row))
	}
	return result, nil
}

func toTransmission(row db.BillingTransmission) Transmission {
	transmission := Transmission{
		ID:                row.ID,
		TenantID:          row.TenantID,
		RunID:             row.RunID,
		LegalArea:         row.LegalArea,
		Procedure:         ProcedureLabel(row.LegalArea),
		Adapter:           row.Adapter,
		Status:            row.Status,
		IdempotencyKey:    row.IdempotencyKey,
		PayloadSHA256:     row.PayloadSha256,
		Attempt:           int(row.Attempt),
		ExternalReference: row.ExternalReference,
		ResponseCode:      row.ResponseCode,
		ResponseMessage:   row.ResponseMessage,
		SubmittedBy:       row.SubmittedBy,
		SubmittedAt:       row.SubmittedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
	if row.CompletedAt.Valid {
		value := row.CompletedAt.Time
		transmission.CompletedAt = &value
	}
	return transmission
}

func toBillingRun(row db.BillingRun) BillingRun {
	run := BillingRun{
		ID:          row.ID,
		TenantID:    row.TenantID,
		ReportID:    row.ReportID,
		Status:      row.Status,
		Version:     int(row.Version),
		GeneratedBy: row.GeneratedBy,
		GeneratedAt: row.GeneratedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.ApprovedBy.Valid {
		value := row.ApprovedBy.UUID
		run.ApprovedBy = &value
	}
	if row.ApprovedAt.Valid {
		value := row.ApprovedAt.Time
		run.ApprovedAt = &value
	}
	if row.VoidedBy.Valid {
		value := row.VoidedBy.UUID
		run.VoidedBy = &value
	}
	if row.VoidedAt.Valid {
		value := row.VoidedAt.Time
		run.VoidedAt = &value
	}
	return run
}

func toBillingLine(row db.BillingLine) BillingLine {
	return BillingLine{
		ID:                  row.ID,
		RunID:               row.RunID,
		ReportActivityID:    row.ReportActivityID,
		ActivityID:          row.ActivityID,
		ServiceDefinitionID: row.ServiceDefinitionID,
		ContractID:          row.ContractID,
		Quantity:            int(row.Quantity),
		PerformedAt:         row.PerformedAt,
		ServiceCode:         row.ServiceCode,
		ServiceName:         row.ServiceName,
		LegalArea:           row.LegalArea,
		PayerName:           row.PayerName,
		ContractReference:   row.ContractReference,
		SortOrder:           int(row.SortOrder),
	}
}

func toBillingFinding(row db.BillingFinding) BillingFinding {
	finding := BillingFinding{
		ID:       row.ID,
		RunID:    row.RunID,
		Severity: row.Severity,
		Code:     row.Code,
		Message:  row.Message,
	}
	if row.ActivityID.Valid {
		value := row.ActivityID.UUID
		finding.ActivityID = &value
	}
	return finding
}

func toBillingRule(row db.BillingRule) BillingRule {
	rule := BillingRule{
		ID:                         row.ID,
		TenantID:                   row.TenantID,
		Kind:                       row.Kind,
		ServiceDefinitionID:        row.ServiceDefinitionID,
		RelatedServiceDefinitionID: row.RelatedServiceDefinitionID,
		LegalArea:                  row.LegalArea,
		Description:                row.Description,
		ValidFrom:                  row.ValidFrom,
		CreatedAt:                  row.CreatedAt,
		UpdatedAt:                  row.UpdatedAt,
	}
	if row.ValidUntil.Valid {
		value := row.ValidUntil.Time
		rule.ValidUntil = &value
	}
	return rule
}

func toCandidate(row db.ListBillingCandidatesRow) Candidate {
	candidate := Candidate{
		ReportActivityID:          row.ReportActivityID,
		ActivityID:                row.ActivityID,
		Quantity:                  int(row.Quantity),
		PerformedAt:               row.ReportPerformedAt,
		SortOrder:                 int(row.SortOrder),
		ActivityStatus:            row.ActivityStatus,
		SnapshotServiceCode:       row.SnapshotServiceCode,
		SnapshotServiceName:       row.SnapshotServiceName,
		SnapshotLegalArea:         row.SnapshotLegalArea,
		SnapshotPayerName:         row.SnapshotPayerName,
		SnapshotContractReference: row.SnapshotContractReference,
	}
	if row.ServiceDefinitionID.Valid {
		value := row.ServiceDefinitionID.UUID
		candidate.ServiceDefinitionID = &value
	}
	if row.ContractID.Valid {
		value := row.ContractID.UUID
		candidate.ContractID = &value
	}
	if row.DefinitionCode.Valid {
		candidate.DefinitionFound = true
		candidate.DefinitionCode = row.DefinitionCode.String
		candidate.DefinitionName = row.DefinitionName.String
		candidate.DefinitionLegalArea = row.DefinitionLegalArea.String
		if row.DefinitionValidFrom.Valid {
			candidate.DefinitionValidFrom = row.DefinitionValidFrom.Time
		}
		if row.DefinitionValidUntil.Valid {
			value := row.DefinitionValidUntil.Time
			candidate.DefinitionValidUntil = &value
		}
	}
	if row.ContractPayerName.Valid {
		candidate.ContractFound = true
		candidate.ContractPayerName = row.ContractPayerName.String
		candidate.ContractReference = row.ContractReference.String
		candidate.ContractLegalArea = row.ContractLegalArea.String
		if row.ContractValidFrom.Valid {
			candidate.ContractValidFrom = row.ContractValidFrom.Time
		}
		if row.ContractValidUntil.Valid {
			value := row.ContractValidUntil.Time
			candidate.ContractValidUntil = &value
		}
	}
	return candidate
}
