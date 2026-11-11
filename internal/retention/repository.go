package retention

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Store is the persistence boundary used by the service. Every method runs
// inside the caller's tenant-scoped transaction, so row-level security limits
// each query to one tenant.
type Store interface {
	PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error)

	CreatePolicy(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input PolicyInput, createdBy uuid.UUID) (Policy, error)
	GetPolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Policy, error)
	ListPolicies(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Policy, error)
	UpdatePolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, periodMonths int, note string) (Policy, error)
	ConfirmPolicy(ctx context.Context, q *db.Queries, tenantID, id, confirmedBy uuid.UUID) (Policy, error)
	DisablePolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Policy, error)

	CreateLegalHold(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input LegalHoldInput, declaredBy uuid.UUID) (LegalHold, error)
	GetLegalHold(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (LegalHold, error)
	ListLegalHolds(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]LegalHold, error)
	ListActiveLegalHoldsForPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]LegalHold, error)
	HasActiveLegalHold(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error)
	LiftLegalHold(ctx context.Context, q *db.Queries, tenantID, id, liftedBy uuid.UUID, reason string) (LegalHold, error)

	CreateDeletionRequest(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input DeletionRequestInput, requestedBy uuid.UUID) (DeletionRequest, error)
	GetDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (DeletionRequest, error)
	ListDeletionRequests(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]DeletionRequest, error)
	ApproveDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error)
	RejectDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error)
	CancelDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, cancelledBy uuid.UUID) (DeletionRequest, error)
	MarkDeletionExecuted(ctx context.Context, q *db.Queries, tenantID, id, executedBy uuid.UUID) (DeletionRequest, error)

	CreateRegisterEntry(ctx context.Context, q *db.Queries, tenantID, requestID, patientID, executedBy uuid.UUID, rowCounts json.RawMessage) (RegisterEntry, error)
	ListRegister(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]RegisterEntry, error)

	CountPatientDeletionRows(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (json.RawMessage, error)
	DeletePatientData(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) error

	ReviewCandidatesForTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Candidate, error)
	ReviewAllCandidates(ctx context.Context, q *db.Queries) ([]ReviewCandidate, error)
}

// ReviewCandidate is one review line for the scheduled job. It keeps the tenant
// id because the job spans every tenant.
type ReviewCandidate struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Category string    `json:"category"`
	Count    int64     `json:"count"`
}

// Repository implements Store with sqlc queries.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) PatientExists(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error) {
	exists, err := q.PatientExists(ctx, db.PatientExistsParams{ID: patientID, TenantID: tenantID})
	if err != nil {
		return false, fmt.Errorf("check patient existence: %w", err)
	}
	return exists, nil
}

func (r *Repository) CreatePolicy(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input PolicyInput, createdBy uuid.UUID) (Policy, error) {
	row, err := q.CreateRetentionPolicy(ctx, db.CreateRetentionPolicyParams{
		TenantID:     tenantID,
		Category:     input.Category,
		TriggerEvent: input.TriggerEvent,
		PeriodMonths: int32(input.PeriodMonths),
		Note:         input.Note,
		CreatedBy:    createdBy,
	})
	if err != nil {
		return Policy{}, mapWriteError(err, "create retention policy")
	}
	return toPolicy(row), nil
}

func (r *Repository) GetPolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Policy, error) {
	row, err := q.GetRetentionPolicy(ctx, db.GetRetentionPolicyParams{ID: id, TenantID: tenantID})
	if err != nil {
		return Policy{}, mapReadError(err, "read retention policy")
	}
	return toPolicy(row), nil
}

func (r *Repository) ListPolicies(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Policy, error) {
	rows, err := q.ListRetentionPolicies(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list retention policies: %w", err)
	}
	policies := make([]Policy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, toPolicy(row))
	}
	return policies, nil
}

func (r *Repository) UpdatePolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, periodMonths int, note string) (Policy, error) {
	row, err := q.UpdateRetentionPolicy(ctx, db.UpdateRetentionPolicyParams{
		ID:           id,
		TenantID:     tenantID,
		PeriodMonths: int32(periodMonths),
		Note:         note,
	})
	if err != nil {
		return Policy{}, mapWriteError(err, "update retention policy")
	}
	return toPolicy(row), nil
}

func (r *Repository) ConfirmPolicy(ctx context.Context, q *db.Queries, tenantID, id, confirmedBy uuid.UUID) (Policy, error) {
	row, err := q.ConfirmRetentionPolicy(ctx, db.ConfirmRetentionPolicyParams{
		ID:          id,
		TenantID:    tenantID,
		ConfirmedBy: uuid.NullUUID{UUID: confirmedBy, Valid: true},
	})
	if err != nil {
		return Policy{}, mapWriteError(err, "confirm retention policy")
	}
	return toPolicy(row), nil
}

func (r *Repository) DisablePolicy(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Policy, error) {
	row, err := q.DisableRetentionPolicy(ctx, db.DisableRetentionPolicyParams{ID: id, TenantID: tenantID})
	if err != nil {
		return Policy{}, mapWriteError(err, "disable retention policy")
	}
	return toPolicy(row), nil
}

func (r *Repository) CreateLegalHold(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input LegalHoldInput, declaredBy uuid.UUID) (LegalHold, error) {
	patient := uuid.NullUUID{}
	if input.PatientID != nil {
		patient = uuid.NullUUID{UUID: *input.PatientID, Valid: true}
	}
	reviewDue := sql.NullTime{}
	if input.ReviewDueAt != nil {
		reviewDue = sql.NullTime{Time: *input.ReviewDueAt, Valid: true}
	}
	row, err := q.CreateLegalHold(ctx, db.CreateLegalHoldParams{
		TenantID:    tenantID,
		PatientID:   patient,
		Reason:      input.Reason,
		DeclaredBy:  declaredBy,
		ReviewDueAt: reviewDue,
	})
	if err != nil {
		return LegalHold{}, mapWriteError(err, "create legal hold")
	}
	return toLegalHold(row), nil
}

func (r *Repository) GetLegalHold(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (LegalHold, error) {
	row, err := q.GetLegalHold(ctx, db.GetLegalHoldParams{ID: id, TenantID: tenantID})
	if err != nil {
		return LegalHold{}, mapReadError(err, "read legal hold")
	}
	return toLegalHold(row), nil
}

func (r *Repository) ListLegalHolds(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]LegalHold, error) {
	rows, err := q.ListLegalHolds(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list legal holds: %w", err)
	}
	holds := make([]LegalHold, 0, len(rows))
	for _, row := range rows {
		holds = append(holds, toLegalHold(row))
	}
	return holds, nil
}

func (r *Repository) ListActiveLegalHoldsForPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) ([]LegalHold, error) {
	rows, err := q.ListActiveLegalHoldsForPatient(ctx, db.ListActiveLegalHoldsForPatientParams{
		TenantID:  tenantID,
		PatientID: uuid.NullUUID{UUID: patientID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list active legal holds: %w", err)
	}
	holds := make([]LegalHold, 0, len(rows))
	for _, row := range rows {
		holds = append(holds, toLegalHold(row))
	}
	return holds, nil
}

func (r *Repository) HasActiveLegalHold(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (bool, error) {
	held, err := q.HasActiveLegalHold(ctx, db.HasActiveLegalHoldParams{
		TenantID:  tenantID,
		PatientID: uuid.NullUUID{UUID: patientID, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("check legal hold: %w", err)
	}
	return held, nil
}

func (r *Repository) LiftLegalHold(ctx context.Context, q *db.Queries, tenantID, id, liftedBy uuid.UUID, reason string) (LegalHold, error) {
	row, err := q.LiftLegalHold(ctx, db.LiftLegalHoldParams{
		ID:         id,
		TenantID:   tenantID,
		LiftedBy:   uuid.NullUUID{UUID: liftedBy, Valid: true},
		LiftReason: reason,
	})
	if err != nil {
		return LegalHold{}, mapWriteError(err, "lift legal hold")
	}
	return toLegalHold(row), nil
}

func (r *Repository) CreateDeletionRequest(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input DeletionRequestInput, requestedBy uuid.UUID) (DeletionRequest, error) {
	row, err := q.CreateDeletionRequest(ctx, db.CreateDeletionRequestParams{
		TenantID:    tenantID,
		PatientID:   input.PatientID,
		Reason:      input.Reason,
		RequestedBy: requestedBy,
	})
	if err != nil {
		return DeletionRequest{}, mapWriteError(err, "create deletion request")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) GetDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (DeletionRequest, error) {
	row, err := q.GetDeletionRequest(ctx, db.GetDeletionRequestParams{ID: id, TenantID: tenantID})
	if err != nil {
		return DeletionRequest{}, mapReadError(err, "read deletion request")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) ListDeletionRequests(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]DeletionRequest, error) {
	rows, err := q.ListDeletionRequests(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list deletion requests: %w", err)
	}
	requests := make([]DeletionRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, toDeletionRequest(row))
	}
	return requests, nil
}

func (r *Repository) ApproveDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error) {
	row, err := q.ApproveDeletionRequest(ctx, db.ApproveDeletionRequestParams{
		ID:             id,
		TenantID:       tenantID,
		DecidedBy:      uuid.NullUUID{UUID: decidedBy, Valid: true},
		DecisionReason: reason,
	})
	if err != nil {
		return DeletionRequest{}, mapWriteError(err, "approve deletion request")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) RejectDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, decidedBy uuid.UUID, reason string) (DeletionRequest, error) {
	row, err := q.RejectDeletionRequest(ctx, db.RejectDeletionRequestParams{
		ID:             id,
		TenantID:       tenantID,
		DecidedBy:      uuid.NullUUID{UUID: decidedBy, Valid: true},
		DecisionReason: reason,
	})
	if err != nil {
		return DeletionRequest{}, mapWriteError(err, "reject deletion request")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) CancelDeletionRequest(ctx context.Context, q *db.Queries, tenantID, id, cancelledBy uuid.UUID) (DeletionRequest, error) {
	row, err := q.CancelDeletionRequest(ctx, db.CancelDeletionRequestParams{
		ID:          id,
		TenantID:    tenantID,
		CancelledBy: uuid.NullUUID{UUID: cancelledBy, Valid: true},
	})
	if err != nil {
		return DeletionRequest{}, mapWriteError(err, "cancel deletion request")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) MarkDeletionExecuted(ctx context.Context, q *db.Queries, tenantID, id, executedBy uuid.UUID) (DeletionRequest, error) {
	row, err := q.MarkDeletionExecuted(ctx, db.MarkDeletionExecutedParams{
		ID:         id,
		TenantID:   tenantID,
		ExecutedBy: uuid.NullUUID{UUID: executedBy, Valid: true},
	})
	if err != nil {
		return DeletionRequest{}, mapWriteError(err, "mark deletion executed")
	}
	return toDeletionRequest(row), nil
}

func (r *Repository) CreateRegisterEntry(ctx context.Context, q *db.Queries, tenantID, requestID, patientID, executedBy uuid.UUID, rowCounts json.RawMessage) (RegisterEntry, error) {
	row, err := q.CreateDeletionRegisterEntry(ctx, db.CreateDeletionRegisterEntryParams{
		TenantID:          tenantID,
		DeletionRequestID: requestID,
		PatientID:         patientID,
		ExecutedBy:        executedBy,
		RowCounts:         rowCounts,
	})
	if err != nil {
		return RegisterEntry{}, mapWriteError(err, "create deletion register entry")
	}
	return toRegisterEntry(row), nil
}

func (r *Repository) ListRegister(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]RegisterEntry, error) {
	rows, err := q.ListDeletionRegister(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list deletion register: %w", err)
	}
	entries := make([]RegisterEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, toRegisterEntry(row))
	}
	return entries, nil
}

func (r *Repository) CountPatientDeletionRows(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) (json.RawMessage, error) {
	counts, err := q.CountPatientDeletionRows(ctx, db.CountPatientDeletionRowsParams{
		TenantID:  tenantID,
		PatientID: patientID,
	})
	if err != nil {
		return nil, fmt.Errorf("count patient deletion rows: %w", err)
	}
	return counts, nil
}

// DeletePatientData removes a patient and every dependent row in dependency
// order. The caller must already have checked the legal holds and the deletion
// approval, and the whole sequence runs inside one transaction, so a failure
// leaves no partial deletion behind.
func (r *Repository) DeletePatientData(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID) error {
	if _, err := q.DeleteBillingRunsForPatient(ctx, db.DeleteBillingRunsForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete billing runs: %w", err)
	}
	if _, err := q.DeleteReportsForPatient(ctx, db.DeleteReportsForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete reports: %w", err)
	}
	if _, err := q.DeleteVisitsForPatient(ctx, db.DeleteVisitsForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete visits: %w", err)
	}
	if _, err := q.DeleteActivitiesForPatient(ctx, db.DeleteActivitiesForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete activities: %w", err)
	}
	if _, err := q.DeleteTemplatesForPatient(ctx, db.DeleteTemplatesForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete templates: %w", err)
	}
	if _, err := q.DeleteAssignmentsForPatient(ctx, db.DeleteAssignmentsForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete assignments: %w", err)
	}
	if _, err := q.DeletePatientSearchTokensForPatient(ctx, db.DeletePatientSearchTokensForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete patient search tokens: %w", err)
	}
	if _, err := q.DeleteBreakGlassGrantsForPatient(ctx, db.DeleteBreakGlassGrantsForPatientParams{TenantID: tenantID, PatientID: patientID}); err != nil {
		return fmt.Errorf("delete break-glass grants: %w", err)
	}
	deleted, err := q.DeletePatientRow(ctx, db.DeletePatientRowParams{TenantID: tenantID, ID: patientID})
	if err != nil {
		return fmt.Errorf("delete patient row: %w", err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ReviewCandidatesForTenant(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Candidate, error) {
	rows, err := q.ReviewRetentionCandidatesForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("review retention candidates: %w", err)
	}
	candidates := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, Candidate{Category: row.Category, Count: row.CandidateCount})
	}
	return candidates, nil
}

func (r *Repository) ReviewAllCandidates(ctx context.Context, q *db.Queries) ([]ReviewCandidate, error) {
	rows, err := q.RetentionReviewCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("review retention candidates: %w", err)
	}
	candidates := make([]ReviewCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, ReviewCandidate{
			TenantID: row.TenantID,
			Category: row.Category,
			Count:    row.CandidateCount,
		})
	}
	return candidates, nil
}

func mapReadError(err error, action string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", action, err)
}

func mapWriteError(err error, action string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return fmt.Errorf("%s: %w", action, err)
}

func toPolicy(row db.RetentionPolicy) Policy {
	policy := Policy{
		ID:           row.ID,
		TenantID:     row.TenantID,
		Category:     row.Category,
		TriggerEvent: row.TriggerEvent,
		PeriodMonths: int(row.PeriodMonths),
		Status:       row.Status,
		Note:         row.Note,
		CreatedBy:    row.CreatedBy,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if row.ConfirmedBy.Valid {
		value := row.ConfirmedBy.UUID
		policy.ConfirmedBy = &value
	}
	if row.ConfirmedAt.Valid {
		value := row.ConfirmedAt.Time
		policy.ConfirmedAt = &value
	}
	return policy
}

func toLegalHold(row db.LegalHold) LegalHold {
	hold := LegalHold{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Status:     row.Status,
		Reason:     row.Reason,
		LiftReason: row.LiftReason,
		DeclaredBy: row.DeclaredBy,
		DeclaredAt: row.DeclaredAt,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
	if row.PatientID.Valid {
		value := row.PatientID.UUID
		hold.PatientID = &value
	}
	if row.LiftedBy.Valid {
		value := row.LiftedBy.UUID
		hold.LiftedBy = &value
	}
	if row.LiftedAt.Valid {
		value := row.LiftedAt.Time
		hold.LiftedAt = &value
	}
	if row.ReviewDueAt.Valid {
		value := row.ReviewDueAt.Time
		hold.ReviewDueAt = &value
	}
	return hold
}

func toDeletionRequest(row db.DeletionRequest) DeletionRequest {
	request := DeletionRequest{
		ID:             row.ID,
		TenantID:       row.TenantID,
		PatientID:      row.PatientID,
		Status:         row.Status,
		Reason:         row.Reason,
		DecisionReason: row.DecisionReason,
		RequestedBy:    row.RequestedBy,
		RequestedAt:    row.RequestedAt,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if row.DecidedBy.Valid {
		value := row.DecidedBy.UUID
		request.DecidedBy = &value
	}
	if row.DecidedAt.Valid {
		value := row.DecidedAt.Time
		request.DecidedAt = &value
	}
	if row.CancelledBy.Valid {
		value := row.CancelledBy.UUID
		request.CancelledBy = &value
	}
	if row.CancelledAt.Valid {
		value := row.CancelledAt.Time
		request.CancelledAt = &value
	}
	if row.ExecutedBy.Valid {
		value := row.ExecutedBy.UUID
		request.ExecutedBy = &value
	}
	if row.ExecutedAt.Valid {
		value := row.ExecutedAt.Time
		request.ExecutedAt = &value
	}
	return request
}

func toRegisterEntry(row db.DeletionRegister) RegisterEntry {
	return RegisterEntry{
		ID:                row.ID,
		TenantID:          row.TenantID,
		DeletionRequestID: row.DeletionRequestID,
		PatientID:         row.PatientID,
		ExecutedBy:        row.ExecutedBy,
		ExecutedAt:        row.ExecutedAt,
		RowCounts:         row.RowCounts,
		CreatedAt:         row.CreatedAt,
	}
}
