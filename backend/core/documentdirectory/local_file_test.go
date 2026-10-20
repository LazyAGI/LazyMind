package documentdirectory

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestLocalFileLocatorOwnershipAndStoredPath(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "local")
	e := orm.DocumentDirectoryEntry{ID: "local", DirectoryID: d.ID, ResourceKey: "local", Source: "local", Namespace: "host:nt", ResourceID: `C:\Users\test\资料\a.md`, LocatorJSON: encoded(map[string]string{"path": `C:\Users\test\资料\a.md`, "kind": "file"})}
	must(t, db.Create(&e).Error)
	input, err := localFileLocator(context.Background(), db, "u", d.ID, e.ID)
	must(t, err)
	if input["path"] != e.ResourceID || input["namespace"] != e.Namespace {
		t.Fatal(input)
	}
	if _, err = localFileLocator(context.Background(), db, "other", d.ID, e.ID); err == nil {
		t.Fatal("cross-user read allowed")
	}
	must(t, db.Model(&e).Update("excluded", true).Error)
	if _, err = localFileLocator(context.Background(), db, "u", d.ID, e.ID); err == nil {
		t.Fatal("excluded reference read allowed")
	}
	must(t, db.Model(&e).Updates(map[string]any{"excluded": false, "locator_json": encoded(map[string]string{"path": `C:\secret.txt`})}).Error)
	if _, err = localFileLocator(context.Background(), db, "u", d.ID, e.ID); err == nil {
		t.Fatal("mismatched path accepted")
	}
}
