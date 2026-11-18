package historyinjection

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/migrate"
)

func TestReplaceInstalledSampleOnUpgrade(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "core.db")
	runner, err := migrate.NewRunner("sqlite", dsn, "../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(0); err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	owner := TargetOwner{ID: "admin", Username: "admin"}
	roots := RuntimeRoots{Uploads: t.TempDir(), Subagent: t.TempDir()}
	resource := orm.WorkflowResource{ID: "resource", WorkflowRef: "builtin:ppt-workflow", WorkflowID: "ppt-workflow"}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	current := writeReplacementBundle(t, "ppt-group-meeting-v2", "new-ppt", "new", "history-new")
	apply := func(source BundleSource) ApplyResult {
		t.Helper()
		result, err := Apply(t.Context(), db, source, owner, roots)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for index, previous := range releasePredecessors[current.Manifest.BundleID] {
		historyID := fmt.Sprintf("history-old-%d", index)
		apply(writeReplacementBundle(t, previous.bundleID, previous.conversationID, "old", historyID))
	}
	resolved, found, err := ResolveImportedOwner(t.Context(), db, []BundleSource{current})
	if err != nil || !found || resolved != owner {
		t.Fatalf("resolve owner from marked predecessor: %#v %t %v", resolved, found, err)
	}
	apply(current)
	var visible int64
	if err := db.Table("conversations").Where("deleted_at IS NULL").Count(&visible).Error; err != nil || visible != 1 {
		t.Fatalf("visible samples = %d, %v", visible, err)
	}

	// A revised bundle can keep both the bundle ID and conversation ID.
	revised := writeReplacementBundle(t, current.Manifest.BundleID, current.Manifest.ConversationID, "revised", "history-revised")
	payload := filepath.Join(roots.Uploads, "demo.txt")
	if err := os.Mkdir(payload+".blocked", 0o755); err != nil {
		t.Fatal(err)
	}
	// Force a failure after the existing payload has been replaced.
	blocked := revised.Manifest.Files[0]
	blocked.RelativePath = "demo.txt.blocked"
	broken := revised
	broken.Manifest.Files = append(append([]PayloadFile{}, revised.Manifest.Files...), blocked)
	writeTestManifest(t, broken)
	if _, err := Apply(t.Context(), db, broken, owner, roots); err == nil {
		t.Fatal("expected a payload write failure")
	}
	assertSampleContent(t, db, current.Manifest.ConversationID, payload, "history-new", "new")

	writeTestManifest(t, revised)
	if result := apply(revised); result.AlreadyPresent || result.FilesCopied != 1 {
		t.Fatalf("replacement result: %#v", result)
	}
	assertSampleContent(t, db, current.Manifest.ConversationID, payload, "history-revised", "revised")
	if result := apply(revised); !result.AlreadyPresent || result.FilesCopied != 0 {
		t.Fatalf("repeated replacement result: %#v", result)
	}
}

func writeReplacementBundle(t *testing.T, bundleID, conversationID, content, historyID string) BundleSource {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "demo.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, size, err := fileDigest(filepath.Join(root, "demo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, BundleID: bundleID, Category: "ppt", Title: content,
		ConversationID: conversationID, SourceOwnerID: "source", WorkflowRef: "builtin:ppt-workflow", SQLFile: "data.sql",
		WorkflowRevision: WorkflowRevision{ID: "revision", CreatedAt: "2026-01-01T00:00:00Z", CompiledGraph: []byte(`{}`)},
		Files:            []PayloadFile{{Source: "demo.txt", TargetRoot: "uploads", RelativePath: "demo.txt", SHA256: hash, Size: size}},
	}
	source := BundleSource{Path: root, Manifest: manifest}
	writeTestManifest(t, source)
	data := fmt.Sprintf(`
INSERT INTO conversations (id, display_name, create_user_id, create_user_name, created_at, updated_at)
VALUES ('%s', '%s', '{{OWNER_USER_ID}}', '{{OWNER_USER_NAME}}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z') ON CONFLICT DO NOTHING;
INSERT INTO chat_histories (id, seq, conversation_id, create_time, update_time)
VALUES ('%s', 1, '%s', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z') ON CONFLICT DO NOTHING;
`, conversationID, content, historyID, conversationID)
	if err := os.WriteFile(filepath.Join(root, manifest.SQLFile), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return source
}

func writeTestManifest(t *testing.T, source BundleSource) {
	t.Helper()
	body, err := jsonMarshalIndent(source.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Path, "manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertSampleContent(t *testing.T, db *gorm.DB, conversationID, payload, historyID, content string) {
	t.Helper()
	var histories []string
	if err := db.Table("chat_histories").Where("conversation_id = ?", conversationID).Pluck("id", &histories).Error; err != nil {
		t.Fatal(err)
	}
	if len(histories) != 1 || histories[0] != historyID {
		t.Fatalf("histories = %v, want [%s]", histories, historyID)
	}
	body, err := os.ReadFile(payload)
	if err != nil || strings.TrimSpace(string(body)) != content {
		t.Fatalf("payload = %q, %v; want %q", body, err, content)
	}
}
