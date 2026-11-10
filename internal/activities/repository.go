package activities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// Cipher encrypts and decrypts the free-text activity note.
type Cipher interface {
	Encrypt(plaintext string, aad []byte) ([]byte, error)
	Decrypt(ciphertext, aad []byte) (string, error)
	KeyVersion() int
}

// Repository persists activities and encrypts the free-text note around the
// sqlc queries.
type Repository struct {
	cipher Cipher
}

func NewRepository(cipher Cipher) *Repository {
	return &Repository{cipher: cipher}
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, tenantID, createdBy uuid.UUID, input CreateInput, snapshot Snapshot) (Activity, error) {
	id := uuid.New()

	var noteEnc []byte
	noteVersion := sql.NullInt32{}
	if input.Note != "" {
		version := r.cipher.KeyVersion()
		encrypted, err := r.cipher.Encrypt(input.Note, noteAAD(tenantID, id, version))
		if err != nil {
			return Activity{}, err
		}
		noteEnc = encrypted
		noteVersion = sql.NullInt32{Int32: int32(version), Valid: true}
	}

	row, err := q.CreateActivity(ctx, db.CreateActivityParams{
		ID:                  id,
		TenantID:            tenantID,
		PatientID:           input.PatientID,
		EmployeeID:          input.EmployeeID,
		ServiceDefinitionID: nullableUUID(input.ServiceDefinitionID),
		ContractID:          nullableUUID(input.ContractID),
		PerformedAt:         input.PerformedAt,
		DurationMinutes:     int32(input.DurationMinutes),
		NoteEnc:             noteEnc,
		NoteKeyVersion:      noteVersion,
		Status:              "recorded",
		ServiceCode:         snapshot.ServiceCode,
		ServiceName:         snapshot.ServiceName,
		LegalArea:           snapshot.LegalArea,
		ContractReference:   snapshot.ContractReference,
		PayerName:           snapshot.PayerName,
		CreatedBy:           createdBy,
	})
	if err != nil {
		return Activity{}, fmt.Errorf("create activity: %w", err)
	}
	return r.toActivity(row)
}

func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Activity, error) {
	row, err := q.GetActivity(ctx, db.GetActivityParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Activity{}, ErrNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("get activity: %w", err)
	}
	return r.toActivity(row)
}

func (r *Repository) ListByPatient(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, limit int) ([]Activity, error) {
	rows, err := q.ListActivitiesByPatient(ctx, db.ListActivitiesByPatientParams{
		TenantID:   tenantID,
		PatientID:  patientID,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list activities by patient: %w", err)
	}
	return r.toActivities(rows)
}

func (r *Repository) List(ctx context.Context, q *db.Queries, tenantID uuid.UUID, limit int) ([]Activity, error) {
	rows, err := q.ListActivities(ctx, db.ListActivitiesParams{
		TenantID:   tenantID,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	return r.toActivities(rows)
}

func (r *Repository) GetUserRole(ctx context.Context, q *db.Queries, tenantID, userID uuid.UUID) (string, error) {
	user, err := q.GetUserByID(ctx, db.GetUserByIDParams{ID: userID, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get user role: %w", err)
	}
	return user.Role, nil
}

func (r *Repository) toActivities(rows []db.Activity) ([]Activity, error) {
	activities := make([]Activity, 0, len(rows))
	for _, row := range rows {
		activity, err := r.toActivity(row)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	return activities, nil
}

func (r *Repository) toActivity(row db.Activity) (Activity, error) {
	activity := Activity{
		ID:                  row.ID,
		TenantID:            row.TenantID,
		PatientID:           row.PatientID,
		EmployeeID:          row.EmployeeID,
		ServiceDefinitionID: uuidPtr(row.ServiceDefinitionID),
		ContractID:          uuidPtr(row.ContractID),
		PerformedAt:         row.PerformedAt,
		DurationMinutes:     int(row.DurationMinutes),
		Status:              row.Status,
		ServiceCode:         row.ServiceCode,
		ServiceName:         row.ServiceName,
		LegalArea:           row.LegalArea,
		ContractReference:   row.ContractReference,
		PayerName:           row.PayerName,
		CreatedBy:           row.CreatedBy,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}

	if len(row.NoteEnc) > 0 && row.NoteKeyVersion.Valid {
		note, err := r.cipher.Decrypt(row.NoteEnc, noteAAD(row.TenantID, row.ID, int(row.NoteKeyVersion.Int32)))
		if err != nil {
			return Activity{}, err
		}
		activity.Note = note
	}
	return activity, nil
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func uuidPtr(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := value.UUID
	return &id
}

// noteAAD binds the note ciphertext to the tenant, activity and field so a
// swapped value fails authentication (doc 7).
func noteAAD(tenantID, activityID uuid.UUID, keyVersion int) []byte {
	return []byte(fmt.Sprintf("v%d|%s|%s|%s", keyVersion, tenantID, activityID, "note"))
}
