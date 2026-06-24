package agent

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/store"
)

const (
	defaultABTestCaseDetailPageSize = 10
	maxABTestCaseDetailPageSize     = 100
)

var errABTestNotFound = errors.New("abtest not found")

type abtestCaseDetailListQuery struct {
	PageSize int
	Offset   int
	Keyword  string
	Outcome  string
}

type abtestCaseDetailListResponse struct {
	Items         []map[string]any `json:"items"`
	TotalSize     int              `json:"total_size"`
	NextPageToken string           `json:"next_page_token"`
}

func GetThreadABTestCaseDetails(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	abtestID := strings.TrimSpace(mux.Vars(r)["abtest_id"])
	if threadID == "" || abtestID == "" {
		common.ReplyErr(w, "thread_id and abtest_id required", http.StatusBadRequest)
		return
	}
	if _, err := loadUserThread(store.DB(), r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	query, err := parseABTestCaseDetailListQuery(r)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	proxy, statusCode, err := fetchUpstreamProxy(r.Context(), r, threadResultsURL(threadID, "abtests"))
	if err != nil {
		common.ReplyErrWithData(w, "fetch abtests failed", map[string]any{"detail": err.Error()}, statusCode)
		return
	}
	result, err := listABTestCaseDetails(proxy.Body, abtestID, query)
	if err != nil {
		switch {
		case errors.Is(err, errABTestNotFound):
			common.ReplyErr(w, "abtest not found", http.StatusNotFound)
		default:
			common.ReplyErrWithData(w, "list abtest case details failed", map[string]any{"detail": err.Error()}, http.StatusInternalServerError)
		}
		return
	}
	common.ReplyJSON(w, result)
}

func parseABTestCaseDetailListQuery(r *http.Request) (abtestCaseDetailListQuery, error) {
	q := r.URL.Query()
	pageSize := parseABTestCaseDetailPageSize(q.Get("page_size"))
	offset, err := parseThreadPageToken(q.Get("page_token"))
	if err != nil {
		return abtestCaseDetailListQuery{}, err
	}
	return abtestCaseDetailListQuery{
		PageSize: pageSize,
		Offset:   offset,
		Keyword:  strings.TrimSpace(q.Get("keyword")),
		Outcome:  strings.TrimSpace(q.Get("outcome")),
	}, nil
}

func parseABTestCaseDetailPageSize(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultABTestCaseDetailPageSize
	}
	value, err := parseEvalReportPositiveInt(raw)
	if err != nil {
		return defaultABTestCaseDetailPageSize
	}
	if value > maxABTestCaseDetailPageSize {
		return maxABTestCaseDetailPageSize
	}
	return value
}

func listABTestCaseDetails(payload any, abtestID string, query abtestCaseDetailListQuery) (abtestCaseDetailListResponse, error) {
	row, ok := findABTestResultRowByID(payload, abtestID)
	if !ok {
		return abtestCaseDetailListResponse{}, errABTestNotFound
	}
	cases, ok := abtestCaseDetailsFromPayload(row)
	if !ok {
		return abtestCaseDetailListResponse{}, nil
	}
	return buildABTestCaseDetailListResponse(cases, query), nil
}

func findABTestResultRowByID(payload any, abtestID string) (map[string]any, bool) {
	abtestID = strings.TrimSpace(abtestID)
	if abtestID == "" {
		return nil, false
	}
	switch value := payload.(type) {
	case []any:
		for _, item := range value {
			row, ok := item.(map[string]any)
			if ok && abtestResultRowMatchesID(row, abtestID) {
				return row, true
			}
		}
	case map[string]any:
		if abtestResultRowMatchesID(value, abtestID) {
			return value, true
		}
	}
	return nil, false
}

func abtestResultRowMatchesID(row map[string]any, abtestID string) bool {
	if row == nil {
		return false
	}
	aliases := abtestIDAliases(abtestID)
	if data, ok := row["data"].(map[string]any); ok {
		for _, key := range []string{"abtest_id", "id", "task_id"} {
			if abtestIDMatchesAliases(data[key], aliases) {
				return true
			}
		}
	}
	for _, key := range []string{"abtest_id", "id", "task_id", "artifact_id", "runtime_artifact_id", "source_artifact_id", "ref", "artifact_ref"} {
		if abtestIDMatchesAliases(row[key], aliases) {
			return true
		}
	}
	return false
}

func abtestIDAliases(abtestID string) map[string]struct{} {
	aliases := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			aliases[value] = struct{}{}
		}
	}
	add(abtestID)
	if index := strings.LastIndex(abtestID, "@"); index > 0 {
		add(abtestID[:index])
	}
	add(strings.ReplaceAll(abtestID, ".", "_"))
	if abtestID == "abtest_comparison" {
		add("abtest.comparison")
	}
	if abtestID == "abtest.comparison" {
		add("abtest_comparison")
	}
	return aliases
}

func abtestIDMatchesAliases(value any, aliases map[string]struct{}) bool {
	text := strings.TrimSpace(caseCSVScalarString(value))
	if text == "" {
		return false
	}
	if _, ok := aliases[text]; ok {
		return true
	}
	if index := strings.LastIndex(text, "@"); index > 0 {
		if _, ok := aliases[strings.TrimSpace(text[:index])]; ok {
			return true
		}
	}
	return false
}

func abtestCaseDetailsFromPayload(payload any) ([]any, bool) {
	record, ok := payload.(map[string]any)
	if !ok {
		return nil, false
	}
	if cases, ok := abtestCaseDetailsFromRecord(record); ok {
		return cases, true
	}
	if data, ok := record["data"].(map[string]any); ok {
		return abtestCaseDetailsFromRecord(data)
	}
	return nil, false
}

func abtestCaseDetailsFromRecord(record map[string]any) ([]any, bool) {
	if cases, ok := record["case_details"].([]any); ok {
		return cases, true
	}
	if cases, ok := record["case_deltas"].([]any); ok {
		return cases, true
	}
	if summary, ok := record["summary"].(map[string]any); ok {
		if cases, ok := summary["case_details"].([]any); ok {
			return cases, true
		}
		if cases, ok := summary["case_deltas"].([]any); ok {
			return cases, true
		}
	}
	return nil, false
}

func buildABTestCaseDetailListResponse(cases []any, query abtestCaseDetailListQuery) abtestCaseDetailListResponse {
	if query.PageSize <= 0 {
		query.PageSize = defaultABTestCaseDetailPageSize
	}
	if query.PageSize > maxABTestCaseDetailPageSize {
		query.PageSize = maxABTestCaseDetailPageSize
	}
	if query.Offset < 0 {
		query.Offset = 0
	}

	filtered := make([]map[string]any, 0, len(cases))
	for _, item := range cases {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if !abtestCaseDetailMatches(row, query) {
			continue
		}
		filtered = append(filtered, row)
	}

	total := len(filtered)
	if query.Offset >= total {
		return abtestCaseDetailListResponse{
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
	return abtestCaseDetailListResponse{
		Items:         filtered[query.Offset:end],
		TotalSize:     total,
		NextPageToken: nextPageToken,
	}
}

func abtestCaseDetailMatches(row map[string]any, query abtestCaseDetailListQuery) bool {
	if query.Outcome != "" && abtestCaseDetailFieldString(row, "outcome", "Outcome") != query.Outcome {
		return false
	}
	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(abtestCaseDetailSearchText(row)), keyword)
}

func abtestCaseDetailFieldString(row map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := row[name]; ok {
			return strings.TrimSpace(caseCSVScalarString(value))
		}
	}
	return ""
}

func abtestCaseDetailSearchText(row map[string]any) string {
	values := make([]string, 0, len(row))
	for _, key := range []string{"case_id", "caseId", "case_key", "id", "query", "question", "outcome", "conclusion", "judgement", "result"} {
		if value, ok := row[key]; ok {
			values = append(values, caseCSVScalarString(value))
		}
	}
	for _, value := range row {
		values = append(values, caseCSVScalarString(value))
	}
	return strings.Join(values, " ")
}
