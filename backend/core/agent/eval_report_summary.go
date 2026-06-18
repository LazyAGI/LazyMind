package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	evalReportIDField            = "report_id"
	evalReportBadCaseCountField  = "bad_case_count"
	evalReportTraceCoverageField = "trace_coverage"
	evalReportDefaultBaseDir     = "/var/lib/lazymind/evo"
	evalReportRunID              = "run_1"
)

var errEvalReportNotFound = errors.New("eval report not found")

type evalReportTraceCoverage struct {
	CoveredCount int     `json:"covered_count"`
	TotalCount   int     `json:"total_count"`
	Rate         float64 `json:"rate"`
}

type evalReportManifest struct {
	LatestVersion int                         `json:"latest_version"`
	Versions      []evalReportManifestVersion `json:"versions"`
}

type evalReportManifestVersion struct {
	Version    int    `json:"version"`
	PayloadRef string `json:"payload_ref"`
}

func attachEvalReportSummaryResult(payload any, threadID string) (bool, error) {
	rows, ok := payload.([]any)
	if !ok {
		return false, nil
	}
	found := false
	var firstErr error
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok || !isEvalReportResultRow(row) {
			continue
		}
		found = true
		artifactID := strings.TrimSpace(caseCSVScalarString(row["artifact_id"]))
		if artifactID == "" {
			artifactID = "eval_report"
		}
		if _, exists := row[evalReportIDField]; !exists {
			reportID, err := evalReportIDFromManifest(threadID, artifactID)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
			} else if reportID != "" {
				row[evalReportIDField] = reportID
			}
		}
		if _, exists := row[evalReportTraceCoverageField]; !exists {
			row[evalReportTraceCoverageField] = buildEvalReportTraceCoverage(row["data"])
		}
		if _, exists := row[evalReportBadCaseCountField]; !exists {
			row[evalReportBadCaseCountField] = evalReportBadCaseCount(row["data"])
		}
		removeEvalReportBadCases(row["data"])
	}
	return found, firstErr
}

func isEvalReportResultRow(row map[string]any) bool {
	schema := strings.TrimSpace(caseCSVScalarString(row["schema"]))
	if schema == "EvalReport" {
		return true
	}
	artifactID := strings.TrimSpace(caseCSVScalarString(row["artifact_id"]))
	return artifactID == "eval_report" || strings.HasSuffix(artifactID, "_eval_report")
}

func evalReportIDFromManifest(threadID, artifactID string) (string, error) {
	manifest, err := loadEvalReportManifest(threadID, artifactID)
	if err != nil {
		return "", err
	}
	version := selectEvalReportManifestVersion(manifest)
	payloadRef := strings.TrimSpace(version.PayloadRef)
	if payloadRef == "" {
		return "", nil
	}
	name := filepath.Base(payloadRef)
	return strings.TrimSuffix(name, filepath.Ext(name)), nil
}

func evalReportManifestPath(threadID, artifactID string) (string, error) {
	artifactID = strings.TrimSpace(artifactID)
	if !safeEvalReportPathSegment(artifactID) {
		return "", fmt.Errorf("invalid artifact_id")
	}
	runDir, err := evalReportRunDir(threadID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "artifacts", "manifests", artifactID+".json"), nil
}

func evalReportRunDir(threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if !safeEvalReportPathSegment(threadID) {
		return "", fmt.Errorf("invalid thread_id")
	}
	return filepath.Join(evalReportBaseDir(), "dev-runs", threadID, "store", "runs", evalReportRunID), nil
}

func loadEvalReportManifest(threadID, artifactID string) (evalReportManifest, error) {
	manifestPath, err := evalReportManifestPath(threadID, artifactID)
	if err != nil {
		return evalReportManifest{}, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return evalReportManifest{}, fmt.Errorf("read eval report manifest failed: %w", err)
	}
	var manifest evalReportManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return evalReportManifest{}, fmt.Errorf("decode eval report manifest failed: %w", err)
	}
	return manifest, nil
}

func evalReportBaseDir() string {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_EVO_BASE_DIR")); value != "" {
		return value
	}
	return evalReportDefaultBaseDir
}

func safeEvalReportPathSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\`)
}

func selectEvalReportManifestVersion(manifest evalReportManifest) evalReportManifestVersion {
	if manifest.LatestVersion > 0 {
		for _, version := range manifest.Versions {
			if version.Version == manifest.LatestVersion {
				return version
			}
		}
	}
	for idx := len(manifest.Versions) - 1; idx >= 0; idx-- {
		if manifest.Versions[idx].PayloadRef != "" {
			return manifest.Versions[idx]
		}
	}
	return evalReportManifestVersion{}
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

func removeEvalReportBadCases(data any) {
	record, ok := data.(map[string]any)
	if !ok {
		return
	}
	delete(record, "bad_cases")
}
