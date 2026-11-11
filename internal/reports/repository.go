package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Cipher encrypts and decrypts the report note and the return reason.
type Cipher interface {
	Encrypt(plaintext string, aad []byte) ([]byte, error)
	Decrypt(ciphertext, aad []byte) (string, error)
	KeyVersion() int
}

// Repository persists reports with sqlc queries and encrypts the free-text
// fields around them.
type Repository struct {
	cipher Cipher
}

func NewRepository(cipher Cipher) *Repository {
	return &Repository{cipher: cipher}
}

func (r *Repository) GetVisit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Visit, error) {
	row, err := q.GetVisitForReport(ctx, db.GetVisitForReportParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Visit{}, ErrNotFound
	}
	if err != nil {
		return db.Visit{}, fmt.Errorf("get visit for report: %w", err)
	}
	return row, nil
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, tenantID, createdBy, employeeID uuid.UUID, visit db.Visit, input CreateInput) (Report, error) {
	id := uuid.New()

	var noteEnc []byte
	noteVersion := sql.NullInt32{}
	if input.Note != "" {
		version := r.cipher.KeyVersion()
		encrypted, err := r.cipher.Encrypt(input.Note, noteAAD(tenantID, id, version))
		if err != nil {
			return Report{}, err
		}
		noteEnc = encrypted
		noteVersion = sql.NullInt32{Int32: int32(version), Valid: true}
	}

	row, err := q.CreateReport(ctx, db.CreateReportParams{
		ID:             id,
		TenantID:       tenantID,
		VisitID:        visit.ID,
		PatientID:      visit.PatientID,
		EmployeeID:     employeeID,
		NoteEnc:        noteEnc,
		NoteKeyVersion: noteVersion,
		CreatedBy:      createdBy,
	})
	if err != nil {
		return Report{}, fmt.Errorf("create report: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Report, error) {
	row, err := q.GetReport(ctx, db.GetReportParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("get report: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) ListByVisit(ctx context.Context, q *db.Queries, tenantID, visitID uuid.UUID) ([]Report, error) {
	rows, err := q.ListReportsByVisit(ctx, db.ListReportsByVisitParams{TenantID: tenantID, VisitID: visitID})
	if err != nil {
		return nil, fmt.Errorf("list reports by visit: %w", err)
	}
	return r.toReports(rows)
}

func (r *Repository) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Report, error) {
	rows, err := q.ListReportsByPatient(ctx, db.ListReportsByPatientParams{
		TenantID:   tenantID,
		PatientID:  patientID,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list reports by patient: %w", err)
	}
	return r.toReports(rows)
}

// UpdateDraft writes the note and bumps the version, but only for a draft with
// the expected version. No rows means a stale or non-draft write.
func (r *Repository) UpdateDraft(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int, note *string) (Report, error) {
	var (
		row db.Report
		err error
	)
	if note == nil {
		row, err = q.TouchReportDraft(ctx, db.TouchReportDraftParams{ID: id, TenantID: tenantID, Version: int32(version)})
	} else {
		noteEnc := []byte(nil)
		noteVersion := sql.NullInt32{}
		if *note != "" {
			keyVersion := r.cipher.KeyVersion()
			noteEnc, err = r.cipher.Encrypt(*note, noteAAD(tenantID, id, keyVersion))
			if err != nil {
				return Report{}, err
			}
			noteVersion = sql.NullInt32{Int32: int32(keyVersion), Valid: true}
		}
		row, err = q.UpdateReportDraft(ctx, db.UpdateReportDraftParams{
			ID:             id,
			TenantID:       tenantID,
			NoteEnc:        noteEnc,
			NoteKeyVersion: noteVersion,
			Version:        int32(version),
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, fmt.Errorf("update report draft: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) Submit(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	row, err := q.SubmitReport(ctx, db.SubmitReportParams{ID: id, TenantID: tenantID, Version: int32(version)})
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, fmt.Errorf("submit report: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) Approve(ctx context.Context, q *db.Queries, tenantID, id, approvedBy uuid.UUID, version int) (Report, error) {
	row, err := q.ApproveReport(ctx, db.ApproveReportParams{
		ID:         id,
		TenantID:   tenantID,
		ApprovedBy: uuid.NullUUID{UUID: approvedBy, Valid: true},
		Version:    int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, fmt.Errorf("approve report: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) Return(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	row, err := q.ReturnReport(ctx, db.ReturnReportParams{ID: id, TenantID: tenantID, Version: int32(version)})
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, fmt.Errorf("return report: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) MarkReadyForBilling(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, version int) (Report, error) {
	row, err := q.MarkReportReadyForBilling(ctx, db.MarkReportReadyForBillingParams{
		ID:       id,
		TenantID: tenantID,
		Version:  int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, fmt.Errorf("mark report ready for billing: %w", err)
	}
	return r.toReport(row)
}

func (r *Repository) GetActivity(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (db.Activity, error) {
	row, err := q.GetActivity(ctx, db.GetActivityParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Activity{}, ErrNotFound
	}
	if err != nil {
		return db.Activity{}, fmt.Errorf("get activity: %w", err)
	}
	return row, nil
}

func (r *Repository) GetActivityAttachment(ctx context.Context, q *db.Queries, tenantID, activityID uuid.UUID) (uuid.UUID, error) {
	reportID, err := q.GetReportActivityAttachment(ctx, db.GetReportActivityAttachmentParams{
		TenantID:   tenantID,
		ActivityID: activityID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get report activity attachment: %w", err)
	}
	return reportID, nil
}

// ReplaceActivities deletes the current activity rows and inserts the new set.
// It runs inside the caller's transaction, so a failure leaves the old set in
// place.
func (r *Repository) ReplaceActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID, activities []storedActivity) ([]ReportActivity, error) {
	if err := q.DeleteReportActivities(ctx, db.DeleteReportActivitiesParams{ReportID: reportID, TenantID: tenantID}); err != nil {
		return nil, fmt.Errorf("delete report activities: %w", err)
	}

	result := make([]ReportActivity, 0, len(activities))
	for index, activity := range activities {
		row, err := q.InsertReportActivity(ctx, db.InsertReportActivityParams{
			TenantID:    tenantID,
			ReportID:    reportID,
			ActivityID:  activity.ActivityID,
			Quantity:    int32(activity.Quantity),
			PerformedAt: activity.PerformedAt,
			SortOrder:   int32(index),
		})
		if err != nil {
			return nil, fmt.Errorf("insert report activity: %w", err)
		}
		result = append(result, toReportActivity(row))
	}
	return result, nil
}

func (r *Repository) ListActivities(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportActivity, error) {
	rows, err := q.ListReportActivities(ctx, db.ListReportActivitiesParams{ReportID: reportID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list report activities: %w", err)
	}
	result := make([]ReportActivity, 0, len(rows))
	for _, row := range rows {
		result = append(result, toReportActivity(row))
	}
	return result, nil
}

func (r *Repository) CreateTransition(ctx context.Context, q *db.Queries, tenantID, reportID, actorID uuid.UUID, fromStatus, toStatus, kind, reason string) (ReportTransition, error) {
	id := uuid.New()

	var reasonEnc []byte
	reasonVersion := sql.NullInt32{}
	if reason != "" {
		version := r.cipher.KeyVersion()
		encrypted, err := r.cipher.Encrypt(reason, reasonAAD(tenantID, id, version))
		if err != nil {
			return ReportTransition{}, err
		}
		reasonEnc = encrypted
		reasonVersion = sql.NullInt32{Int32: int32(version), Valid: true}
	}

	row, err := q.CreateReportTransition(ctx, db.CreateReportTransitionParams{
		ID:               id,
		TenantID:         tenantID,
		ReportID:         reportID,
		FromStatus:       fromStatus,
		ToStatus:         toStatus,
		Kind:             kind,
		ActorUserID:      actorID,
		ReasonEnc:        reasonEnc,
		ReasonKeyVersion: reasonVersion,
	})
	if err != nil {
		return ReportTransition{}, fmt.Errorf("create report transition: %w", err)
	}

	transition := ReportTransition{
		ID:          row.ID,
		TenantID:    row.TenantID,
		ReportID:    row.ReportID,
		FromStatus:  row.FromStatus,
		ToStatus:    row.ToStatus,
		Kind:        row.Kind,
		ActorUserID: row.ActorUserID,
		CreatedAt:   row.CreatedAt,
	}
	if len(row.ReasonEnc) > 0 && row.ReasonKeyVersion.Valid {
		reason, err := r.cipher.Decrypt(row.ReasonEnc, reasonAAD(row.TenantID, row.ID, int(row.ReasonKeyVersion.Int32)))
		if err != nil {
			return ReportTransition{}, err
		}
		transition.Reason = reason
	}
	return transition, nil
}

func (r *Repository) ListTransitions(ctx context.Context, q *db.Queries, tenantID, reportID uuid.UUID) ([]ReportTransition, error) {
	rows, err := q.ListReportTransitions(ctx, db.ListReportTransitionsParams{ReportID: reportID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list report transitions: %w", err)
	}
	result := make([]ReportTransition, 0, len(rows))
	for _, row := range rows {
		transition := ReportTransition{
			ID:          row.ID,
			TenantID:    row.TenantID,
			ReportID:    row.ReportID,
			FromStatus:  row.FromStatus,
			ToStatus:    row.ToStatus,
			Kind:        row.Kind,
			ActorUserID: row.ActorUserID,
			CreatedAt:   row.CreatedAt,
		}
		if len(row.ReasonEnc) > 0 && row.ReasonKeyVersion.Valid {
			reason, err := r.cipher.Decrypt(row.ReasonEnc, reasonAAD(row.TenantID, row.ID, int(row.ReasonKeyVersion.Int32)))
			if err != nil {
				return nil, err
			}
			transition.Reason = reason
		}
		result = append(result, transition)
	}
	return result, nil
}

func (r *Repository) toReports(rows []db.Report) ([]Report, error) {
	reports := make([]Report, 0, len(rows))
	for _, row := range rows {
		report, err := r.toReport(row)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func (r *Repository) toReport(row db.Report) (Report, error) {
	report := Report{
		ID:         row.ID,
		TenantID:   row.TenantID,
		VisitID:    row.VisitID,
		PatientID:  row.PatientID,
		EmployeeID: row.EmployeeID,
		Status:     row.Status,
		Version:    int(row.Version),
		CreatedBy:  row.CreatedBy,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
	if row.SubmittedAt.Valid {
		value := row.SubmittedAt.Time
		report.SubmittedAt = &value
	}
	if row.ApprovedAt.Valid {
		value := row.ApprovedAt.Time
		report.ApprovedAt = &value
	}
	if row.ApprovedBy.Valid {
		value := row.ApprovedBy.UUID
		report.ApprovedBy = &value
	}
	if len(row.NoteEnc) > 0 && row.NoteKeyVersion.Valid {
		note, err := r.cipher.Decrypt(row.NoteEnc, noteAAD(row.TenantID, row.ID, int(row.NoteKeyVersion.Int32)))
		if err != nil {
			return Report{}, err
		}
		report.Note = note
	}
	return report, nil
}

func toReportActivity(row db.ReportActivity) ReportActivity {
	return ReportActivity{
		ID:          row.ID,
		TenantID:    row.TenantID,
		ReportID:    row.ReportID,
		ActivityID:  row.ActivityID,
		Quantity:    int(row.Quantity),
		PerformedAt: row.PerformedAt,
		SortOrder:   int(row.SortOrder),
	}
}

// storedActivity is a validated activity ready to attach to a report.
type storedActivity struct {
	ActivityID  uuid.UUID
	Quantity    int
	PerformedAt time.Time
}

// noteAAD binds the note ciphertext to the tenant, report and field so a
// swapped value fails authentication (doc 7).
func noteAAD(tenantID, reportID uuid.UUID, keyVersion int) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|%s", keyVersion, tenantID, reportID, "note"))
}

// reasonAAD binds a return reason to its transition row.
func reasonAAD(tenantID, transitionID uuid.UUID, keyVersion int) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|%s", keyVersion, tenantID, transitionID, "reason"))
}
