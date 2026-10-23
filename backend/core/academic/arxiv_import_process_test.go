package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type arxivProcessSpec struct {
	Driver, DSN, Directory, Worker, Mode string
}

func newArxivProcessFixture(t *testing.T) (*gorm.DB, arxivProcessSpec) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.Dataset{}, &orm.Document{}, &orm.Task{}, &orm.DocumentProcessingState{}, &orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}).DB
	if err := db.Create(&orm.Dataset{ID: "dataset", ProcessingLevel: "stored"}).Error; err != nil {
		t.Fatal(err)
	}
	spec := arxivProcessSpec{Driver: db.Dialector.Name(), Directory: t.TempDir()}
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
	var first, second ArxivImportResult
	for worker, result := range map[string]*ArxivImportResult{"first": &first, "second": &second} {
		encoded, err := os.ReadFile(filepath.Join(spec.Directory, worker+".result"))
		if err != nil || json.Unmarshal(encoded, result) != nil {
			t.Fatalf("read worker result: %v", err)
		}
	}
	if first.DocumentID != second.DocumentID || first.WorkID != second.WorkID {
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
	var recovered ArxivImportResult
	if err != nil || json.Unmarshal(encoded, &recovered) != nil {
		t.Fatalf("recovery result: %v", err)
	}
	if recovered.DocumentID != "doc-first" || recovered.Status != "already_present" || recovered.DocumentTaskID != "task-first" || len(recovered.ContentSHA256) != 64 {
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
	svc := newArxivImportService(db.DB)
	svc.lookup = func(ctx context.Context, _ string) (arxivsource.Metadata, error) {
		waitForArxivProcessFile(t, ctx, filepath.Join(spec.Directory, "go"))
		return arxivsource.Metadata{BaseID: "2404.06654", Version: "v2", Title: "Shared import", Authors: []string{"Author"}}, nil
	}
	svc.acquire = func(context.Context, FulltextCandidate) (string, int64, error) {
		path := filepath.Join(spec.Directory, spec.Worker+".download")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			return "", 0, err
		}
		// Keep both processes inside the download window without a shared lock.
		time.Sleep(200 * time.Millisecond)
		pdf := filepath.Join(spec.Directory, spec.Worker+".pdf")
		return pdf, 12, os.WriteFile(pdf, []byte("%PDF-1.7\nPDF"), 0600)
	}
	svc.importFile = func(ctx context.Context, input doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
		contents, err := os.ReadFile(input.SourcePath)
		if err != nil {
			return doc.LocalFileImportResult{}, err
		}
		stored := filepath.Join(spec.Directory, spec.Worker+"-stored.pdf")
		if err = os.WriteFile(stored, contents, 0600); err != nil {
			return doc.LocalFileImportResult{}, err
		}
		ext, _ := json.Marshal(map[string]any{"stored_path": stored})
		tags, _ := json.Marshal(input.Tags)
		document := orm.Document{ID: "doc-" + spec.Worker, DatasetID: input.DatasetID, DisplayName: input.DisplayName, Ext: ext, Tags: tags}
		if err = db.WithContext(ctx).Create(&document).Error; err != nil {
			return doc.LocalFileImportResult{}, err
		}
		task := orm.Task{ID: "task-" + spec.Worker, DocID: document.ID, DatasetID: input.DatasetID, Ext: json.RawMessage(`{"task_state":"SUCCEEDED","data_source_type":"ARXIV_IMPORT"}`)}
		if err = db.WithContext(ctx).Create(&task).Error; err != nil {
			return doc.LocalFileImportResult{}, err
		}
		if spec.Mode == "exit-after-save" {
			// Simulate a crashed Core: skip all deferred lock and file cleanup.
			os.Exit(0)
		}
		return doc.LocalFileImportResult{DocumentID: document.ID, TaskID: task.ID, ContentSHA256: strings.Repeat("a", 64)}, nil
	}
	if err := os.WriteFile(filepath.Join(spec.Directory, spec.Worker+".ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := svc.importPaper(t.Context(), "2404.06654v2", "dataset", "user", "User")
	if err != nil {
		t.Fatal(fmt.Errorf("import: %w", err))
	}
	encodedResult, _ := json.Marshal(result)
	if err = os.WriteFile(filepath.Join(spec.Directory, spec.Worker+".result"), encodedResult, 0600); err != nil {
		t.Fatal(err)
	}
}
