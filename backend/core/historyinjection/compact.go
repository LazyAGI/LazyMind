package historyinjection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CompactSQLStats describes how many persisted SubAgent stream fragments were
// normalized to the buffered persistence format used by current SubAgents.
type CompactSQLStats struct {
	InputSteps  int
	OutputSteps int
}

// MergedSteps is the number of redundant stream-fragment rows removed.
func (stats CompactSQLStats) MergedSteps() int {
	return stats.InputSteps - stats.OutputSteps
}

type portableInsert struct {
	table   string
	columns []string
	values  []string
	suffix  string
}

type compactStep struct {
	insert       portableInsert
	taskID       string
	role         string
	content      string
	contentIndex int
	createdIndex int
}

// CompactPortableSQL merges the fine-grained think/text events produced by the
// remote Workflow stream into the same per-ReAct-round rows written by the
// buffered in-process SubAgent path. Structured assistant/tool rows retain
// their order and payload. Sequence numbers are rebuilt per task.
func CompactPortableSQL(source string) (string, CompactSQLStats, error) {
	statements, err := splitSQLStatements(source)
	if err != nil {
		return "", CompactSQLStats{}, err
	}
	stats := CompactSQLStats{}
	var output strings.Builder
	writePortableSQLHeader(&output, source)
	lastTable := ""
	nextSequence := make(map[string]int)
	var pendingTask string
	var pendingThink *compactStep
	var pendingText *compactStep

	writeInsert := func(insert portableInsert) {
		if insert.table != lastTable {
			if lastTable != "" {
				output.WriteByte('\n')
			}
			_, _ = fmt.Fprintf(&output, "-- table: %s\n", insert.table)
			lastTable = insert.table
		}
		_, _ = fmt.Fprintf(&output, "INSERT INTO %s (%s) VALUES (%s)", insert.table,
			strings.Join(insert.columns, ", "), strings.Join(insert.values, ", "))
		if insert.suffix != "" {
			output.WriteByte(' ')
			output.WriteString(insert.suffix)
		}
		output.WriteString(";\n")
	}

	emitStep := func(step *compactStep) {
		if step == nil || ((step.role == "think" || step.role == "text") && step.content == "") {
			return
		}
		insert := step.insert
		if step.role == "think" || step.role == "text" {
			body, _ := json.Marshal(map[string]string{"content": step.content})
			insert.values[step.contentIndex] = quotePortableSQLText(string(body))
		}
		sequenceIndex := columnIndex(insert.columns, "seq")
		insert.values[sequenceIndex] = strconv.Itoa(nextSequence[step.taskID])
		nextSequence[step.taskID]++
		writeInsert(insert)
		stats.OutputSteps++
	}

	flushStream := func() {
		// The current runner flushes reasoning before visible text at every tool
		// boundary, even though both streams are accumulated independently.
		emitStep(pendingThink)
		emitStep(pendingText)
		pendingThink, pendingText = nil, nil
	}

	for _, statement := range statements {
		insert, ok := parsePortableInsert(statement)
		if !ok {
			flushStream()
			pendingTask = ""
			if trimmed := strings.TrimSpace(statement); trimmed != "" {
				if lastTable != "" {
					output.WriteByte('\n')
					lastTable = ""
				}
				output.WriteString(trimmed)
				output.WriteString(";\n")
			}
			continue
		}
		if insert.table != "sub_agent_steps" {
			flushStream()
			pendingTask = ""
			writeInsert(insert)
			continue
		}

		step, err := parseCompactStep(insert)
		if err != nil {
			return "", stats, err
		}
		stats.InputSteps++
		if pendingTask != "" && pendingTask != step.taskID {
			flushStream()
		}
		pendingTask = step.taskID
		if step.role != "think" && step.role != "text" {
			flushStream()
			emitStep(step)
			continue
		}
		pending := &pendingText
		if step.role == "think" {
			pending = &pendingThink
		}
		if *pending == nil {
			*pending = step
			continue
		}
		(*pending).content += step.content
		if (*pending).createdIndex >= 0 && step.createdIndex >= 0 {
			(*pending).insert.values[(*pending).createdIndex] = step.insert.values[step.createdIndex]
		}
	}
	flushStream()
	if lastTable != "" {
		output.WriteByte('\n')
	}
	return output.String(), stats, nil
}

// CompactPortableSQLFile writes a compacted portable SQL file atomically. The
// source and destination may be the same path.
func CompactPortableSQLFile(sourcePath, outputPath string) (CompactSQLStats, error) {
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		return CompactSQLStats{}, err
	}
	compacted, stats, err := CompactPortableSQL(string(body))
	if err != nil {
		return stats, err
	}
	if strings.TrimSpace(outputPath) == "" {
		return stats, fmt.Errorf("history injection failed: compact output is required")
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(outputPath)), 0o755); err != nil {
		return stats, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filepath.Clean(outputPath)), ".history-injection-sql-*")
	if err != nil {
		return stats, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(compacted); err != nil {
		_ = temporary.Close()
		return stats, err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return stats, err
	}
	if err := temporary.Close(); err != nil {
		return stats, err
	}
	if err := os.Rename(temporaryPath, outputPath); err == nil {
		return stats, nil
	}
	// Windows cannot rename over an existing file. The compact command and
	// exporter both support source == output, so fall back to the same bounded
	// replacement used by payload installation.
	if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
		return stats, err
	}
	return stats, os.Rename(temporaryPath, outputPath)
}

func parsePortableInsert(statement string) (portableInsert, bool) {
	table, columnsOpen, ok := insertStatementTable(statement)
	if !ok {
		return portableInsert{}, false
	}
	columnsClose, ok := matchingSQLParen(statement, columnsOpen)
	if !ok {
		return portableInsert{}, false
	}
	afterColumns := statement[columnsClose+1:]
	valuesOffset := strings.Index(strings.ToUpper(afterColumns), "VALUES")
	if valuesOffset < 0 {
		return portableInsert{}, false
	}
	valuesOpen := columnsClose + 1 + valuesOffset + len("VALUES")
	for valuesOpen < len(statement) && strings.ContainsRune(" \t\r\n", rune(statement[valuesOpen])) {
		valuesOpen++
	}
	if valuesOpen >= len(statement) || statement[valuesOpen] != '(' {
		return portableInsert{}, false
	}
	valuesClose, ok := matchingSQLParen(statement, valuesOpen)
	if !ok {
		return portableInsert{}, false
	}
	columns := splitSQLList(statement[columnsOpen+1 : columnsClose])
	values := splitSQLList(statement[valuesOpen+1 : valuesClose])
	if len(columns) != len(values) {
		return portableInsert{}, false
	}
	return portableInsert{table: table, columns: columns, values: values,
		suffix: strings.TrimSpace(statement[valuesClose+1:])}, true
}

func parseCompactStep(insert portableInsert) (*compactStep, error) {
	taskIndex := columnIndex(insert.columns, "task_id")
	roleIndex := columnIndex(insert.columns, "role")
	contentIndex := columnIndex(insert.columns, "content")
	sequenceIndex := columnIndex(insert.columns, "seq")
	if taskIndex < 0 || roleIndex < 0 || contentIndex < 0 || sequenceIndex < 0 {
		return nil, fmt.Errorf("history injection failed: sub_agent_steps INSERT is missing required columns")
	}
	taskID, ok := decodePortableSQLText(insert.values[taskIndex])
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("history injection failed: sub_agent_steps has invalid task_id")
	}
	role, ok := decodePortableSQLText(insert.values[roleIndex])
	if !ok || strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("history injection failed: sub_agent_steps has invalid role")
	}
	step := &compactStep{insert: insert, taskID: taskID, role: role, contentIndex: contentIndex,
		createdIndex: columnIndex(insert.columns, "created_at")}
	if role != "think" && role != "text" {
		return step, nil
	}
	encoded, ok := decodePortableSQLText(insert.values[contentIndex])
	if !ok {
		return nil, fmt.Errorf("history injection failed: sub_agent_steps has invalid %s content literal", role)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		return nil, fmt.Errorf("history injection failed: decode %s stream content: %w", role, err)
	}
	if len(payload) != 1 || payload["content"] == nil {
		return nil, fmt.Errorf("history injection failed: %s stream content has unsupported fields", role)
	}
	if err := json.Unmarshal(payload["content"], &step.content); err != nil {
		return nil, fmt.Errorf("history injection failed: decode %s stream text: %w", role, err)
	}
	return step, nil
}

func columnIndex(columns []string, name string) int {
	for index, column := range columns {
		if strings.Trim(strings.TrimSpace(column), `"`) == name {
			return index
		}
	}
	return -1
}

func decodePortableSQLText(literal string) (string, bool) {
	literal = strings.TrimSpace(literal)
	if len(literal) < 2 || literal[0] != '\'' || literal[len(literal)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(literal[1:len(literal)-1], "''", "'"), true
}

func quotePortableSQLText(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func writePortableSQLHeader(output *strings.Builder, source string) {
	wrote := false
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "INSERT INTO ") {
			break
		}
		if !strings.HasPrefix(line, "--") || strings.HasPrefix(line, "-- table:") ||
			strings.Contains(line, "buffered SubAgent persistence") {
			continue
		}
		output.WriteString(line)
		output.WriteByte('\n')
		wrote = true
	}
	if !wrote {
		output.WriteString("-- LazyMind portable history injection SQL\n")
	}
	output.WriteString("-- sub_agent_steps normalized to buffered SubAgent persistence\n\n")
}
