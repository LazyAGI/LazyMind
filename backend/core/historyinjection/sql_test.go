package historyinjection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSplitSQLStatementsPreservesQuotedSemicolons(t *testing.T) {
	statements, err := splitSQLStatements("-- comment\nINSERT INTO x(v) VALUES ('a; b''s');\nINSERT INTO x(v) VALUES ('line 1\nline 2');")
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0], "a; b''s") || !strings.Contains(statements[1], "line 2") {
		t.Fatalf("unexpected statements: %#v", statements)
	}
}

func TestRewritePostgresBooleanLiteralsOnlyChangesBooleanColumns(t *testing.T) {
	statement := `INSERT INTO plugin_slot_revisions
        (id, revision, selected, content_snapshot)
        VALUES ('revision-1', 1, 1, '{"enabled":1,"values":[0,1],"label":"it''s, fine"}') ON CONFLICT DO NOTHING`
	got := rewritePostgresBooleanLiterals(statement, portableBooleanColumns)
	if !strings.Contains(got, `VALUES ('revision-1', 1, TRUE, '{"enabled":1,"values":[0,1],"label":"it''s, fine"}')`) {
		t.Fatalf("rewritten statement = %s", got)
	}
	if strings.Contains(got, "'revision-1', TRUE, TRUE") {
		t.Fatalf("non-boolean revision was rewritten: %s", got)
	}
}

func TestRewritePostgresBooleanLiteralsSupportsSeveralConversationFlags(t *testing.T) {
	statement := `INSERT INTO conversations (id, enable_plugin, enable_subagent, is_task_conv, is_ephemeral)
        VALUES ('conversation-1', 1, 0, 1, 0) ON CONFLICT DO NOTHING`
	want := `VALUES ('conversation-1', TRUE, FALSE, TRUE, FALSE)`
	if got := rewritePostgresBooleanLiterals(statement, portableBooleanColumns); !strings.Contains(got, want) {
		t.Fatalf("rewritten statement = %s, want fragment %s", got, want)
	}
}

func TestDiscoverSkipsWorkflowPayloadManifests(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "ppt", "demo-v1")
	if err := os.MkdirAll(filepath.Join(bundle, "payload", "uploads", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, BundleID: "demo-v1", Category: "ppt", Title: "demo",
		ConversationID: "conversation-1", SourceOwnerID: "source-user", WorkflowRef: "builtin:ppt-workflow", SQLFile: "data.sql",
		WorkflowRevision: WorkflowRevision{ID: "revision-1", TreeHash: "tree", CreatedAt: "2026-01-01T00:00:00Z", CompiledGraph: []byte(`{}`), GraphHash: "graph", GraphSchemaVersion: "3"},
	}
	body, err := jsonMarshalIndent(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "payload", "uploads", "workspace", "manifest.json"), []byte(`{"workspace":"not-an-injection-bundle"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Manifest.BundleID != "demo-v1" {
		t.Fatalf("unexpected sources: %#v", sources)
	}
}

func TestApplyPortableBundleToSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE plugins (id TEXT PRIMARY KEY, plugin_ref TEXT UNIQUE NOT NULL)`,
		`CREATE TABLE plugin_revisions (id TEXT PRIMARY KEY, plugin_resource_id TEXT NOT NULL, parent_revision_id TEXT, revision_no INTEGER NOT NULL, tree_hash TEXT NOT NULL, message TEXT NOT NULL, created_by TEXT, created_at TEXT NOT NULL, compiled_graph TEXT, graph_hash TEXT NOT NULL, graph_schema_version TEXT NOT NULL, UNIQUE(plugin_resource_id, revision_no))`,
		`CREATE TABLE conversations (id TEXT PRIMARY KEY, display_name TEXT, is_task_conv NUMERIC NOT NULL DEFAULT TRUE, create_user_id TEXT NOT NULL, create_user_name TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, ext JSON)`,
		`CREATE TABLE chat_histories (id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, run_terminal TEXT)`,
		`INSERT INTO plugins(id, plugin_ref) VALUES ('workflow-resource', 'builtin:ppt-workflow')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}

	bundle := t.TempDir()
	payloadPath := filepath.Join(bundle, "payload", "uploads", "workflow-artifacts", "demo.txt")
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, []byte("demo"), 0o640); err != nil {
		t.Fatal(err)
	}
	digest, size, err := fileDigest(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, BundleID: "ppt-demo-v1", Category: "ppt", Title: "demo",
		ConversationID: "conversation-1", SourceOwnerID: "source-user", WorkflowRef: "builtin:ppt-workflow", SQLFile: "data.sql",
		WorkflowRevision: WorkflowRevision{ID: "source-revision", RevisionNo: 40, TreeHash: "tree", CreatedAt: "2026-01-01T00:00:00Z", CompiledGraph: []byte(`{}`), GraphHash: "graph", GraphSchemaVersion: "3"},
		Files:            []PayloadFile{{Source: "payload/uploads/workflow-artifacts/demo.txt", TargetRoot: "uploads", RelativePath: "workflow-artifacts/demo.txt", SHA256: digest, Size: size, Mode: 0o640}},
	}
	body, _ := jsonMarshalIndent(manifest)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sqlText := `-- portable bundle
INSERT INTO conversations (id, display_name, create_user_id, create_user_name, created_at, updated_at, ext) VALUES ('conversation-1', 'demo', '{{OWNER_USER_ID}}', '{{OWNER_USER_NAME}}', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '{"source":"bundle"}') ON CONFLICT DO NOTHING;
INSERT INTO chat_histories (id, conversation_id, run_terminal) VALUES ('history-1', 'conversation-1', '{"status":"completed"}') ON CONFLICT DO NOTHING;`
	if err := os.WriteFile(filepath.Join(bundle, "data.sql"), []byte(sqlText), 0o644); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	injectionStarted := time.Now().UTC()
	result, err := Apply(t.Context(), db, BundleSource{Path: bundle, Manifest: manifest},
		TargetOwner{ID: "target-user", Username: "admin"}, RuntimeRoots{Uploads: uploads, Subagent: t.TempDir()})
	injectionFinished := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesCopied != 1 || result.AlreadyPresent {
		t.Fatalf("unexpected result: %#v", result)
	}
	var owner string
	if err := db.Raw("SELECT create_user_id FROM conversations WHERE id = 'conversation-1'").Scan(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if owner != "target-user" {
		t.Fatalf("owner = %q", owner)
	}
	var conversation struct {
		DisplayName string `gorm:"column:display_name"`
		IsTaskConv  bool   `gorm:"column:is_task_conv"`
	}
	if err := db.Raw("SELECT display_name, is_task_conv FROM conversations WHERE id = 'conversation-1'").Scan(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.DisplayName != "demo" || conversation.IsTaskConv {
		t.Fatalf("normalized conversation = %#v", conversation)
	}
	var timestamps struct {
		CreatedAt time.Time `gorm:"column:created_at"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	if err := db.Raw("SELECT created_at, updated_at FROM conversations WHERE id = 'conversation-1'").Scan(&timestamps).Error; err != nil {
		t.Fatal(err)
	}
	if timestamps.CreatedAt.Before(injectionStarted) || timestamps.CreatedAt.After(injectionFinished) ||
		!timestamps.CreatedAt.Equal(timestamps.UpdatedAt) {
		t.Fatalf("conversation timestamps = %#v, want injection window %s..%s", timestamps, injectionStarted, injectionFinished)
	}
	if body, err := os.ReadFile(filepath.Join(uploads, "workflow-artifacts", "demo.txt")); err != nil || string(body) != "demo" {
		t.Fatalf("installed payload = %q, %v", body, err)
	}
	var graphStorage string
	if err := db.Raw("SELECT typeof(compiled_graph) FROM plugin_revisions WHERE id = 'source-revision'").Scan(&graphStorage).Error; err != nil {
		t.Fatal(err)
	}
	if graphStorage != "blob" {
		t.Fatalf("compiled_graph SQLite storage = %q, want blob", graphStorage)
	}
	var extStorage string
	if err := db.Raw("SELECT typeof(ext) FROM conversations WHERE id = 'conversation-1'").Scan(&extStorage).Error; err != nil {
		t.Fatal(err)
	}
	if extStorage != "blob" {
		t.Fatalf("conversation ext SQLite storage = %q, want blob", extStorage)
	}
	var terminalStorage string
	if err := db.Raw("SELECT typeof(run_terminal) FROM chat_histories WHERE id = 'history-1'").Scan(&terminalStorage).Error; err != nil {
		t.Fatal(err)
	}
	if terminalStorage != "blob" {
		t.Fatalf("chat history run_terminal SQLite storage = %q, want blob", terminalStorage)
	}
	var history struct {
		RunTerminal json.RawMessage `gorm:"column:run_terminal"`
	}
	if err := db.Raw("SELECT run_terminal FROM chat_histories WHERE id = 'history-1'").Scan(&history).Error; err != nil {
		t.Fatalf("scan chat history run_terminal: %v", err)
	}
	if string(history.RunTerminal) != `{"status":"completed"}` {
		t.Fatalf("chat history run_terminal = %q", history.RunTerminal)
	}
	var extRow struct {
		Ext json.RawMessage `gorm:"column:ext"`
	}
	if err := db.Raw("SELECT ext FROM conversations WHERE id = 'conversation-1'").Scan(&extRow).Error; err != nil {
		t.Fatal(err)
	}
	var extObject map[string]any
	if err := json.Unmarshal(extRow.Ext, &extObject); err != nil {
		t.Fatal(err)
	}
	if !hasInjectionTimestamp(extObject, manifest.BundleID) {
		t.Fatalf("injection timestamp metadata missing: %s", extRow.Ext)
	}
	if err := db.Exec("UPDATE conversations SET display_name = 'stale title', is_task_conv = TRUE WHERE id = 'conversation-1'").Error; err != nil {
		t.Fatal(err)
	}
	installedPayload := filepath.Join(uploads, "workflow-artifacts", "demo.txt")
	if err := os.WriteFile(installedPayload, []byte("user-edited"), 0o640); err != nil {
		t.Fatal(err)
	}
	second, err := Apply(t.Context(), db, BundleSource{Path: bundle, Manifest: manifest},
		TargetOwner{ID: "target-user", Username: "admin"}, RuntimeRoots{Uploads: uploads, Subagent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyPresent || second.FilesCopied != 0 {
		t.Fatalf("unexpected idempotent result: %#v", second)
	}
	if body, err := os.ReadFile(installedPayload); err != nil || string(body) != "user-edited" {
		t.Fatalf("idempotent apply overwrote user payload = %q, %v", body, err)
	}
	if err := db.Raw("SELECT display_name, is_task_conv FROM conversations WHERE id = 'conversation-1'").Scan(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.DisplayName != "demo" || conversation.IsTaskConv {
		t.Fatalf("idempotent normalization = %#v", conversation)
	}
	var secondTimestamps struct {
		CreatedAt time.Time `gorm:"column:created_at"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	if err := db.Raw("SELECT created_at, updated_at FROM conversations WHERE id = 'conversation-1'").Scan(&secondTimestamps).Error; err != nil {
		t.Fatal(err)
	}
	if !secondTimestamps.CreatedAt.Equal(timestamps.CreatedAt) || !secondTimestamps.UpdatedAt.Equal(timestamps.UpdatedAt) {
		t.Fatalf("idempotent apply changed injection timestamps: first=%#v second=%#v", timestamps, secondTimestamps)
	}
	if err := os.Remove(installedPayload); err != nil {
		t.Fatal(err)
	}
	repaired, err := Apply(t.Context(), db, BundleSource{Path: bundle, Manifest: manifest},
		TargetOwner{ID: "target-user", Username: "admin"}, RuntimeRoots{Uploads: uploads, Subagent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.AlreadyPresent || repaired.FilesCopied != 1 {
		t.Fatalf("missing payload was not repaired: %#v", repaired)
	}
	if body, err := os.ReadFile(installedPayload); err != nil || string(body) != "demo" {
		t.Fatalf("repaired payload = %q, %v", body, err)
	}

	// Simulate a database populated by an older importer: the bundle dates are
	// still present and there is no one-time injection timestamp marker.
	legacyTime := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	if err := db.Exec(
		"UPDATE conversations SET created_at = ?, updated_at = ?, ext = ? WHERE id = 'conversation-1'",
		legacyTime, legacyTime, []byte(`{"source":"bundle"}`),
	).Error; err != nil {
		t.Fatal(err)
	}
	upgradeStarted := time.Now().UTC()
	upgraded, err := Apply(t.Context(), db, BundleSource{Path: bundle, Manifest: manifest},
		TargetOwner{ID: "target-user", Username: "admin"}, RuntimeRoots{Uploads: uploads, Subagent: t.TempDir()})
	upgradeFinished := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	if !upgraded.AlreadyPresent {
		t.Fatalf("legacy upgrade was not recognized as already present: %#v", upgraded)
	}
	var upgradedTimestamps struct {
		CreatedAt time.Time `gorm:"column:created_at"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	if err := db.Raw("SELECT created_at, updated_at FROM conversations WHERE id = 'conversation-1'").Scan(&upgradedTimestamps).Error; err != nil {
		t.Fatal(err)
	}
	if upgradedTimestamps.CreatedAt.Before(upgradeStarted) || upgradedTimestamps.CreatedAt.After(upgradeFinished) ||
		!upgradedTimestamps.CreatedAt.Equal(upgradedTimestamps.UpdatedAt) {
		t.Fatalf("legacy timestamps were not upgraded to injection time: %#v, window %s..%s",
			upgradedTimestamps, upgradeStarted, upgradeFinished)
	}
}

func jsonMarshalIndent(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	return append(body, '\n'), err
}
