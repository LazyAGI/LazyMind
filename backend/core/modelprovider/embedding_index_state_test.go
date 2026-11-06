package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEmbeddingIndexStatusReportsLegacyIndexesAsUnknown(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{})
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "kb-old", KbID: "kb-old", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	state := orm.DocumentProcessingState{DatasetID: dataset.ID, DocumentID: "doc", IndexStatus: "succeeded", UpdatedAt: now}
	for _, row := range []any{&dataset, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	result := getEmbeddingIndexStatus(t)
	if result.Status != "unknown" || result.IndexedKnowledgeBases != 1 || result.AffectedKnowledgeBases != 1 || result.PendingKnowledgeBases != 0 {
		t.Fatalf("legacy vectors must not be treated as signed: %+v", result)
	}
}

func TestEmbeddingIndexStatusCountsLegacyIndexedDatasetWithoutDocumentState(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{})
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "kb-legacy", KbID: "kb-legacy", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	result := getEmbeddingIndexStatus(t)
	if result.Status != "unknown" || result.IndexedKnowledgeBases != 1 || result.AffectedKnowledgeBases != 1 {
		t.Fatalf("legacy indexed dataset without per-document state must remain protected: %+v", result)
	}
}

func TestEmbeddingIndexStatusTreatsBlankLegacyLevelAsIndexed(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{})
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "kb-blank", KbID: "kb-blank", ProcessingLevel: "", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "kb-blank").Update("processing_level", "").Error; err != nil {
		t.Fatal(err)
	}
	result := getEmbeddingIndexStatus(t)
	if result.Status != "unknown" || result.AffectedKnowledgeBases != 1 {
		t.Fatalf("blank legacy level defaults to indexed and must be protected: %+v", result)
	}
}

func TestEmbeddingIndexStatusReportsActiveModelAndRebuildProgress(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{})
	now := time.Now().UTC()
	if err := db.Create(&orm.EmbeddingIndexState{Scope: "global", ActiveModelID: "model-new", ActiveSource: "own", ActiveModelName: "Embed New", ActiveProviderName: "Supplier", Signature: "v1:1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Phase: "ready", Generation: 2, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"kb-a", "kb-b"} {
		if err := db.Create(&orm.Dataset{ID: id, KbID: id, ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ kb, status string }{{"kb-a", "succeeded"}, {"kb-b", "pending"}} {
		if err := db.Create(&orm.DocumentProcessingState{DatasetID: item.kb, DocumentID: item.kb + "-doc", IndexStatus: item.status, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}

	result := getEmbeddingIndexStatus(t)
	if result.Status != "rebuilding" || result.ModelName != "Embed New" || result.ProviderName != "Supplier" ||
		result.IndexedKnowledgeBases != 1 || result.AffectedKnowledgeBases != 2 || result.PendingKnowledgeBases != 1 {
		t.Fatalf("wrong embedding rebuild status: %+v", result)
	}
}

func TestEmbeddingIndexStatusReportsCompletedOfflineClear(t *testing.T) {
	db, _, _, _ := setupProbeTest(t, &orm.EmbeddingIndexState{}, &orm.Dataset{}, &orm.DocumentProcessingState{})
	now := time.Now().UTC()
	if err := db.Create(&orm.EmbeddingIndexState{Scope: "global", Phase: "cleared", Generation: 3, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Dataset{ID: "kb-cleared", KbID: "kb-cleared", ProcessingLevel: "indexed", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.DocumentProcessingState{DatasetID: "kb-cleared", DocumentID: "doc", IndexStatus: "pending", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	result := getEmbeddingIndexStatus(t)
	if result.Status != "cleared" || result.IndexedKnowledgeBases != 0 || result.AffectedKnowledgeBases != 1 || result.PendingKnowledgeBases != 1 {
		t.Fatalf("cleared vector space must remain visible while documents await reindex: %+v", result)
	}
}

func getEmbeddingIndexStatus(t *testing.T) EmbeddingIndexStatusResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/model_providers/embedding/index-state", nil)
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	GetEmbeddingIndexState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data EmbeddingIndexStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
