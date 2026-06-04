package evalset

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	importFileTypeCSV  = "csv"
	importFileTypeJSON = "json"
	importFileTypeXLSX = "xlsx"

	importPreviewRowCount = 20
	importMaxErrors       = 200
)

var importTemplateFields = []string{
	"case_id",
	"generate_reason",
	"ground_truth",
	"is_deleted",
	"key_points",
	"question",
	"question_type",
	"reference_chunk_ids",
	"reference_context",
	"reference_doc",
	"reference_doc_ids",
}

var importRequiredFields = []string{"question", "ground_truth", "question_type"}

type importParseResult struct {
	rows             []ImportNormalizedRow
	invalidRows      []importInvalidRow
	invalidCSVHeader []string
	errorDetails     []ImportValidationErrorDetail
	errorsTruncated  bool
	totalRows        int64
	emptyRows        int64
}

type importInvalidRow struct {
	rowNumber int
	values    map[string]string
	fields    []string
	errors    []ImportValidationErrorDetail
}

type importValidationError struct {
	response ImportValidationErrorResponse
}

func (e *importValidationError) Error() string {
	return "eval set import validation failed"
}

func parseImportRows(fileType string, data []byte) (*importParseResult, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    0,
			Column: "",
			Reason: "文件必须是 UTF-8",
		}}, false)
	}

	switch fileType {
	case importFileTypeCSV:
		return parseCSVImportRows(data)
	case importFileTypeJSON:
		return parseJSONImportRows(data)
	default:
		return nil, errors.New("unsupported file_type")
	}
}

func parseCSVImportRows(data []byte) (*importParseResult, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    1,
			Column: "",
			Reason: "header required",
		}}, false)
	}
	if err != nil {
		return nil, csvParseValidationError(err)
	}

	collector := newImportErrorCollector()
	headerIndex := map[string]int{}
	for i, raw := range header {
		name := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if name == "" {
			continue
		}
		if _, exists := headerIndex[name]; exists {
			collector.add(1, name, "header 重复")
			continue
		}
		headerIndex[name] = i
	}
	for _, field := range importRequiredFields {
		if _, ok := headerIndex[field]; !ok {
			collector.add(1, field, field+" header 缺失")
		}
	}
	if collector.hasErrors() {
		return nil, collector.validationError()
	}

	result := &importParseResult{
		rows:             make([]ImportNormalizedRow, 0),
		invalidCSVHeader: cleanedCSVHeader(header),
	}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, csvParseValidationError(err)
		}
		result.totalRows++
		rowNumber := int(result.totalRows) + 1
		if len(record) > 0 {
			line, _ := reader.FieldPos(0)
			if line > 0 {
				rowNumber = line
			}
		}

		values := csvImportValues(record, headerIndex)
		if importValuesEmpty(values) {
			result.emptyRows++
			continue
		}

		row, err := normalizedRowFromValues(values)
		rowErrors := make([]ImportValidationErrorDetail, 0, len(importRequiredFields)+1)
		if err != nil {
			rowErrors = append(rowErrors, ImportValidationErrorDetail{Row: rowNumber, Column: "is_deleted", Reason: err.Error()})
			row = rowFromValuesWithoutBool(values)
		}
		rowErrors = append(rowErrors, requiredImportErrors(rowNumber, row)...)
		if len(rowErrors) > 0 {
			result.addInvalidRow(rowNumber, csvValuesByHeader(result.invalidCSVHeader, record), record, rowErrors)
			continue
		}
		result.rows = append(result.rows, row)
		if len(result.rows) > importMaxRows() {
			return nil, newImportValidationError([]ImportValidationErrorDetail{{
				Row:    rowNumber,
				Column: "",
				Reason: fmt.Sprintf("valid rows exceeds %d", importMaxRows()),
			}}, false)
		}
	}
	return result, nil
}

func parseJSONImportRows(data []byte) (*importParseResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var top any
	if err := decoder.Decode(&top); err != nil {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    0,
			Column: "",
			Reason: "JSON 解析失败: " + err.Error(),
		}}, false)
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    0,
			Column: "",
			Reason: "JSON 解析失败: " + err.Error(),
		}}, false)
	} else if err == nil {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    0,
			Column: "",
			Reason: "JSON 只能包含一个顶层值",
		}}, false)
	}

	items, ok := jsonImportItems(top)
	if !ok {
		return nil, newImportValidationError([]ImportValidationErrorDetail{{
			Row:    0,
			Column: "",
			Reason: `JSON 顶层必须是数组或包含 items 数组的对象`,
		}}, false)
	}

	result := &importParseResult{
		rows:             make([]ImportNormalizedRow, 0, len(items)),
		invalidCSVHeader: append([]string(nil), importTemplateFields...),
	}
	for i, item := range items {
		rowNumber := i + 1
		result.totalRows++
		obj, ok := item.(map[string]any)
		if !ok {
			result.addInvalidRow(rowNumber, map[string]string{}, nil, []ImportValidationErrorDetail{{
				Row:    rowNumber,
				Column: "",
				Reason: "item 必须是 object",
			}})
			continue
		}

		originalValues := valuesFromJSONObjectBestEffort(obj)
		row, err := normalizedRowFromJSONObject(obj)
		rowErrors := make([]ImportValidationErrorDetail, 0, len(importRequiredFields)+1)
		if err != nil {
			var fieldErr *importFieldError
			if errors.As(err, &fieldErr) {
				rowErrors = append(rowErrors, ImportValidationErrorDetail{Row: rowNumber, Column: fieldErr.column, Reason: fieldErr.reason})
			} else {
				rowErrors = append(rowErrors, ImportValidationErrorDetail{Row: rowNumber, Column: "", Reason: err.Error()})
			}
			row = rowFromJSONObjectBestEffort(obj)
		}
		if normalizedImportRowEmpty(row) {
			result.emptyRows++
			continue
		}
		rowErrors = append(rowErrors, requiredImportErrors(rowNumber, row)...)
		if len(rowErrors) > 0 {
			result.addInvalidRow(rowNumber, originalValues, valuesByHeader(result.invalidCSVHeader, originalValues), rowErrors)
			continue
		}
		result.rows = append(result.rows, row)
		if len(result.rows) > importMaxRows() {
			return nil, newImportValidationError([]ImportValidationErrorDetail{{
				Row:    rowNumber,
				Column: "",
				Reason: fmt.Sprintf("valid rows exceeds %d", importMaxRows()),
			}}, false)
		}
	}
	return result, nil
}

func csvParseValidationError(err error) error {
	row := 0
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		row = parseErr.Line
	}
	return newImportValidationError([]ImportValidationErrorDetail{{
		Row:    row,
		Column: "",
		Reason: "CSV 解析失败: " + err.Error(),
	}}, false)
}

func csvImportValues(record []string, headerIndex map[string]int) map[string]string {
	values := make(map[string]string, len(importTemplateFields))
	for _, field := range importTemplateFields {
		idx, ok := headerIndex[field]
		if !ok || idx >= len(record) {
			values[field] = ""
			continue
		}
		values[field] = strings.TrimSpace(record[idx])
	}
	return values
}

func cleanedCSVHeader(header []string) []string {
	out := make([]string, len(header))
	for i, value := range header {
		if i == 0 {
			value = strings.TrimPrefix(value, "\ufeff")
		}
		out[i] = value
	}
	return out
}

func csvValuesByHeader(header, record []string) map[string]string {
	values := make(map[string]string, len(header))
	for i, name := range header {
		if name == "" {
			continue
		}
		if i < len(record) {
			values[name] = record[i]
		} else {
			values[name] = ""
		}
	}
	return values
}

func importValuesEmpty(values map[string]string) bool {
	for _, field := range importTemplateFields {
		if strings.TrimSpace(values[field]) != "" {
			return false
		}
	}
	return true
}

func normalizedRowFromValues(values map[string]string) (ImportNormalizedRow, error) {
	isDeleted, err := parseImportBool(values["is_deleted"])
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	return ImportNormalizedRow{
		CaseID:            values["case_id"],
		GenerateReason:    values["generate_reason"],
		GroundTruth:       values["ground_truth"],
		IsDeleted:         isDeleted,
		KeyPoints:         values["key_points"],
		Question:          values["question"],
		QuestionType:      values["question_type"],
		ReferenceChunkIDs: values["reference_chunk_ids"],
		ReferenceContext:  values["reference_context"],
		ReferenceDoc:      values["reference_doc"],
		ReferenceDocIDs:   values["reference_doc_ids"],
	}, nil
}

func rowFromValuesWithoutBool(values map[string]string) ImportNormalizedRow {
	return ImportNormalizedRow{
		CaseID:            values["case_id"],
		GenerateReason:    values["generate_reason"],
		GroundTruth:       values["ground_truth"],
		KeyPoints:         values["key_points"],
		Question:          values["question"],
		QuestionType:      values["question_type"],
		ReferenceChunkIDs: values["reference_chunk_ids"],
		ReferenceContext:  values["reference_context"],
		ReferenceDoc:      values["reference_doc"],
		ReferenceDocIDs:   values["reference_doc_ids"],
	}
}

func jsonImportItems(top any) ([]any, bool) {
	switch value := top.(type) {
	case []any:
		return value, true
	case map[string]any:
		items, ok := value["items"].([]any)
		return items, ok
	default:
		return nil, false
	}
}

func normalizedRowFromJSONObject(obj map[string]any) (ImportNormalizedRow, error) {
	stringField := func(field string) (string, error) {
		value, ok := obj[field]
		if !ok || value == nil {
			return "", nil
		}
		out, err := importJSONString(value)
		if err != nil {
			return "", &importFieldError{column: field, reason: field + " 必须是字符串"}
		}
		return out, nil
	}

	caseID, err := stringField("case_id")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	generateReason, err := stringField("generate_reason")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	groundTruth, err := stringField("ground_truth")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	keyPoints, err := stringField("key_points")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	question, err := stringField("question")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	questionType, err := stringField("question_type")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	referenceChunkIDs, err := stringField("reference_chunk_ids")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	referenceContext, err := stringField("reference_context")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	referenceDoc, err := stringField("reference_doc")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	referenceDocIDs, err := stringField("reference_doc_ids")
	if err != nil {
		return ImportNormalizedRow{}, err
	}
	isDeleted, err := importJSONBool(obj["is_deleted"])
	if err != nil {
		return ImportNormalizedRow{}, &importFieldError{column: "is_deleted", reason: err.Error()}
	}

	return ImportNormalizedRow{
		CaseID:            caseID,
		GenerateReason:    generateReason,
		GroundTruth:       groundTruth,
		IsDeleted:         isDeleted,
		KeyPoints:         keyPoints,
		Question:          question,
		QuestionType:      questionType,
		ReferenceChunkIDs: referenceChunkIDs,
		ReferenceContext:  referenceContext,
		ReferenceDoc:      referenceDoc,
		ReferenceDocIDs:   referenceDocIDs,
	}, nil
}

func rowFromJSONObjectBestEffort(obj map[string]any) ImportNormalizedRow {
	value := func(field string) string {
		out, _ := importJSONString(obj[field])
		return out
	}
	isDeleted, _ := importJSONBool(obj["is_deleted"])
	return ImportNormalizedRow{
		CaseID:            value("case_id"),
		GenerateReason:    value("generate_reason"),
		GroundTruth:       value("ground_truth"),
		IsDeleted:         isDeleted,
		KeyPoints:         value("key_points"),
		Question:          value("question"),
		QuestionType:      value("question_type"),
		ReferenceChunkIDs: value("reference_chunk_ids"),
		ReferenceContext:  value("reference_context"),
		ReferenceDoc:      value("reference_doc"),
		ReferenceDocIDs:   value("reference_doc_ids"),
	}
}

func valuesFromNormalizedRow(row ImportNormalizedRow) map[string]string {
	values := map[string]string{
		"case_id":             row.CaseID,
		"generate_reason":     row.GenerateReason,
		"ground_truth":        row.GroundTruth,
		"is_deleted":          strconv.FormatBool(row.IsDeleted),
		"key_points":          row.KeyPoints,
		"question":            row.Question,
		"question_type":       row.QuestionType,
		"reference_chunk_ids": row.ReferenceChunkIDs,
		"reference_context":   row.ReferenceContext,
		"reference_doc":       row.ReferenceDoc,
		"reference_doc_ids":   row.ReferenceDocIDs,
	}
	return values
}

func valuesFromJSONObjectBestEffort(obj map[string]any) map[string]string {
	values := make(map[string]string, len(importTemplateFields))
	for _, field := range importTemplateFields {
		value, ok := obj[field]
		if !ok || value == nil {
			values[field] = ""
			continue
		}
		switch v := value.(type) {
		case string:
			values[field] = strings.TrimSpace(v)
		case json.Number:
			values[field] = strings.TrimSpace(v.String())
		case bool:
			values[field] = strconv.FormatBool(v)
		default:
			values[field] = fmt.Sprint(v)
		}
	}
	return values
}

func valuesByHeader(header []string, values map[string]string) []string {
	out := make([]string, len(header))
	for i, name := range header {
		out[i] = values[name]
	}
	return out
}

func importJSONString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), nil
	case json.Number:
		return strings.TrimSpace(v.String()), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", errors.New("not a string")
	}
}

func importJSONBool(value any) (bool, error) {
	switch v := value.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		return parseImportBool(v)
	case json.Number:
		return parseImportBool(v.String())
	default:
		return false, errors.New("is_deleted 必须是布尔值")
	}
}

func parseImportBool(raw string) (bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false, nil
	}
	switch strings.ToLower(value) {
	case "true", "t", "1", "yes", "y":
		return true, nil
	case "false", "f", "0", "no", "n":
		return false, nil
	default:
		return false, errors.New("is_deleted 必须是布尔值")
	}
}

func normalizedImportRowEmpty(row ImportNormalizedRow) bool {
	return !row.IsDeleted &&
		row.CaseID == "" &&
		row.GenerateReason == "" &&
		row.GroundTruth == "" &&
		row.KeyPoints == "" &&
		row.Question == "" &&
		row.QuestionType == "" &&
		row.ReferenceChunkIDs == "" &&
		row.ReferenceContext == "" &&
		row.ReferenceDoc == "" &&
		row.ReferenceDocIDs == ""
}

func requiredImportErrors(rowNumber int, row ImportNormalizedRow) []ImportValidationErrorDetail {
	out := make([]ImportValidationErrorDetail, 0, len(importRequiredFields))
	if strings.TrimSpace(row.Question) == "" {
		out = append(out, ImportValidationErrorDetail{Row: rowNumber, Column: "question", Reason: "question 不能为空"})
	}
	if strings.TrimSpace(row.GroundTruth) == "" {
		out = append(out, ImportValidationErrorDetail{Row: rowNumber, Column: "ground_truth", Reason: "ground_truth 不能为空"})
	}
	if strings.TrimSpace(row.QuestionType) == "" {
		out = append(out, ImportValidationErrorDetail{Row: rowNumber, Column: "question_type", Reason: "question_type 不能为空"})
	}
	return out
}

type importFieldError struct {
	column string
	reason string
}

func (e *importFieldError) Error() string {
	return e.reason
}

type importErrorCollector struct {
	errors      []ImportValidationErrorDetail
	truncated   bool
	rowsWithErr map[int]struct{}
}

func newImportErrorCollector() importErrorCollector {
	return importErrorCollector{rowsWithErr: map[int]struct{}{}}
}

func (r *importParseResult) addInvalidRow(rowNumber int, values map[string]string, fields []string, details []ImportValidationErrorDetail) {
	if len(details) == 0 {
		return
	}
	rowDetails := make([]ImportValidationErrorDetail, len(details))
	copy(rowDetails, details)
	r.invalidRows = append(r.invalidRows, importInvalidRow{
		rowNumber: rowNumber,
		values:    cloneStringMap(values),
		fields:    append([]string(nil), fields...),
		errors:    rowDetails,
	})
	for _, detail := range rowDetails {
		if len(r.errorDetails) >= importMaxErrors {
			r.errorsTruncated = true
			continue
		}
		r.errorDetails = append(r.errorDetails, detail)
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *importErrorCollector) add(row int, column, reason string) {
	if c.rowsWithErr == nil {
		c.rowsWithErr = map[int]struct{}{}
	}
	c.rowsWithErr[row] = struct{}{}
	if len(c.errors) >= importMaxErrors {
		c.truncated = true
		return
	}
	c.errors = append(c.errors, ImportValidationErrorDetail{
		Row:    row,
		Column: column,
		Reason: reason,
	})
}

func (c *importErrorCollector) hasErrors() bool {
	return len(c.errors) > 0
}

func (c *importErrorCollector) hasRowError(row int) bool {
	_, ok := c.rowsWithErr[row]
	return ok
}

func (c *importErrorCollector) validationError() error {
	return newImportValidationError(c.errors, c.truncated)
}

func newImportValidationError(details []ImportValidationErrorDetail, truncated bool) error {
	return &importValidationError{
		response: ImportValidationErrorResponse{
			Errors:    details,
			Truncated: truncated,
		},
	}
}
