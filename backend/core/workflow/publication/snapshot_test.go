package publication

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"os"
	"testing"
)

func TestLegacyInlineEditBecomesManagedFile(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "legacy", WorkflowID: "ppt"}).Error; err != nil {
		t.Fatal(err)
	}
	raw, dir, err := SnapshotEdit(db, "legacy", "file", json.RawMessage(`{"storage":"inline_base64","filename":"presentation.pptx","content_base64":"aGVsbG8=","size":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if dir == "" || value["storage"] != "managed_file" || value["content_base64"] != nil {
		t.Fatalf("bad metadata: %s", raw)
	}
	content, err := os.ReadFile(value["path"].(string))
	if err != nil || string(content) != "hello" {
		t.Fatalf("file missing: %v", err)
	}
}
