package doc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

type AggregateDocumentsRequest struct {
	DatasetIDs      []string `json:"dataset_ids,omitempty"`
	FileTypes       []string `json:"file_types,omitempty"`
	DocumentStages  []string `json:"document_stages,omitempty"`
	DataSourceTypes []string `json:"data_source_types,omitempty"`
	Creators        []string `json:"creators,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	GroupBy         []string `json:"group_by,omitempty"`
}

type AggregateDocumentsGroup struct {
	Key   map[string]string `json:"key"`
	Count int64             `json:"count"`
	Size  int64             `json:"size"`
}

type AggregateDocumentsResponse struct {
	TotalCount int64                     `json:"total_count"`
	TotalSize  int64                     `json:"total_size"`
	Groups     []AggregateDocumentsGroup `json:"groups,omitempty"`
}

func AggregateDocuments(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req AggregateDocumentsRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	datasetIDs := normalizeDocumentDatasetIDs(req.DatasetIDs)
	var err error
	if len(datasetIDs) == 0 {
		datasetIDs, err = accessibleDatasetIDs(r.Context(), userID)
	} else {
		datasetIDs, err = readableRequestedDatasetIDs(r.Context(), userID, datasetIDs)
	}
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query datasets failed", err), http.StatusInternalServerError)
		return
	}
	if len(datasetIDs) == 0 {
		common.ReplyJSON(w, AggregateDocumentsResponse{Groups: []AggregateDocumentsGroup{}})
		return
	}

	rows, _, err := loadMergedDocumentsBySearch(r.Context(), datasetIDs, "", nil, int(^uint(0)>>1), 0)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query documents failed", err), http.StatusInternalServerError)
		return
	}
	resp := aggregateDocumentRows(rows, req)
	common.ReplyJSON(w, resp)
}

func aggregateDocumentRows(rows []mergedDocRow, req AggregateDocumentsRequest) AggregateDocumentsResponse {
	fileTypes := normalizedSet(req.FileTypes)
	documentStages := normalizedSet(req.DocumentStages)
	dataSourceTypes := normalizedSet(req.DataSourceTypes)
	creators := normalizedSet(req.Creators)
	tags := normalizedSet(req.Tags)
	groupBy := normalizeGroupBy(req.GroupBy)

	resp := AggregateDocumentsResponse{}
	groups := map[string]*AggregateDocumentsGroup{}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Type), "FOLDER") {
			continue
		}
		rowTags := documentRowTags(row)
		if !matchesNormalizedSet(fileTypes, row.Type) ||
			!matchesNormalizedSet(documentStages, row.DocumentStage) ||
			!matchesNormalizedSet(dataSourceTypes, row.DataSourceType) ||
			!matchesNormalizedSet(creators, row.Creator) ||
			!containsAllNormalized(rowTags, tags) {
			continue
		}

		resp.TotalCount++
		resp.TotalSize += row.DocumentSize
		if len(groupBy) == 0 {
			continue
		}
		for _, key := range aggregateKeys(row, rowTags, groupBy) {
			groupKey := encodeAggregateKey(key, groupBy)
			group := groups[groupKey]
			if group == nil {
				groups[groupKey] = &AggregateDocumentsGroup{Key: key}
				group = groups[groupKey]
			}
			group.Count++
			group.Size += row.DocumentSize
		}
	}
	resp.Groups = make([]AggregateDocumentsGroup, 0, len(groups))
	for _, group := range groups {
		resp.Groups = append(resp.Groups, *group)
	}
	return resp
}

func normalizedSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = normalizeAggregateValue(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func normalizeAggregateValue(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func matchesNormalizedSet(set map[string]struct{}, value string) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[normalizeAggregateValue(value)]
	return ok
}

func containsAllNormalized(values []string, want map[string]struct{}) bool {
	if len(want) == 0 {
		return true
	}
	have := normalizedSet(values)
	for value := range want {
		if _, ok := have[value]; !ok {
			return false
		}
	}
	return true
}

func documentRowTags(row mergedDocRow) []string {
	var tags []string
	_ = json.Unmarshal(row.Tags, &tags)
	return tags
}

func normalizeGroupBy(values []string) []string {
	allowed := map[string]struct{}{
		"dataset_id":       {},
		"dataset_display":  {},
		"file_type":        {},
		"document_stage":   {},
		"data_source_type": {},
		"creator":          {},
		"tag":              {},
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func aggregateKeys(row mergedDocRow, tags []string, groupBy []string) []map[string]string {
	keys := []map[string]string{{}}
	for _, field := range groupBy {
		values := aggregateFieldValues(row, tags, field)
		next := make([]map[string]string, 0, len(keys)*len(values))
		for _, key := range keys {
			for _, value := range values {
				copyKey := make(map[string]string, len(key)+1)
				for k, v := range key {
					copyKey[k] = v
				}
				copyKey[field] = value
				next = append(next, copyKey)
			}
		}
		keys = next
	}
	return keys
}

func aggregateFieldValues(row mergedDocRow, tags []string, field string) []string {
	switch field {
	case "dataset_id":
		return []string{strings.TrimSpace(row.DatasetID)}
	case "dataset_display":
		return []string{strings.TrimSpace(row.DatasetDisplay)}
	case "file_type":
		return []string{strings.TrimSpace(row.Type)}
	case "document_stage":
		return []string{strings.TrimSpace(row.DocumentStage)}
	case "data_source_type":
		return []string{strings.TrimSpace(row.DataSourceType)}
	case "creator":
		return []string{strings.TrimSpace(row.Creator)}
	case "tag":
		if len(tags) == 0 {
			return []string{""}
		}
		return tags
	default:
		return []string{""}
	}
}

func encodeAggregateKey(key map[string]string, groupBy []string) string {
	parts := make([]string, 0, len(groupBy))
	for _, field := range groupBy {
		parts = append(parts, field+"="+key[field])
	}
	return strings.Join(parts, "\x00")
}
