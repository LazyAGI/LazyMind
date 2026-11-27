package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

func TestMarkdownSourcePreviewsScopeIdenticalRelativeImagePaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowSlotRevision{})
	if err := db.Create(&orm.WorkflowSession{ID: "session", StateVersion: 7}).Error; err != nil {
		t.Fatal(err)
	}
	assets := map[string]any{}
	for index := range 2 {
		filename := filepath.Join(root, fmt.Sprintf("image-%d.png", index))
		if err := os.WriteFile(filename, []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
		assets[fmt.Sprintf("asset-%d", index)] = map[string]any{"local_path": filename, "meta": map[string]any{
			"source_reference": "images/a.png", "markdown_source_references": map[string][]string{fmt.Sprint(index): {"images/a.png"}},
		}}
		listIndex := index
		seedWriterRevision(t, db, fmt.Sprintf("source-%d", index), "source_materials", 1, true, "ai", json.RawMessage(`{"schema":"text/markdown","data":"# Body"}`))
		if err := db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", fmt.Sprintf("source-%d", index)).Update("list_index", &listIndex).Error; err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(map[string]any{"data": map[string]any{"assets": assets}})
	seedWriterRevision(t, db, "media", "media_assets", 1, true, "ai", data)
	first, second := 0, 1
	a := writerDocumentMediaURLs(context.Background(), db.DB, "session", "source_materials", &first)
	b := writerDocumentMediaURLs(context.Background(), db.DB, "session", "source_materials", &second)
	if a["images/a.png"] == "" || b["images/a.png"] == "" || a["images/a.png"] == b["images/a.png"] {
		t.Fatalf("crossed image aliases: %v %v", a, b)
	}
	for _, index := range []*int{&first, &second} {
		selected, err := loadSelectedWriterArtifact(context.Background(), db.DB, "session", "source_materials", index)
		if err != nil {
			t.Fatal(err)
		}
		if selected.Revision.SelectionVersion != 7 || selected.Revision.ListIndex == nil || *selected.Revision.ListIndex != *index {
			t.Fatalf("selected wrong document: %v %+v", err, selected)
		}
	}
}
