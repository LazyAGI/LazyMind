package doc

import (
	"context"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestDefaultProcessingLevelDoesNotInvalidateArtifacts(t *testing.T) {
	db := libraryKindTestDB(t)
	for _, from := range []string{ProcessingLevelStored, ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed} {
		for _, to := range []string{ProcessingLevelStored, ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed} {
			ds := orm.Dataset{ID: "policy", Scene: "general", ProcessingLevel: from, ProcessingRevision: 7}
			updates := map[string]any{}
			if err := updateKnowledgeConfiguration(context.Background(), db.DB, "owner", &ds, ResourceMetadataChanges{ProcessingLevel: &to}, updates, map[string]any{}, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			if _, changed := updates["processing_revision"]; changed {
				t.Fatalf("policy %s -> %s invalidates artifact revision: %v", from, to, updates)
			}
		}
	}
}

func TestCatalogIncludesManuallyIndexedArtifactsBelowDefaultLevel(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	installDatasetCatalogScanTransport(t)
	service := mustDatasetCatalogService(t, db.DB)
	seedDatasetCatalogDataset(t, db, "manual", "user-1", "Manual", "", nil, time.Now())
	seedDatasetCatalogDocument(t, db, "document", "manual", "paper.pdf", 1)
	for _, level := range []string{ProcessingLevelStored, ProcessingLevelParsed, ProcessingLevelChunked} {
		db.Model(&orm.Dataset{}).Where("id=?", "manual").Update("processing_level", level)
		db.Where("dataset_id=?", "manual").Delete(&orm.DocumentProcessingState{})
		if err := db.Create(&orm.DocumentProcessingState{DatasetID: "manual", DocumentID: "document", ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "succeeded"}).Error; err != nil {
			t.Fatal(err)
		}
		result, err := service.ListDatasets(context.Background(), DatasetListRequest{UserID: "user-1", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Datasets) != 1 || !result.Datasets[0].Capabilities.Retrieve || !result.Datasets[0].Capabilities.Search {
			t.Fatalf("%s hides manual index: %+v", level, result.Datasets)
		}
	}
}

func TestRebuildingSourceMarksIndexStaleWithoutDeletingArtifacts(t *testing.T) {
	db := libraryKindTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	state := orm.DocumentProcessingState{DatasetID: "kb", DocumentID: "doc", ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "succeeded", ParseArtifactRef: "retained-body", ChunkArtifactRef: "retained-chunks", IndexArtifactRef: "previous-index"}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := markSourceRebuildPending(context.Background(), db.DB, "kb", []orm.Task{{ID: "rebuild", DocID: "doc"}}); err != nil {
		t.Fatal(err)
	}
	db.Where("dataset_id=? AND document_id=?", "kb", "doc").Take(&state)
	if state.IndexStatus != "pending" || state.EmbeddingVersion != embeddingVersionRebuildRequired || state.ParseArtifactRef != "retained-body" || state.ChunkArtifactRef != "retained-chunks" {
		t.Fatalf("wrong rebuild state: %+v", state)
	}
	document := orm.Document{ID: "doc", DatasetID: "kb"}
	task := orm.Task{ID: "rebuild", Ext: mustJSON(taskExt{ProcessingLevel: ProcessingLevelParsed, TaskState: "SUCCESS"})}
	snapshot := processingSnapshot{states: map[string]orm.DocumentProcessingState{"doc": state}, tasks: map[string]orm.Task{"doc": task}}
	got, _ := snapshot.observe(document)
	if got.ParseStatus != "succeeded" || got.IndexStatus != "pending" {
		t.Fatalf("parse completion restored stale vectors: %+v", got)
	}
	if textIndexReparseMode(state) != "rebuild" {
		t.Fatal("stale chunks would be reembedded instead of rebuilt")
	}
	if textIndexReparseMode(orm.DocumentProcessingState{}) != "slice_and_embed" {
		t.Fatal("ordinary manual index should reuse retained chunks")
	}
}

func TestStaleIndexDoesNotBlockDocumentReadButCannotBeRetrieved(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset", "kb", "general_algo", "owner", time.Now())
	for _, id := range []string{"fresh", "stale"} {
		if err := db.Create(&orm.Document{ID: id, LazyllmDocID: id, DatasetID: "dataset", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&orm.DocumentProcessingState{DatasetID: "dataset", DocumentID: "stale", EmbeddingVersion: embeddingVersionRebuildRequired, IndexStatus: "pending"})
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	svc.ready.Store(true)
	refs := []DocumentVisibilityRef{{KnowledgeID: "kb", DocumentID: "fresh"}, {KnowledgeID: "dataset", DocumentID: "stale"}}
	allowed, err := svc.Filter(context.Background(), DatasetCatalogCaller{UserID: "owner"}, refs)
	if err != nil || len(allowed) != 2 {
		t.Fatalf("read access lost: %v %v", allowed, err)
	}
	current, err := svc.filterCurrentIndexes(context.Background(), allowed)
	if err != nil || len(current) != 1 || current[0].DocumentID != "fresh" {
		t.Fatalf("stale vector exposed: %v %v", current, err)
	}
}

func TestLegacyRebuildCreatesFreshnessState(t *testing.T) {
	db := libraryKindTestDB(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	if err := markSourceRebuildPending(context.Background(), db.DB, "legacy", []orm.Task{{ID: "new", DocID: "document"}}); err != nil {
		t.Fatal(err)
	}
	var state orm.DocumentProcessingState
	if err := db.Where("dataset_id=? AND document_id=?", "legacy", "document").Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.EmbeddingVersion != embeddingVersionRebuildRequired || state.IndexArtifactRef != "core-task:new" {
		t.Fatalf("unfenced legacy rebuild: %+v", state)
	}
}
