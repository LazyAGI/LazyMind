package doc

import (
	"lazymind/core/common/orm"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMissingParseFilesAreStructuredFailures(t *testing.T) {
	for _, scenario := range []string{"artifact", "source", "running"} {
		t.Run(scenario, func(t *testing.T) {
			db := newDocumentTestDB(t)
			seedDocumentServiceDataset(t, db, "dataset-1", "user-1", time.Now().UTC())
			source := filepath.Join(t.TempDir(), "private-paper.pdf")
			seedDocumentServiceDocument(t, db, "dataset-1", "doc-1", "user-1", documentExt{StoredPath: source})
			state := newDocumentProcessingState("dataset-1", "doc-1", time.Now().UTC())
			state.ParseStatus = "succeeded"
			if scenario == "running" {
				state.ParseStatus = "running"
			}
			state.ParseArtifactRef = source + baseArtifactSuffix
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			if scenario == "source" {
				if err := os.WriteFile(state.ParseArtifactRef, []byte(`{"source_sha":"old"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := mustDocumentService(t, db).EnsureDocumentParsed(httptest.NewRequest("GET", "/", nil), EnsureDocumentParsedRequest{ObserveOnly: true, UserID: "user-1", DatasetID: "dataset-1", DocumentID: "doc-1"})
			if scenario == "running" {
				if err != nil || result.Status != "parsing" || result.Error != nil {
					t.Fatalf("active task masked: %+v %v", result, err)
				}
				return
			}
			if err != nil || result.Status != "failed" || result.Error == nil || result.Error.Code != "FILE_NOT_FOUND" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(result.Error.Message, source) {
				t.Fatal("private path leaked")
			}
			var count int64
			db.Model(&orm.Task{}).Count(&count)
			if count != 0 {
				t.Fatal("observation started retry")
			}
		})
	}
}

func TestMissingFileDiagnosticHidesPlatformPaths(t *testing.T) {
	for _, raw := range []string{`[WinError 3] 系统找不到指定的路径。: 'C://Users//Administrator//private//paper.pdf'`, `open /Users/private/paper.pdf: no such file or directory`, `[WinError 2] The system cannot find the file specified: 'C:\private\paper.pdf'`} {
		message := SafeProcessingMessage(raw)
		if strings.Contains(message, "private") || !strings.Contains(message, "文件") {
			t.Fatalf("unsafe message: %s", message)
		}
	}
	const network = "MinerU network connection failed; check DNS, proxy and service connectivity"
	if SafeProcessingMessage(network) != network {
		t.Fatal("network failure misclassified")
	}
}

func TestMissingFileProcessingDiagnosticCode(t *testing.T) {
	diagnostic := processingDiagnostic(orm.DocumentProcessingState{}, orm.Task{ID: "task"}, "FAILED", "", `[WinError 3] missing: 'C:\private\paper.pdf'`)
	if diagnostic == nil || diagnostic.Code != "FILE_NOT_FOUND" || diagnostic.TaskID != "task" || strings.Contains(diagnostic.Message, "private") {
		t.Fatalf("diagnostic: %+v", diagnostic)
	}
}
