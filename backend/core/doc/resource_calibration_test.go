package doc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Explicit opt-in: this writes roughly 100 MiB of disposable originals per run.
// It measures local SQLite/real disk copy, never parser/index or sqliteproxy latency.
func TestResourceBudgetCalibration(t *testing.T) {
	if os.Getenv("LAZYMIND_RESOURCE_BUDGET_CALIBRATION") != "1" {
		t.Skip("set LAZYMIND_RESOURCE_BUDGET_CALIBRATION=1 for same-volume disk calibration")
	}
	if driver := os.Getenv("TEST_DB_DRIVER"); driver != "" && driver != "sqlite" {
		t.Fatal("calibration requires direct SQLite")
	}
	root, err := os.MkdirTemp(".", ".resource-budget-calibration-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("TMPDIR", root)
	const totalBytes = 32 << 20
	const documents = 100
	t.Logf("environment go=%s os=%s arch=%s temporary_root=%s processing=stored source_policy=in_process_fixture parser=index=not_measured", runtime.Version(), runtime.GOOS, runtime.GOARCH, root)
	for run := 1; run <= 3; run++ {
		t.Run(fmt.Sprintf("run-%d", run), func(t *testing.T) {
			ctx := context.Background()
			s, db, op := resourceFixture(t)
			var databaseRows []struct {
				Seq  int
				Name string
				File string
			}
			if err := db.Raw("PRAGMA database_list").Scan(&databaseRows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range databaseRows {
				if row.Name == "main" {
					relative, err := filepath.Rel(root, row.File)
					if err != nil || !filepath.IsLocal(relative) {
						t.Fatalf("database not on the requested same-volume temporary path: %s", row.File)
					}
				}
			}
			var journal string
			var synchronous int
			db.Raw("PRAGMA journal_mode").Scan(&journal)
			db.Raw("PRAGMA synchronous").Scan(&synchronous)
			// Include the actual deployment's resource version trigger overhead.
			migration, err := os.ReadFile("../migrations/dev_mode/v0_3/20261020034257_knowledge_resource_commands.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			marker := "-- +migrate Dialect sqlite\n"
			start := strings.Index(string(migration), marker)
			if start < 0 {
				t.Fatal("SQLite version triggers missing from migration")
			}
			if err = db.Exec(string(migration[start+len(marker):])).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Model(&orm.Dataset{}).Where("id IN ?", []string{"source", "target"}).Update("processing_level", ProcessingLevelStored).Error; err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYMIND_UPLOAD_ROOT", filepath.Join(t.TempDir(), "originals"))
			originals, err := NewOriginalService(OriginalServiceDeps{DB: db.DB})
			if err != nil {
				t.Fatal(err)
			}
			s.originals = originals
			s.budget.Calibrated = true // This isolated fixture is not a deployment configuration.
			folders := []string{}
			for i := 0; i < 20; i++ {
				folders = append(folders, fmt.Sprintf("root/empty-%02d", i))
			}
			made, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: folders})
			if err != nil {
				t.Fatal(err)
			}
			sourceRoot := made.Items[0].ResourceRef
			imports := make([]ImportOriginalRequest, 0, documents)
			expectedHashes := map[string][32]byte{}
			random := rand.New(rand.NewSource(int64(run)))
			for i := 0; i < documents; i++ {
				size := totalBytes / documents
				if i < totalBytes%documents {
					size++
				}
				data := make([]byte, size)
				for j := range data {
					data[j] = byte(' ' + random.Intn(95))
				}
				name := fmt.Sprintf("file-%03d.txt", i)
				expectedHashes[name] = sha256.Sum256(data)
				original, err := originals.AcquireOriginal(ctx, op.Caller, AcquireOriginalRequest{Filename: name, ContentType: "text/plain", Reader: bytes.NewReader(data)})
				if err != nil {
					t.Fatal(err)
				}
				imports = append(imports, ImportOriginalRequest{FileRef: original.FileRef, DatasetID: "source", ParentID: sourceRoot.ID(), Tags: []string{"calibration", fmt.Sprintf("group-%d", i%5)}})
			}
			uploaded, err := s.Upload(ctx, op, imports)
			if err != nil || uploaded.Summary.Succeeded != documents {
				t.Fatalf("fixture upload: %+v %v", uploaded.Summary, err)
			}
			for _, item := range uploaded.Items {
				if item.ProcessingStatus != "not_requested" {
					t.Fatalf("unexpected parsing during calibration: %+v", item)
				}
			}
			request := MoveResourcesRequest{Sources: []ResourceRef{sourceRoot}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
			snapshot, err := s.SnapshotMove(ctx, op, request)
			if err != nil || snapshot.TotalBytes != totalBytes || snapshot.DocumentCount != documents || snapshot.FolderCount != 21 {
				t.Fatalf("bad calibration shape: %+v %v", snapshot, err)
			}
			op.ExpectedSnapshot = snapshot.Digest
			op.OperationKey = fmt.Sprintf("calibration-%d", run)
			timing := &resourceCalibrationTiming{}
			measuredDB := db.DB.Session(&gorm.Session{NewDB: true, Context: ctx})
			measuredDB.Statement.ConnPool = &resourceCalibrationPool{ConnPool: db.Statement.ConnPool, timing: timing}
			s.db = measuredDB
			begin := time.Now()
			moved, err := s.Move(ctx, op, request)
			elapsed := time.Since(begin)
			if err != nil || len(moved.Items) != 1 || moved.Summary.Succeeded != 1 || moved.Items[0].FolderCount != 21 || len(moved.Items[0].IDMapping) != 121 {
				t.Fatalf("calibration move: %+v %v", moved, err)
			}
			if timing.started.IsZero() || timing.ended.IsZero() {
				t.Fatal("resource transaction was not measured")
			}
			preparation := timing.started.Sub(begin)
			transaction := timing.ended.Sub(timing.started)
			postCommit := elapsed - preparation - transaction
			// Read back every copied original and verify its bytes, outside measured move time.
			var targetRows []orm.Document
			if err = db.Where("dataset_id = ? AND deleted_at IS NULL", "target").Find(&targetRows).Error; err != nil {
				t.Fatal(err)
			}
			var copied int64
			ids := []string{}
			for _, row := range targetRows {
				if isFolderLikeDocument(row) {
					continue
				}
				ids = append(ids, row.ID)
				var ext documentExt
				if err = json.Unmarshal(row.Ext, &ext); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(ext.StoredPath)
				if err != nil {
					t.Fatal(err)
				}
				if sha256.Sum256(data) != expectedHashes[row.DisplayName] {
					t.Fatalf("copied original hash mismatch: %s", row.DisplayName)
				}
				copied += int64(len(data))
			}
			if copied != totalBytes {
				t.Fatalf("copied %d bytes, want %d", copied, totalBytes)
			}
			catalog, _ := NewDatasetCatalogService(DatasetCatalogServiceDeps{DB: db.DB})
			documentsCatalog := NewDocumentCatalog(db.DB, catalog)
			queryStart := time.Now()
			page, err := documentsCatalog.List(ctx, op.Caller, DocumentCatalogQuery{DatasetIDs: []string{"target"}, ResourceType: "document", Limit: 100})
			catalogElapsed := time.Since(queryStart)
			if err != nil || page.Total != documents || len(page.Rows) != documents {
				t.Fatalf("catalog: count=%d rows=%d %v", page.Total, len(page.Rows), err)
			}
			queryStart = time.Now()
			metadata, _, err := loadMergedDocumentsWithDB(ctx, db.DB, db.DB, ids, "target", "", "", false, 100, 0)
			metadataElapsed := time.Since(queryStart)
			if err != nil || len(metadata) != documents {
				t.Fatalf("metadata: rows=%d %v", len(metadata), err)
			}
			queryStart = time.Now()
			aggregate, err := AggregateMetadata(ctx, db.DB, db.DB, op.Caller, AggregateDocumentsRequest{DatasetIDs: []string{"target"}, GroupBy: []string{"file_type", "document_stage", "tag"}}, AggregateBudget{})
			aggregateElapsed := time.Since(queryStart)
			if err != nil || aggregate.TotalCount != documents || aggregate.TotalSize != totalBytes {
				t.Fatalf("aggregate: %+v %v", aggregate, err)
			}
			queryStart = time.Now()
			stats := calcDatasetStatsWithDB(ctx, db.DB, "target")
			statsElapsed := time.Since(queryStart)
			if stats.DocumentCount != documents || stats.DocumentSize != totalBytes {
				t.Fatalf("stats: %+v", stats)
			}
			result := map[string]any{"run": run, "documents": documents, "folders": 21, "empty_folders": 20, "bytes": copied, "journal": journal, "synchronous": synchronous, "prepare_and_copy_ms": float64(preparation) / float64(time.Millisecond), "root_transaction_including_commit_ms": float64(transaction) / float64(time.Millisecond), "post_commit_ms": float64(postCommit) / float64(time.Millisecond), "move_total_ms": float64(elapsed) / float64(time.Millisecond), "effective_prepare_mib_per_second": 32 / preparation.Seconds(), "catalog_100_ms": float64(catalogElapsed) / float64(time.Millisecond), "metadata_100_ms": float64(metadataElapsed) / float64(time.Millisecond), "aggregate_ms": float64(aggregateElapsed) / float64(time.Millisecond), "stats_ms": float64(statsElapsed) / float64(time.Millisecond)}
			raw, _ := json.Marshal(result)
			t.Logf("CALIBRATION %s", raw)
		})
	}
}

type resourceCalibrationTiming struct{ started, ended time.Time }
type resourceCalibrationPool struct {
	gorm.ConnPool
	timing *resourceCalibrationTiming
}

func (p *resourceCalibrationPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	// Later cleanup/receipt updates also use GORM transactions. Keep the first
	// transaction, which is the atomic root move, instead of overwriting it.
	if !p.timing.started.IsZero() {
		tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, options)
		return tx, err
	}
	p.timing.started = time.Now()
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &resourceCalibrationTx{Tx: tx, timing: p.timing}, nil
}

type resourceCalibrationTx struct {
	*sql.Tx
	timing *resourceCalibrationTiming
}

func (tx *resourceCalibrationTx) Commit() error {
	err := tx.Tx.Commit()
	tx.timing.ended = time.Now()
	return err
}
