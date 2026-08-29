package historyinjection

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	canonicalUploadRoot   = "/var/lib/lazymind/uploads"
	canonicalSubagentRoot = "/data/subagent"
)

type ExportOptions struct {
	BundleID       string
	Category       string
	Title          string
	ConversationID string
	WorkflowRef    string
	OutputDir      string
	UploadRoot     string
	SubagentRoot   string
}

type exportTable struct {
	Name    string
	Where   string
	Args    []any
	OrderBy string
	Omit    map[string]bool
}

func Export(ctx context.Context, db *gorm.DB, options ExportOptions) (Manifest, error) {
	for name, value := range map[string]string{
		"bundle ID": options.BundleID, "category": options.Category, "conversation ID": options.ConversationID,
		"workflow ref": options.WorkflowRef, "output directory": options.OutputDir,
		"upload root": options.UploadRoot, "subagent root": options.SubagentRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return Manifest{}, fmt.Errorf("history injection export %s is required", name)
		}
	}
	if _, err := os.Stat(options.OutputDir); err == nil {
		return Manifest{}, fmt.Errorf("history injection output already exists: %s", options.OutputDir)
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	parent := filepath.Dir(filepath.Clean(options.OutputDir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Manifest{}, err
	}
	staging, err := os.MkdirTemp(parent, ".history-injection-export-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(staging)

	var conversation struct {
		DisplayName    string `gorm:"column:display_name"`
		CreateUserID   string `gorm:"column:create_user_id"`
		CreateUserName string `gorm:"column:create_user_name"`
	}
	if err := db.WithContext(ctx).Raw(
		"SELECT display_name, create_user_id, create_user_name FROM conversations WHERE id = ?",
		options.ConversationID,
	).Scan(&conversation).Error; err != nil {
		return Manifest{}, err
	}
	if conversation.CreateUserID == "" {
		return Manifest{}, fmt.Errorf("conversation %s was not found", options.ConversationID)
	}
	var sessions []string
	if err := db.WithContext(ctx).Raw(
		"SELECT id FROM plugin_sessions WHERE conversation_id = ? AND plugin_ref = ? ORDER BY created_at",
		options.ConversationID, options.WorkflowRef,
	).Scan(&sessions).Error; err != nil {
		return Manifest{}, err
	}
	if len(sessions) == 0 {
		return Manifest{}, fmt.Errorf("conversation %s has no %s workflow session", options.ConversationID, options.WorkflowRef)
	}
	var revisionID string
	if err := db.WithContext(ctx).Raw(
		"SELECT plugin_revision_id FROM plugin_sessions WHERE id = ?", sessions[len(sessions)-1],
	).Scan(&revisionID).Error; err != nil {
		return Manifest{}, err
	}
	var revisionRow struct {
		ID                 string          `gorm:"column:id"`
		RevisionNo         int64           `gorm:"column:revision_no"`
		TreeHash           string          `gorm:"column:tree_hash"`
		Message            string          `gorm:"column:message"`
		CreatedBy          string          `gorm:"column:created_by"`
		CreatedAt          time.Time       `gorm:"column:created_at"`
		CompiledGraph      json.RawMessage `gorm:"column:compiled_graph"`
		GraphHash          string          `gorm:"column:graph_hash"`
		GraphSchemaVersion string          `gorm:"column:graph_schema_version"`
	}
	if err := db.WithContext(ctx).Raw(`SELECT id, revision_no, tree_hash, message, created_by, created_at,
        compiled_graph, graph_hash, graph_schema_version FROM plugin_revisions WHERE id = ?`, revisionID).Scan(&revisionRow).Error; err != nil {
		return Manifest{}, err
	}
	if revisionRow.ID == "" {
		return Manifest{}, fmt.Errorf("workflow revision %s was not found", revisionID)
	}

	var taskIDs []string
	if err := db.WithContext(ctx).Raw("SELECT task_id FROM plugin_session_steps WHERE session_id IN ? ORDER BY created_at", sessions).Scan(&taskIDs).Error; err != nil {
		return Manifest{}, err
	}
	tables := exportTables(options.ConversationID, sessions, taskIDs)
	sqlPath := filepath.Join(staging, "data.sql")
	sqlFile, err := os.OpenFile(sqlPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Manifest{}, err
	}
	_, _ = fmt.Fprintf(sqlFile, "-- LazyMind portable history injection SQL\n-- bundle: %s\n-- compatible with PostgreSQL 16+ and SQLite 3.24+\n\n", options.BundleID)
	for _, table := range tables {
		if err := writeTableSQL(ctx, db, sqlFile, table, conversation.CreateUserID, conversation.CreateUserName); err != nil {
			_ = sqlFile.Close()
			return Manifest{}, err
		}
	}
	if err := sqlFile.Close(); err != nil {
		return Manifest{}, err
	}
	if _, err := CompactPortableSQLFile(sqlPath, sqlPath); err != nil {
		return Manifest{}, fmt.Errorf("history injection failed: compact exported SQL: %w", err)
	}

	files, err := exportPayload(ctx, db, staging, options, sessions, taskIDs, conversation.CreateUserID)
	if err != nil {
		return Manifest{}, err
	}
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = conversation.DisplayName
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, BundleID: options.BundleID, Category: options.Category,
		Title: title, ConversationID: options.ConversationID, SessionIDs: sessions,
		SourceOwnerID: conversation.CreateUserID, WorkflowRef: options.WorkflowRef, SQLFile: "data.sql", Files: files,
		WorkflowRevision: WorkflowRevision{
			ID: revisionRow.ID, RevisionNo: revisionRow.RevisionNo, TreeHash: revisionRow.TreeHash,
			Message: revisionRow.Message, CreatedBy: revisionRow.CreatedBy,
			CreatedAt: revisionRow.CreatedAt.UTC().Format(time.RFC3339Nano), CompiledGraph: revisionRow.CompiledGraph,
			GraphHash: revisionRow.GraphHash, GraphSchemaVersion: revisionRow.GraphSchemaVersion,
		},
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), append(body, '\n'), 0o644); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(staging, options.OutputDir); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func exportTables(conversationID string, sessions, taskIDs []string) []exportTable {
	sessionFilter := "session_id IN ?"
	return []exportTable{
		{Name: "conversations", Where: "id = ?", Args: []any{conversationID}, OrderBy: "created_at, id"},
		{Name: "chat_histories", Where: "conversation_id = ?", Args: []any{conversationID}, OrderBy: "seq, id"},
		{Name: "task_center_tasks", Where: "conversation_id = ?", Args: []any{conversationID}, OrderBy: "created_at, id"},
		{Name: "sub_agent_tasks", Where: "id IN ?", Args: []any{taskIDs}, OrderBy: "created_at, id"},
		{Name: "sub_agent_steps", Where: "task_id IN ?", Args: []any{taskIDs}, OrderBy: "task_id, seq, id"},
		{Name: "sub_agent_artifacts", Where: "task_id IN ?", Args: []any{taskIDs}, OrderBy: "task_id, slot, seq, id"},
		// advance_mode existed in the PostgreSQL development schema used by the
		// source session, but is not part of the current Desktop SQLite schema.
		// It is legacy scheduler state and is not needed to render history.
		{Name: "plugin_sessions", Where: "id IN ?", Args: []any{sessions}, OrderBy: "created_at, id", Omit: map[string]bool{"advance_mode": true}}, // workflow-naming: persistence
		{Name: "plugin_session_steps", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},                                     // workflow-naming: persistence
		{Name: "plugin_human_artifacts", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},                                   // workflow-naming: persistence
		{Name: "plugin_slot_revisions", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},                                    // workflow-naming: persistence
		{Name: "plugin_attempt_input_bindings", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},                            // workflow-naming: persistence
		{Name: "plugin_route_decisions", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},                                   // workflow-naming: persistence
		{Name: "plugin_transition_commands", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, command_id"},                       // workflow-naming: persistence
		{Name: "plugin_slot_order", Where: sessionFilter, Args: []any{sessions}, OrderBy: "session_id, slot_id"},                                   // workflow-naming: persistence
		{Name: "plugin_step_intents", Where: sessionFilter, Args: []any{sessions}, OrderBy: "session_id, step_id"},                                 // workflow-naming: persistence
		{Name: "plugin_run_outbox", Where: "task_id IN ?", Args: []any{taskIDs}, OrderBy: "created_at, task_id"},                                   // workflow-naming: persistence
		{Name: "workflow_preparations", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},
		{Name: "workflow_commands", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, command_id"},
		{Name: "workflow_outbox", Where: sessionFilter, Args: []any{sessions}, OrderBy: "created_at, id"},
		{Name: "workflow_events", Where: sessionFilter, Args: []any{sessions}, OrderBy: "id", Omit: map[string]bool{"id": true}},
	}
}

func writeTableSQL(ctx context.Context, db *gorm.DB, output io.Writer, table exportTable, sourceOwner, sourceUsername string) error {
	query := db.WithContext(ctx).Table(table.Name).Where(table.Where, table.Args...)
	if table.OrderBy != "" {
		query = query.Order(table.OrderBy)
	}
	rows, err := query.Rows()
	if err != nil {
		return fmt.Errorf("query %s for history injection export: %w", table.Name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	selectedColumns := make([]string, 0, len(columns))
	selectedIndexes := make([]int, 0, len(columns))
	for index, column := range columns {
		if table.Omit[column] {
			continue
		}
		selectedColumns = append(selectedColumns, column)
		selectedIndexes = append(selectedIndexes, index)
	}
	wroteHeader := false
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		if !wroteHeader {
			_, _ = fmt.Fprintf(output, "-- table: %s\n", table.Name)
			wroteHeader = true
		}
		literals := make([]string, 0, len(selectedColumns))
		for position, index := range selectedIndexes {
			column := selectedColumns[position]
			if table.Name == "plugin_sessions" && column == "plugin_revision_no" {
				literals = append(literals, workflowRevisionToken)
				continue
			}
			literals = append(literals, exportSQLLiteral(values[index], table.Name, column, sourceOwner, sourceUsername))
		}
		_, _ = fmt.Fprintf(output, "INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING;\n",
			table.Name, strings.Join(selectedColumns, ", "), strings.Join(literals, ", "))
	}
	if wroteHeader {
		_, _ = io.WriteString(output, "\n")
	}
	return rows.Err()
}

func exportSQLLiteral(value any, table, column, sourceOwner, sourceUsername string) string {
	if value == nil {
		return "NULL"
	}
	if portableBooleanColumns[table][column] {
		if literal, ok := portableBooleanLiteral(value); ok {
			return literal
		}
	}
	var text string
	switch typed := value.(type) {
	case bool:
		if typed {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return strconv.Itoa(typed)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case time.Time:
		text = typed.UTC().Format(time.RFC3339Nano)
	case []byte:
		text = string(typed)
	case string:
		text = typed
	default:
		text = fmt.Sprint(typed)
	}
	ownerColumns := map[string]bool{"create_user_id": true, "owner_user_id": true, "user_id": true, "created_by": true}
	if ownerColumns[column] && text == sourceOwner {
		text = ownerIDToken
	}
	if column == "create_user_name" && text == sourceUsername {
		text = ownerNameToken
	}
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}

func portableBooleanLiteral(value any) (string, bool) {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "TRUE", true
		}
		return "FALSE", true
	case int:
		return integerBooleanLiteral(int64(typed))
	case int8:
		return integerBooleanLiteral(int64(typed))
	case int16:
		return integerBooleanLiteral(int64(typed))
	case int32:
		return integerBooleanLiteral(int64(typed))
	case int64:
		return integerBooleanLiteral(typed)
	case uint:
		return unsignedBooleanLiteral(uint64(typed))
	case uint64:
		return unsignedBooleanLiteral(typed)
	case []byte:
		return textBooleanLiteral(string(typed))
	case string:
		return textBooleanLiteral(typed)
	default:
		return "", false
	}
}

func integerBooleanLiteral(value int64) (string, bool) {
	switch value {
	case 0:
		return "FALSE", true
	case 1:
		return "TRUE", true
	default:
		return "", false
	}
}

func unsignedBooleanLiteral(value uint64) (string, bool) {
	if value > 1 {
		return "", false
	}
	return integerBooleanLiteral(int64(value))
}

func textBooleanLiteral(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "f", "no", "off":
		return "FALSE", true
	case "1", "true", "t", "yes", "on":
		return "TRUE", true
	default:
		return "", false
	}
}

func exportPayload(ctx context.Context, db *gorm.DB, staging string, options ExportOptions, sessions, taskIDs []string, sourceOwner string) ([]PayloadFile, error) {
	type artifactValue struct {
		Value json.RawMessage `gorm:"column:value"`
	}
	var values []artifactValue
	if err := db.WithContext(ctx).Table("plugin_human_artifacts").Select("value").Where("session_id IN ?", sessions).Scan(&values).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Table("sub_agent_artifacts").Select("value").Where("task_id IN ?", taskIDs).Scan(&values).Error; err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, value := range values {
		collectJSONPaths(value.Value, paths)
	}
	// Workflow workspaces are not laid out uniformly. PPT currently stores a
	// conversation below ppt_sessions, while other workflows may use a different
	// bucket or no conversation workspace at all. Discover matching directories
	// instead of inventing a PPT-only path for every workflow export.
	workspacePaths, err := discoverConversationWorkspaces(options, sourceOwner, options.ConversationID)
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspacePaths {
		paths[workspace] = true
	}
	var taskWorkspaces []string
	if err := db.WithContext(ctx).Raw("SELECT workspace_path FROM sub_agent_tasks WHERE id IN ?", taskIDs).Scan(&taskWorkspaces).Error; err != nil {
		return nil, err
	}
	for _, path := range taskWorkspaces {
		paths[filepath.ToSlash(strings.TrimSpace(path))] = true
	}

	type sourceFile struct {
		Actual       string
		TargetRoot   string
		RelativePath string
	}
	collected := map[string]sourceFile{}
	for path := range paths {
		actual, targetRoot, relative, ok := resolveExportPath(path, options)
		if !ok {
			continue
		}
		info, err := os.Stat(actual)
		if err != nil {
			return nil, fmt.Errorf("history injection source payload %s: %w", actual, err)
		}
		if info.IsDir() {
			err = filepath.WalkDir(actual, func(child string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("history injection payload may not contain symlink %s", child)
				}
				tail, err := filepath.Rel(actual, child)
				if err != nil {
					return err
				}
				rel := filepath.ToSlash(filepath.Join(relative, tail))
				collected[targetRoot+"/"+rel] = sourceFile{Actual: child, TargetRoot: targetRoot, RelativePath: rel}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		collected[targetRoot+"/"+relative] = sourceFile{Actual: actual, TargetRoot: targetRoot, RelativePath: relative}
	}
	keys := make([]string, 0, len(collected))
	for key := range collected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]PayloadFile, 0, len(keys))
	for _, key := range keys {
		file := collected[key]
		bundleRelative := filepath.ToSlash(filepath.Join("payload", file.TargetRoot, file.RelativePath))
		target := filepath.Join(staging, filepath.FromSlash(bundleRelative))
		info, err := os.Stat(file.Actual)
		if err != nil {
			return nil, err
		}
		if err := copyFile(file.Actual, target, info.Mode().Perm()); err != nil {
			return nil, err
		}
		digest, size, err := fileDigest(target)
		if err != nil {
			return nil, err
		}
		result = append(result, PayloadFile{Source: bundleRelative, TargetRoot: file.TargetRoot,
			RelativePath: file.RelativePath, SHA256: digest, Size: size, Mode: uint32(info.Mode().Perm())})
	}
	return result, nil
}

func discoverConversationWorkspaces(options ExportOptions, sourceOwner, conversationID string) ([]string, error) {
	workflowID := strings.TrimPrefix(options.WorkflowRef, "builtin:")
	actualRoot := filepath.Join(options.UploadRoot, "workflow-workspaces", workflowID, sourceOwner)
	if _, err := os.Stat(actualRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []string
	err := filepath.WalkDir(actualRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("history injection workspace may not contain symlink %s", path)
		}
		if !entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(actualRoot, path)
		if err != nil {
			return err
		}
		if relative != "." && len(strings.Split(filepath.ToSlash(relative), "/")) > 4 {
			return filepath.SkipDir
		}
		if entry.Name() != conversationID {
			return nil
		}
		canonical := filepath.ToSlash(filepath.Join(canonicalUploadRoot, "workflow-workspaces",
			workflowID, sourceOwner, relative))
		result = append(result, canonical)
		return filepath.SkipDir
	})
	sort.Strings(result)
	return result, err
}

func collectJSONPaths(raw json.RawMessage, paths map[string]bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "path" {
					if path, ok := child.(string); ok {
						path = filepath.ToSlash(strings.TrimSpace(path))
						if strings.HasPrefix(path, canonicalUploadRoot+"/") || strings.HasPrefix(path, canonicalSubagentRoot+"/") {
							paths[path] = true
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

func resolveExportPath(path string, options ExportOptions) (actual, targetRoot, relative string, ok bool) {
	path = strings.TrimRight(filepath.ToSlash(strings.TrimSpace(path)), "/")
	for _, candidate := range []struct {
		canonical string
		actual    string
		name      string
	}{
		{canonicalUploadRoot, options.UploadRoot, "uploads"},
		{canonicalSubagentRoot, options.SubagentRoot, "subagent"},
	} {
		prefix := candidate.canonical + "/"
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		relative = strings.TrimPrefix(path, prefix)
		if !safeRelativePath(relative) {
			return "", "", "", false
		}
		return filepath.Join(candidate.actual, filepath.FromSlash(relative)), candidate.name, relative, true
	}
	return "", "", "", false
}

func copyFile(source, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func Pack(sourceDir, outputPath string) error {
	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("history injection ZIP already exists: %s", outputPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	var files []string
	if err := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("history injection ZIP source contains symlink %s", path)
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(files)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	for _, path := range files {
		relative, err := filepath.Rel(sourceDir, path)
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		if filepath.ToSlash(relative) == "data.sql" {
			body, err := os.ReadFile(path)
			if err != nil {
				_ = archive.Close()
				_ = output.Close()
				return err
			}
			compacted, _, err := CompactPortableSQL(string(body))
			if err != nil {
				_ = archive.Close()
				_ = output.Close()
				return err
			}
			if _, err := io.WriteString(writer, compacted); err != nil {
				_ = archive.Close()
				_ = output.Close()
				return err
			}
			continue
		}
		input, err := os.Open(path)
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		_, copyErr := io.Copy(writer, input)
		_ = input.Close()
		if copyErr != nil {
			_ = archive.Close()
			_ = output.Close()
			return copyErr
		}
	}
	if err := archive.Close(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
