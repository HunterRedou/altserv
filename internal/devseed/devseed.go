// Package devseed creates a synthetic development dataset. It is not a
// production feature and it refuses to run unless PLATFORM is dev.
//
// The data is deliberately fake: the catalog code and every service code carry
// a SYNTH prefix, and the source note says that the catalog is not a real
// Brandenburg catalog. No invented statutory code may ship as if it were real
// (doc 9).
package devseed

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/google/uuid"
)

var (
	// ErrNotDevelopment means the caller tried to seed a non-development
	// deployment. The seed must never touch production data.
	ErrNotDevelopment = errors.New("dev seed data may only be created when PLATFORM=dev")
	// ErrAlreadySeeded means the demo tenant or one of its users already exists.
	ErrAlreadySeeded = errors.New("the demo dataset already exists")
	// ErrInvalidOptions means the caller passed an unusable configuration.
	ErrInvalidOptions = errors.New("invalid dev seed options")
)

const (
	// CatalogCode and the definition codes are synthetic on purpose. Do not
	// replace them with real Brandenburg codes without professional review.
	CatalogCode       = "SYNTH-DEMO-2026"
	CatalogName       = "Synthetischer Demo-Katalog (nicht produktiv)"
	CatalogSourceNote = "SYNTHETISCHE ENTWICKLUNGSDATEN - kein echter Brandenburger Katalog, nicht fachlich verifiziert"

	ChefEmail     = "chef@demo.invalid"
	EmployeeEmail = "employee@demo.invalid"
	AdminEmail    = "platform-admin@demo.invalid"

	minPasswordLength = 12
	seedRequestID     = "devseed"
)

// Options is the input of one seed run.
type Options struct {
	// Platform must be "dev". Anything else is refused.
	Platform string

	TenantEmail string
	TenantName  string
	// Password is the login password for the three demo users. It must be at
	// least 12 characters. The caller supplies it; there is no built-in default.
	Password string

	// Patient keys are the same keys the application uses. The seed needs them
	// because patient data is encrypted and blind-indexed.
	PatientEncryptionKey    []byte
	PatientKeyVersion       int
	PatientSearchKey        []byte
	PatientSearchKeyVersion int

	// Now is optional. It defaults to time.Now and makes tests deterministic.
	Now time.Time
}

// Result lists the identifiers the seed created so the operator can log in and
// open the records.
type Result struct {
	TenantID      uuid.UUID
	ChefID        uuid.UUID
	EmployeeID    uuid.UUID
	AdminID       uuid.UUID
	CatalogID     uuid.UUID
	DefinitionIDs []uuid.UUID
	PatientIDs    []uuid.UUID
	TemplateID    uuid.UUID
	VisitID       uuid.UUID
}

// EnsureDevelopment refuses every platform except dev. It is a separate
// function so a caller cannot forget the guard.
func EnsureDevelopment(platform string) error {
	if platform != "dev" {
		return fmt.Errorf("%w: PLATFORM=%q", ErrNotDevelopment, platform)
	}
	return nil
}

// Run creates the demo dataset in one transaction and commits it.
func Run(ctx context.Context, conn *sql.DB, opts Options) (Result, error) {
	if err := opts.validate(); err != nil {
		return Result{}, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin dev seed transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := RunTx(ctx, tx, opts)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit dev seed transaction: %w", err)
	}
	return result, nil
}

// RunTx creates the demo dataset inside an existing transaction and leaves the
// commit to the caller. Integration tests use it to roll the seed back.
func RunTx(ctx context.Context, tx *sql.Tx, opts Options) (Result, error) {
	if err := opts.validate(); err != nil {
		return Result{}, err
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	queries := db.New(tx)
	if err := ensureNotSeeded(ctx, tx, opts); err != nil {
		return Result{}, err
	}

	tenant, err := queries.CreateTenant(ctx, db.CreateTenantParams{
		Email:          opts.TenantEmail,
		Ustid:          "DE000000000",
		Streetname:     opts.TenantName,
		Plz:            "10115",
		HashedPassword: "devseed-tenant-login-unused",
	})
	if err != nil {
		return Result{}, fmt.Errorf("create demo tenant: %w", err)
	}

	result := Result{TenantID: tenant.ID}
	err = db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
		if _, err := q.UpdateTenantStatus(ctx, db.UpdateTenantStatusParams{ID: tenant.ID, Status: "valid"}); err != nil {
			return fmt.Errorf("activate demo tenant: %w", err)
		}
		return seedTenant(ctx, q, opts, &result)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (o Options) validate() error {
	if err := EnsureDevelopment(o.Platform); err != nil {
		return err
	}
	if strings.TrimSpace(o.TenantEmail) == "" {
		return fmt.Errorf("%w: tenant email is required", ErrInvalidOptions)
	}
	if strings.TrimSpace(o.TenantName) == "" {
		return fmt.Errorf("%w: tenant name is required", ErrInvalidOptions)
	}
	if len(o.Password) < minPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidOptions, minPasswordLength)
	}
	if len(o.PatientEncryptionKey) != 32 {
		return fmt.Errorf("%w: patient encryption key must be 32 bytes", ErrInvalidOptions)
	}
	if o.PatientKeyVersion < 1 {
		return fmt.Errorf("%w: patient key version must be at least 1", ErrInvalidOptions)
	}
	if len(o.PatientSearchKey) != 32 {
		return fmt.Errorf("%w: patient search key must be 32 bytes", ErrInvalidOptions)
	}
	if o.PatientSearchKeyVersion < 1 {
		return fmt.Errorf("%w: patient search key version must be at least 1", ErrInvalidOptions)
	}
	if bytes.Equal(o.PatientEncryptionKey, o.PatientSearchKey) {
		return fmt.Errorf("%w: patient search key must differ from the encryption key", ErrInvalidOptions)
	}
	return nil
}

func ensureNotSeeded(ctx context.Context, tx *sql.Tx, opts Options) error {
	var existing uuid.UUID
	err := tx.QueryRowContext(ctx, "SELECT id FROM tenants WHERE email = $1 LIMIT 1", opts.TenantEmail).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("%w: tenant %q already exists", ErrAlreadySeeded, opts.TenantEmail)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("check demo tenant: %w", err)
	}

	for _, email := range []string{ChefEmail, EmployeeEmail, AdminEmail} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE email = $1", email).Scan(&count); err != nil {
			return fmt.Errorf("check demo user %q: %w", email, err)
		}
		if count > 0 {
			return fmt.Errorf("%w: user %q already exists", ErrAlreadySeeded, email)
		}
	}
	return nil
}

func seedTenant(ctx context.Context, q *db.Queries, opts Options, result *Result) error {
	recorder := audit.SQLRecorder{}
	now := opts.Now
	catalogFrom := now.AddDate(0, 0, -30).UTC().Truncate(time.Second)
	catalogUntil := now.AddDate(1, 0, 0).UTC().Truncate(time.Second)
	validFrom := now.AddDate(0, 0, -1).UTC().Truncate(time.Second)

	hash, err := auth.HashPassword(opts.Password)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}

	chef, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: "Demo Chef", Role: "chef", HashedPassword: hash,
		Email: ChefEmail, TenantID: result.TenantID,
	})
	if err != nil {
		return fmt.Errorf("create demo chef: %w", err)
	}
	employee, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: "Demo Mitarbeiter", Role: "employee", HashedPassword: hash,
		Email: EmployeeEmail, TenantID: result.TenantID,
	})
	if err != nil {
		return fmt.Errorf("create demo employee: %w", err)
	}
	admin, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: "Demo Plattform", Role: "platform_admin", HashedPassword: hash,
		Email: AdminEmail, TenantID: result.TenantID,
	})
	if err != nil {
		return fmt.Errorf("create demo platform admin: %w", err)
	}
	result.ChefID = chef.ID
	result.EmployeeID = employee.ID
	result.AdminID = admin.ID

	catalogService := catalog.NewService(catalog.NewRepository(), recorder)
	adminActor := catalog.Actor{UserID: admin.ID, TenantID: result.TenantID, Role: "platform_admin"}
	chefActor := catalog.Actor{UserID: chef.ID, TenantID: result.TenantID, Role: "chef"}

	catalogVersion, err := catalogService.CreateCatalog(ctx, q, adminActor, seedRequestID, catalog.CreateCatalogInput{
		Code:       CatalogCode,
		Name:       CatalogName,
		Region:     "brandenburg",
		SourceNote: CatalogSourceNote,
		ValidFrom:  catalogFrom,
		ValidUntil: &catalogUntil,
	})
	if err != nil {
		return fmt.Errorf("create demo catalog: %w", err)
	}
	result.CatalogID = catalogVersion.ID

	definitions := []struct {
		code      string
		name      string
		legalArea string
		unit      string
	}{
		{code: "SYNTH-XI-001", name: "Synthetische Grundleistung A", legalArea: "sgb_xi", unit: "Einsatz"},
		{code: "SYNTH-XI-002", name: "Synthetische Grundleistung B", legalArea: "sgb_xi", unit: "Stunde"},
		{code: "SYNTH-V-001", name: "Synthetische Leistung C (SGB V)", legalArea: "sgb_v", unit: "Sitzung"},
	}
	for _, entry := range definitions {
		definition, err := catalogService.CreateDefinition(ctx, q, adminActor, seedRequestID, catalog.CreateDefinitionInput{
			CatalogID:  catalogVersion.ID,
			Code:       entry.code,
			Name:       entry.name,
			LegalArea:  entry.legalArea,
			Unit:       entry.unit,
			ValidFrom:  catalogFrom,
			ValidUntil: &catalogUntil,
		})
		if err != nil {
			return fmt.Errorf("create demo definition %q: %w", entry.code, err)
		}
		result.DefinitionIDs = append(result.DefinitionIDs, definition.ID)
	}

	contract, err := catalogService.CreateContract(ctx, q, chefActor, seedRequestID, catalog.CreateContractInput{
		ServiceDefinitionID: result.DefinitionIDs[0],
		PayerName:           "Synthetische Kasse (Demo)",
		ContractReference:   "SYNTH-VERTRAG-2026",
		LegalArea:           "sgb_xi",
		ValidFrom:           validFrom,
		ValidUntil:          &catalogUntil,
	})
	if err != nil {
		return fmt.Errorf("create demo contract: %w", err)
	}

	if _, err := catalogService.CreateRule(ctx, q, chefActor, seedRequestID, catalog.CreateRuleInput{
		ServiceDefinitionID: result.DefinitionIDs[0],
		ContractID:          &contract.ID,
		ActivityLabel:       "Synthetische Grundleistung A",
		LegalArea:           "sgb_xi",
		ValidFrom:           validFrom,
		ValidUntil:          &catalogUntil,
	}); err != nil {
		return fmt.Errorf("create demo activity rule: %w", err)
	}

	cipher, err := patients.NewCipher(opts.PatientEncryptionKey, opts.PatientKeyVersion)
	if err != nil {
		return fmt.Errorf("demo patient cipher: %w", err)
	}
	searchIndexer, err := patients.NewSearchIndexer(opts.PatientSearchKey, opts.PatientSearchKeyVersion)
	if err != nil {
		return fmt.Errorf("demo patient search index: %w", err)
	}
	assignmentService := assignments.NewService(assignments.NewRepository(), recorder)
	patientService := patients.NewServiceWithSearch(patients.NewRepository(cipher), recorder, assignmentService, searchIndexer)
	patientActor := patients.Actor{UserID: chef.ID, TenantID: result.TenantID, Role: "chef"}

	patientInputs := []patients.CreateInput{
		{FirstName: "Demo", LastName: "Patientin Eins", BirthDate: time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC)},
		{FirstName: "Demo", LastName: "Patient Zwei", BirthDate: time.Date(1945, 6, 15, 0, 0, 0, 0, time.UTC)},
	}
	for _, input := range patientInputs {
		patient, err := patientService.Create(ctx, q, patientActor, seedRequestID, input)
		if err != nil {
			return fmt.Errorf("create demo patient %q: %w", input.LastName, err)
		}
		result.PatientIDs = append(result.PatientIDs, patient.ID)
	}

	assignmentActor := assignments.Actor{UserID: chef.ID, TenantID: result.TenantID, Role: "chef"}
	for _, patientID := range result.PatientIDs {
		if _, err := assignmentService.Create(ctx, q, assignmentActor, seedRequestID, patientID, assignments.CreateInput{
			EmployeeID:     employee.ID,
			AssignmentType: "regular",
			ValidFrom:      validFrom,
		}); err != nil {
			return fmt.Errorf("assign demo employee to patient %s: %w", patientID, err)
		}
	}

	templateService := templates.NewService(templates.NewRepository(), catalog.NewRepository(), recorder, assignmentService)
	template, err := templateService.Create(ctx, q, templates.Actor{UserID: chef.ID, TenantID: result.TenantID, Role: "chef"}, seedRequestID, templates.CreateInput{
		PatientID: result.PatientIDs[0],
		Name:      "Synthetische Vorlage",
		Activities: []templates.ActivityInput{
			{ActivityID: result.DefinitionIDs[0], DefaultQuantity: 1, SortOrder: 1},
			{ActivityID: result.DefinitionIDs[1], DefaultQuantity: 2, SortOrder: 2},
		},
	})
	if err != nil {
		return fmt.Errorf("create demo template: %w", err)
	}
	result.TemplateID = template.ID

	visitService := visits.NewService(visits.NewRepository(), recorder, assignmentService)
	visit, err := visitService.Create(ctx, q, visits.Actor{UserID: chef.ID, TenantID: result.TenantID, Role: "chef"}, seedRequestID, visits.CreateInput{
		PatientID:    result.PatientIDs[0],
		EmployeeID:   employee.ID,
		PlannedStart: now.Add(24 * time.Hour).UTC().Truncate(time.Second),
	})
	if err != nil {
		return fmt.Errorf("create demo visit: %w", err)
	}
	result.VisitID = visit.ID

	return nil
}
