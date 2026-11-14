package doc

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestCreateTaskPartialBatchContinuesAfterInvalidPDF(t *testing.T) {
	db := libraryKindTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds-kind", "ds-kind", "general_algo", "owner", time.Now().UTC())
	files := []struct{ name, body string }{{"good.md", "text"}, {"table.csv", "a,b\n1,2"}, {"blank.txt", " \t\r\n"}, {"broken.pdf", "%PDF-1.4\nbroken"}, {"last.ipynb", `{"nbformat":4,"cells":[]}`}}
	items := []map[string]any{}
	for _, f := range files {
		id := f.name
		path := filepath.Join(root, f.name)
		if err := os.WriteFile(path, []byte(f.body), 0600); err != nil {
			t.Fatal(err)
		}
		row := newTestUploadedFile(id, "owner", "", UploadedFileStateUploaded, path, time.Now().UTC())
		row.DatasetID = "ds-kind"
		var meta uploadedFileExt
		json.Unmarshal(row.Ext, &meta)
		meta.OriginalFilename, meta.FileSize = f.name, int64(len(f.body))
		row.Ext = mustJSON(meta)
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		items = append(items, map[string]any{"upload_file_id": id, "task": map[string]any{"display_name": f.name, "relative_path": "same-folder/" + f.name}})
	}
	body := mustJSON(map[string]any{"allow_partial": true, "items": items})
	w := libraryKindRequest(t, CreateTask, "POST", "/api/core/datasets/ds-kind/tasks", string(body), "owner")
	if w.Code != 200 {
		t.Fatalf("partial results lost: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Tasks    []TaskResponse `json:"tasks"`
		Outcomes []struct {
			InputIndex  int      `json:"input_index"`
			TargetPID   string   `json:"target_pid"`
			Status      string   `json:"status"`
			DisplayName string   `json:"display_name"`
			Message     string   `json:"message"`
			TaskIDs     []string `json:"task_ids"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 4 || len(result.Outcomes) != 5 {
		t.Fatalf("missing outcomes: %s", w.Body.String())
	}
	for i, outcome := range result.Outcomes {
		if outcome.InputIndex != i || outcome.DisplayName != files[i].name {
			t.Fatalf("wrong association: %+v", outcome)
		}
		if i == 3 {
			if outcome.Status != "FAILED" || !strings.Contains(outcome.Message, "PDF") {
				t.Fatalf("missing reason: %+v", outcome)
			}
		} else if outcome.Status != "CREATED" || len(outcome.TaskIDs) != 1 {
			t.Fatalf("not created: %+v", outcome)
		}
	}
	var count int64
	db.Model(&orm.Document{}).Where("document_type <> ?", "FOLDER").Count(&count)
	if count != 4 {
		t.Fatalf("documents=%d", count)
	}
	var last orm.Document
	if err := db.Where("display_name=?", "last.ipynb").Take(&last).Error; err != nil {
		t.Fatal("last file was skipped", err)
	}
	if result.Outcomes[3].TargetPID == "" {
		t.Fatal("failed folder item lost target parent")
	}
	retry := mustJSON(map[string]any{"allow_partial": true, "items": []any{map[string]any{"upload_file_id": "broken.pdf", "task": map[string]any{"display_name": "broken.pdf", "relative_path": "same-folder/broken.pdf", "document_pid": result.Outcomes[3].TargetPID}}}})
	again := libraryKindRequest(t, CreateTask, "POST", "/api/core/datasets/ds-kind/tasks", string(retry), "owner")
	if again.Code != 200 {
		t.Fatal(again.Body.String())
	}
	db.Model(&orm.Document{}).Where("document_type = ?", "FOLDER").Count(&count)
	if count != 1 {
		t.Fatalf("retry duplicated folder: %d", count)
	}

}

func TestUploadZeroByteReportsFilenameAndReason(t *testing.T) {
	db := libraryKindTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds-kind", "ds-kind", "general_algo", "owner", time.Now().UTC())
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.CreateFormFile("files", "empty.txt")
	writer.Close()
	r := httptest.NewRequest(http.MethodPost, "/", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-User-Id", "owner")
	r = mux.SetURLVars(r, map[string]string{"dataset": "ds-kind"})
	w := httptest.NewRecorder()
	UploadFile(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "empty.txt") || !strings.Contains(w.Body.String(), "空文件") {
		t.Fatalf("lost empty-file diagnosis: %d %s", w.Code, w.Body.String())
	}
	for _, model := range []any{&orm.UploadedFile{}, &orm.Document{}, &orm.Task{}} {
		var n int64
		db.Model(model).Count(&n)
		if n != 0 {
			t.Fatalf("empty upload wrote %T: %d", model, n)
		}
	}
}

func TestCreateTaskPartialZIPRetainsMembersAndContinues(t *testing.T) {
	db := libraryKindTestDB(t)
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	seedDocumentServiceDatasetWithKBAndAlgo(t, db, "ds-kind", "ds-kind", "general_algo", "owner", time.Now().UTC())
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for _, f := range []struct{ name, body string }{{"first.txt", "first"}, {"broken.pdf", "%PDF-1.4 no EOF"}, {"last.txt", "last"}} {
		w, err := archive.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f.body))
	}
	archive.Close()
	path := filepath.Join(root, "mixed.zip")
	os.WriteFile(path, data.Bytes(), 0600)
	sum := sha256.Sum256(data.Bytes())
	hash := hex.EncodeToString(sum[:])
	row := newTestUploadedFile("zip", "owner", hash, UploadedFileStateUploaded, path, time.Now().UTC())
	row.DatasetID = "ds-kind"
	var meta uploadedFileExt
	json.Unmarshal(row.Ext, &meta)
	meta.OriginalFilename = "mixed.zip"
	meta.FileSize = int64(data.Len())
	row.Ext = mustJSON(meta)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	w := libraryKindRequest(t, CreateTask, "POST", "/api/core/datasets/ds-kind/tasks", `{"allow_partial":true,"items":[{"upload_file_id":"zip","task":{"display_name":"mixed.zip"}}]}`, "owner")
	var result struct {
		Tasks    []TaskResponse `json:"tasks"`
		Outcomes []struct {
			Status  string   `json:"status"`
			Message string   `json:"message"`
			TaskIDs []string `json:"task_ids"`
		} `json:"outcomes"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Tasks) != 2 || len(result.Outcomes) != 1 || result.Outcomes[0].Status != "PARTIAL" || len(result.Outcomes[0].TaskIDs) != 2 || !strings.Contains(result.Outcomes[0].Message, "broken.pdf") {
		t.Fatalf("lost ZIP partial result: %d %s", w.Code, w.Body.String())
	}
	cachedBody := mustJSON(map[string]any{"allow_partial": true, "items": []any{map[string]any{"content_hash": hash, "task": map[string]any{"display_name": "cached.zip"}}}})
	cached := libraryKindRequest(t, CreateTask, "POST", "/api/core/datasets/ds-kind/tasks", string(cachedBody), "owner")
	json.Unmarshal(cached.Body.Bytes(), &result)
	if cached.Code != 200 || len(result.Tasks) != 2 || result.Outcomes[0].Status != "PARTIAL" {
		t.Fatalf("hash-reused ZIP lost partial members: %d %s", cached.Code, cached.Body.String())
	}

}
