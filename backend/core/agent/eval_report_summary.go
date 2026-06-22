package agent

import (
	"errors"
	"strings"
)

const (
	evalReportIDField            = "report_id"
	evalReportBadCaseCountField  = "bad_case_count"
	evalReportTraceCoverageField = "trace_coverage"
)

var errEvalReportNotFound = errors.New("eval report not found")

type evalReportTraceCoverage struct {
	CoveredCount int     `json:"covered_count"`
	TotalCount   int     `json:"total_count"`
	Rate         float64 `json:"rate"`
}

func attachEvalReportSummaryResult(payload any, _ string) (bool, error) {
	rows, ok := payload.([]any)
	if !ok {
		return false, nil
	}
	found := false
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok || !isEvalReportResultRow(row) {
			continue
		}
		found = true
		if _, exists := row[evalReportIDField]; !exists {
			if reportID := evalReportIDFromRow(row); reportID != "" {
				row[evalReportIDField] = reportID
			}
		}
		if _, exists := row[evalReportTraceCoverageField]; !exists {
			row[evalReportTraceCoverageField] = buildEvalReportTraceCoverage(row["data"])
		}
		if _, exists := row[evalReportBadCaseCountField]; !exists {
			row[evalReportBadCaseCountField] = evalReportBadCaseCount(row["data"])
		}
	}
	return found, nil
}

func isEvalReportResultRow(row map[string]any) bool {
	schema := strings.TrimSpace(caseCSVScalarString(row["schema"]))
	if schema == "EvalReport" {
		return true
	}
	artifactID := strings.TrimSpace(caseCSVScalarString(row["artifact_id"]))
	return artifactID == "eval_report" || strings.HasSuffix(artifactID, "_eval_report")
}

func evalReportIDFromRow(row map[string]any) string {
	if data, ok := row["data"].(map[string]any); ok {
		if reportID := strings.TrimSpace(caseCSVScalarString(data["id"])); reportID != "" {
			return reportID
		}
	}
	if reportID := evalReportIDFromRef(row["ref"]); reportID != "" {
		return reportID
	}
	return evalReportIDFromRef(row["artifact_ref"])
}

func evalReportIDFromRef(value any) string {
	ref := strings.TrimSpace(caseCSVScalarString(value))
	if ref == "" {
		return ""
	}
	if index := strings.LastIndex(ref, "@"); index > 0 {
		return strings.TrimSpace(ref[:index])
	}
	return ref
}

func buildEvalReportTraceCoverage(data any) evalReportTraceCoverage {
	badCases, ok := evalReportBadCases(data)
	if !ok {
		return evalReportTraceCoverage{}
	}
	covered := 0
	for _, item := range badCases {
		row, ok := item.(map[string]any)
		if ok && strings.TrimSpace(caseCSVScalarString(row["trace_id"])) != "" {
			covered++
		}
	}
	total := len(badCases)
	coverage := evalReportTraceCoverage{CoveredCount: covered, TotalCount: total}
	if total > 0 {
		coverage.Rate = float64(covered) / float64(total)
	}
	return coverage
}

func evalReportBadCaseCount(data any) int {
	badCases, ok := evalReportBadCases(data)
	if !ok {
		return 0
	}
	return len(badCases)
}

func evalReportBadCases(data any) ([]any, bool) {
	record, ok := data.(map[string]any)
	if !ok {
		return nil, false
	}
	badCases, ok := record["bad_cases"].([]any)
	return badCases, ok
}
