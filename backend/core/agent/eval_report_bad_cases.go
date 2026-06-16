package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/store"
)

const (
	defaultEvalReportBadCasePageSize = 10
	maxEvalReportBadCasePageSize     = 100
)

type evalReportBadCaseListQuery struct {
	PageSize    int
	Offset      int
	Keyword     string
	FailureType string
}

type evalReportBadCaseListResponse struct {
	Items         []map[string]any `json:"items"`
	TotalSize     int              `json:"total_size"`
	NextPageToken string           `json:"next_page_token"`
}

func GetThreadEvalReportBadCases(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	reportID := strings.TrimSpace(mux.Vars(r)["report_id"])
	if threadID == "" || reportID == "" {
		common.ReplyErr(w, "thread_id and report_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	query, err := parseEvalReportBadCaseListQuery(r)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := listEvalReportBadCases(threadID, "eval_report", reportID, query)
	if err != nil {
		switch {
		case errors.Is(err, errEvalReportNotFound), errors.Is(err, gorm.ErrRecordNotFound):
			common.ReplyErr(w, "eval report not found", http.StatusNotFound)
		default:
			common.ReplyErrWithData(w, "list eval report bad cases failed", map[string]any{"detail": err.Error()}, http.StatusInternalServerError)
		}
		return
	}
	common.ReplyJSON(w, result)
}

func parseEvalReportBadCaseListQuery(r *http.Request) (evalReportBadCaseListQuery, error) {
	q := r.URL.Query()
	pageSize := parseEvalReportBadCasePageSize(q.Get("page_size"))
	offset, err := parseThreadPageToken(q.Get("page_token"))
	if err != nil {
		return evalReportBadCaseListQuery{}, err
	}
	return evalReportBadCaseListQuery{
		PageSize:    pageSize,
		Offset:      offset,
		Keyword:     strings.TrimSpace(q.Get("keyword")),
		FailureType: strings.TrimSpace(q.Get("failure_type")),
	}, nil
}

func parseEvalReportBadCasePageSize(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultEvalReportBadCasePageSize
	}
	value, err := parseEvalReportPositiveInt(raw)
	if err != nil {
		return defaultEvalReportBadCasePageSize
	}
	if value > maxEvalReportBadCasePageSize {
		return maxEvalReportBadCasePageSize
	}
	return value
}

func parseEvalReportPositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func listEvalReportBadCases(threadID, artifactID, reportID string, query evalReportBadCaseListQuery) (evalReportBadCaseListResponse, error) {
	payload, err := readEvalReportPayloadByReportID(threadID, artifactID, reportID)
	if err != nil {
		return evalReportBadCaseListResponse{}, err
	}
	badCases, ok := evalReportBadCasesFromPayload(payload)
	if !ok {
		return evalReportBadCaseListResponse{}, nil
	}
	return buildEvalReportBadCaseListResponse(badCases, query), nil
}

func readEvalReportPayloadByReportID(threadID, artifactID, reportID string) (any, error) {
	reportID = strings.TrimSpace(reportID)
	if !safeEvalReportPathSegment(reportID) {
		return nil, fmt.Errorf("invalid report_id")
	}
	manifest, err := loadEvalReportManifest(threadID, artifactID)
	if err != nil {
		return nil, err
	}
	version, ok := selectEvalReportManifestVersionByReportID(manifest, reportID)
	if !ok {
		return nil, errEvalReportNotFound
	}
	path, err := evalReportPayloadPath(threadID, version.PayloadRef)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errEvalReportNotFound
		}
		return nil, fmt.Errorf("read eval report payload failed: %w", err)
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode eval report payload failed: %w", err)
	}
	return payload, nil
}

func selectEvalReportManifestVersionByReportID(manifest evalReportManifest, reportID string) (evalReportManifestVersion, bool) {
	for _, version := range manifest.Versions {
		if evalReportIDFromPayloadRef(version.PayloadRef) == reportID {
			return version, true
		}
	}
	return evalReportManifestVersion{}, false
}

func evalReportIDFromPayloadRef(payloadRef string) string {
	name := filepath.Base(strings.TrimSpace(payloadRef))
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func evalReportPayloadPath(threadID, payloadRef string) (string, error) {
	runDir, err := evalReportRunDir(threadID)
	if err != nil {
		return "", err
	}
	payloadRef = strings.TrimSpace(payloadRef)
	if payloadRef == "" {
		return "", errEvalReportNotFound
	}
	rel := filepath.Clean(filepath.FromSlash(payloadRef))
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid payload_ref")
	}
	return filepath.Join(runDir, rel), nil
}

func evalReportBadCasesFromPayload(payload any) ([]any, bool) {
	if badCases, ok := evalReportBadCases(payload); ok {
		return badCases, true
	}
	record, ok := payload.(map[string]any)
	if !ok {
		return nil, false
	}
	return evalReportBadCases(record["data"])
}

func buildEvalReportBadCaseListResponse(badCases []any, query evalReportBadCaseListQuery) evalReportBadCaseListResponse {
	if query.PageSize <= 0 {
		query.PageSize = defaultEvalReportBadCasePageSize
	}
	if query.PageSize > maxEvalReportBadCasePageSize {
		query.PageSize = maxEvalReportBadCasePageSize
	}
	if query.Offset < 0 {
		query.Offset = 0
	}

	filtered := make([]map[string]any, 0, len(badCases))
	for _, item := range badCases {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if !evalReportBadCaseMatches(row, query) {
			continue
		}
		filtered = append(filtered, row)
	}

	total := len(filtered)
	if query.Offset >= total {
		return evalReportBadCaseListResponse{
			Items:     []map[string]any{},
			TotalSize: total,
		}
	}
	end := query.Offset + query.PageSize
	if end > total {
		end = total
	}
	nextPageToken := ""
	if end < total {
		nextPageToken = fmt.Sprintf("%d", end)
	}
	return evalReportBadCaseListResponse{
		Items:         filtered[query.Offset:end],
		TotalSize:     total,
		NextPageToken: nextPageToken,
	}
}

func evalReportBadCaseMatches(row map[string]any, query evalReportBadCaseListQuery) bool {
	if query.FailureType != "" && evalReportBadCaseFieldString(row, "failure_type", "FailureType", "failureType") != query.FailureType {
		return false
	}
	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	if keyword == "" {
		return true
	}
	defect := strings.ToLower(evalReportBadCaseFieldString(row, "Defect", "defect"))
	reason := strings.ToLower(evalReportBadCaseFieldString(row, "Reason", "reason"))
	return strings.Contains(defect, keyword) || strings.Contains(reason, keyword)
}

func evalReportBadCaseFieldString(row map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := row[name]; ok {
			return strings.TrimSpace(caseCSVScalarString(value))
		}
	}
	return ""
}
