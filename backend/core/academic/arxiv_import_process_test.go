package academic

import (
	"context"
	"encoding/json"
	"io"
	"lazymind/core/asyncjob"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type arxivProcessSpec struct {
	Driver, DSN, Directory, Worker, Mode, ItemID string
}

func newArxivProcessFixture(t *testing.T) (*gorm.DB, arxivProcessSpec) {
	t.Helper()
	db, batch, item, _ := importPipelineFixture(t)
	otherBatch, otherItem := batch, item
	otherBatch.ID, otherBatch.AsyncJobID = "other-batch", "other-job"
	otherItem.ID, otherItem.BatchID = "other-item", otherBatch.ID
	for _, row := range []any{&orm.AsyncJob{ID: otherBatch.AsyncJobID, Status: "running", AttemptCount: 1, PayloadJSON: marshal(map[string]string{})}, &otherBatch, &otherItem} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	spec := arxivProcessSpec{Driver: db.Dialector.Name(), Directory: t.TempDir(), ItemID: item.ID}
	if spec.Driver == "postgres" {
		var schema string
		if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
			t.Fatal(err)
		}
		dsn, err := url.Parse(os.Getenv("TEST_DB_DSN"))
		if err != nil {
			t.Fatal(err)
		}
		query := dsn.Query()
		query.Set("search_path", schema)
		dsn.RawQuery = query.Encode()
		spec.DSN = dsn.String()
	} else {
		var databases []struct{ Name, File string }
		if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
			t.Fatal(err)
		}
		for _, database := range databases {
			if database.Name == "main" {
				spec.DSN = database.File
			}
		}
	}
	return db, spec
}

func startArxivProcess(t *testing.T, ctx context.Context, spec arxivProcessSpec) *exec.Cmd {
	t.Helper()
	encoded, _ := json.Marshal(spec)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArxivImportProcessWorker$")
	command.Env = append(os.Environ(), "LAZYMIND_ARXIV_PROCESS_TEST="+string(encoded))
	output, err := os.Create(filepath.Join(spec.Directory, spec.Worker+".log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	command.Stdout, command.Stderr = output, output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	return command
}

func TestArxivImportAcrossProcesses(t *testing.T) {
	db, spec := newArxivProcessFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var commands []*exec.Cmd
	for _, worker := range []string{"first", "second"} {
		spec.Worker = worker
		if worker == "second" {
			spec.ItemID = "other-item"
		}
		commands = append(commands, startArxivProcess(t, ctx, spec))
	}
	for _, worker := range []string{"first", "second"} {
		waitForArxivProcessFile(t, ctx, filepath.Join(spec.Directory, worker+".ready"))
	}
	if err := os.WriteFile(filepath.Join(spec.Directory, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			output, _ := os.ReadFile(filepath.Join(spec.Directory, []string{"first", "second"}[i]+".log"))
			t.Errorf("worker failed: %v\n%s", err, output)
		}
	}
	downloads, err := filepath.Glob(filepath.Join(spec.Directory, "*.download"))
	if err != nil || len(downloads) != 1 {
		t.Fatalf("independent Core processes downloaded %d PDFs, want 1: %v", len(downloads), err)
	}
	var documents, links int64
	if err = db.Model(&orm.Document{}).Count(&documents).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&orm.AcademicWorkDocument{}).Count(&links).Error; err != nil {
		t.Fatal(err)
	}
	if documents != 1 || links != 1 {
		t.Fatalf("independent Core processes created documents=%d links=%d, want 1 each", documents, links)
	}
	var first, second orm.PaperImportItem
	for worker, result := range map[string]*orm.PaperImportItem{"first": &first, "second": &second} {
		encoded, err := os.ReadFile(filepath.Join(spec.Directory, worker+".result"))
		if err != nil || json.Unmarshal(encoded, result) != nil {
			t.Fatalf("read worker result: %v", err)
		}
	}
	if first.DocumentID != second.DocumentID || first.AcademicWorkID != second.AcademicWorkID {
		t.Fatalf("processes returned different attachments: %#v %#v", first, second)
	}
}

func TestArxivImportRecoversAfterImporterProcessExits(t *testing.T) {
	db, spec := newArxivProcessFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := os.WriteFile(filepath.Join(spec.Directory, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	spec.Worker, spec.Mode = "first", "exit-after-save"
	first := startArxivProcess(t, ctx, spec)
	if err := first.Wait(); err != nil {
		output, _ := os.ReadFile(filepath.Join(spec.Directory, "first.log"))
		t.Fatalf("first worker: %v\n%s", err, output)
	}
	var documentCount int64
	if err := db.Model(&orm.Document{}).Count(&documentCount).Error; err != nil || documentCount != 1 {
		t.Fatalf("process did not exit after persisting one document: %d %v", documentCount, err)
	}
	spec.Worker, spec.Mode = "second", ""
	second := startArxivProcess(t, ctx, spec)
	if err := second.Wait(); err != nil {
		output, _ := os.ReadFile(filepath.Join(spec.Directory, "second.log"))
		t.Fatalf("recovery worker: %v\n%s", err, output)
	}
	downloads, _ := filepath.Glob(filepath.Join(spec.Directory, "*.download"))
	if len(downloads) != 1 {
		t.Fatalf("retry downloaded another PDF after process exit: %v", downloads)
	}
	encoded, err := os.ReadFile(filepath.Join(spec.Directory, "second.result"))
	var recovered orm.PaperImportItem
	if err != nil || json.Unmarshal(encoded, &recovered) != nil {
		t.Fatalf("recovery result: %v", err)
	}
	var persisted orm.Document
	if err := db.Take(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.DocumentID != persisted.ID || recovered.Status != "succeeded" || recovered.DocumentTaskID == "" || len(recovered.ContentSHA256) != 64 {
		t.Fatalf("process exit recovery lost persisted attachment: %#v", recovered)
	}
}

func waitForArxivProcessFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", path, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestArxivImportProcessWorker(t *testing.T) {
	encoded := os.Getenv("LAZYMIND_ARXIV_PROCESS_TEST")
	if encoded == "" {
		t.Skip("subprocess helper")
	}
	var spec arxivProcessSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		t.Fatal(err)
	}
	db, err := orm.Connect(spec.Driver, spec.DSN)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB.DB()
	defer pool.Close()
	store.Init(db.DB, db.DB, nil)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", filepath.Join(spec.Directory, "uploads"))
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true}],"source_map":{}}`)), Request: r}, nil
	})
	if err := os.WriteFile(filepath.Join(spec.Directory, spec.Worker+".ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitForArxivProcessFile(t, t.Context(), filepath.Join(spec.Directory, "go"))
	var item orm.PaperImportItem
	if err := db.Where("id=?", spec.ItemID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var batch orm.PaperImportBatch
	if err := db.Where("id=?", item.BatchID).Take(&batch).Error; err != nil {
		t.Fatal(err)
	}
	var selected savedImportCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &selected); err != nil {
		t.Fatal(err)
	}
	guard, err := lockArxivImport(t.Context(), db.DB, selected.VerifiedPaper.ArxivID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = acquireCachedPDFLocked(guard.ctx, batch.CreatedBy, *selected.VerifiedPaper, func(context.Context, FulltextCandidate) (string, int64, error) {
		marker := filepath.Join(spec.Directory, spec.Worker+".download")
		if err := os.WriteFile(marker, nil, 0600); err != nil {
			return "", 0, err
		}
		time.Sleep(200 * time.Millisecond)
		path := filepath.Join(spec.Directory, spec.Worker+".pdf")
		content := []byte("%PDF-1.7\nshared original PDF\n%%EOF")
		return path, int64(len(content)), os.WriteFile(path, content, 0600)
	})
	guard.release()
	if err != nil {
		t.Fatal(err)
	}
	service, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(ctx context.Context, caller doc.DatasetCatalogCaller, dataset, task string) (doc.StartTaskResult, error) {
		if spec.Mode == "exit-after-save" {
			os.Exit(0)
		}
		return doc.SubmitOriginalTask(ctx, caller, dataset, task)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := processImportItem(t.Context(), asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}, batch, item, sourceImportCaller(batch), service); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	encodedResult, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(spec.Directory, spec.Worker+".result"), encodedResult, 0600); err != nil {
		t.Fatal(err)
	}
}
