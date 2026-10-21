package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/learning"
)

func TestKnowledgeParityScenesAndAtomicConfiguration(t *testing.T) {
	s, db, op := resourceFixture(t)
	if err := db.AutoMigrate(&orm.KBModel{}, &orm.ACLModel{}); err != nil {
		t.Fatal(err)
	}
	s.provision = func(context.Context, DatasetCatalogCaller, *orm.Dataset) error { return nil }
	ctx := context.Background()
	for i, profile := range learning.BuiltinProfiles() {
		t.Run(profile.Key, func(t *testing.T) {
			result, err := s.CreateKnowledge(ctx, op, CreateKnowledgeRequest{
				Name: fmt.Sprintf("Scene-%d", i), Tags: []string{"论文", "论文"}, Scene: profile.Key, ProcessingLevel: "stored",
			})
			if err != nil {
				t.Fatal(err)
			}
			id := result.Items[0].ResourceRef.ID()
			var ds orm.Dataset
			if err := db.First(&ds, "id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			if ds.Scene != profile.Key || !reflect.DeepEqual(parseDatasetTags(ds.Ext), []string{"论文"}) {
				t.Fatalf("configuration lost: %+v", ds)
			}
			var rows []learning.KnowledgeBaseCapability
			if err := db.Where("dataset_id = ? AND enabled = ?", id, true).Order("display_order").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, row := range rows {
				keys = append(keys, row.CapabilityKey)
			}
			if !reflect.DeepEqual(keys, profile.Capabilities) {
				t.Fatalf("capabilities %v != %v", keys, profile.Capabilities)
			}
			// A capability-only update must retain the selected scene.
			refs := []learning.CapabilityRef{{Key: "general_translation", Version: 1, Enabled: true, DisplayOrder: 1}}
			_, err = s.UpdateMetadata(ctx, op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{
				Target: result.Items[0].ResourceRef, Changes: ResourceMetadataChanges{Capabilities: &refs},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			db.First(&ds, "id = ?", id)
			if ds.Scene != profile.Key {
				t.Fatal("capability change altered scene identity")
			}
		})
	}
	if _, err := s.CreateKnowledge(ctx, op, CreateKnowledgeRequest{Name: "Missing-tags", ProcessingLevel: "stored"}); err == nil {
		t.Fatal("missing required tags accepted")
	}
	invalid := []learning.CapabilityRef{{Key: "nonexistent", Version: 1, Enabled: true}}
	if _, err := s.CreateKnowledge(ctx, op, CreateKnowledgeRequest{Name: "Invalid-caps", Tags: []string{"test"}, ProcessingLevel: "stored", Capabilities: invalid}); err == nil {
		t.Fatal("invalid capabilities accepted")
	}
	var count int64
	db.Model(&orm.Dataset{}).Where("display_name = ?", "Invalid-caps").Count(&count)
	if count != 0 {
		t.Fatal("invalid configuration left a knowledge base behind")
	}
	if scene, err := resourceScene("paper_collection"); err != nil || scene != "academic_papers" {
		t.Fatalf("legacy scene: %s %v", scene, err)
	}
}

func TestKnowledgeParityLocalOriginalIndependentAndValidated(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, err := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	caller := DatasetCatalogCaller{UserID: "user-1"}
	dir := t.TempDir()
	path := filepath.Join(dir, "paper.txt")
	body := []byte("original paper content")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := svc.AcquireLocalOriginal(context.Background(), caller, path, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var row orm.UploadedFile
	if err := db.Where("upload_file_id = ?", ref.FileRef).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var ext uploadedFileExt
	if err := json.Unmarshal(row.Ext, &ext); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(ext.StoredPath)
	if err != nil || string(stored) != string(body) {
		t.Fatalf("original depends on source: %q %v", stored, err)
	}
	for _, tc := range []struct {
		name, data string
		remaining  int64
	}{
		{"empty.txt", "", 100}, {"bad.pdf", "<html>login</html>", 100},
		{"large.txt", "too large", 3}, {"unsupported.exe", "payload", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.AcquireLocalOriginal(context.Background(), caller, path, tc.remaining); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	if _, err := svc.AcquireLocalOriginal(context.Background(), caller, "relative.txt", 100); err == nil {
		t.Fatal("relative path accepted")
	}
	directory := filepath.Join(dir, "directory.txt")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcquireLocalOriginal(context.Background(), caller, directory, 100); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestPageAndResourceUpdatesPreserveUnsubmittedKnowledgeConfiguration(t *testing.T) {
	service, db, op := resourceFixture(t)
	originalExt := json.RawMessage(`{"tags":["keep"],"algo_id":"original","algo_name":"Original","profile_id":"custom-preset","parsers":[{"type":"pdf","name":"original-parser","params":{}}],"other":"kept"}`)
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "source").Updates(map[string]any{"cover_image": "old-cover", "ext": originalExt}).Error; err != nil {
		t.Fatal(err)
	}
	req := mux.SetURLVars(httptest.NewRequest("POST", "/datasets/source", bytes.NewBufferString(`{"cover_image":"new-cover","parsers":[{"type":"txt","name":"text-parser","params":{}}]}`)), map[string]string{"dataset": "source"})
	req.Header.Set("X-User-Id", op.Caller.UserID)
	rec := httptest.NewRecorder()
	UpdateDataset(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var saved orm.Dataset
	if err := db.Where("id = ?", "source").Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	ext := knowledgeExt(saved.Ext)
	if saved.CoverImage != "new-cover" || ext["algo_id"] != "original" || ext["profile_id"] != "custom-preset" || ext["other"] != "kept" || !reflect.DeepEqual(parseDatasetTags(saved.Ext), []string{"keep"}) {
		t.Fatalf("page discarded configuration: %+v %s", saved, saved.Ext)
	}
	parsers := parseDatasetParsers(saved.Ext)
	if len(parsers) != 1 || parsers[0].Name != "text-parser" {
		t.Fatalf("parsers not updated: %+v", parsers)
	}
	description := "changed through the resource command"
	if _, err := service.UpdateMetadata(t.Context(), op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "source"}, Changes: ResourceMetadataChanges{Description: &description}}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", "source").Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Desc != description || saved.CoverImage != "new-cover" || !reflect.DeepEqual(parseDatasetParsers(saved.Ext), parsers) || knowledgeExt(saved.Ext)["profile_id"] != "custom-preset" {
		t.Fatalf("resource command discarded configuration: %+v %s", saved, saved.Ext)
	}
}
