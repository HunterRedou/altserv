package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// scriptedTransmitter returns a fixed result so a test can drive every
// adapter outcome without a network.
type scriptedTransmitter struct {
	result TransmissionResult
	err    error
	calls  int
	last   TransmissionRequest
}

func (s *scriptedTransmitter) Name() string { return "scripted" }

func (s *scriptedTransmitter) Submit(_ context.Context, request TransmissionRequest) (TransmissionResult, error) {
	s.calls++
	s.last = request
	return s.result, s.err
}

func approvedRun(tenantID uuid.UUID, legalArea string) BillingRun {
	runID := uuid.New()
	performedAt := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	return BillingRun{
		ID:       runID,
		TenantID: tenantID,
		ReportID: uuid.New(),
		Status:   "approved",
		Version:  2,
		Lines: []BillingLine{
			{
				ID:                  uuid.New(),
				RunID:               runID,
				ActivityID:          uuid.New(),
				ServiceDefinitionID: uuid.New(),
				ContractID:          uuid.New(),
				Quantity:            1,
				PerformedAt:         performedAt,
				ServiceCode:         "SYNTH-01",
				ServiceName:         "Synthetic service",
				LegalArea:           legalArea,
				PayerName:           "Synthetic payer",
				ContractReference:   "SYNTH-CONTRACT",
				SortOrder:           1,
			},
			{
				ID:                  uuid.New(),
				RunID:               runID,
				ActivityID:          uuid.New(),
				ServiceDefinitionID: uuid.New(),
				ContractID:          uuid.New(),
				Quantity:            2,
				PerformedAt:         performedAt.Add(time.Hour),
				ServiceCode:         "SYNTH-02",
				ServiceName:         "Synthetic service 2",
				LegalArea:           legalArea,
				PayerName:           "Synthetic payer",
				ContractReference:   "SYNTH-CONTRACT",
				SortOrder:           2,
			},
		},
	}
}

func chefActor(tenantID uuid.UUID) Actor {
	return Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}
}

func transmitFixture(transmitter Transmitter) (*Service, *fakeStore, *fakeAudit, Actor) {
	tenantID := uuid.New()
	store := &fakeStore{run: approvedRun(tenantID, "sgb_xi")}
	recorder := &fakeAudit{}
	return NewServiceWithTransmitter(store, recorder, transmitter), store, recorder, chefActor(tenantID)
}

func transmitInput(store *fakeStore, key string) TransmitInput {
	return TransmitInput{RunID: store.run.ID, LegalArea: "sgb_xi", IdempotencyKey: key}
}

func auditActions(recorder *fakeAudit) []string {
	actions := make([]string, 0, len(recorder.events))
	for _, event := range recorder.events {
		actions = append(actions, event.Action)
	}
	return actions
}

func TestTransmitDeniesNonChef(t *testing.T) {
	service, store, recorder, chef := transmitFixture(TestPayer{})
	for _, role := range []string{"employee", "platform_admin"} {
		actor := Actor{UserID: uuid.New(), TenantID: chef.TenantID, Role: role}
		if _, err := service.Transmit(context.Background(), nil, actor, "req", transmitInput(store, "key-00000001")); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s err = %v, want ErrForbidden", role, err)
		}
		if _, err := service.ListTransmissions(context.Background(), nil, actor, "req", store.run.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s list err = %v, want ErrForbidden", role, err)
		}
	}
	if store.createTransCalls != 0 {
		t.Fatalf("create calls = %d, want 0", store.createTransCalls)
	}
	for _, event := range recorder.events {
		if event.Action != "access.denied" {
			t.Fatalf("unexpected audit action %q", event.Action)
		}
	}
}

func TestTransmitValidatesInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeStore, *TransmitInput)
		want   error
	}{
		{"missing run id", func(_ *fakeStore, in *TransmitInput) { in.RunID = uuid.Nil }, ErrInvalid},
		{"unknown legal area", func(_ *fakeStore, in *TransmitInput) { in.LegalArea = "sgb_ix" }, ErrInvalid},
		{"short key", func(_ *fakeStore, in *TransmitInput) { in.IdempotencyKey = "short" }, ErrInvalid},
		{"key with space", func(_ *fakeStore, in *TransmitInput) { in.IdempotencyKey = "key with space" }, ErrInvalid},
		{"key too long", func(_ *fakeStore, in *TransmitInput) { in.IdempotencyKey = strings.Repeat("a", 129) }, ErrInvalid},
		{"draft run", func(s *fakeStore, _ *TransmitInput) { s.run.Status = "draft" }, ErrConflict},
		{"voided run", func(s *fakeStore, _ *TransmitInput) { s.run.Status = "voided" }, ErrConflict},
		{"no lines", func(s *fakeStore, _ *TransmitInput) { s.run.Lines = nil }, ErrInvalid},
		{"mixed legal area", func(s *fakeStore, _ *TransmitInput) { s.run.Lines[1].LegalArea = "sgb_v" }, ErrInvalid},
		{"wrong legal area", func(_ *fakeStore, in *TransmitInput) { in.LegalArea = "sgb_v" }, ErrInvalid},
		{"missing run", func(s *fakeStore, _ *TransmitInput) { s.runErr = ErrNotFound }, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, store, _, chef := transmitFixture(TestPayer{})
			input := transmitInput(store, "key-00000001")
			tc.mutate(store, &input)
			if _, err := service.Transmit(context.Background(), nil, chef, "req", input); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if store.createTransCalls != 0 {
				t.Fatalf("create calls = %d, want 0", store.createTransCalls)
			}
		})
	}
}

// Production default: nothing is sent and no transmission row is created, but
// the attempt is audited.
func TestTransmitUnconfiguredRecordsNoAttempt(t *testing.T) {
	service, store, recorder, chef := transmitFixture(UnconfiguredTransmitter{})
	outcome, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if outcome.Transmission.Status != TransmissionStatusNotConfigured || outcome.Created {
		t.Fatalf("outcome = %+v, want not_configured and not created", outcome)
	}
	if store.createTransCalls != 0 || len(store.transmissions) != 0 {
		t.Fatalf("unconfigured adapter created %d rows", len(store.transmissions))
	}
	if got := auditActions(recorder); len(got) != 1 || got[0] != "billing.transmission_not_configured" {
		t.Fatalf("audit = %v", got)
	}
}

func TestNewServiceDefaultsToUnconfigured(t *testing.T) {
	if got := NewService(&fakeStore{}, &fakeAudit{}).TransmissionAdapter(); got != AdapterUnconfigured {
		t.Fatalf("NewService adapter = %q", got)
	}
	if got := NewServiceWithTransmitter(&fakeStore{}, &fakeAudit{}, nil).TransmissionAdapter(); got != AdapterUnconfigured {
		t.Fatalf("nil transmitter adapter = %q", got)
	}
}

func TestTransmitTestPayerAcceptsAndAudits(t *testing.T) {
	service, store, recorder, chef := transmitFixture(TestPayer{})
	outcome, err := service.Transmit(context.Background(), nil, chef, "req-1", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	got := outcome.Transmission
	if !outcome.Created || got.Status != TransmissionStatusAccepted || got.Adapter != AdapterTest {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got.Attempt != 1 || got.Procedure != "SGB XI §105" || got.CompletedAt == nil {
		t.Fatalf("transmission = %+v", got)
	}
	if got.PayloadSHA256 != transmissionPayloadHash(store.run, "sgb_xi") || len(got.PayloadSHA256) != 64 {
		t.Fatalf("payload hash = %q", got.PayloadSHA256)
	}
	if !strings.HasPrefix(got.ExternalReference, "TEST-") {
		t.Fatalf("external reference = %q", got.ExternalReference)
	}
	actions := auditActions(recorder)
	if len(actions) != 2 || actions[0] != "billing.transmission_requested" || actions[1] != "billing.transmission_accepted" {
		t.Fatalf("audit = %v", actions)
	}
	for _, event := range recorder.events {
		if event.TenantID != chef.TenantID || event.ActorUserID != chef.UserID || event.RequestID != "req-1" {
			t.Fatalf("audit event = %+v", event)
		}
	}
}

// Audit metadata must never carry patient names, free text, payer messages,
// service names or the payload itself.
func TestTransmitAuditMetadataIsPrivacySafe(t *testing.T) {
	transmitter := &scriptedTransmitter{result: TransmissionResult{
		Status:          TransmissionStatusRejected,
		ResponseCode:    "R01",
		ResponseMessage: "Patient Erika Mustermann: Diagnose fehlt",
	}}
	service, store, recorder, chef := transmitFixture(transmitter)
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	allowed := map[string]bool{
		"run_id": true, "legal_area": true, "adapter": true,
		"attempt": true, "status": true, "response_code": true, "external_reference": true,
	}
	for _, event := range recorder.events {
		for key, value := range event.Metadata {
			if !allowed[key] {
				t.Fatalf("%s: unexpected metadata key %q", event.Action, key)
			}
			text, _ := value.(string)
			for _, forbidden := range []string{"Mustermann", "Diagnose", "Synthetic service", "Synthetic payer", "SYNTH-CONTRACT", "§"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s: metadata %q leaks %q", event.Action, key, forbidden)
				}
			}
		}
	}
}

func TestTransmitReplaySameKeyDoesNotResubmit(t *testing.T) {
	transmitter := &scriptedTransmitter{result: TransmissionResult{Status: TransmissionStatusAccepted, ExternalReference: "EXT-1"}}
	service, store, recorder, chef := transmitFixture(transmitter)
	first, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	eventsAfterFirst := len(recorder.events)

	replay, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "  key-00000001  "))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Transmission.ID != first.Transmission.ID {
		t.Fatalf("replay = %+v, want the first attempt", replay)
	}
	if transmitter.calls != 1 || store.createTransCalls != 1 || len(recorder.events) != eventsAfterFirst {
		t.Fatalf("replay resubmitted: calls=%d creates=%d events=%d", transmitter.calls, store.createTransCalls, len(recorder.events))
	}
}

func TestTransmitKeyReuseForOtherRunConflicts(t *testing.T) {
	service, store, _, chef := transmitFixture(TestPayer{})
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); err != nil {
		t.Fatalf("first: %v", err)
	}
	store.run = approvedRun(chef.TenantID, "sgb_xi")
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); !errors.Is(err, ErrConflict) {
		t.Fatalf("other run err = %v, want ErrConflict", err)
	}
}

func TestTransmitAcceptedRunBlocksSecondKey(t *testing.T) {
	service, store, _, chef := transmitFixture(TestPayer{})
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000002")); !errors.Is(err, ErrConflict) {
		t.Fatalf("second key err = %v, want ErrConflict", err)
	}
	if store.createTransCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createTransCalls)
	}
}

func TestTransmitRejectedAllowsRetryWithNewKey(t *testing.T) {
	transmitter := &scriptedTransmitter{result: TransmissionResult{Status: TransmissionStatusRejected, ResponseCode: "R01"}}
	service, store, recorder, chef := transmitFixture(transmitter)
	first, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Transmission.Status != TransmissionStatusRejected || first.Transmission.ResponseCode != "R01" {
		t.Fatalf("first = %+v", first.Transmission)
	}
	if got := auditActions(recorder); got[len(got)-1] != "billing.transmission_rejected" {
		t.Fatalf("audit = %v", got)
	}

	transmitter.result = TransmissionResult{Status: TransmissionStatusAccepted}
	second, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000002"))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if second.Transmission.Status != TransmissionStatusAccepted || second.Transmission.Attempt != 2 {
		t.Fatalf("retry = %+v", second.Transmission)
	}
	list, err := service.ListTransmissions(context.Background(), nil, chef, "req", store.run.ID)
	if err != nil {
		t.Fatalf("ListTransmissions: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.Transmission.ID {
		t.Fatalf("list = %+v, want newest first", list)
	}
}

// An adapter error is a recorded failed attempt with a generic code. The raw
// adapter error text is not stored because it could contain payload data.
func TestTransmitAdapterErrorIsRecordedAsFailed(t *testing.T) {
	transmitter := &scriptedTransmitter{err: errors.New("dial tcp: patient Erika Mustermann")}
	service, store, recorder, chef := transmitFixture(transmitter)
	outcome, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	got := outcome.Transmission
	if got.Status != TransmissionStatusFailed || got.ResponseCode != "adapter_error" {
		t.Fatalf("transmission = %+v", got)
	}
	if strings.Contains(got.ResponseMessage, "Mustermann") {
		t.Fatalf("response message leaks adapter error: %q", got.ResponseMessage)
	}
	if actions := auditActions(recorder); actions[len(actions)-1] != "billing.transmission_failed" {
		t.Fatalf("audit = %v", actions)
	}
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000002")); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
}

func TestTransmitInvalidAdapterStatusBecomesFailed(t *testing.T) {
	transmitter := &scriptedTransmitter{result: TransmissionResult{Status: "sent"}}
	service, store, _, chef := transmitFixture(transmitter)
	outcome, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001"))
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if outcome.Transmission.Status != TransmissionStatusFailed || outcome.Transmission.ResponseCode != "invalid_adapter_result" {
		t.Fatalf("transmission = %+v", outcome.Transmission)
	}
}

func TestTransmitUniqueViolationIsConflict(t *testing.T) {
	service, store, _, chef := transmitFixture(TestPayer{})
	// A concurrent request committed first: the pre-checks saw no row, but the
	// insert hits the unique index.
	store.createTransErr = &pgconn.PgError{Code: "23505"}
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestVoidBlockedByActiveTransmission(t *testing.T) {
	service, store, _, chef := transmitFixture(TestPayer{})
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if _, err := service.Void(context.Background(), nil, chef, "req", store.run.ID, store.run.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("void err = %v, want ErrConflict", err)
	}
	if store.voidCalls != 0 {
		t.Fatalf("void calls = %d, want 0", store.voidCalls)
	}
}

func TestVoidAllowedAfterRejectedTransmission(t *testing.T) {
	transmitter := &scriptedTransmitter{result: TransmissionResult{Status: TransmissionStatusRejected}}
	service, store, _, chef := transmitFixture(transmitter)
	if _, err := service.Transmit(context.Background(), nil, chef, "req", transmitInput(store, "key-00000001")); err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if _, err := service.Void(context.Background(), nil, chef, "req", store.run.ID, store.run.Version); err != nil {
		t.Fatalf("void after rejection: %v", err)
	}
}

func TestTransmissionPayloadHashIsStable(t *testing.T) {
	run := approvedRun(uuid.New(), "sgb_xi")
	base := transmissionPayloadHash(run, "sgb_xi")

	reordered := run
	reordered.Lines = []BillingLine{run.Lines[1], run.Lines[0]}
	if transmissionPayloadHash(reordered, "sgb_xi") != base {
		t.Fatal("hash depends on line order")
	}

	changed := run
	changed.Lines = append([]BillingLine(nil), run.Lines...)
	changed.Lines[0].Quantity = 5
	if transmissionPayloadHash(changed, "sgb_xi") == base {
		t.Fatal("hash ignores a quantity change")
	}
	if transmissionPayloadHash(run, "sgb_v") == base {
		t.Fatal("hash ignores the legal area")
	}
	// Service names are not part of the fingerprint input, so renaming a
	// catalog label does not invalidate proof of what was submitted.
	renamed := run
	renamed.Lines = append([]BillingLine(nil), run.Lines...)
	renamed.Lines[0].ServiceName = "Renamed"
	if transmissionPayloadHash(renamed, "sgb_xi") != base {
		t.Fatal("hash depends on the display name")
	}
}

func TestTestPayerRefusesInvalidRequests(t *testing.T) {
	run := approvedRun(uuid.New(), "sgb_v")
	valid := TransmissionRequest{Run: run, LegalArea: "sgb_v", IdempotencyKey: "key-00000001"}
	if result, err := (TestPayer{}).Submit(context.Background(), valid); err != nil || result.Status != TransmissionStatusAccepted {
		t.Fatalf("valid = %+v, %v", result, err)
	}
	draft := valid
	draft.Run.Status = "draft"
	empty := valid
	empty.Run.Lines = nil
	unknown := valid
	unknown.LegalArea = "other"
	noKey := valid
	noKey.IdempotencyKey = ""
	for name, request := range map[string]TransmissionRequest{"draft": draft, "empty": empty, "unknown": unknown, "no key": noKey} {
		if _, err := (TestPayer{}).Submit(context.Background(), request); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}

	long := valid
	long.IdempotencyKey = strings.Repeat("a", 128)
	result, err := (TestPayer{}).Submit(context.Background(), long)
	if err != nil || len(result.ExternalReference) > 64 {
		t.Fatalf("long key reference = %q, %v", result.ExternalReference, err)
	}
}

func TestNormalizeTransmissionResultBoundsFields(t *testing.T) {
	result := normalizeTransmissionResult(TransmissionResult{
		Status:            TransmissionStatusRejected,
		ExternalReference: strings.Repeat("r", 300),
		ResponseCode:      strings.Repeat("c", 300),
		ResponseMessage:   strings.Repeat("ü", 900),
	})
	if len([]rune(result.ExternalReference)) != 200 || len([]rune(result.ResponseCode)) != 100 || len([]rune(result.ResponseMessage)) != 500 {
		t.Fatalf("lengths = %d/%d/%d", len([]rune(result.ExternalReference)), len([]rune(result.ResponseCode)), len([]rune(result.ResponseMessage)))
	}
}

func TestValidIdempotencyKey(t *testing.T) {
	for _, key := range []string{"abcdefgh", "A1:b2.c3_d4-e5", strings.Repeat("x", 128), uuid.NewString()} {
		if !validIdempotencyKey(key) {
			t.Fatalf("%q should be valid", key)
		}
	}
	for _, key := range []string{"", "abcdefg", strings.Repeat("x", 129), "abc/defgh", "abcdefgä", "abc defgh"} {
		if validIdempotencyKey(key) {
			t.Fatalf("%q should be invalid", key)
		}
	}
}
