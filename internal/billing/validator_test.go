package billing

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidateApproval(t *testing.T) {
	validRun := BillingRun{
		ID:     uuid.New(),
		Status: "draft",
		Lines:  []BillingLine{{ID: uuid.New()}},
	}

	if err := ValidateApproval(validRun); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}

	cases := []struct {
		name string
		run  BillingRun
	}{
		{
			name: "approved run",
			run:  BillingRun{Status: "approved", Lines: []BillingLine{{ID: uuid.New()}}},
		},
		{
			name: "voided run",
			run:  BillingRun{Status: "voided", Lines: []BillingLine{{ID: uuid.New()}}},
		},
		{
			name: "no lines",
			run:  BillingRun{Status: "draft"},
		},
		{
			name: "error finding",
			run: BillingRun{
				Status:   "draft",
				Lines:    []BillingLine{{ID: uuid.New()}},
				Findings: []BillingFinding{{Severity: SeverityError, Code: CodeServiceMissing}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateApproval(tc.run)
			if err == nil {
				t.Fatal("approval was not rejected")
			}
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want ErrInvalid or ErrConflict", err)
			}
		})
	}
}

func TestValidateApprovalAllowsWarningsAndInfo(t *testing.T) {
	run := BillingRun{
		Status: "draft",
		Lines:  []BillingLine{{ID: uuid.New()}},
		Findings: []BillingFinding{
			{Severity: SeverityWarning, Code: CodeSnapshotMismatch},
			{Severity: SeverityInfo, Code: CodeServiceRepeated},
		},
	}
	if err := ValidateApproval(run); err != nil {
		t.Fatalf("warnings/info blocked approval: %v", err)
	}
}

func TestValidateExport(t *testing.T) {
	approved := BillingRun{
		Status: "approved",
		Lines:  []BillingLine{{ID: uuid.New()}},
	}
	if err := ValidateExport(approved); err != nil {
		t.Fatalf("approved run rejected: %v", err)
	}

	draft := BillingRun{Status: "draft", Lines: []BillingLine{{ID: uuid.New()}}}
	if err := ValidateExport(draft); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft export err = %v, want ErrConflict", err)
	}

	empty := BillingRun{Status: "approved"}
	if err := ValidateExport(empty); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty export err = %v, want ErrInvalid", err)
	}
}
