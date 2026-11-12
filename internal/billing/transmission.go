package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotConfigured means that no verified electronic transmission procedure is
// configured yet. It is deliberate: doc 14 requires the concrete SGB XI §105 or
// SGB V §302 procedure and its current technical rules to be clarified before a
// real submission is implemented.
var ErrNotConfigured = errors.New("billing transmission is not configured")

const (
	// TransmissionStatusNotConfigured is the production state. An approved run
	// can be exported internally, but it is not sent to a payer.
	TransmissionStatusNotConfigured = "not_configured"
	// TransmissionStatusPending is a recorded attempt whose adapter call has not
	// finished yet. The test adapter completes synchronously.
	TransmissionStatusPending = "pending"
	// TransmissionStatusAccepted means the payer adapter accepted the claim.
	TransmissionStatusAccepted = "accepted"
	// TransmissionStatusRejected means the payer adapter rejected the claim. The
	// tenant can correct the run and retry with a new idempotency key.
	TransmissionStatusRejected = "rejected"
	// TransmissionStatusFailed means the adapter or transport failed. The
	// attempt is recorded and may be retried with a new idempotency key.
	TransmissionStatusFailed = "failed"

	// AdapterUnconfigured is the production-safe default. It refuses every
	// submission.
	AdapterUnconfigured = "unconfigured"
	// AdapterTest is a deterministic in-process payer used by tests and local
	// development. It must never be wired in production.
	AdapterTest = "test"
)

// TransmissionProcedure describes the legal procedure that a later, verified
// transmission must follow. It is informational and never sends data.
type TransmissionProcedure struct {
	LegalArea string `json:"legal_area"`
	Procedure string `json:"procedure"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// Transmission is one recorded submission attempt. It contains ids, status
// codes, a payload fingerprint and the payer's reference. It never stores the
// claim payload, patient names or free text.
type Transmission struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	RunID             uuid.UUID  `json:"run_id"`
	LegalArea         string     `json:"legal_area"`
	Procedure         string     `json:"procedure"`
	Adapter           string     `json:"adapter"`
	Status            string     `json:"status"`
	IdempotencyKey    string     `json:"idempotency_key"`
	PayloadSHA256     string     `json:"payload_sha256"`
	Attempt           int        `json:"attempt"`
	ExternalReference string     `json:"external_reference,omitempty"`
	ResponseCode      string     `json:"response_code,omitempty"`
	ResponseMessage   string     `json:"response_message,omitempty"`
	SubmittedBy       uuid.UUID  `json:"submitted_by"`
	SubmittedAt       time.Time  `json:"submitted_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// TransmissionRequest is the adapter input. The adapter receives the approved
// run snapshot and the idempotency key, so a retry after a crash cannot create
// a second claim at the payer.
type TransmissionRequest struct {
	Run            BillingRun
	LegalArea      string
	Procedure      string
	IdempotencyKey string
}

// TransmissionResult is the adapter response. A rejected claim is a valid
// business outcome, not a transport error.
type TransmissionResult struct {
	Status            string
	ExternalReference string
	ResponseCode      string
	ResponseMessage   string
}

// Transmitter is the boundary for an electronic submission. A concrete
// production implementation must use a verified SGB XI §105 or SGB V §302
// procedure and a secure transport. Plain email is never acceptable for health
// data.
//
// A real network adapter must be idempotent on IdempotencyKey. The current
// synchronous path is safe for the in-process test adapter; a network adapter
// should be moved to an outbox/worker so a database transaction is not held
// open across network I/O.
type Transmitter interface {
	// Name is stored on every attempt and exposed by the status endpoint.
	Name() string
	// Submit sends one approved run. It must not log or persist patient data.
	Submit(ctx context.Context, request TransmissionRequest) (TransmissionResult, error)
}

// UnconfiguredTransmitter is the safe default. It refuses every submission.
type UnconfiguredTransmitter struct{}

func (UnconfiguredTransmitter) Name() string { return AdapterUnconfigured }

func (UnconfiguredTransmitter) Submit(context.Context, TransmissionRequest) (TransmissionResult, error) {
	return TransmissionResult{}, ErrNotConfigured
}

// TestPayer is a deterministic, in-process payer adapter for tests and local
// development. It never opens a network connection and never stores data. It
// must not be wired in production; config.Load rejects it when PLATFORM=prod.
type TestPayer struct{}

func (TestPayer) Name() string { return AdapterTest }

func (TestPayer) Submit(_ context.Context, request TransmissionRequest) (TransmissionResult, error) {
	if request.Run.Status != "approved" {
		return TransmissionResult{}, fmt.Errorf("%w: only an approved run can be submitted", ErrConflict)
	}
	if len(request.Run.Lines) == 0 {
		return TransmissionResult{}, fmt.Errorf("%w: the billing run has no lines", ErrInvalid)
	}
	if ProcedureLabel(request.LegalArea) == "" {
		return TransmissionResult{}, fmt.Errorf("%w: unknown legal area", ErrInvalid)
	}
	if request.IdempotencyKey == "" {
		return TransmissionResult{}, fmt.Errorf("%w: idempotency key is required", ErrInvalid)
	}

	reference := "TEST-" + strings.ToUpper(strings.ReplaceAll(request.IdempotencyKey, "-", ""))
	if len(reference) > 64 {
		reference = reference[:64]
	}
	return TransmissionResult{
		Status:            TransmissionStatusAccepted,
		ExternalReference: reference,
		ResponseCode:      "TEST_ACCEPTED",
		ResponseMessage:   "Synthetic test payer accepted the claim; no data left the process.",
	}, nil
}

var transmissionProcedures = []TransmissionProcedure{
	{
		LegalArea: "sgb_xi",
		Procedure: "SGB XI §105",
		Status:    TransmissionStatusNotConfigured,
		Note: "Ein gesichertes elektronisches Einreichungsverfahren muss nach den aktuellen " +
			"technischen Vorgaben der Pflegekassen konfiguriert und fachlich geprueft werden. " +
			"Unverschluesselte E-Mail ist fuer Gesundheitsdaten nicht zulaessig.",
	},
	{
		LegalArea: "sgb_v",
		Procedure: "SGB V §302",
		Status:    TransmissionStatusNotConfigured,
		Note: "Ein gesichertes elektronisches Einreichungsverfahren muss nach den aktuellen " +
			"technischen Vorgaben der Krankenkassen konfiguriert und fachlich geprueft werden. " +
			"Unverschluesselte E-Mail ist fuer Gesundheitsdaten nicht zulaessig.",
	},
}

// TransmissionProcedures returns a copy of the known procedures. The caller
// cannot mutate the package state.
func TransmissionProcedures() []TransmissionProcedure {
	result := make([]TransmissionProcedure, len(transmissionProcedures))
	copy(result, transmissionProcedures)
	return result
}

// ProcedureLabel maps the stored legal area to the legal procedure name.
func ProcedureLabel(legalArea string) string {
	switch legalArea {
	case "sgb_xi":
		return "SGB XI §105"
	case "sgb_v":
		return "SGB V §302"
	default:
		return ""
	}
}

// transmissionPayloadHash is a stable fingerprint of the run and line snapshots
// that would be submitted. It contains no patient name or free text. It lets an
// operator prove which billing data an attempt referred to without storing the
// claim payload again.
func transmissionPayloadHash(run BillingRun, legalArea string) string {
	lines := make([]BillingLine, len(run.Lines))
	copy(lines, run.Lines)
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].SortOrder != lines[j].SortOrder {
			return lines[i].SortOrder < lines[j].SortOrder
		}
		return lines[i].ID.String() < lines[j].ID.String()
	})

	var builder strings.Builder
	fmt.Fprintf(&builder, "run=%s\nreport=%s\nversion=%d\nlegal_area=%s\n",
		run.ID, run.ReportID, run.Version, legalArea)
	for _, line := range lines {
		fmt.Fprintf(&builder,
			"line=%s|activity=%s|definition=%s|contract=%s|quantity=%d|performed_at=%s|code=%s|area=%s|payer=%s|contract_ref=%s\n",
			line.ID,
			line.ActivityID,
			line.ServiceDefinitionID,
			line.ContractID,
			line.Quantity,
			line.PerformedAt.UTC().Format(time.RFC3339Nano),
			line.ServiceCode,
			line.LegalArea,
			line.PayerName,
			line.ContractReference,
		)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
