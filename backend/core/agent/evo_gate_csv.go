package agent

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

func buildGateCSV(resultKind string, content any) ([]byte, int, error) {
	rows := gateCSVRows(resultKind, content)
	headers := gateCSVHeaders(rows, gateCSVPreferredHeaders(resultKind, content))

	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buf)
	if len(headers) > 0 {
		if err := writer.Write(headers); err != nil {
			return nil, 0, err
		}
	}
	for _, row := range rows {
		record := make([]string, 0, len(headers))
		for _, header := range headers {
			record = append(record, gateCSVCell(row[header]))
		}
		if err := writer.Write(record); err != nil {
			return nil, 0, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), len(rows), nil
}

func gateCSVRows(resultKind string, content any) []map[string]any {
	switch resultKind {
	case "datasets":
		for _, path := range [][]string{{"download_cases"}, {"cases"}} {
			if rows, ok := csvRowsAtPath(content, path); ok {
				return rows
			}
		}
	case "eval-reports", "analysis-reports":
		for _, path := range [][]string{{"rows"}, {"cases"}} {
			if rows, ok := csvRowsAtPath(content, path); ok {
				return rows
			}
		}
	case "diffs":
		if rows, ok := repairPatchRows(content); ok {
			return rows
		}
	case "abtests":
		for _, path := range [][]string{{"case_deltas"}, {"summary", "case_deltas"}} {
			if rows, ok := csvRowsAtPath(content, path); ok {
				return rows
			}
		}
		if rows, ok := abtestComparisonRows(content); ok {
			return rows
		}
	}
	rows, _ := csvRowsFromValue(content)
	return rows
}

func csvRowsAtPath(root any, path []string) ([]map[string]any, bool) {
	current := root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := object[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return csvRowsFromValue(current)
}

func csvRowsFromValue(value any) ([]map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case []any:
		rows := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
				continue
			}
			rows = append(rows, map[string]any{"value": item})
		}
		return rows, true
	case map[string]any:
		return []map[string]any{typed}, true
	default:
		return []map[string]any{{"value": typed}}, true
	}
}

func gateCSVHeaders(rows []map[string]any, preferred []string) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		for key := range row {
			if key != "" {
				seen[key] = struct{}{}
			}
		}
	}
	headers := make([]string, 0, len(seen))
	for _, key := range preferred {
		if _, ok := seen[key]; ok {
			headers = append(headers, key)
			delete(seen, key)
		}
	}
	extras := make([]string, 0, len(seen))
	for key := range seen {
		extras = append(extras, key)
	}
	sort.Strings(extras)
	headers = append(headers, extras...)
	return headers
}

func gateCSVPreferredHeaders(resultKind string, content any) []string {
	root, ok := content.(map[string]any)
	if !ok {
		return nil
	}
	if resultKind == "datasets" {
		if fields := stringListFromAny(root["download_fields"]); len(fields) > 0 {
			return fields
		}
	}
	return stringListFromAny(root["fields"])
}

func gateCSVCell(value any) string {
	if value == nil {
		return ""
	}
	if items, ok := value.([]any); ok {
		if allCSVScalars(items) {
			parts := make([]string, 0, len(items))
			for _, item := range items {
				parts = append(parts, normalizeGateCSVCell(agentScalarString(item)))
			}
			return protectGateCSVFormula(strings.Join(parts, "; "))
		}
	}
	if isSliceValue(value) && !isByteSlice(value) {
		if encoded, err := json.Marshal(value); err == nil {
			return protectGateCSVFormula(normalizeGateCSVCell(string(encoded)))
		}
	}
	return protectGateCSVFormula(normalizeGateCSVCell(agentScalarString(value)))
}

func allCSVScalars(items []any) bool {
	for _, item := range items {
		if item == nil {
			continue
		}
		if isJSONLikeValue(item) {
			return false
		}
	}
	return true
}

func normalizeGateCSVCell(value string) string {
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	pendingSpace := false
	for _, char := range value {
		switch char {
		case '\r', '\n', '\t':
			pendingSpace = true
			continue
		default:
			if char < ' ' {
				continue
			}
		}
		if pendingSpace && builder.Len() > 0 && char != ' ' {
			builder.WriteByte(' ')
		}
		pendingSpace = false
		builder.WriteRune(char)
	}
	return strings.TrimSpace(builder.String())
}

func protectGateCSVFormula(value string) string {
	if value == "" {
		return ""
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func repairPatchRows(content any) ([]map[string]any, bool) {
	root, ok := content.(map[string]any)
	if !ok {
		return nil, false
	}
	if rows, ok := csvRowsAtPath(root, []string{"files"}); ok && len(rows) > 0 {
		return rows, true
	}
	diff, ok := root["diff"].(map[string]any)
	if !ok {
		return nil, false
	}
	files := make([]string, 0, len(diff))
	for file := range diff {
		files = append(files, file)
	}
	sort.Strings(files)

	rows := make([]map[string]any, 0, len(files))
	for _, file := range files {
		row := map[string]any{
			"file": file,
			"diff": diff[file],
		}
		for _, key := range []string{"run_id", "algo_id", "candidate_algo_id", "status"} {
			if value, ok := root[key]; ok {
				row[key] = value
			}
		}
		rows = append(rows, row)
	}
	return rows, true
}

func abtestComparisonRows(content any) ([]map[string]any, bool) {
	originRows, originOK := csvRowsAtPath(content, []string{"origin", "cases"})
	candidateRows, candidateOK := csvRowsAtPath(content, []string{"candidate", "cases"})
	if !originOK && !candidateOK {
		return nil, false
	}

	orderedKeys := make([]string, 0, len(originRows)+len(candidateRows))
	rowsByKey := map[string]map[string]any{}
	appendRows := func(prefix string, rows []map[string]any) {
		for index, row := range rows {
			caseID := firstNonEmptyScalar(row["case_id"], row["id"])
			rowKey := caseID
			if rowKey == "" {
				rowKey = fmt.Sprintf("\x00row_%d", index)
			}
			merged, ok := rowsByKey[rowKey]
			if !ok {
				merged = map[string]any{}
				rowsByKey[rowKey] = merged
				orderedKeys = append(orderedKeys, rowKey)
			}
			if caseID != "" {
				merged["case_id"] = caseID
			}
			for key, value := range row {
				if key == "case_id" || key == "id" {
					continue
				}
				merged[prefix+"_"+key] = value
			}
		}
	}
	appendRows("origin", originRows)
	appendRows("candidate", candidateRows)

	rows := make([]map[string]any, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		rows = append(rows, rowsByKey[key])
	}
	return rows, true
}

func gateCSVDownloadFilename(threadID, resultKind string, version int) string {
	base := fmt.Sprintf("%s_%s_v%d.csv", safeDownloadNamePart(threadID), safeDownloadNamePart(resultKind), version)
	if base == "__v0.csv" {
		return "evo_result.csv"
	}
	return base
}

func gateCSVContentDisposition(filename string) string {
	escaped := url.PathEscape(filename)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", filename, escaped)
}

func safeDownloadNamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_' || char == '.':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "._-")
}

func isSliceValue(value any) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

func isByteSlice(value any) bool {
	_, ok := value.([]byte)
	return ok
}
