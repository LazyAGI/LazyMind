package evalset

import (
	"encoding/json"
	"strings"

	"lazymind/core/common/orm"
)

func normalizeDatasetIDs(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func datasetIDsJSON(ids []string) json.RawMessage {
	raw, err := json.Marshal(normalizeDatasetIDs(ids))
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(raw)
}

func parseDatasetIDsJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil
	}
	return normalizeDatasetIDs(ids)
}

func collectEvalSetDatasetIDs(rows []orm.EvalSet) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, row := range rows {
		for _, id := range parseDatasetIDsJSON(row.DatasetIDs) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func datasetNamesForIDs(ids []string, names map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := strings.TrimSpace(names[id])
		if name == "" {
			name = id
		}
		out = append(out, name)
	}
	return out
}

func filterRowsByDatasetIDs(rows []orm.EvalSet, ids []string) []orm.EvalSet {
	filterIDs := normalizeDatasetIDs(ids)
	if len(filterIDs) == 0 {
		return rows
	}
	want := make(map[string]struct{}, len(filterIDs))
	for _, id := range filterIDs {
		want[id] = struct{}{}
	}
	out := make([]orm.EvalSet, 0, len(rows))
	for _, row := range rows {
		for _, id := range parseDatasetIDsJSON(row.DatasetIDs) {
			if _, ok := want[id]; ok {
				out = append(out, row)
				break
			}
		}
	}
	return out
}
