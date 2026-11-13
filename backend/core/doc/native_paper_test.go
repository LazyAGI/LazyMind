package doc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"lazymind/core/common/orm"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPaperWorkerParsesWithoutRAGOrEmbedding(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentServiceDataset(t, db, "papers", "user-1", time.Now().UTC())
	if err := db.Model(&orm.Dataset{}).Where("id=?", "papers").Updates(map[string]any{"scene": "academic_papers", "processing_level": "parsed"}).Error; err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(source, []byte("original PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	document := orm.Document{ID: "paper", DatasetID: "papers", DisplayName: "paper.pdf", Ext: mustJSON(documentExt{StoredPath: source})}
	task := orm.Task{ID: "prepare", DatasetID: "papers", DocID: "paper", TaskType: string(TaskTypeParse)}
	state := newDocumentProcessingState("papers", "paper", time.Now().UTC())
	state.ParseStatus = "running"
	for _, row := range []any{&document, &task, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/documents:prepare" {
			t.Fatalf("unexpected optional-service request: %s", r.URL.Path)
		}
		calls++
		artifact := baseArtifact{SourceSHA: fmt.Sprintf("%x", sha256.Sum256([]byte("original PDF"))), ParserVersion: "native", ChunkerVersion: "base", Roots: []baseNode{{Text: "paper body"}}, Chunks: []baseNode{{Text: "paper body"}}}
		if err := os.WriteFile(source+baseArtifactSuffix, mustJSON(artifact), 0600); err != nil {
			t.Fatal(err)
		}
		return testJSONResponse(200, string(mustJSON(map[string]any{"artifact_ref": source + baseArtifactSuffix, "source_sha256": artifact.SourceSHA, "parser_version": "native", "chunker_version": "base"}))), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	ctx := context.WithValue(context.Background(), paperProcessingWorker{}, true)
	results, err := startParseTasksAtLevel(ctx, "user-1", "papers", []string{"prepare"}, ProcessingLevelParsed)
	if err != nil || len(results) != 1 || results[0].Status != "STARTED" || calls != 1 {
		t.Fatalf("native paper: %+v %v calls=%d", results, err, calls)
	}
	db.Where("dataset_id=? AND document_id=?", "papers", "paper").Take(&state)
	if state.ParseStatus != "succeeded" || state.ParseArtifactRef == "" {
		t.Fatalf("missing readable artifact: %+v", state)
	}
	artifact, err := readBaseArtifact(ctx, documentServiceRecord{row: document, ext: documentExt{StoredPath: source}})
	if err != nil || artifact == nil || artifact.Roots[0].Text != "paper body" {
		t.Fatalf("reading preparation cannot consume roots: %+v %v", artifact, err)
	}
}

func TestNonIndexedTaskStillCapturesCurrentGeneration(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.EmbeddingIndexState{})
	db.Create(&orm.EmbeddingIndexState{Scope: "global", Generation: 8, Phase: "detached"})
	for _, level := range []string{ProcessingLevelParsed, ProcessingLevelChunked} {
		config, _ := loadParsingLLMConfig(context.Background(), db.DB, "user-1", level)
		if config["_embedding_generation"] != int64(8) {
			t.Fatalf("%s lost generation: %+v", level, config)
		}
	}
}
