package historyinjection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	sqlBody, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(manifest.SQLFile)))
	if err != nil {
		return result, err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return result, err
	}
	digest := sha256.New()
	digest.Write(manifestJSON)
	digest.Write(sqlBody)
	fingerprint := fmt.Sprintf("%x", digest.Sum(nil))

	var payload payloadChanges
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize imports for this workflow, including first installs where the
		// conversation row doesn't exist yet.
		var resource struct{ ID string }
		if err := tx.Table("plugins").Select("id").Where("plugin_ref = ?", manifest.WorkflowRef).
			Clauses(clause.Locking{Strength: "UPDATE"}).Take(&resource).Error; err != nil {
			return fmt.Errorf("lock workflow %s for history injection: %w", manifest.WorkflowRef, err)
		}
		existing, err := findInstalledConversation(tx, manifest.ConversationID)
		if err != nil {
			return err
		}
		if existing.ID != "" && existing.OwnerID != owner.ID {
			return fmt.Errorf("conversation %s already belongs to another user", manifest.ConversationID)
		}
		marker := existing.marker()
		if existing.ID != "" && marker.BundleID == "" {
			// An ID can also belong to the original user conversation on the
			// machine where an example was recorded.
			result.AlreadyPresent = true
			return retirePredecessor(tx, manifest, owner.ID)
		}
		result.AlreadyPresent = marker.BundleID == manifest.BundleID &&
			(marker.Fingerprint == "" || marker.Fingerprint == fingerprint)

		revisionNo, err := ensureWorkflowRevision(ctx, tx, manifest, owner, resource.ID)
		if err != nil {
			return err
		}
		portableSQL := string(sqlBody)
		if !result.AlreadyPresent {
			portableSQL, _, err = CompactPortableSQL(portableSQL)
			if err != nil {
				return fmt.Errorf("history injection failed: compact imported SQL: %w", err)
			}
		}
		rendered := renderSQL(portableSQL, map[string]string{
			ownerIDToken: owner.ID, ownerNameToken: owner.Username,
			workflowResourceToken: resource.ID, workflowRevisionToken: fmt.Sprintf("%d", revisionNo),
		})
		statements, err := splitSQLStatements(rendered)
		if err != nil {
			return err
		}
		if !result.AlreadyPresent {
			replacing := existing.ID != ""
			if replacing {
				if err := clearInstalledSampleRows(tx, manifest.ConversationID, owner.ID); err != nil {
					return err
				}
			}
			if err := insertSampleRows(ctx, tx, statements, manifest.ConversationID, replacing); err != nil {
				return err
			}
		}
		if err := normalizeInjectedConversation(ctx, tx, manifest, owner, fingerprint, result.AlreadyPresent); err != nil {
			return err
		}
		if err := reconcileInjectedWorkflowTasks(ctx, tx, manifest, owner); err != nil {
			return err
		}
		if err := normalizeSQLiteJSONColumns(ctx, tx, statements); err != nil {
			return err
		}
		if err := retirePredecessor(tx, manifest, owner.ID); err != nil {
			return err
		}
		return payload.install(bundleRoot, manifest, roots, result.AlreadyPresent)
	})
	if err != nil {
		return result, errors.Join(err, payload.finish(false))
	}
	result.FilesCopied = len(payload)
	return result, payload.finish(true)
}

func insertSampleRows(ctx context.Context, tx *gorm.DB, statements []string, conversationID string, replacing bool) error {
	var booleanColumns map[string]map[string]bool
	if tx.Dialector.Name() == "postgres" {
		var err error
		booleanColumns, err = postgresBooleanColumns(ctx, tx)
		if err != nil {
			return err
		}
	}
	replaced := false
	for index, statement := range statements {
		statement = rewritePostgresBooleanLiterals(statement, booleanColumns)
		if replacing {
			if insert, ok := parsePortableInsert(statement); ok && insert.table == "conversations" {
				var err error
				statement, err = replaceConversationInsert(insert, conversationID)
				if err != nil {
					return err
				}
				replaced = true
			}
		}
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("execute SQL statement %d: %w", index+1, err)
		}
	}
	if replacing && !replaced {
		return fmt.Errorf("replacement bundle has no conversation row for %s", conversationID)
	}
	return nil
}

func normalizeInjectedConversation(ctx context.Context, db *gorm.DB, manifest Manifest, owner TargetOwner, fingerprint string, preserveTimestamp bool) error {
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
	marker := readInjectionMarker(ext)
	if !preserveTimestamp || marker.Fingerprint != fingerprint {
		if !preserveTimestamp {
			injectedAt := time.Now().UTC()
			marker = injectionMarker{BundleID: manifest.BundleID, InjectedAt: injectedAt.Format(time.RFC3339Nano)}
			updates["created_at"] = injectedAt
			updates["updated_at"] = injectedAt
		}
		marker.Fingerprint = fingerprint
		ext[historyInjectionMetadataKey] = marker
		encoded, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		if db.Dialector.Name() == "sqlite" {
			updates["ext"] = encoded
		} else {
			updates["ext"] = string(encoded)
		}
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

func ensureWorkflowRevision(ctx context.Context, db *gorm.DB, manifest Manifest, owner TargetOwner, resourceID string) (int64, error) {
	compiledGraph := strings.TrimSpace(string(manifest.WorkflowRevision.CompiledGraph))
	if compiledGraph == "" || !json.Valid([]byte(compiledGraph)) {
		return 0, fmt.Errorf("workflow revision %s has invalid compiled_graph", manifest.WorkflowRevision.ID)
	}
	type existingRevision struct {
		WorkflowResourceID string `gorm:"column:plugin_resource_id"`
		RevisionNo         int64  `gorm:"column:revision_no"`
	}
	var existing existingRevision
	if err := db.WithContext(ctx).Raw("SELECT plugin_resource_id, revision_no FROM plugin_revisions WHERE id = ?", manifest.WorkflowRevision.ID).Scan(&existing).Error; err != nil {
		return 0, err
	}
	if existing.WorkflowResourceID != "" {
		if existing.WorkflowResourceID != resourceID {
			return 0, fmt.Errorf("workflow revision %s belongs to a different workflow resource", manifest.WorkflowRevision.ID)
		}
		// SQLite's JSON columns are TEXT-affinity, but the workflow projection
		// scans compiled_graph into json.RawMessage and therefore requires the
		// driver value to be []byte. Refreshing this one immutable bundle value
		// also repairs bundles injected by an older importer.
		if db.Dialector.Name() == "sqlite" {
			if err := db.WithContext(ctx).Exec("UPDATE plugin_revisions SET compiled_graph = ? WHERE id = ?", []byte(compiledGraph), manifest.WorkflowRevision.ID).Error; err != nil {
				return 0, err
			}
		}
		return existing.RevisionNo, nil
	}
	var maximum int64
	if err := db.WithContext(ctx).Raw("SELECT COALESCE(MAX(revision_no), 0) FROM plugin_revisions WHERE plugin_resource_id = ?", resourceID).Scan(&maximum).Error; err != nil {
		return 0, err
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
		return 0, err
	}
	return revisionNo, nil
}

type payloadChange struct {
	target string
	backup string
}

type payloadChanges []payloadChange

func (changes *payloadChanges) install(bundleRoot string, manifest Manifest, roots RuntimeRoots, preserveExisting bool) error {
	targetRoots := map[string]string{"uploads": roots.Uploads, "subagent": roots.Subagent}
	for _, file := range manifest.Files {
		digest, size, err := fileDigest(filepath.Join(bundleRoot, filepath.FromSlash(file.Source)))
		if err != nil {
			return err
		}
		if digest != file.SHA256 || size != file.Size {
			return fmt.Errorf("payload checksum mismatch for %s", file.Source)
		}
		root := filepath.Clean(strings.TrimSpace(targetRoots[file.TargetRoot]))
		if root == "." {
			return fmt.Errorf("runtime root %s is not configured", file.TargetRoot)
		}
	}
	for _, file := range manifest.Files {
		target := filepath.Join(strings.TrimSpace(targetRoots[file.TargetRoot]), filepath.FromSlash(file.RelativePath))
		info, err := os.Stat(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		change := payloadChange{target: target}
		if info != nil {
			if preserveExisting {
				continue
			}
			if digest, size, err := fileDigest(target); err == nil && digest == file.SHA256 && size == file.Size {
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("history injection payload target is not a regular file: %s", target)
			}
			backup, err := os.CreateTemp(filepath.Dir(target), ".history-injection-backup-")
			if err != nil {
				return err
			}
			change.backup = backup.Name()
			if err := backup.Close(); err != nil {
				_ = os.Remove(change.backup)
				return err
			}
			if err := copyAtomic(target, change.backup, info.Mode()); err != nil {
				_ = os.Remove(change.backup)
				return err
			}
		}
		source := filepath.Join(bundleRoot, filepath.FromSlash(file.Source))
		if err := copyAtomic(source, target, os.FileMode(file.Mode)); err != nil {
			if change.backup != "" {
				_ = os.Remove(change.backup)
			}
			return err
		}
		*changes = append(*changes, change)
	}
	return nil
}

func (changes payloadChanges) finish(committed bool) error {
	var result error
	for index := len(changes) - 1; index >= 0; index-- {
		change := changes[index]
		switch {
		case committed && change.backup != "":
			result = errors.Join(result, os.Remove(change.backup))
		case !committed && change.backup != "":
			result = errors.Join(result, os.Rename(change.backup, change.target))
		case !committed:
			if err := os.Remove(change.target); err != nil && !os.IsNotExist(err) {
				result = errors.Join(result, err)
			}
		}
	}
	return result
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
