package billing

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// TestBillingTransmissionDatabase covers migration 027: persistence,
// idempotent replay, the one-active-attempt index, RLS, the missing DELETE
// grant and the audit trail. It is skipped unless TEST_DB_URL is set. Each
// subtest uses its own transaction because a database error aborts it.
func TestBillingTransmissionDatabase(t *testing.T) {
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

	ctx := context.Background()
	payer := NewServiceWithTransmitter(NewRepository(), audit.SQLRecorder{}, TestPayer{})

	transmit := func(t *testing.T, tx *sql.Tx, service *Service, actor Actor, runID uuid.UUID, key string) (TransmissionOutcome, error) {
		t.Helper()
		var outcome TransmissionOutcome
		err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			var err error
			outcome, err = service.Transmit(ctx, q, actor, "req-transmit", TransmitInput{RunID: runID, LegalArea: "sgb_xi", IdempotencyKey: key})
			return err
		})
		return outcome, err
	}

	t.Run("accepted attempt persists, replays and blocks void", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()
		actor, run := seedApprovedBillingRun(t, tx, queries, payer)

		first, err := transmit(t, tx, payer, actor, run.ID, "db-key-0001")
		if err != nil {
			t.Fatalf("transmit: %v", err)
		}
		got := first.Transmission
		if !first.Created || got.Status != TransmissionStatusAccepted || got.Adapter != AdapterTest || got.Attempt != 1 {
			t.Fatalf("first = %+v", first)
		}
		if got.ID == uuid.Nil || got.CompletedAt == nil || got.PayloadSHA256 != transmissionPayloadHash(run, "sgb_xi") {
			t.Fatalf("persisted = %+v", got)
		}

		replay, err := transmit(t, tx, payer, actor, run.ID, "db-key-0001")
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		if replay.Created || replay.Transmission.ID != got.ID {
			t.Fatalf("replay = %+v", replay)
		}

		if _, err := transmit(t, tx, payer, actor, run.ID, "db-key-0002"); !errors.Is(err, ErrConflict) {
			t.Fatalf("second key err = %v, want ErrConflict", err)
		}

		var list []Transmission
		if err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			var err error
			list, err = payer.ListTransmissions(ctx, q, actor, "req", run.ID)
			return err
		}); err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 1 || list[0].ID != got.ID {
			t.Fatalf("list = %+v", list)
		}

		if err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			_, err := payer.Void(ctx, q, actor, "req", run.ID, run.Version)
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("void err = %v, want ErrConflict", err)
		}

		// A completed attempt cannot be completed again.
		if err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			_, err := NewRepository().CompleteTransmission(ctx, q, actor.TenantID, got.ID, TransmissionResult{Status: TransmissionStatusRejected})
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("recomplete err = %v, want ErrConflict", err)
		}

		rows, err := tx.QueryContext(ctx,
			`SELECT action, metadata::text FROM audit_events WHERE tenant_id = $1 AND entity_type = 'billing_transmission' ORDER BY occurred_at, action`,
			actor.TenantID)
		if err != nil {
			t.Fatalf("query audit: %v", err)
		}
		defer rows.Close()
		var actions []string
		for rows.Next() {
			var action, metadata string
			if err := rows.Scan(&action, &metadata); err != nil {
				t.Fatalf("scan audit: %v", err)
			}
			actions = append(actions, action)
			for _, forbidden := range []string{"Testleistung", "Pflegekasse", "response_message", "payload", "procedure", "§"} {
				if strings.Contains(metadata, forbidden) {
					t.Fatalf("%s metadata leaks %q: %s", action, forbidden, metadata)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("audit rows: %v", err)
		}
		if strings.Join(actions, ",") != "billing.transmission_accepted,billing.transmission_requested" {
			t.Fatalf("audit actions = %v", actions)
		}
	})

	t.Run("rejected attempt allows a retry", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()
		rejecting := NewServiceWithTransmitter(NewRepository(), audit.SQLRecorder{}, &scriptedTransmitter{
			result: TransmissionResult{Status: TransmissionStatusRejected, ResponseCode: "R01"},
		})
		actor, run := seedApprovedBillingRun(t, tx, queries, payer)

		first, err := transmit(t, tx, rejecting, actor, run.ID, "db-key-0001")
		if err != nil || first.Transmission.Status != TransmissionStatusRejected {
			t.Fatalf("first = %+v, %v", first, err)
		}
		second, err := transmit(t, tx, payer, actor, run.ID, "db-key-0002")
		if err != nil || second.Transmission.Status != TransmissionStatusAccepted || second.Transmission.Attempt != 2 {
			t.Fatalf("retry = %+v, %v", second, err)
		}

		// Regression: submitted_at defaulted to NOW(), so attempts in one
		// transaction had equal timestamps and the list order was random.
		if !second.Transmission.SubmittedAt.After(first.Transmission.SubmittedAt) {
			t.Fatalf("submitted_at %s not after %s", second.Transmission.SubmittedAt, first.Transmission.SubmittedAt)
		}
		var list []Transmission
		if err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			var err error
			list, err = payer.ListTransmissions(ctx, q, actor, "req", run.ID)
			return err
		}); err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 2 || list[0].ID != second.Transmission.ID || list[1].ID != first.Transmission.ID {
			t.Fatalf("list order = %+v, want newest first", list)
		}
	})

	t.Run("database enforces one active attempt per run", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()
		actor, run := seedApprovedBillingRun(t, tx, queries, payer)
		repo := NewRepository()

		err := db.WithTenantTx(ctx, tx, actor.TenantID, func(q *db.Queries) error {
			for _, key := range []string{"db-key-0001", "db-key-0002"} {
				if _, err := repo.CreateTransmission(ctx, q, CreateTransmissionParams{
					TenantID: actor.TenantID, RunID: run.ID, LegalArea: "sgb_xi", Adapter: AdapterTest,
					IdempotencyKey: key, PayloadSHA256: "x", Attempt: 1, SubmittedBy: actor.UserID,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if !isUniqueViolation(err) {
			t.Fatalf("second pending insert err = %v, want unique violation", err)
		}
	})

	t.Run("application role cannot delete a transmission", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()
		actor, run := seedApprovedBillingRun(t, tx, queries, payer)
		if _, err := transmit(t, tx, payer, actor, run.ID, "db-key-0001"); err != nil {
			t.Fatalf("transmit: %v", err)
		}
		err := db.WithTenantTx(ctx, tx, actor.TenantID, func(*db.Queries) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM billing_transmissions WHERE run_id = $1`, run.ID)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("delete err = %v, want permission denied", err)
		}
	})

	t.Run("another tenant cannot see or replay a transmission", func(t *testing.T) {
		tx, queries := newBillingTx(t, conn)
		defer tx.Rollback()
		actor, run := seedApprovedBillingRun(t, tx, queries, payer)
		if _, err := transmit(t, tx, payer, actor, run.ID, "db-key-0001"); err != nil {
			t.Fatalf("transmit: %v", err)
		}
		otherTenant, otherChef := seedBillingTenant(t, queries)
		other := Actor{UserID: otherChef.ID, TenantID: otherTenant.ID, Role: "chef"}

		if err := db.WithTenantTx(ctx, tx, other.TenantID, func(q *db.Queries) error {
			_, err := payer.ListTransmissions(ctx, q, other, "req", run.ID)
			return err
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant list err = %v, want ErrNotFound", err)
		}
		var visible int
		if err := db.WithTenantTx(ctx, tx, other.TenantID, func(*db.Queries) error {
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM billing_transmissions`).Scan(&visible)
		}); err != nil {
			t.Fatalf("count: %v", err)
		}
		if visible != 0 {
			t.Fatalf("other tenant sees %d transmissions", visible)
		}
		// The same key is scoped per tenant; the other tenant's run does not
		// exist for it, so the replay cannot reach the first tenant's row.
		if _, err := transmit(t, tx, payer, other, run.ID, "db-key-0001"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant transmit err = %v, want ErrNotFound", err)
		}
	})
}

// seedApprovedBillingRun builds one released report with one SGB XI activity,
// generates a run and approves it.
func seedApprovedBillingRun(t *testing.T, tx *sql.Tx, queries *db.Queries, service *Service) (Actor, BillingRun) {
	t.Helper()
	ctx := context.Background()

	tenant, chef := seedBillingTenant(t, queries)
	employee := seedBillingUser(t, queries, tenant.ID, "employee")
	patientID := seedBillingPatient(t, tx, tenant.ID)
	definitionID := seedBillingDefinition(t, tx, "sgb_xi")
	contractID := seedBillingContract(t, tx, tenant.ID, definitionID, "sgb_xi")
	visitID := seedBillingVisit(t, tx, tenant.ID, patientID, employee.ID)
	activityID := seedBillingActivity(t, tx, tenant.ID, patientID, employee.ID, definitionID, contractID, "sgb_xi")
	reportID := seedBillingReport(t, tx, tenant.ID, patientID, visitID, employee.ID, chef.ID, "ready_for_billing")
	seedBillingReportActivity(t, tx, tenant.ID, reportID, activityID, 1)

	actor := Actor{UserID: chef.ID, TenantID: tenant.ID, Role: "chef"}
	var run BillingRun
	if err := db.WithTenantTx(ctx, tx, tenant.ID, func(q *db.Queries) error {
		var err error
		run, err = service.Generate(ctx, q, actor, "req", reportID, GenerateInput{})
		if err != nil {
			return err
		}
		run, err = service.Approve(ctx, q, actor, "req", run.ID, run.Version)
		return err
	}); err != nil {
		t.Fatalf("generate and approve: %v", err)
	}
	return actor, run
}
