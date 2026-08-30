package historyinjection

import (
	"fmt"
	"strings"
)

const (
	ownerIDToken          = "{{OWNER_USER_ID}}"
	ownerNameToken        = "{{OWNER_USER_NAME}}"
	workflowResourceToken = "{{WORKFLOW_RESOURCE_ID}}"
	workflowRevisionToken = "{{WORKFLOW_REVISION_NO}}"
)

// portableBooleanColumns are exported from both PostgreSQL and SQLite source
// databases. SQLite drivers commonly scan BOOLEAN/NUMERIC values as 0/1, but
// PostgreSQL deliberately rejects integer expressions for boolean columns.
// Keeping this list next to the SQL portability helpers makes newly exported
// bundles use TRUE/FALSE and lets the importer repair older portable bundles.
var portableBooleanColumns = map[string]map[string]bool{
	"conversations": {
		"enable_plugin":   true,
		"enable_subagent": true,
		"is_task_conv":    true,
		"is_ephemeral":    true,
	},
	"plugin_sessions":       {"dismissed": true}, // workflow-naming: persistence
	"plugin_slot_revisions": {"selected": true},  // workflow-naming: persistence
	"sub_agent_artifacts":   {"hidden": true},
	"task_center_tasks":     {"has_late_inputs": true},
}

func renderSQL(source string, values map[string]string) string {
	for token, value := range values {
		source = strings.ReplaceAll(source, token, strings.ReplaceAll(value, "'", "''"))
	}
	return source
}

// splitSQLStatements is deliberately small but quote-aware. Exported SQL uses
// ordinary single-quoted SQL literals, including multiline JSON/text values.
// Semicolons inside those literals must never terminate a statement.
func splitSQLStatements(source string) ([]string, error) {
	var statements []string
	var current strings.Builder
	inQuote := false
	lineComment := false
	for i := 0; i < len(source); i++ {
		char := source[i]
		if lineComment {
			if char == '\n' {
				lineComment = false
				current.WriteByte(char)
			}
			continue
		}
		if !inQuote && char == '-' && i+1 < len(source) && source[i+1] == '-' {
			lineComment = true
			i++
			continue
		}
		if char == '\'' {
			current.WriteByte(char)
			if inQuote && i+1 < len(source) && source[i+1] == '\'' {
				current.WriteByte(source[i+1])
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if char == ';' && !inQuote {
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
			continue
		}
		current.WriteByte(char)
	}
	if inQuote {
		return nil, fmt.Errorf("history injection SQL contains an unterminated string literal")
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}
	return statements, nil
}

func rewritePostgresBooleanLiterals(statement string, booleanColumns map[string]map[string]bool) string {
	table, columnsOpen, ok := insertStatementTable(statement)
	if !ok || len(booleanColumns[table]) == 0 {
		return statement
	}
	columnsClose, ok := matchingSQLParen(statement, columnsOpen)
	if !ok {
		return statement
	}
	afterColumns := statement[columnsClose+1:]
	valuesOffset := strings.Index(strings.ToUpper(afterColumns), "VALUES")
	if valuesOffset < 0 {
		return statement
	}
	valuesOpen := columnsClose + 1 + valuesOffset + len("VALUES")
	for valuesOpen < len(statement) && (statement[valuesOpen] == ' ' || statement[valuesOpen] == '\t' ||
		statement[valuesOpen] == '\r' || statement[valuesOpen] == '\n') {
		valuesOpen++
	}
	if valuesOpen >= len(statement) || statement[valuesOpen] != '(' {
		return statement
	}
	valuesClose, ok := matchingSQLParen(statement, valuesOpen)
	if !ok {
		return statement
	}
	columns := splitSQLList(statement[columnsOpen+1 : columnsClose])
	values := splitSQLList(statement[valuesOpen+1 : valuesClose])
	if len(columns) != len(values) {
		return statement
	}
	changed := false
	for index, column := range columns {
		column = strings.Trim(strings.TrimSpace(column), `"`)
		if !booleanColumns[table][column] {
			continue
		}
		switch strings.TrimSpace(values[index]) {
		case "0":
			values[index] = "FALSE"
			changed = true
		case "1":
			values[index] = "TRUE"
			changed = true
		}
	}
	if !changed {
		return statement
	}
	return statement[:valuesOpen+1] + strings.Join(values, ", ") + statement[valuesClose:]
}

func insertStatementTable(statement string) (string, int, bool) {
	trimmed := strings.TrimLeft(statement, " \t\r\n")
	const prefix = "INSERT INTO "
	if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
		return "", 0, false
	}
	leading := len(statement) - len(trimmed)
	tail := trimmed[len(prefix):]
	open := strings.IndexByte(tail, '(')
	if open < 0 {
		return "", 0, false
	}
	table := strings.Trim(strings.TrimSpace(tail[:open]), `"`)
	if !safeSQLIdentifier(table) {
		return "", 0, false
	}
	return table, leading + len(prefix) + open, true
}

func matchingSQLParen(source string, open int) (int, bool) {
	depth := 0
	inQuote := false
	for index := open; index < len(source); index++ {
		character := source[index]
		if character == '\'' {
			if inQuote && index+1 < len(source) && source[index+1] == '\'' {
				index++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

func splitSQLList(source string) []string {
	values := make([]string, 0, 8)
	start := 0
	depth := 0
	inQuote := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		if character == '\'' {
			if inQuote && index+1 < len(source) && source[index+1] == '\'' {
				index++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				values = append(values, strings.TrimSpace(source[start:index]))
				start = index + 1
			}
		}
	}
	return append(values, strings.TrimSpace(source[start:]))
}
