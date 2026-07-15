package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestNormalizeFileHashes(t *testing.T) {
	hash := strings.Repeat("A1", sha256.Size)
	got, err := normalizeFileHashes([]string{" " + hash + " ", strings.ToLower(hash)})
	if err != nil {
		t.Fatalf("normalize hashes: %v", err)
	}
	if len(got) != 1 || got[0] != strings.ToLower(hash) {
		t.Fatalf("unexpected normalized hashes: %#v", got)
	}

	if _, err := normalizeFileHashes([]string{"not-a-sha256"}); err == nil {
		t.Fatal("expected invalid hash error")
	}
	tooMany := make([]string, maxCheckFileHashes+1)
	if _, err := normalizeFileHashes(tooMany); err == nil {
		t.Fatal("expected hash batch limit error")
	}
}

func TestFindAvailableUploadedFilesByHash(t *testing.T) {
	db := newDocumentTestDB(t)
	root := t.TempDir()
	availablePath := filepath.Join(root, "available.txt")
	if err := os.WriteFile(availablePath, []byte("available"), 0o644); err != nil {
		t.Fatalf("write available file: %v", err)
	}
	now := time.Now().UTC()
	hashes := []string{
		strings.Repeat("1", sha256.Size*2),
		strings.Repeat("2", sha256.Size*2),
		strings.Repeat("3", sha256.Size*2),
	}
	rows := []orm.UploadedFile{
		newTestUploadedFile("available", "user-1", hashes[0], UploadedFileStateBound, availablePath, now),
		newTestUploadedFile("missing-path", "user-1", hashes[1], UploadedFileStateUploaded, filepath.Join(root, "missing.txt"), now),
		newTestUploadedFile("other-user", "user-2", hashes[2], UploadedFileStateUploaded, availablePath, now),
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create uploaded files: %v", err)
	}

	available, err := findAvailableUploadedFilesByHash(t.Context(), "user-1", hashes)
	if err != nil {
		t.Fatalf("find available uploaded files: %v", err)
	}
	if len(available) != 1 || available[hashes[0]].UploadFileID != "available" {
		t.Fatalf("unexpected available files: %#v", available)
	}
}

func TestCreateTaskFromContentHashCreatesTargetHardLink(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	now := time.Now().UTC()
	dataset := orm.Dataset{
		ID:          "dataset-target",
		KbID:        "dataset-target",
		DisplayName: "Target",
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "User One",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	content := []byte("same file content")
	digest := sha256.Sum256(content)
	contentHash := hex.EncodeToString(digest[:])
	sourcePath := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(sourcePath, content, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	source := newTestUploadedFile("source-upload", "user-1", contentHash, UploadedFileStateBound, sourcePath, now)
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("create source upload: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	tasks, err := createTaskFromContentHash(request, &dataset, dataset.ID, "user-1", "User One", CreateTaskItem{
		ContentHash: contentHash,
		Task: TaskPayload{
			DisplayName:  "target-name.txt",
			DocumentTags: []string{"shared"},
		},
	}, string(TaskTypeParseUploaded))
	if err != nil {
		t.Fatalf("create task from content hash: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}

	var target orm.UploadedFile
	if err := db.Where("upload_file_id <> ? AND dataset_id = ? AND content_hash = ?", source.UploadFileID, dataset.ID, contentHash).Take(&target).Error; err != nil {
		t.Fatalf("find target upload: %v", err)
	}
	if target.Status != UploadedFileStateBound || target.TaskID != tasks[0].ID {
		t.Fatalf("target upload was not bound to task: %#v", target)
	}
	var targetExt uploadedFileExt
	if err := json.Unmarshal(target.Ext, &targetExt); err != nil {
		t.Fatalf("decode target upload metadata: %v", err)
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatalf("stat source file: %v", err)
	}
	targetInfo, err := os.Stat(targetExt.StoredPath)
	if err != nil {
		t.Fatalf("stat target file: %v", err)
	}
	if !os.SameFile(sourceInfo, targetInfo) {
		t.Fatal("target file is not a hard link to the reusable source")
	}

	var unchanged orm.UploadedFile
	if err := db.Where("upload_file_id = ?", source.UploadFileID).Take(&unchanged).Error; err != nil {
		t.Fatalf("reload source upload: %v", err)
	}
	if unchanged.Status != UploadedFileStateBound || unchanged.TaskID != source.TaskID {
		t.Fatalf("source upload was modified: %#v", unchanged)
	}
}

func newTestUploadedFile(uploadFileID, userID, contentHash, status, storedPath string, now time.Time) orm.UploadedFile {
	ext := uploadedFileExt{
		StoredPath:       storedPath,
		StoredName:       filepath.Base(storedPath),
		OriginalFilename: filepath.Base(storedPath),
	}
	return orm.UploadedFile{
		UploadFileID: uploadFileID,
		ContentHash:  contentHash,
		DatasetID:    "dataset-source",
		Status:       status,
		Ext:          mustJSON(ext),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}
