package historyinjection

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
)

type TargetOwner struct {
	ID       string
	Username string
}

type RuntimeRoots struct {
	Uploads  string
	Subagent string
}

type ApplyResult struct {
	BundleID       string
	ConversationID string
	FilesCopied    int
	AlreadyPresent bool
}

func ApplyAll(ctx context.Context, db *gorm.DB, root string, owner TargetOwner, roots RuntimeRoots) ([]ApplyResult, error) {
	if db == nil || strings.TrimSpace(owner.ID) == "" {
		return nil, fmt.Errorf("history injection requires a database and target owner")
	}
	sources, err := Discover(root)
	if err != nil {
		return nil, err
	}
	results := make([]ApplyResult, 0, len(sources))
	for _, source := range sources {
		result, err := Apply(ctx, db, source, owner, roots)
		if err != nil {
			return results, fmt.Errorf("apply history injection bundle %s: %w", source.Manifest.BundleID, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func Apply(ctx context.Context, db *gorm.DB, source BundleSource, owner TargetOwner, roots RuntimeRoots) (ApplyResult, error) {
	bundleRoot, cleanup, err := source.materialize()
	if err != nil {
		return ApplyResult{}, err
	}
	defer cleanup()
	manifest, err := readManifest(filepath.Join(bundleRoot, "manifest.json"))
	if err != nil {
		return ApplyResult{}, err
	}
	if manifest.BundleID != source.Manifest.BundleID {
		return ApplyResult{}, fmt.Errorf("materialized bundle ID changed from %q to %q", source.Manifest.BundleID, manifest.BundleID)
	}
	result := ApplyResult{BundleID: manifest.BundleID, ConversationID: manifest.ConversationID}
	var existingOwner string
	err = db.WithContext(ctx).Raw("SELECT create_user_id FROM conversations WHERE id = ?", manifest.ConversationID).Scan(&existingOwner).Error
	if err != nil {
		return result, err
	}
	if existingOwner != "" && existingOwner != owner.ID {
		return result, fmt.Errorf("conversation %s already belongs to another user", manifest.ConversationID)
	}
	if existingOwner == owner.ID {
		result.AlreadyPresent = true
	}
	filesCopied, err := installPayload(bundleRoot, manifest, roots, result.AlreadyPresent)
	if err != nil {
		return result, err
	}
	result.FilesCopied = filesCopied
	sqlBody, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(manifest.SQLFile)))
	if err != nil {
		return result, err
	}
	if !result.AlreadyPresent {
		compacted, _, err := CompactPortableSQL(string(sqlBody))
		if err != nil {
			return result, fmt.Errorf("history injection failed: compact imported SQL: %w", err)
		}
		sqlBody = []byte(compacted)
	}
	rendered := renderSQL(string(sqlBody), map[string]string{
		ownerIDToken: owner.ID, ownerNameToken: owner.Username,
		workflowResourceToken: "", workflowRevisionToken: "0",
	})
	statements, err := splitSQLStatements(rendered)
	if err != nil {
		return result, err
	}
	if result.AlreadyPresent {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			_, _, err := ensureWorkflowRevision(ctx, tx, manifest, owner)
			if err != nil {
				return err
			}
			if err := normalizeInjectedConversation(ctx, tx, manifest, owner); err != nil {
				return err
			}
			return normalizeSQLiteJSONColumns(ctx, tx, statements)
		})
		return result, err
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resourceID, revisionNo, err := ensureWorkflowRevision(ctx, tx, manifest, owner)
		if err != nil {
			return err
		}
		rendered = renderSQL(string(sqlBody), map[string]string{
			ownerIDToken: owner.ID, ownerNameToken: owner.Username,
			workflowResourceToken: resourceID, workflowRevisionToken: fmt.Sprintf("%d", revisionNo),
		})
		statements, err = splitSQLStatements(rendered)
		if err != nil {
			return err
		}
		if tx.Dialector.Name() == "postgres" {
			booleanColumns, err := postgresBooleanColumns(ctx, tx)
			if err != nil {
				return err
			}
			for index, statement := range statements {
				statements[index] = rewritePostgresBooleanLiterals(statement, booleanColumns)
			}
		}
		for index, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("execute SQL statement %d: %w", index+1, err)
			}
		}
		if err := normalizeInjectedConversation(ctx, tx, manifest, owner); err != nil {
			return err
		}
		return normalizeSQLiteJSONColumns(ctx, tx, statements)
	})
	return result, err
}

func normalizeInjectedConversation(ctx context.Context, db *gorm.DB, manifest Manifest, owner TargetOwner) error {
	var extValue any
	row := db.WithContext(ctx).Raw(
		"SELECT ext FROM conversations WHERE id = ? AND create_user_id = ?",
		manifest.ConversationID, owner.ID,
	).Row()
	if err := row.Scan(&extValue); err != nil {
		return fmt.Errorf("read injected conversation %s metadata: %w", manifest.ConversationID, err)
	}
	ext, err := decodeConversationExt(extValue)
	if err != nil {
		return fmt.Errorf("decode injected conversation %s metadata: %w", manifest.ConversationID, err)
	}
	updates := map[string]any{
		"display_name": manifest.Title,
		"is_task_conv": false,
	}
	if !hasInjectionTimestamp(ext, manifest.BundleID) {
		injectedAt := time.Now().UTC()
		ext[historyInjectionMetadataKey] = map[string]any{
			"bundle_id":   manifest.BundleID,
			"injected_at": injectedAt.Format(time.RFC3339Nano),
		}
		encoded, err := json.Marshal(ext)
		if err != nil {
			return fmt.Errorf("encode injected conversation %s metadata: %w", manifest.ConversationID, err)
		}
		extStorage := any(string(encoded))
		if db.Dialector.Name() == "sqlite" {
			extStorage = encoded
		}
		updates["ext"] = extStorage
		updates["created_at"] = injectedAt
		updates["updated_at"] = injectedAt
	}
	result := db.WithContext(ctx).Table("conversations").
		Where("id = ? AND create_user_id = ?", manifest.ConversationID, owner.ID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("normalize injected conversation %s: %w", manifest.ConversationID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("normalize injected conversation %s: expected one owned row, updated %d",
			manifest.ConversationID, result.RowsAffected)
	}
	return nil
}

const historyInjectionMetadataKey = "_lazymind_history_injection"

func decodeConversationExt(value any) (map[string]any, error) {
	var body []byte
	switch typed := value.(type) {
	case nil:
		return map[string]any{}, nil
	case []byte:
		body = typed
	case string:
		body = []byte(typed)
	default:
		body = []byte(fmt.Sprint(typed))
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 || string(body) == "null" {
		return map[string]any{}, nil
	}
	var ext map[string]any
	if err := json.Unmarshal(body, &ext); err != nil {
		return nil, err
	}
	if ext == nil {
		ext = map[string]any{}
	}
	return ext, nil
}

func hasInjectionTimestamp(ext map[string]any, bundleID string) bool {
	metadata, ok := ext[historyInjectionMetadataKey].(map[string]any)
	if !ok || strings.TrimSpace(fmt.Sprint(metadata["bundle_id"])) != bundleID {
		return false
	}
	injectedAt := strings.TrimSpace(fmt.Sprint(metadata["injected_at"]))
	if injectedAt == "" || injectedAt == "<nil>" {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, injectedAt)
	return err == nil
}

func postgresBooleanColumns(ctx context.Context, db *gorm.DB) (map[string]map[string]bool, error) {
	type booleanColumn struct {
		TableName  string `gorm:"column:table_name"`
		ColumnName string `gorm:"column:column_name"`
	}
	var rows []booleanColumn
	err := db.WithContext(ctx).Raw(`
        SELECT table_name, column_name
        FROM information_schema.columns
        WHERE table_schema = current_schema() AND data_type = 'boolean'`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL boolean columns: %w", err)
	}
	columns := make(map[string]map[string]bool)
	for _, row := range rows {
		if !safeSQLIdentifier(row.TableName) || !safeSQLIdentifier(row.ColumnName) {
			continue
		}
		if columns[row.TableName] == nil {
			columns[row.TableName] = make(map[string]bool)
		}
		columns[row.TableName][row.ColumnName] = true
	}
	return columns, nil
}

func normalizeSQLiteJSONColumns(ctx context.Context, db *gorm.DB, statements []string) error {
	if db.Dialector.Name() != "sqlite" {
		return nil
	}
	// SQLite migrations occasionally have to declare JSON payloads as TEXT even
	// though the corresponding Go model uses json.RawMessage. modernc/sqlite
	// returns TEXT values as string, which database/sql cannot scan directly into
	// json.RawMessage. Keep these compatibility columns in BLOB storage so they
	// have the same []byte read behaviour as PostgreSQL JSON/JSONB columns.
	//
	// Most JSON columns are discovered from their declared JSON/JSONB type below.
	// This list only covers the deliberately TEXT-backed exceptions.
	textBackedRawJSONColumns := map[string]map[string]bool{
		"chat_histories":               {"run_terminal": true},
		"multi_answers_chat_histories": {"run_terminal": true},
		"workflow_preparations":        {"request_json": true, "response_json": true},
		"workflow_events":              {"payload_json": true},
		"workflow_commands":            {"response_json": true},
		"workflow_outbox":              {"payload_json": true},
	}
	tables := map[string]bool{}
	for _, statement := range statements {
		upper := strings.ToUpper(statement)
		index := strings.Index(upper, "INSERT INTO ")
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace(statement[index+len("INSERT INTO "):])
		end := strings.IndexAny(tail, " (\t\r\n")
		if end >= 0 {
			tail = tail[:end]
		}
		table := strings.Trim(tail, `"`)
		if !safeSQLIdentifier(table) {
			return fmt.Errorf("history injection SQL contains unsafe table %q", table)
		}
		tables[table] = true
	}
	for table := range tables {
		var columns []struct {
			Name string `gorm:"column:name"`
			Type string `gorm:"column:type"`
		}
		if err := db.WithContext(ctx).Raw(fmt.Sprintf(`PRAGMA table_info("%s")`, table)).Scan(&columns).Error; err != nil {
			return err
		}
		for _, column := range columns {
			declaredJSON := strings.Contains(strings.ToUpper(column.Type), "JSON")
			textBackedRawJSON := textBackedRawJSONColumns[table][column.Name]
			if (!declaredJSON && !textBackedRawJSON) || !safeSQLIdentifier(column.Name) {
				continue
			}
			statement := fmt.Sprintf(`UPDATE "%s" SET "%s" = CAST("%s" AS BLOB) WHERE "%s" IS NOT NULL AND typeof("%s") = 'text'`,
				table, column.Name, column.Name, column.Name, column.Name)
			if err := db.WithContext(ctx).Exec(statement).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func safeSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func ensureWorkflowRevision(ctx context.Context, db *gorm.DB, manifest Manifest, owner TargetOwner) (string, int64, error) {
	compiledGraph := strings.TrimSpace(string(manifest.WorkflowRevision.CompiledGraph))
	if compiledGraph == "" || !json.Valid([]byte(compiledGraph)) {
		return "", 0, fmt.Errorf("workflow revision %s has invalid compiled_graph", manifest.WorkflowRevision.ID)
	}
	var resourceID string
	if err := db.WithContext(ctx).Raw("SELECT id FROM plugins WHERE plugin_ref = ?", manifest.WorkflowRef).Scan(&resourceID).Error; err != nil {
		return "", 0, err
	}
	if resourceID == "" {
		return "", 0, fmt.Errorf("workflow %s is not installed", manifest.WorkflowRef)
	}
	type existingRevision struct {
		WorkflowResourceID string `gorm:"column:plugin_resource_id"`
		RevisionNo         int64  `gorm:"column:revision_no"`
	}
	var existing existingRevision
	if err := db.WithContext(ctx).Raw("SELECT plugin_resource_id, revision_no FROM plugin_revisions WHERE id = ?", manifest.WorkflowRevision.ID).Scan(&existing).Error; err != nil {
		return "", 0, err
	}
	if existing.WorkflowResourceID != "" {
		if existing.WorkflowResourceID != resourceID {
			return "", 0, fmt.Errorf("workflow revision %s belongs to a different workflow resource", manifest.WorkflowRevision.ID)
		}
		// SQLite's JSON columns are TEXT-affinity, but the workflow projection
		// scans compiled_graph into json.RawMessage and therefore requires the
		// driver value to be []byte. Refreshing this one immutable bundle value
		// also repairs bundles injected by an older importer.
		if db.Dialector.Name() == "sqlite" {
			if err := db.WithContext(ctx).Exec("UPDATE plugin_revisions SET compiled_graph = ? WHERE id = ?", []byte(compiledGraph), manifest.WorkflowRevision.ID).Error; err != nil {
				return "", 0, err
			}
		}
		return resourceID, existing.RevisionNo, nil
	}
	var maximum int64
	if err := db.WithContext(ctx).Raw("SELECT COALESCE(MAX(revision_no), 0) FROM plugin_revisions WHERE plugin_resource_id = ?", resourceID).Scan(&maximum).Error; err != nil {
		return "", 0, err
	}
	revisionNo := maximum + 1
	compiledGraphValue := any(compiledGraph)
	if db.Dialector.Name() == "sqlite" {
		compiledGraphValue = []byte(compiledGraph)
	}
	createdBy := manifest.WorkflowRevision.CreatedBy
	if createdBy == manifest.SourceOwnerID {
		createdBy = owner.ID
	}
	statement := `INSERT INTO plugin_revisions
        (id, plugin_resource_id, parent_revision_id, revision_no, tree_hash, message, created_by, created_at, compiled_graph, graph_hash, graph_schema_version)
        VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := db.WithContext(ctx).Exec(statement,
		manifest.WorkflowRevision.ID, resourceID, revisionNo, manifest.WorkflowRevision.TreeHash,
		manifest.WorkflowRevision.Message, createdBy, manifest.WorkflowRevision.CreatedAt,
		compiledGraphValue, manifest.WorkflowRevision.GraphHash, manifest.WorkflowRevision.GraphSchemaVersion,
	).Error; err != nil {
		return "", 0, err
	}
	return resourceID, revisionNo, nil
}

func installPayload(bundleRoot string, manifest Manifest, roots RuntimeRoots, preserveExisting bool) (int, error) {
	targetRoots := map[string]string{"uploads": roots.Uploads, "subagent": roots.Subagent}
	copied := 0
	for _, file := range manifest.Files {
		targetRoot := filepath.Clean(strings.TrimSpace(targetRoots[file.TargetRoot]))
		if targetRoot == "" || targetRoot == "." {
			return copied, fmt.Errorf("runtime root %s is not configured", file.TargetRoot)
		}
		source := filepath.Join(bundleRoot, filepath.FromSlash(file.Source))
		digest, size, err := fileDigest(source)
		if err != nil {
			return copied, err
		}
		if digest != file.SHA256 || size != file.Size {
			return copied, fmt.Errorf("payload checksum mismatch for %s", file.Source)
		}
		target := filepath.Join(targetRoot, filepath.FromSlash(file.RelativePath))
		if preserveExisting {
			if _, err := os.Stat(target); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return copied, err
			}
		}
		if existingDigest, existingSize, err := fileDigest(target); err == nil && existingDigest == digest && existingSize == size {
			continue
		}
		if err := copyAtomic(source, target, os.FileMode(file.Mode)); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

func copyAtomic(source, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(target), ".history-injection-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return err
	}
	if mode.Perm() == 0 {
		mode = 0o644
	}
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err == nil {
		return nil
	}
	_ = os.Remove(target)
	return os.Rename(temporaryPath, target)
}
