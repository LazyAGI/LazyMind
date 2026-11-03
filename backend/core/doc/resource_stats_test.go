package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestFolderStatsAllowSmallOperationsInLargeParent(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	var err error
	s.originals, err = NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	rows := []orm.Document{{ID: "folder", DatasetID: "source", DisplayName: "folder", DocumentType: "FOLDER", Ext: json.RawMessage(`{"custom":"keep"}`)}}
	for i := 0; i < 1002; i++ {
		rows = append(rows, orm.Document{ID: fmt.Sprintf("doc-%04d", i), DatasetID: "source", PID: "folder", DisplayName: fmt.Sprintf("%d.txt", i), DocumentType: "TXT", Ext: json.RawMessage(`{"file_size":1}`)})
	}
	if err := db.CreateInBatches(&rows, 50).Error; err != nil {
		t.Fatal(err)
	}
	check := func(documents, folders, size int64) orm.Document {
		t.Helper()
		var row orm.Document
		if err := db.First(&row, "id = ?", "folder").Error; err != nil {
			t.Fatal(err)
		}
		stats := folderStatsFromExt(row.Ext)
		if stats.ChildDocumentCount != documents || stats.ChildFolderCount != folders || stats.RecursiveDocumentCount != documents || stats.RecursiveFileSize != size {
			t.Fatalf("stats=%+v want documents=%d folders=%d bytes=%d", stats, documents, folders, size)
		}
		if !strings.Contains(string(row.Ext), `"custom":"keep"`) {
			t.Fatal("unrelated metadata lost")
		}
		return row
	}
	if _, err = s.Delete(ctx, op, DeleteResourcesRequest{Targets: []ResourceRef{{Kind: "document", KnowledgeBaseID: "source", DocumentID: "doc-0000"}}}); err != nil {
		t.Fatal(err)
	}
	check(1001, 0, 1001)
	if _, err = s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", ParentID: "folder", Folders: []string{"empty"}}); err != nil {
		t.Fatal(err)
	}
	check(1001, 1, 1001)
	var uploads []ImportOriginalRequest
	for _, name := range []string{"one.txt", "two.txt"} {
		file, e := s.originals.AcquireOriginal(ctx, op.Caller, AcquireOriginalRequest{Filename: name, ContentType: "text/plain", Reader: strings.NewReader(name)})
		if e != nil {
			t.Fatal(e)
		}
		uploads = append(uploads, ImportOriginalRequest{FileRef: file.FileRef, DatasetID: "source", ParentID: "folder"})
	}
	before := check(1001, 1, 1001)
	if _, err = s.Upload(ctx, op, uploads); err != nil {
		t.Fatal(err)
	}
	after := check(1003, 1, 1015)
	if after.ResourceVersion != before.ResourceVersion+1 {
		t.Fatalf("shared ancestor updated more than once: before=%d after=%d", before.ResourceVersion, after.ResourceVersion)
	}
	if _, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{{Kind: "document", KnowledgeBaseID: "source", DocumentID: "doc-0001"}}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "source"}}); err != nil {
		t.Fatal(err)
	}
	check(1002, 1, 1014)
	// Legacy entry points share the same derived values and do not bump unchanged versions.
	before = check(1002, 1, 1014)
	recalcAffectedFolderStats(ctx, "source", "folder", "folder")
	after = check(1002, 1, 1014)
	if before.ResourceVersion != after.ResourceVersion {
		t.Fatal("unchanged refresh modified resource version")
	}
}

func TestFolderStatsMemoizesNestedFoldersAndRejectsCycles(t *testing.T) {
	_, db, _ := resourceFixture(t)
	rows := []orm.Document{
		{ID: "root", DatasetID: "source", DisplayName: "root", DocumentType: "FOLDER", Ext: json.RawMessage(`{}`)},
		{ID: "child", DatasetID: "source", PID: "root", DisplayName: "child", DocumentType: "FOLDER", Ext: json.RawMessage(`{}`)},
		{ID: "leaf", DatasetID: "source", PID: "child", DisplayName: "a.txt", DocumentType: "TXT", Ext: json.RawMessage(`{"file_size":7}`)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return refreshFolderStats(tx, map[string][]string{"source": {"child", "root", "child"}})
	}); err != nil {
		t.Fatal(err)
	}
	var root orm.Document
	db.First(&root, "id = ?", "root")
	stats := folderStatsFromExt(root.Ext)
	if stats.ChildFolderCount != 1 || stats.RecursiveFolderCount != 1 || stats.RecursiveDocumentCount != 1 || stats.RecursiveFileSize != 7 {
		t.Fatalf("nested stats=%+v", stats)
	}
	if err := db.Model(&orm.Document{}).Where("id = ?", "root").Update("p_id", "child").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return refreshFolderStats(tx, map[string][]string{"source": {"child"}}) }); err == nil {
		t.Fatal("cycle accepted")
	}
}
