package patients

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

const dateLayout = "2006-01-02"

// Repository encrypts and decrypts patient PII around the sqlc queries.
type Repository struct {
	cipher *Cipher
}

func NewRepository(cipher *Cipher) *Repository {
	return &Repository{cipher: cipher}
}

func (r *Repository) Create(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateInput) (Patient, error) {
	id := uuid.New()
	version := r.cipher.KeyVersion()

	firstName, err := r.cipher.Encrypt(input.FirstName, FieldAAD(tenantID, id, "first_name", version))
	if err != nil {
		return Patient{}, err
	}
	lastName, err := r.cipher.Encrypt(input.LastName, FieldAAD(tenantID, id, "last_name", version))
	if err != nil {
		return Patient{}, err
	}
	birthDate, err := r.cipher.Encrypt(input.BirthDate.Format(dateLayout), FieldAAD(tenantID, id, "birth_date", version))
	if err != nil {
		return Patient{}, err
	}

	row, err := q.CreatePatient(ctx, db.CreatePatientParams{
		ID:           id,
		TenantID:     tenantID,
		FirstNameEnc: firstName,
		LastNameEnc:  lastName,
		BirthDateEnc: birthDate,
		Status:       input.Status,
		KeyVersion:   int32(version),
	})
	if err != nil {
		return Patient{}, fmt.Errorf("create patient: %w", err)
	}
	return r.decrypt(row)
}

func (r *Repository) Get(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Patient, error) {
	row, err := q.GetPatient(ctx, db.GetPatientParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Patient{}, ErrNotFound
	}
	if err != nil {
		return Patient{}, fmt.Errorf("get patient: %w", err)
	}
	return r.decrypt(row)
}

func (r *Repository) List(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Patient, error) {
	rows, err := q.ListPatients(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list patients: %w", err)
	}

	return r.decryptAll(rows)
}

func (r *Repository) ListForEmployee(ctx context.Context, q *db.Queries, tenantID, employeeID uuid.UUID) ([]Patient, error) {
	rows, err := q.ListPatientsForEmployee(ctx, db.ListPatientsForEmployeeParams{
		TenantID:   tenantID,
		EmployeeID: employeeID,
	})
	if err != nil {
		return nil, fmt.Errorf("list patients for employee: %w", err)
	}

	return r.decryptAll(rows)
}

// Update re-encrypts every PII field with a fresh nonce, so a PATCH never
// reuses a nonce even when the plaintext is unchanged (doc 7).
func (r *Repository) Update(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID, input CreateInput) (Patient, error) {
	version := r.cipher.KeyVersion()

	firstName, err := r.cipher.Encrypt(input.FirstName, FieldAAD(tenantID, id, "first_name", version))
	if err != nil {
		return Patient{}, err
	}
	lastName, err := r.cipher.Encrypt(input.LastName, FieldAAD(tenantID, id, "last_name", version))
	if err != nil {
		return Patient{}, err
	}
	birthDate, err := r.cipher.Encrypt(input.BirthDate.Format(dateLayout), FieldAAD(tenantID, id, "birth_date", version))
	if err != nil {
		return Patient{}, err
	}

	row, err := q.UpdatePatient(ctx, db.UpdatePatientParams{
		ID:           id,
		TenantID:     tenantID,
		FirstNameEnc: firstName,
		LastNameEnc:  lastName,
		BirthDateEnc: birthDate,
		Status:       input.Status,
		KeyVersion:   int32(version),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Patient{}, ErrNotFound
	}
	if err != nil {
		return Patient{}, fmt.Errorf("update patient: %w", err)
	}
	return r.decrypt(row)
}

// ReplaceSearchTokens rebuilds the blind index in one call. It runs inside the
// same tenant transaction as the patient change, so old and new names never
// diverge.
func (r *Repository) ReplaceSearchTokens(ctx context.Context, q *db.Queries, tenantID, patientID uuid.UUID, tokens []SearchToken) error {
	if err := q.DeletePatientSearchTokens(ctx, db.DeletePatientSearchTokensParams{
		TenantID:  tenantID,
		PatientID: patientID,
	}); err != nil {
		return fmt.Errorf("delete patient search tokens: %w", err)
	}

	for _, token := range tokens {
		if err := q.CreatePatientSearchToken(ctx, db.CreatePatientSearchTokenParams{
			TenantID:   tenantID,
			PatientID:  patientID,
			TokenHash:  token.Hash,
			TokenType:  token.Type,
			KeyVersion: int32(token.KeyVersion),
		}); err != nil {
			return fmt.Errorf("create patient search token: %w", err)
		}
	}
	return nil
}

// Search runs the blind-index lookup. A nil employeeID means the actor is a
// chef and may search every patient in the tenant; otherwise the query also
// enforces an active assignment. An empty token list matches all accessible
// patients.
func (r *Repository) Search(ctx context.Context, q *db.Queries, tenantID uuid.UUID, employeeID *uuid.UUID, tokenHashes []string, limit int) ([]Patient, error) {
	employee := uuid.Nil
	if employeeID != nil {
		employee = *employeeID
	}

	rows, err := q.SearchPatients(ctx, db.SearchPatientsParams{
		TenantID:    tenantID,
		EmployeeID:  employee,
		TokenHashes: strings.Join(tokenHashes, ","),
		MaxResults:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("search patients: %w", err)
	}

	return r.decryptAll(rows)
}

func (r *Repository) decryptAll(rows []db.Patient) ([]Patient, error) {
	patients := make([]Patient, 0, len(rows))
	for _, row := range rows {
		patient, err := r.decrypt(row)
		if err != nil {
			return nil, err
		}
		patients = append(patients, patient)
	}
	return patients, nil
}

func (r *Repository) decrypt(row db.Patient) (Patient, error) {
	version := int(row.KeyVersion)
	if version != r.cipher.KeyVersion() {
		return Patient{}, fmt.Errorf("patient %s uses unsupported key version %d", row.ID, version)
	}

	firstName, err := r.cipher.Decrypt(row.FirstNameEnc, FieldAAD(row.TenantID, row.ID, "first_name", version))
	if err != nil {
		return Patient{}, err
	}
	lastName, err := r.cipher.Decrypt(row.LastNameEnc, FieldAAD(row.TenantID, row.ID, "last_name", version))
	if err != nil {
		return Patient{}, err
	}
	birthDateRaw, err := r.cipher.Decrypt(row.BirthDateEnc, FieldAAD(row.TenantID, row.ID, "birth_date", version))
	if err != nil {
		return Patient{}, err
	}
	birthDate, err := time.Parse(dateLayout, birthDateRaw)
	if err != nil {
		return Patient{}, fmt.Errorf("parse patient birth date: %w", err)
	}

	return Patient{
		ID:        row.ID,
		TenantID:  row.TenantID,
		FirstName: firstName,
		LastName:  lastName,
		BirthDate: birthDate,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}
