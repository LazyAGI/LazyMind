package workflow

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/staticstorage"
	corestore "lazymind/core/store"
	"lazymind/core/subagent"
	workflowstore "lazymind/core/workflow/store"
)

const documentImageJobType = "document.image_export"
const documentImageExportTTL = 24 * time.Hour

type DocumentImageExportRequest struct {
	BaseRevision     *int                    `json:"base_revision" required:"true"`
	BaseDraftVersion *int64                  `json:"base_draft_version,omitempty"`
	Document         DocumentConvertSnapshot `json:"document" required:"true"`
	ImageReference   string                  `json:"image_reference,omitempty"`
}

type DocumentImageExportItem struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	Status    string `json:"status" enum:"pending,ready,failed"`
	ErrorCode string `json:"error_code,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type DocumentImageExportState struct {
	ID        string                    `json:"id"`
	Status    string                    `json:"status" enum:"pending,running,ready,partial,failed,expired"`
	Total     int                       `json:"total"`
	Completed int                       `json:"completed"`
	Ready     int                       `json:"ready"`
	Filename  string                    `json:"filename"`
	Single    bool                      `json:"single"`
	Items     []DocumentImageExportItem `json:"items"`
	ExpiresAt time.Time                 `json:"expires_at"`
}

type imageExportPayload struct {
	SessionID  string              `json:"session_id"`
	ArtifactID string              `json:"artifact_id"`
	SourceHash string              `json:"source_hash"`
	Title      string              `json:"title"`
	Single     bool                `json:"single"`
	CacheKey   string              `json:"cache_key"`
	ExpiresAt  time.Time           `json:"expires_at"`
	Images     []imageExportSource `json:"images"`
}

func imageExportRoot() string {
	return filepath.Join(subagent.WorkspaceRoot(), "_document_image_exports")
}
func imageExportUserRoot(owner string) string {
	return filepath.Join(imageExportRoot(), imageExportHash([]byte(owner)))
}
func imageExportDirectory(owner string, payload imageExportPayload) string {
	return filepath.Join(imageExportUserRoot(owner), payload.CacheKey)
}

func init() {
	asyncjob.Register(documentImageJobType, runDocumentImageExport)
	// Export files are accessible only through the owned export handlers. Never
	// grant generic static-file signatures, including the legacy subagent alias.
	for _, prefix := range []string{"document-image-exports/", "subagent/_document_image_exports/"} {
		staticstorage.Register(staticstorage.Namespace{Prefix: prefix, Root: imageExportRoot, Authorize: func(context.Context, string, string) bool { return false }})
	}
}

func imageExportState(job *orm.AsyncJob, payload imageExportPayload) DocumentImageExportState {
	state := DocumentImageExportState{ID: job.ID, Status: job.Status, Total: len(payload.Images), Completed: int(job.ProgressCurrent),
		Filename: payload.Title + "-images.zip", Single: payload.Single, ExpiresAt: payload.ExpiresAt, Items: []DocumentImageExportItem{}}
	if len(job.ResultJSON) > 0 {
		var result DocumentImageExportState
		if json.Unmarshal(job.ResultJSON, &result) == nil {
			state.Items = result.Items
			state.Ready = result.Ready
		}
	}
	if len(state.Items) == 0 {
		for _, image := range payload.Images {
			state.Items = append(state.Items, DocumentImageExportItem{ID: image.ID, Filename: image.Name, Status: "pending"})
		}
	}
	if job.Status == string(asyncjob.StatusSucceeded) {
		state.Completed = state.Total
		switch {
		case state.Ready == state.Total:
			state.Status = "ready"
		case state.Ready > 0:
			state.Status = "partial"
		default:
			state.Status = "failed"
		}
	}
	if payload.Single && len(state.Items) == 1 && state.Items[0].Status == "ready" {
		state.Filename = state.Items[0].Filename
	}
	if !time.Now().Before(payload.ExpiresAt) {
		state.Status = "expired"
	}
	return state
}

func runDocumentImageExport(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload imageExportPayload
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || !validImageExportPayload(payload) {
		return imageExportJobFailure()
	}
	if corestore.DB() == nil || workflowstore.New(corestore.DB()).AuthorizeSession(ctx, payload.SessionID, job.CreateUserID) != nil {
		return imageExportJobFailure()
	}
	session, err := GetSession(ctx, corestore.DB(), payload.SessionID)
	if err != nil || session.Dismissed || !time.Now().Before(payload.ExpiresAt) {
		return imageExportJobFailure()
	}
	directory := imageExportDirectory(job.CreateUserID, payload)
	if os.MkdirAll(directory, 0o700) != nil {
		return imageExportJobFailure()
	}
	if err := writeImageExportFile(filepath.Join(directory, ".expires"), []byte(strconv.FormatInt(payload.ExpiresAt.Unix(), 10))); err != nil {
		return imageExportJobFailure()
	}
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	state, err := buildDocumentImageExport(ctx, job.ID, directory, payload, func(item imageExportSource) ([]byte, string) {
		return fetchDocumentImage(ctx, client, job.CreateUserID, item)
	}, func(current int) error { return reporter.SetProgress(ctx, int64(current), int64(len(payload.Images))) })
	if err != nil {
		return imageExportJobFailure()
	}
	raw, err := json.Marshal(state)
	return asyncjob.Result{ResultJSON: raw}, err
}

func imageExportJobFailure() (asyncjob.Result, error) {
	return asyncjob.Result{ErrorCode: "IMAGE_EXPORT_FAILED", Permanent: true}, errors.New("image export unavailable")
}

func validImageExportPayload(payload imageExportPayload) bool {
	if len(payload.Images) == 0 || len(payload.Images) > maxDocumentImages || !documentCommitToken.MatchString(payload.CacheKey) {
		return false
	}
	for i, item := range payload.Images {
		if item.ID != fmt.Sprintf("image-%03d", i+1) {
			return false
		}
	}
	return true
}

// Ready files survive a retry; only missing/failed files are fetched again.
// Each job writes its own ZIP atomically, so concurrent readers keep a complete archive.
func buildDocumentImageExport(ctx context.Context, jobID, directory string, payload imageExportPayload, fetch func(imageExportSource) ([]byte, string), progress func(int) error) (DocumentImageExportState, error) {
	state := DocumentImageExportState{Items: []DocumentImageExportItem{}}
	if err := progress(0); err != nil {
		return state, err
	}
	var totalBytes int64
	for index, source := range payload.Images {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		item := DocumentImageExportItem{ID: source.ID, Filename: source.Name, Status: "failed"}
		path := filepath.Join(directory, source.ID+".image")
		data, err := readImageExportCache(path)
		code := ""
		if err != nil {
			data, code = fetch(source)
		}
		if code == "" && (imageFileExtension(data) == "" || int64(len(data)) > maxExportImageBytes) {
			code = "IMAGE_FORMAT_UNSUPPORTED"
		}
		if code == "" && totalBytes+int64(len(data)) > maxExportTotalBytes {
			code = "IMAGE_EXPORT_TOO_LARGE"
		}
		if code == "" && err != nil {
			if writeImageExportFile(path, data) != nil {
				code = "IMAGE_UNAVAILABLE"
			}
		}
		if code == "" {
			item.Status = "ready"
			item.Size = int64(len(data))
			totalBytes += item.Size
			item.Filename = fmt.Sprintf("%03d-%s%s", index+1, source.Name, imageFileExtension(data))
			state.Ready++
		} else {
			item.ErrorCode = code
		}
		state.Items = append(state.Items, item)
		if err := progress(index + 1); err != nil {
			return state, err
		}
	}
	if state.Ready == 0 || payload.Single {
		return state, nil
	}
	file, err := os.CreateTemp(directory, ".archive-*")
	if err != nil {
		return state, err
	}
	temp := file.Name()
	defer os.Remove(temp)
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, item := range state.Items {
		if item.Status != "ready" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return state, err
		}
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: item.Filename, Method: zip.Store})
		if err != nil {
			return state, err
		}
		data, err := readImageExportCache(filepath.Join(directory, item.ID+".image"))
		if err != nil {
			return state, err
		}
		if _, err = entry.Write(data); err != nil {
			return state, err
		}
	}
	if err := archive.Close(); err != nil {
		return state, err
	}
	if err := file.Close(); err != nil {
		return state, err
	}
	return state, os.Rename(temp, filepath.Join(directory, jobID+".zip"))
}

func readImageExportCache(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxExportImageBytes {
		return nil, errors.New("image unavailable")
	}
	return io.ReadAll(io.LimitReader(file, maxExportImageBytes+1))
}

func writeImageExportFile(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".image-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func StartDocumentImageExportCleanup(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			purgeDocumentImageExports(time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func purgeDocumentImageExports(now time.Time) {
	users, _ := os.ReadDir(imageExportRoot())
	for _, user := range users {
		if !user.IsDir() || len(user.Name()) != 64 {
			continue
		}
		root := filepath.Join(imageExportRoot(), user.Name())
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if !entry.IsDir() || !documentCommitToken.MatchString(entry.Name()) {
				continue
			}
			path := filepath.Join(root, entry.Name())
			data, err := os.ReadFile(filepath.Join(path, ".expires"))
			if err != nil {
				continue
			}
			expires, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err == nil && expires <= now.Unix() {
				_ = os.RemoveAll(path)
			}
		}
	}
}
