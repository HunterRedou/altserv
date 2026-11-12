package billing

import "fmt"

// ValidateApproval checks that a draft run can be approved. Only a draft run
// with at least one line and no error findings may be approved. A warning or an
// info finding is shown to the chef but does not block.
func ValidateApproval(run BillingRun) error {
	if run.Status != "draft" {
		return fmt.Errorf("%w: only a draft billing run can be approved", ErrConflict)
	}
	if len(run.Lines) == 0 {
		return fmt.Errorf("%w: a billing run needs at least one line", ErrInvalid)
	}
	for _, finding := range run.Findings {
		if finding.Severity == SeverityError {
			return fmt.Errorf("%w: the billing run has unresolved errors", ErrInvalid)
		}
	}
	return nil
}

// ValidateExport checks that a run can be exported. PDF and CSV are internal
// exports for now. A concrete, verified SGB XI §105 or SGB V §302 transmission
// is a separate step (doc 14).
func ValidateExport(run BillingRun) error {
	if run.Status != "approved" {
		return fmt.Errorf("%w: only an approved billing run can be exported", ErrConflict)
	}
	if len(run.Lines) == 0 {
		return fmt.Errorf("%w: the billing run has no lines", ErrInvalid)
	}
	return nil
}

// ValidateTransmission checks that a run can be submitted as one legal area.
// A run with mixed legal areas must be split first, because §105 SGB XI and
// §302 SGB V use different procedures and payers.
func ValidateTransmission(run BillingRun, legalArea string) error {
	if legalArea != "sgb_xi" && legalArea != "sgb_v" {
		return fmt.Errorf("%w: unknown legal area", ErrInvalid)
	}
	if run.Status != "approved" {
		return fmt.Errorf("%w: only an approved billing run can be transmitted", ErrConflict)
	}
	if len(run.Lines) == 0 {
		return fmt.Errorf("%w: the billing run has no lines", ErrInvalid)
	}
	for _, line := range run.Lines {
		if line.LegalArea != legalArea {
			return fmt.Errorf("%w: the billing run contains a line outside the selected legal area", ErrInvalid)
		}
	}
	return nil
}
