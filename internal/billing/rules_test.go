package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func testCandidate(activityID, serviceID, contractID uuid.UUID, performedAt time.Time) Candidate {
	definition := serviceID
	contract := contractID
	until := performedAt.Add(24 * time.Hour)
	return Candidate{
		ReportActivityID:     uuid.New(),
		ActivityID:           activityID,
		Quantity:             1,
		PerformedAt:          performedAt,
		SortOrder:            0,
		ActivityStatus:       "recorded",
		ServiceDefinitionID:  &definition,
		ContractID:           &contract,
		DefinitionFound:      true,
		DefinitionCode:       "S-1",
		DefinitionName:       "Leistung",
		DefinitionLegalArea:  "sgb_xi",
		DefinitionValidFrom:  performedAt.Add(-24 * time.Hour),
		DefinitionValidUntil: &until,
		ContractFound:        true,
		ContractPayerName:    "Pflegekasse",
		ContractReference:    "V-1",
		ContractLegalArea:    "sgb_xi",
		ContractValidFrom:    performedAt.Add(-24 * time.Hour),
		ContractValidUntil:   &until,
	}
}

func findingCodes(findings []Finding) []string {
	codes := make([]string, 0, len(findings))
	for _, finding := range findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestEvaluateProducesLineForValidActivity(t *testing.T) {
	performedAt := time.Now().UTC()
	candidate := testCandidate(uuid.New(), uuid.New(), uuid.New(), performedAt)

	result := Evaluate(EvaluateInput{
		ReportStatus: "ready_for_billing",
		Candidates:   []Candidate{candidate},
	})
	if len(result.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", result.Findings)
	}
	if len(result.Lines) != 1 {
		t.Fatalf("lines = %+v, want one", result.Lines)
	}
	line := result.Lines[0]
	if line.ActivityID != candidate.ActivityID || line.ServiceCode != "S-1" || line.PayerName != "Pflegekasse" {
		t.Fatalf("line = %+v", line)
	}
}

func TestEvaluateBlocksWrongReportStatusAndEmptyReport(t *testing.T) {
	result := Evaluate(EvaluateInput{ReportStatus: "approved"})
	if !hasFinding(result.Findings, CodeReportStatus) {
		t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeReportStatus)
	}
	if !hasFinding(result.Findings, CodeReportEmpty) {
		t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeReportEmpty)
	}
	if len(result.Lines) != 0 {
		t.Fatalf("lines = %+v, want none", result.Lines)
	}
}

func TestEvaluateRejectsInvalidActivity(t *testing.T) {
	performedAt := time.Now().UTC()
	activityID := uuid.New()

	cases := []struct {
		name   string
		mutate func(*Candidate)
		code   string
	}{
		{"not recorded", func(c *Candidate) { c.ActivityStatus = "voided" }, CodeActivityVoided},
		{"no service", func(c *Candidate) { c.ServiceDefinitionID = nil }, CodeActivityNotBillable},
		{"no contract", func(c *Candidate) { c.ContractID = nil }, CodeActivityNotBillable},
		{"definition missing", func(c *Candidate) { c.DefinitionFound = false }, CodeServiceMissing},
		{"definition invalid", func(c *Candidate) { c.DefinitionValidFrom = performedAt.Add(time.Hour) }, CodeServiceInvalid},
		{"contract missing", func(c *Candidate) { c.ContractFound = false }, CodeContractMissing},
		{"contract invalid", func(c *Candidate) { c.ContractValidUntil = timePtr(performedAt.Add(-time.Hour)) }, CodeContractInvalid},
		{"legal area mismatch", func(c *Candidate) { c.ContractLegalArea = "sgb_v" }, CodeLegalAreaMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := testCandidate(activityID, uuid.New(), uuid.New(), performedAt)
			tc.mutate(&candidate)
			result := Evaluate(EvaluateInput{
				ReportStatus: "ready_for_billing",
				Candidates:   []Candidate{candidate},
			})
			if !hasFinding(result.Findings, tc.code) {
				t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), tc.code)
			}
			if len(result.Lines) != 0 {
				t.Fatalf("lines = %+v, want none", result.Lines)
			}
		})
	}
}

func TestEvaluateDetectsDuplicateAndAlreadyBilledActivity(t *testing.T) {
	performedAt := time.Now().UTC()
	activityID := uuid.New()
	candidate := testCandidate(activityID, uuid.New(), uuid.New(), performedAt)
	runID := uuid.New()

	result := Evaluate(EvaluateInput{
		ReportStatus:  "ready_for_billing",
		Candidates:    []Candidate{candidate, candidate},
		AlreadyBilled: map[uuid.UUID]uuid.UUID{activityID: runID},
	})
	if !hasFinding(result.Findings, CodeActivityDuplicate) {
		t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeActivityDuplicate)
	}
	if !hasFinding(result.Findings, CodeBillingDuplicate) {
		t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeBillingDuplicate)
	}
	if len(result.Lines) != 0 {
		t.Fatalf("lines = %+v, want none", result.Lines)
	}
}

func TestEvaluateRepeatedServiceIsInfoOnly(t *testing.T) {
	performedAt := time.Now().UTC()
	serviceID := uuid.New()
	contractID := uuid.New()
	first := testCandidate(uuid.New(), serviceID, contractID, performedAt)
	second := testCandidate(uuid.New(), serviceID, contractID, performedAt)

	result := Evaluate(EvaluateInput{
		ReportStatus: "ready_for_billing",
		Candidates:   []Candidate{first, second},
	})
	if len(result.Lines) != 2 {
		t.Fatalf("lines = %+v, want two", result.Lines)
	}
	if !hasFinding(result.Findings, CodeServiceRepeated) {
		t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeServiceRepeated)
	}
	for _, finding := range result.Findings {
		if finding.Code == CodeServiceRepeated && finding.Severity != SeverityInfo {
			t.Fatalf("severity = %q, want info", finding.Severity)
		}
	}
}

func TestEvaluateTenantRules(t *testing.T) {
	performedAt := time.Now().UTC()
	serviceA := uuid.New()
	serviceB := uuid.New()
	contractID := uuid.New()
	candidateA := testCandidate(uuid.New(), serviceA, contractID, performedAt)
	candidateB := testCandidate(uuid.New(), serviceB, contractID, performedAt)

	rule := func(kind string) Rule {
		return Rule{
			ID:                         uuid.New(),
			Kind:                       kind,
			ServiceDefinitionID:        serviceA,
			RelatedServiceDefinitionID: serviceB,
			LegalArea:                  "sgb_xi",
			ValidFrom:                  performedAt.Add(-time.Hour),
		}
	}

	t.Run("exclusion", func(t *testing.T) {
		result := Evaluate(EvaluateInput{
			ReportStatus: "ready_for_billing",
			Candidates:   []Candidate{candidateA, candidateB},
			Rules:        []Rule{rule(RuleKindExclusion)},
		})
		if !hasFinding(result.Findings, CodeRuleExclusion) {
			t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeRuleExclusion)
		}
	})

	t.Run("prerequisite missing", func(t *testing.T) {
		result := Evaluate(EvaluateInput{
			ReportStatus: "ready_for_billing",
			Candidates:   []Candidate{candidateA},
			Rules:        []Rule{rule(RuleKindPrerequisite)},
		})
		if !hasFinding(result.Findings, CodeRulePrerequisite) {
			t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeRulePrerequisite)
		}
	})

	t.Run("combination missing", func(t *testing.T) {
		result := Evaluate(EvaluateInput{
			ReportStatus: "ready_for_billing",
			Candidates:   []Candidate{candidateA},
			Rules:        []Rule{rule(RuleKindCombination)},
		})
		if !hasFinding(result.Findings, CodeRuleCombination) {
			t.Fatalf("findings = %v, want %s", findingCodes(result.Findings), CodeRuleCombination)
		}
	})

	t.Run("rule outside validity does not apply", func(t *testing.T) {
		stale := rule(RuleKindExclusion)
		stale.ValidUntil = timePtr(performedAt.Add(-time.Minute))
		result := Evaluate(EvaluateInput{
			ReportStatus: "ready_for_billing",
			Candidates:   []Candidate{candidateA, candidateB},
			Rules:        []Rule{stale},
		})
		if hasFinding(result.Findings, CodeRuleExclusion) {
			t.Fatalf("findings = %v, want no exclusion", findingCodes(result.Findings))
		}
	})
}

func TestEvaluateIsDeterministic(t *testing.T) {
	performedAt := time.Now().UTC()
	candidate := testCandidate(uuid.New(), uuid.New(), uuid.New(), performedAt)
	input := EvaluateInput{
		ReportStatus: "ready_for_billing",
		Candidates:   []Candidate{candidate},
	}
	first := Evaluate(input)
	second := Evaluate(input)
	if len(first.Findings) != len(second.Findings) || len(first.Lines) != len(second.Lines) {
		t.Fatalf("evaluation is not deterministic: %+v vs %+v", first, second)
	}
	for index := range first.Lines {
		if first.Lines[index] != second.Lines[index] {
			t.Fatalf("line %d differs", index)
		}
	}
}

func TestEvaluateSortsErrorsBeforeWarnings(t *testing.T) {
	performedAt := time.Now().UTC()
	serviceID := uuid.New()
	contractID := uuid.New()
	first := testCandidate(uuid.New(), serviceID, contractID, performedAt)
	second := testCandidate(uuid.New(), serviceID, contractID, performedAt)
	first.SnapshotLegalArea = "sgb_v"

	result := Evaluate(EvaluateInput{
		ReportStatus: "ready_for_billing",
		Candidates:   []Candidate{first, second},
	})
	if len(result.Findings) < 2 {
		t.Fatalf("findings = %+v, want at least two", result.Findings)
	}
	if result.Findings[0].Severity != SeverityWarning && result.Findings[0].Severity != SeverityInfo {
		// The repeated-service info is expected here; the snapshot warning also
		// exists. Ordering only guarantees errors first.
		t.Logf("findings = %+v", result.Findings)
	}
	for index := 1; index < len(result.Findings); index++ {
		if severityRank(result.Findings[index-1].Severity) > severityRank(result.Findings[index].Severity) {
			t.Fatalf("findings are not sorted: %+v", result.Findings)
		}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
