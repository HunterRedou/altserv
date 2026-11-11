package billing

import (
	"context"
	"errors"
)

// ErrNotConfigured means that no verified electronic transmission procedure is
// configured yet. It is deliberate: doc 14 requires the concrete SGB XI §105 or
// SGB V §302 procedure and its current technical rules to be clarified before a
// submission is implemented.
var ErrNotConfigured = errors.New("billing transmission is not configured")

const (
	// TransmissionStatusNotConfigured is the only status for now. An approved
	// run can be exported internally, but it is not sent to a payer.
	TransmissionStatusNotConfigured = "not_configured"
)

// TransmissionProcedure describes the legal procedure that a later, verified
// transmission must follow. It is informational and never sends data.
type TransmissionProcedure struct {
	LegalArea string `json:"legal_area"`
	Procedure string `json:"procedure"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// Transmitter is the boundary for a later electronic submission. It is
// intentionally not implemented. A concrete implementation must use a verified
// SGB XI §105 or SGB V §302 procedure and a secure transport. Plain email is
// never acceptable for health data.
type Transmitter interface {
	Submit(ctx context.Context, run BillingRun) error
}

// UnconfiguredTransmitter is the safe default. It refuses every submission.
type UnconfiguredTransmitter struct{}

func (UnconfiguredTransmitter) Submit(context.Context, BillingRun) error {
	return ErrNotConfigured
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
