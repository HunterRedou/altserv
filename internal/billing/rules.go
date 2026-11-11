package billing

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"

	RuleKindExclusion    = "exclusion"
	RuleKindPrerequisite = "prerequisite"
	RuleKindCombination  = "combination"
)

// Finding codes. They are stable so a client can react to a specific check.
const (
	CodeReportStatus        = "report.status"
	CodeReportEmpty         = "report.empty"
	CodeActivityVoided      = "activity.voided"
	CodeActivityNotBillable = "activity.not_billable"
	CodeActivityDuplicate   = "activity.duplicate"
	CodeServiceMissing      = "service.missing"
	CodeServiceInvalid      = "service.invalid"
	CodeContractMissing     = "contract.missing"
	CodeContractInvalid     = "contract.invalid"
	CodeLegalAreaMismatch   = "legal_area.mismatch"
	CodeSnapshotMismatch    = "snapshot.legal_area_mismatch"
	CodeBillingDuplicate    = "billing.duplicate"
	CodeServiceRepeated     = "service.repeated"
	CodeRuleExclusion       = "rule.exclusion"
	CodeRulePrerequisite    = "rule.prerequisite"
	CodeRuleCombination     = "rule.combination"
)

// Evaluate runs the deterministic billing rules. It has no side effects and no
// randomness, so the same input always produces the same output.
//
// Generic checks (in order):
//   - the report is ready for billing and not empty
//   - the activity is recorded and appears once
//   - the activity has a service definition and a contract
//   - the service definition and the contract are valid at the performed time
//   - the service and contract legal areas match
//   - the activity is not already billed in another run
//
// Tenant rules add exclusions, prerequisites and combination checks.
func Evaluate(input EvaluateInput) Evaluation {
	findings := make([]Finding, 0)
	lines := make([]Line, 0)

	if input.ReportStatus != "ready_for_billing" {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Code:     CodeReportStatus,
			Message:  fmt.Sprintf("report status %q is not ready_for_billing", input.ReportStatus),
		})
	}
	if len(input.Candidates) == 0 {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Code:     CodeReportEmpty,
			Message:  "the report has no performed activities",
		})
	}

	seen := make(map[uuid.UUID]struct{}, len(input.Candidates))
	serviceCounts := make(map[uuid.UUID]int)
	for _, candidate := range input.Candidates {
		activityID := candidate.ActivityID

		if candidate.ActivityStatus != "recorded" {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeActivityVoided,
				Message:    "activity is not recorded",
				ActivityID: &activityID,
			})
			continue
		}
		if _, duplicate := seen[activityID]; duplicate {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeActivityDuplicate,
				Message:    "activity is listed twice",
				ActivityID: &activityID,
			})
			continue
		}
		seen[activityID] = struct{}{}

		if candidate.ServiceDefinitionID == nil || candidate.ContractID == nil {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeActivityNotBillable,
				Message:    "activity has no service definition or contract",
				ActivityID: &activityID,
			})
			continue
		}
		if !candidate.DefinitionFound {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeServiceMissing,
				Message:    "service definition not found",
				ActivityID: &activityID,
			})
			continue
		}
		if !validAt(candidate.DefinitionValidFrom, candidate.DefinitionValidUntil, candidate.PerformedAt) {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeServiceInvalid,
				Message:    "service definition is not valid at the performed time",
				ActivityID: &activityID,
			})
			continue
		}
		if !candidate.ContractFound {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeContractMissing,
				Message:    "contract not found",
				ActivityID: &activityID,
			})
			continue
		}
		if !validAt(candidate.ContractValidFrom, candidate.ContractValidUntil, candidate.PerformedAt) {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeContractInvalid,
				Message:    "contract is not valid at the performed time",
				ActivityID: &activityID,
			})
			continue
		}
		if candidate.DefinitionLegalArea != candidate.ContractLegalArea {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeLegalAreaMismatch,
				Message:    "service and contract legal areas differ",
				ActivityID: &activityID,
			})
			continue
		}
		if candidate.SnapshotLegalArea != "" && candidate.SnapshotLegalArea != candidate.DefinitionLegalArea {
			findings = append(findings, Finding{
				Severity:   SeverityWarning,
				Code:       CodeSnapshotMismatch,
				Message:    "activity snapshot legal area differs from the service definition",
				ActivityID: &activityID,
			})
		}
		if runID, billed := input.AlreadyBilled[activityID]; billed {
			findings = append(findings, Finding{
				Severity:   SeverityError,
				Code:       CodeBillingDuplicate,
				Message:    fmt.Sprintf("activity is already billed in run %s", runID),
				ActivityID: &activityID,
			})
			continue
		}

		lines = append(lines, Line{
			ReportActivityID:    candidate.ReportActivityID,
			ActivityID:          candidate.ActivityID,
			ServiceDefinitionID: *candidate.ServiceDefinitionID,
			ContractID:          *candidate.ContractID,
			Quantity:            candidate.Quantity,
			PerformedAt:         candidate.PerformedAt,
			ServiceCode:         candidate.DefinitionCode,
			ServiceName:         candidate.DefinitionName,
			LegalArea:           candidate.DefinitionLegalArea,
			PayerName:           candidate.ContractPayerName,
			ContractReference:   candidate.ContractReference,
			SortOrder:           candidate.SortOrder,
		})
		serviceCounts[*candidate.ServiceDefinitionID]++
	}

	// The same service twice in one report can be legitimate, but it is worth a
	// human look because it is also a common double-billing pattern.
	for serviceID, count := range serviceCounts {
		if count > 1 {
			findings = append(findings, Finding{
				Severity: SeverityInfo,
				Code:     CodeServiceRepeated,
				Message:  fmt.Sprintf("service %s appears %d times; review whether this is intended", serviceID, count),
			})
		}
	}

	findings = append(findings, evaluateRules(input.Rules, lines)...)
	sortFindings(findings)
	return Evaluation{Lines: lines, Findings: findings}
}

func evaluateRules(rules []Rule, lines []Line) []Finding {
	if len(rules) == 0 || len(lines) == 0 {
		return nil
	}

	byService := make(map[uuid.UUID][]Line, len(lines))
	for _, line := range lines {
		byService[line.ServiceDefinitionID] = append(byService[line.ServiceDefinitionID], line)
	}

	findings := make([]Finding, 0)
	for _, rule := range rules {
		triggerLines := byService[rule.ServiceDefinitionID]
		relatedLines := byService[rule.RelatedServiceDefinitionID]
		if len(triggerLines) == 0 && len(relatedLines) == 0 {
			continue
		}
		if !ruleApplies(rule, triggerLines, relatedLines) {
			continue
		}

		switch rule.Kind {
		case RuleKindExclusion:
			if len(triggerLines) > 0 && len(relatedLines) > 0 {
				findings = append(findings, Finding{
					Severity:   SeverityError,
					Code:       CodeRuleExclusion,
					Message:    ruleMessage("must not be billed together", rule),
					ActivityID: &triggerLines[0].ActivityID,
				})
			}
		case RuleKindPrerequisite:
			if len(triggerLines) > 0 && len(relatedLines) == 0 {
				findings = append(findings, Finding{
					Severity:   SeverityError,
					Code:       CodeRulePrerequisite,
					Message:    ruleMessage("requires another service", rule),
					ActivityID: &triggerLines[0].ActivityID,
				})
			}
		case RuleKindCombination:
			if len(triggerLines) > 0 && len(relatedLines) == 0 {
				findings = append(findings, Finding{
					Severity:   SeverityError,
					Code:       CodeRuleCombination,
					Message:    ruleMessage("must be billed together with another service", rule),
					ActivityID: &triggerLines[0].ActivityID,
				})
			}
			if len(relatedLines) > 0 && len(triggerLines) == 0 {
				findings = append(findings, Finding{
					Severity:   SeverityError,
					Code:       CodeRuleCombination,
					Message:    ruleMessage("must be billed together with another service", rule),
					ActivityID: &relatedLines[0].ActivityID,
				})
			}
		}
	}
	return findings
}

func ruleApplies(rule Rule, groups ...[]Line) bool {
	for _, lines := range groups {
		for _, line := range lines {
			if validAt(rule.ValidFrom, rule.ValidUntil, line.PerformedAt) {
				return true
			}
		}
	}
	return false
}

func ruleMessage(text string, rule Rule) string {
	message := fmt.Sprintf("service %s %s %s", rule.ServiceDefinitionID, text, rule.RelatedServiceDefinitionID)
	if rule.Description != "" {
		message += ": " + rule.Description
	}
	return message
}

// validAt reports whether [from, until) contains at.
func validAt(from time.Time, until *time.Time, at time.Time) bool {
	if at.Before(from) {
		return false
	}
	return until == nil || at.Before(*until)
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) < severityRank(findings[j].Severity)
		}
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		left := ""
		if findings[i].ActivityID != nil {
			left = findings[i].ActivityID.String()
		}
		right := ""
		if findings[j].ActivityID != nil {
			right = findings[j].ActivityID.String()
		}
		if left != right {
			return left < right
		}
		return findings[i].Message < findings[j].Message
	})
}

func severityRank(severity string) int {
	switch severity {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}
