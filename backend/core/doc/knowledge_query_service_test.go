package doc

import (
	"context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

type queryTestCatalog struct{ calls []string }

func (c *queryTestCatalog) GetDataset(_ context.Context, r DatasetGetRequest) (Dataset, error) {
	c.calls = append(c.calls, r.DatasetID)
	return Dataset{DatasetID: r.DatasetID}, nil
}
func TestDocumentCatalogGlobalPageAndRoot(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&orm.Document{}); e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	rows := []orm.Document{
		{ID: "folder", DatasetID: "a", DisplayName: "Folder", DocumentType: "FOLDER"},
		{ID: "nested", DatasetID: "a", PID: "folder", DisplayName: "N", BaseModel: orm.BaseModel{UpdatedAt: now.Add(time.Second)}},
		{ID: "root", DatasetID: "b", DisplayName: "R", BaseModel: orm.BaseModel{UpdatedAt: now.Add(2 * time.Second)}},
	}
	if e = db.Create(&rows).Error; e != nil {
		t.Fatal(e)
	}
	c := &queryTestCatalog{}
	s := NewDocumentCatalog(db, c)
	page, e := s.List(context.Background(), DatasetCatalogCaller{UserID: "u"}, DocumentCatalogQuery{DatasetIDs: []string{"a", "b"}, ResourceType: "all", Limit: 1})
	if e != nil || page.Total != 3 || len(page.Rows) != 1 || page.Rows[0].ID != "root" || len(c.calls) != 2 {
		t.Fatalf("page %+v, err %v, calls %v", page, e, c.calls)
	}
	root := ""
	page, e = s.List(context.Background(), DatasetCatalogCaller{UserID: "u"}, DocumentCatalogQuery{DatasetIDs: []string{"a"}, ResourceType: "all", ParentID: &root, Recursive: false, Limit: 10})
	if e != nil || len(page.Rows) != 1 || page.Rows[0].ID != "folder" {
		t.Fatalf("root %+v %v", page, e)
	}
	folder := "folder"
	page, e = s.List(context.Background(), DatasetCatalogCaller{UserID: "u"}, DocumentCatalogQuery{DatasetIDs: []string{"a"}, ResourceType: "document", ParentID: &folder, Recursive: true, Limit: 10})
	if e != nil || len(page.Rows) != 1 || page.LogicalPaths["nested"] != "Folder/N" {
		t.Fatalf("recursive %+v %v", page, e)
	}
}
func TestDocumentCatalogRejectsForeignParent(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.AutoMigrate(&orm.Document{})
	db.Create(&orm.Document{ID: "folder", DatasetID: "b", DocumentType: "FOLDER"})
	p := "folder"
	s := NewDocumentCatalog(db, &queryTestCatalog{})
	_, e := s.List(context.Background(), DatasetCatalogCaller{UserID: "u"}, DocumentCatalogQuery{DatasetIDs: []string{"a"}, ParentID: &p, Limit: 10})
	if e == nil {
		t.Fatal("foreign parent accepted")
	}
}
