package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	pdfArtifactSearchable  = "SEARCHABLE_PDF"
	pdfArtifactTranslation = "TRANSLATION_PDF"
	pdfJobReady            = "READY"
	pdfJobRunning          = "RUNNING"
	pdfJobWaiting          = "WAITING_DEPENDENCY"
)

type createPDFJobRequest struct {
	TargetLanguage string `json:"target_language,omitempty"`
	ProviderType   string `json:"provider_type,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	OptionsHash    string `json:"options_hash,omitempty"`
	Force          bool   `json:"force,omitempty"`
}

func pdfCacheKey(state orm.DocumentProcessingState, kind string, req createPDFJobRequest) string {
	value := strings.Join([]string{kind, state.SourceFingerprint, state.ParseFingerprint, state.ParserVersion,
		strings.ToLower(req.TargetLanguage), strings.ToLower(req.ProviderType), req.Provider, req.Model, req.OptionsHash, "pdf-render-v6"}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func loadPDFDocument(r *http.Request, write bool) (orm.Document, documentExt, orm.DocumentProcessingState, bool) {
	datasetID, documentID := datasetIDFromPath(r), documentIDFromPath(r)
	permission := acl.PermissionDatasetRead
	if write {
		permission = acl.PermissionDatasetWrite
	}
	if datasetID == "" || documentID == "" {
		return orm.Document{}, documentExt{}, orm.DocumentProcessingState{}, false
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, permission); !ok {
		return orm.Document{}, documentExt{}, orm.DocumentProcessingState{}, false
	}
	row, ext, err := loadDocumentFileMeta(r.Context(), datasetID, documentID)
	if err != nil {
		return orm.Document{}, documentExt{}, orm.DocumentProcessingState{}, false
	}
	var state orm.DocumentProcessingState
	_ = store.DB().WithContext(r.Context()).Where("dataset_id = ? AND document_id = ?", datasetID, documentID).Take(&state).Error
	return row, ext, state, true
}

func saveDocumentExt(r *http.Request, row orm.Document, ext documentExt) error {
	raw, err := json.Marshal(ext)
	if err != nil {
		return err
	}
	return store.DB().WithContext(r.Context()).Model(&orm.Document{}).
		Where("id = ? AND dataset_id = ?", row.ID, row.DatasetID).Update("ext", raw).Error
}

func latestArtifact(ext documentExt, kind string) *pdfArtifactRecord {
	for i := len(ext.PDFArtifacts) - 1; i >= 0; i-- {
		if ext.PDFArtifacts[i].Kind == kind {
			artifact := ext.PDFArtifacts[i]
			return &artifact
		}
	}
	return nil
}

func latestArtifactWithCacheKey(ext documentExt, kind, cacheKey string) *pdfArtifactRecord {
	for i := len(ext.PDFArtifacts) - 1; i >= 0; i-- {
		if ext.PDFArtifacts[i].Kind == kind && ext.PDFArtifacts[i].CacheKey == cacheKey {
			artifact := ext.PDFArtifacts[i]
			return &artifact
		}
	}
	return nil
}

func findArtifact(ext documentExt, id string) *pdfArtifactRecord {
	for i := range ext.PDFArtifacts {
		if ext.PDFArtifacts[i].ID == id {
			artifact := ext.PDFArtifacts[i]
			return &artifact
		}
	}
	return nil
}

func replaceArtifactWithSameCacheKey(artifacts []pdfArtifactRecord, replacement pdfArtifactRecord) ([]pdfArtifactRecord, []pdfArtifactRecord) {
	kept := make([]pdfArtifactRecord, 0, len(artifacts)+1)
	replaced := make([]pdfArtifactRecord, 0)
	for _, artifact := range artifacts {
		if artifact.Kind == replacement.Kind && artifact.CacheKey == replacement.CacheKey {
			replaced = append(replaced, artifact)
			continue
		}
		kept = append(kept, artifact)
	}
	return append(kept, replacement), replaced
}

func removeArtifactCacheEntry(artifacts []pdfArtifactRecord, target pdfArtifactRecord) ([]pdfArtifactRecord, []pdfArtifactRecord) {
	kept := make([]pdfArtifactRecord, 0, len(artifacts))
	removed := make([]pdfArtifactRecord, 0)
	for _, artifact := range artifacts {
		if artifact.Kind == target.Kind && artifact.CacheKey == target.CacheKey {
			removed = append(removed, artifact)
			continue
		}
		kept = append(kept, artifact)
	}
	return kept, removed
}

func removeArtifactFiles(artifacts []pdfArtifactRecord) {
	for _, artifact := range artifacts {
		_ = os.Remove(artifact.StoredPath)
		if artifact.LayoutPath != "" {
			_ = os.Remove(artifact.LayoutPath)
		}
	}
}

func GetPDFCapabilities(w http.ResponseWriter, r *http.Request) {
	_, ext, state, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	filename := strings.ToLower(previewFilenameForContent(ext))
	isPDF := strings.HasSuffix(filename, ".pdf") || strings.Contains(strings.ToLower(previewContentTypeForContent(ext)), "pdf")
	searchable := latestArtifactWithCacheKey(ext, pdfArtifactSearchable, pdfCacheKey(state, pdfArtifactSearchable, createPDFJobRequest{}))
	translations := make([]pdfArtifactRecord, 0)
	jobs := append([]pdfRenderJobRecord(nil), ext.PDFRenderJobs...)
	for i := range jobs {
		if jobs[i].Kind == pdfArtifactTranslation && !jobs[i].BackendManaged && (jobs[i].Status == pdfJobRunning || jobs[i].Status == pdfJobWaiting) {
			jobs[i].Status, jobs[i].Stage = "FAILED", "FAILED"
			jobs[i].ErrorMessage = "旧版浏览器翻译任务已失效，请重新发起"
		}
	}
	for _, artifact := range ext.PDFArtifacts {
		if artifact.Kind == pdfArtifactTranslation {
			translations = append(translations, artifact)
		}
	}
	common.ReplyOK(w, map[string]any{
		"is_pdf": isPDF, "reader_ready": state.ParseStatus == "succeeded" || state.ParseStatus == "success",
		"pdf_kind": "unknown", "searchable_artifact": searchable, "translations": translations, "jobs": jobs,
	})
}

func createPDFJob(w http.ResponseWriter, r *http.Request, kind string) {
	row, ext, state, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	var req createPDFJobRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if kind == pdfArtifactTranslation && strings.TrimSpace(req.TargetLanguage) == "" {
		req.TargetLanguage = "zh"
	}
	key := pdfCacheKey(state, kind, req)
	if !req.Force {
		for _, artifact := range ext.PDFArtifacts {
			if artifact.Kind == kind && artifact.CacheKey == key {
				common.ReplyOK(w, map[string]any{"cache_status": "hit", "artifact": artifact})
				return
			}
		}
		for _, job := range ext.PDFRenderJobs {
			if job.Kind == kind && job.CacheKey == key && job.Status != "FAILED" && job.Status != "CANCELLED" {
				common.ReplyOK(w, map[string]any{"cache_status": "running", "job": job})
				return
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := pdfRenderJobRecord{ID: uuid.NewString(), Kind: kind, CacheKey: key, Status: pdfJobRunning,
		Stage: "PREPARING", Progress: 0, TargetLanguage: req.TargetLanguage, ProviderType: req.ProviderType,
		Provider: req.Provider, Model: req.Model, CreatedAt: now, UpdatedAt: now}
	ext.PDFRenderJobs = append(ext.PDFRenderJobs, job)
	if err := saveDocumentExt(r, row, ext); err != nil {
		common.ReplyErr(w, "create pdf render job failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"cache_status": "miss", "job": job})
}

func CreateSearchablePDFJob(w http.ResponseWriter, r *http.Request) {
	createPDFJob(w, r, pdfArtifactSearchable)
}
func CreateTranslationPDFJob(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		createPDFJob(w, r, pdfArtifactTranslation)
		return
	}
	createBackendTranslationPDFJob(w, r)
}

func UpdatePDFRenderJob(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	jobID := strings.TrimSpace(common.PathVar(r, "job"))
	var req struct {
		Status       string `json:"status"`
		Stage        string `json:"stage"`
		Progress     int    `json:"progress"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	for i := range ext.PDFRenderJobs {
		if ext.PDFRenderJobs[i].ID == jobID {
			ext.PDFRenderJobs[i].Status = strings.ToUpper(strings.TrimSpace(req.Status))
			ext.PDFRenderJobs[i].Stage = strings.ToUpper(strings.TrimSpace(req.Stage))
			ext.PDFRenderJobs[i].Progress = max(0, min(100, req.Progress))
			ext.PDFRenderJobs[i].ErrorMessage = req.ErrorMessage
			ext.PDFRenderJobs[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if ext.PDFRenderJobs[i].Status == "CANCELLED" && ext.PDFRenderJobs[i].BackendManaged {
				_, _ = asyncjob.CancelResourceJobs(r.Context(), store.DB(), pdfTranslationJobType, "pdf_render_job", jobID, "user canceled translation")
			}
			if err := saveDocumentExt(r, row, ext); err != nil {
				common.ReplyErr(w, "update pdf render job failed", http.StatusInternalServerError)
				return
			}
			common.ReplyOK(w, ext.PDFRenderJobs[i])
			return
		}
	}
	common.ReplyErr(w, "pdf render job not found", http.StatusNotFound)
}

func CompletePDFRenderJob(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	jobID := strings.TrimSpace(common.PathVar(r, "job"))
	jobIndex := -1
	for i := range ext.PDFRenderJobs {
		if ext.PDFRenderJobs[i].ID == jobID {
			jobIndex = i
			break
		}
	}
	if jobIndex < 0 {
		common.ReplyErr(w, "pdf render job not found", http.StatusNotFound)
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		common.ReplyErr(w, "invalid multipart body", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		common.ReplyErr(w, "pdf file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	extension := strings.ToLower(filepath.Ext(header.Filename))
	allowedExtensions := map[string]bool{".pdf": true, ".docx": true, ".pptx": true, ".xlsx": true, ".md": true, ".markdown": true, ".txt": true, ".html": true, ".htm": true}
	if !allowedExtensions[extension] {
		common.ReplyErr(w, "unsupported translated artifact format", http.StatusBadRequest)
		return
	}
	baseDir := filepath.Dir(previewPathForContent(ext))
	artifactDir := filepath.Join(baseDir, ".lazymind-artifacts", row.ID)
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		common.ReplyErr(w, "create artifact directory failed", http.StatusInternalServerError)
		return
	}
	artifactID := uuid.NewString()
	path := filepath.Join(artifactDir, artifactID+extension)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		common.ReplyErr(w, "create artifact failed", http.StatusInternalServerError)
		return
	}
	_, copyErr := io.Copy(out, io.LimitReader(file, 128<<20))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		common.ReplyErr(w, "save artifact failed", http.StatusInternalServerError)
		return
	}
	layoutPath := ""
	if layoutFile, _, layoutErr := r.FormFile("layout_manifest"); layoutErr == nil {
		defer layoutFile.Close()
		layoutPath = filepath.Join(artifactDir, artifactID+".layout.json")
		layoutOut, createErr := os.OpenFile(layoutPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
		if createErr != nil {
			_ = os.Remove(path)
			common.ReplyErr(w, "create layout manifest failed", http.StatusInternalServerError)
			return
		}
		_, copyLayoutErr := io.Copy(layoutOut, io.LimitReader(layoutFile, 32<<20))
		closeLayoutErr := layoutOut.Close()
		if copyLayoutErr != nil || closeLayoutErr != nil {
			_ = os.Remove(path)
			_ = os.Remove(layoutPath)
			common.ReplyErr(w, "save layout manifest failed", http.StatusInternalServerError)
			return
		}
	}
	warningCount, _ := strconv.Atoi(r.FormValue("warning_count"))
	job := &ext.PDFRenderJobs[jobIndex]
	artifact := pdfArtifactRecord{ID: artifactID, Kind: job.Kind, CacheKey: job.CacheKey, StoredPath: path,
		LayoutPath: layoutPath, HasLayout: layoutPath != "",
		Filename: header.Filename, ContentType: mime.TypeByExtension(extension), TargetLanguage: job.TargetLanguage,
		ProviderType: job.ProviderType, Provider: job.Provider, Model: job.Model, WarningCount: warningCount,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	var replacedArtifacts []pdfArtifactRecord
	ext.PDFArtifacts, replacedArtifacts = replaceArtifactWithSameCacheKey(ext.PDFArtifacts, artifact)
	job.Status, job.Stage, job.Progress, job.ArtifactID = pdfJobReady, pdfJobReady, 100, artifact.ID
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if job.Kind == pdfArtifactSearchable {
		for i := range ext.PDFRenderJobs {
			if ext.PDFRenderJobs[i].Status == pdfJobWaiting && ext.PDFRenderJobs[i].DependsOnJobID == job.ID {
				ext.PDFRenderJobs[i].Status = pdfJobRunning
				ext.PDFRenderJobs[i].Stage = "PREPARING"
				ext.PDFRenderJobs[i].UpdatedAt = job.UpdatedAt
			}
		}
	}
	if err := saveDocumentExt(r, row, ext); err != nil {
		_ = os.Remove(path)
		if layoutPath != "" {
			_ = os.Remove(layoutPath)
		}
		common.ReplyErr(w, "register artifact failed", http.StatusInternalServerError)
		return
	}
	removeArtifactFiles(replacedArtifacts)
	common.ReplyOK(w, artifact)
}

func GetPDFArtifactLayout(w http.ResponseWriter, r *http.Request) {
	_, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	artifact := findArtifact(ext, strings.TrimSpace(common.PathVar(r, "artifact")))
	if artifact == nil || artifact.LayoutPath == "" {
		common.ReplyErr(w, "artifact layout manifest not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, artifact.LayoutPath)
}

func GetPDFArtifact(w http.ResponseWriter, r *http.Request) {
	_, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	artifact := findArtifact(ext, strings.TrimSpace(common.PathVar(r, "artifact")))
	if artifact == nil {
		common.ReplyErr(w, "artifact not found", http.StatusNotFound)
		return
	}
	f, err := os.Open(artifact.StoredPath)
	if err != nil {
		common.ReplyErr(w, "artifact file not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		common.ReplyErr(w, "artifact unavailable", http.StatusInternalServerError)
		return
	}
	contentType := artifact.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(artifact.Filename))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", artifact.Filename))
	http.ServeContent(w, r, artifact.Filename, stat.ModTime(), f)
}

func DeletePDFArtifact(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	artifactID := strings.TrimSpace(common.PathVar(r, "artifact"))
	artifactIndex := -1
	var artifact pdfArtifactRecord
	for i := range ext.PDFArtifacts {
		if ext.PDFArtifacts[i].ID == artifactID {
			artifactIndex, artifact = i, ext.PDFArtifacts[i]
			break
		}
	}
	if artifactIndex < 0 {
		common.ReplyErr(w, "artifact not found", http.StatusNotFound)
		return
	}
	var removedArtifacts []pdfArtifactRecord
	ext.PDFArtifacts, removedArtifacts = removeArtifactCacheEntry(ext.PDFArtifacts, artifact)
	removedIDs := make(map[string]struct{}, len(removedArtifacts))
	for _, removed := range removedArtifacts {
		removedIDs[removed.ID] = struct{}{}
	}
	jobs := ext.PDFRenderJobs[:0]
	for _, job := range ext.PDFRenderJobs {
		if _, removed := removedIDs[job.ArtifactID]; !removed {
			jobs = append(jobs, job)
		}
	}
	ext.PDFRenderJobs = jobs
	if err := saveDocumentExt(r, row, ext); err != nil {
		common.ReplyErr(w, "delete artifact failed", http.StatusInternalServerError)
		return
	}
	removeArtifactFiles(removedArtifacts)
	common.ReplyOK(w, map[string]any{"deleted": artifactID, "deleted_count": len(removedArtifacts)})
}

func ListPDFTranslations(w http.ResponseWriter, r *http.Request) {
	_, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	items := make([]pdfArtifactRecord, 0)
	for _, artifact := range ext.PDFArtifacts {
		if artifact.Kind == pdfArtifactTranslation {
			items = append(items, artifact)
		}
	}
	common.ReplyOK(w, map[string]any{"items": items})
}
