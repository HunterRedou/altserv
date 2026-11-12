package devseed

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func validOptions() Options {
	return Options{
		Platform:                "dev",
		TenantEmail:             "demo-" + uuid.NewString() + "@example.invalid",
		TenantName:              "Demo Pflegedienst",
		Password:                strings.Repeat("a", minPasswordLength),
		PatientEncryptionKey:    bytes.Repeat([]byte{1}, 32),
		PatientKeyVersion:       1,
		PatientSearchKey:        bytes.Repeat([]byte{2}, 32),
		PatientSearchKeyVersion: 1,
	}
}

func TestEnsureDevelopment(t *testing.T) {
	if err := EnsureDevelopment("dev"); err != nil {
		t.Fatalf("dev should be allowed: %v", err)
	}
	for _, platform := range []string{"", "prod", "production", "DEV", "staging"} {
		t.Run(platform, func(t *testing.T) {
			err := EnsureDevelopment(platform)
			if !errors.Is(err, ErrNotDevelopment) {
				t.Fatalf("err = %v, want ErrNotDevelopment", err)
			}
		})
	}
}

func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
		want   error
	}{
		{name: "prod platform", mutate: func(o *Options) { o.Platform = "prod" }, want: ErrNotDevelopment},
		{name: "empty tenant email", mutate: func(o *Options) { o.TenantEmail = "  " }, want: ErrInvalidOptions},
		{name: "empty tenant name", mutate: func(o *Options) { o.TenantName = "" }, want: ErrInvalidOptions},
		{name: "short password", mutate: func(o *Options) { o.Password = strings.Repeat("a", minPasswordLength-1) }, want: ErrInvalidOptions},
		{name: "short encryption key", mutate: func(o *Options) { o.PatientEncryptionKey = bytes.Repeat([]byte{1}, 31) }, want: ErrInvalidOptions},
		{name: "bad key version", mutate: func(o *Options) { o.PatientKeyVersion = 0 }, want: ErrInvalidOptions},
		{name: "short search key", mutate: func(o *Options) { o.PatientSearchKey = bytes.Repeat([]byte{2}, 31) }, want: ErrInvalidOptions},
		{name: "bad search key version", mutate: func(o *Options) { o.PatientSearchKeyVersion = 0 }, want: ErrInvalidOptions},
		{name: "same keys", mutate: func(o *Options) { o.PatientSearchKey = o.PatientEncryptionKey }, want: ErrInvalidOptions},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validOptions()
			tt.mutate(&opts)
			if err := opts.validate(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	if err := validOptions().validate(); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
}

func TestCatalogConstantsAreSynthetic(t *testing.T) {
	// A real Brandenburg catalog must never be shipped as dev seed data. The
	// prefix and the source note are the visible markers for that.
	if !strings.HasPrefix(CatalogCode, "SYNTH-") {
		t.Errorf("catalog code %q must start with SYNTH-", CatalogCode)
	}
	if !strings.Contains(CatalogSourceNote, "SYNTHETISCH") {
		t.Errorf("source note %q must mark the data as synthetic", CatalogSourceNote)
	}
	if !strings.Contains(CatalogSourceNote, "nicht fachlich verifiziert") {
		t.Errorf("source note %q must state that the data is not verified", CatalogSourceNote)
	}
}

// TestRunTxCreatesSyntheticDataset exercises the whole seed against a real
// database. It runs in one transaction and rolls it back, so it can run against
// the shared test database. It is skipped unless TEST_DB_URL is set.
func TestRunTxCreatesSyntheticDataset(t *testing.T) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping database integration test")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer tx.Rollback()

	ctx := context.Background()
	opts := validOptions()
	opts.Now = time.Now().UTC().Truncate(time.Second)

	result, err := RunTx(ctx, tx, opts)
	if err != nil {
		t.Fatalf("RunTx: %v", err)
	}

	if result.TenantID == uuid.Nil || result.ChefID == uuid.Nil || result.EmployeeID == uuid.Nil || result.AdminID == uuid.Nil {
		t.Fatalf("result is missing ids: %+v", result)
	}
	if result.CatalogID == uuid.Nil {
		t.Fatal("catalog id is nil")
	}
	if len(result.DefinitionIDs) != 3 {
		t.Fatalf("definitions = %d, want 3", len(result.DefinitionIDs))
	}
	if len(result.PatientIDs) != 2 {
		t.Fatalf("patients = %d, want 2", len(result.PatientIDs))
	}
	if result.TemplateID == uuid.Nil || result.VisitID == uuid.Nil {
		t.Fatalf("template/visit id missing: %+v", result)
	}

	var tenantStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM tenants WHERE id = $1`, result.TenantID).Scan(&tenantStatus); err != nil {
		t.Fatalf("read tenant: %v", err)
	}
	if tenantStatus != "valid" {
		t.Errorf("tenant status = %q, want valid", tenantStatus)
	}

	var userCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND role IN ('chef', 'employee', 'platform_admin')`,
		result.TenantID,
	).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 3 {
		t.Errorf("users = %d, want 3", userCount)
	}

	var code, sourceNote string
	if err := tx.QueryRowContext(ctx,
		`SELECT code, source_note FROM service_catalogs WHERE id = $1`, result.CatalogID,
	).Scan(&code, &sourceNote); err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if code != CatalogCode {
		t.Errorf("catalog code = %q, want %q", code, CatalogCode)
	}
	if !strings.Contains(sourceNote, "SYNTHETISCH") {
		t.Errorf("catalog source note = %q, want the synthetic marker", sourceNote)
	}

	var definitionCount, nonSyntheticCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM service_definitions WHERE catalog_id = $1`, result.CatalogID,
	).Scan(&definitionCount); err != nil {
		t.Fatalf("count definitions: %v", err)
	}
	if definitionCount != 3 {
		t.Errorf("definitions = %d, want 3", definitionCount)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM service_definitions WHERE catalog_id = $1 AND code NOT LIKE 'SYNTH-%'`, result.CatalogID,
	).Scan(&nonSyntheticCount); err != nil {
		t.Fatalf("count non-synthetic definitions: %v", err)
	}
	if nonSyntheticCount != 0 {
		t.Errorf("definitions without the SYNTH- prefix = %d, want 0", nonSyntheticCount)
	}

	countRows := []struct {
		name  string
		query string
		want  int
	}{
		{"contracts", `SELECT count(*) FROM tenant_service_contracts WHERE tenant_id = $1`, 1},
		{"rules", `SELECT count(*) FROM activity_service_rules WHERE tenant_id = $1`, 1},
		{"patients", `SELECT count(*) FROM patients WHERE tenant_id = $1`, 2},
		{"assignments", `SELECT count(*) FROM patient_assignments WHERE tenant_id = $1`, 2},
		{"templates", `SELECT count(*) FROM templates WHERE tenant_id = $1`, 1},
		{"visits", `SELECT count(*) FROM visits WHERE tenant_id = $1`, 1},
	}
	for _, row := range countRows {
		var count int
		if err := tx.QueryRowContext(ctx, row.query, result.TenantID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", row.name, err)
		}
		if count != row.want {
			t.Errorf("%s = %d, want %d", row.name, count, row.want)
		}
	}

	var templateActivities int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM template_activities WHERE template_id = $1`, result.TemplateID,
	).Scan(&templateActivities); err != nil {
		t.Fatalf("count template activities: %v", err)
	}
	if templateActivities != 2 {
		t.Errorf("template activities = %d, want 2", templateActivities)
	}

	var auditEvents int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_events WHERE tenant_id = $1`, result.TenantID,
	).Scan(&auditEvents); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditEvents == 0 {
		t.Error("audit events = 0, want at least one")
	}

	// Patient names must be encrypted, not stored in plain text.
	var firstNameEnc []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT first_name_enc FROM patients WHERE id = $1`, result.PatientIDs[0],
	).Scan(&firstNameEnc); err != nil {
		t.Fatalf("read encrypted name: %v", err)
	}
	if len(firstNameEnc) == 0 {
		t.Error("encrypted first name is empty")
	}
	if bytes.Contains(firstNameEnc, []byte("Demo")) {
		t.Error("encrypted first name contains plain text")
	}

	// A second run must not duplicate anything.
	if _, err := RunTx(ctx, tx, opts); !errors.Is(err, ErrAlreadySeeded) {
		t.Fatalf("second RunTx err = %v, want ErrAlreadySeeded", err)
	}
}
