package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	corestore "lazymind/core/store"
	"lazymind/core/workflow/document"
	workflowstore "lazymind/core/workflow/store"
)

// Downloading is read-only and must remain available without the Algorithm
// service. Reuse the owned artifact reader and local document carrier parser.
func prepareImageExport(ctx context.Context, owner, id string, body DocumentImageExportRequest) (*documentActionContext, error) {
	db := corestore.DB()
	if db == nil {
		return nil, documentFailure("IMAGE_EXPORT_FAILED", 500)
	}
	repo := workflowstore.New(db)
	artifact, err := repo.ReadArtifact(ctx, owner, id)
	if err != nil {
		return nil, documentFailure("ARTIFACT_NOT_FOUND", 404)
	}
	session, err := GetSession(ctx, db, artifact.SessionID)
	if err != nil || session.Dismissed {
		return nil, documentFailure("ARTIFACT_NOT_FOUND", 404)
	}
	if !artifact.Selected || artifact.Validity != "effective" || artifact.Revision != *body.BaseRevision {
		return nil, documentFailure("REVISION_CONFLICT", 409)
	}
	if artifact.DraftVersion > 0 && (body.BaseDraftVersion == nil || *body.BaseDraftVersion != artifact.DraftVersion) {
		return nil, documentFailure("DRAFT_VERSION_CONFLICT", 409)
	}
	content, failure := document.ReadContent(artifact.Value, artifact.ContentType, func() (bool, error) { return repo.PinnedMarkdownHint(ctx, session, artifact.SlotID) })
	if failure != nil || content == nil {
		return nil, documentFailure("IMAGE_EXPORT_INVALID", 422)
	}
	return &documentActionContext{db: db, owner: owner, session: session, artifact: artifact, content: content,
		revision: orm.WorkflowSlotRevision{ID: id, SessionID: session.ID, SlotID: artifact.SlotID}}, nil
}

func replyImageExportError(w http.ResponseWriter, code string, status int) {
	common.ReplyErrWithData(w, "Unable to prepare document images", map[string]string{"code": code}, status)
}

func imageExportOwner(w http.ResponseWriter, r *http.Request) string {
	owner := corestore.UserID(r)
	if owner == "" || r.Header.Get("X-LazyMind-External-Ref") != "" {
		replyImageExportError(w, "PERMISSION_DENIED", http.StatusForbidden)
		return ""
	}
	return owner
}

func CreateDocumentImageExportHTTP(w http.ResponseWriter, r *http.Request) {
	owner := imageExportOwner(w, r)
	if owner == "" {
		return
	}
	var body DocumentImageExportRequest
	if decodeDocumentJSON(http.MaxBytesReader(w, r.Body, documentActionMaxBytes), &body) != nil || body.BaseRevision == nil || *body.BaseRevision < 1 ||
		(body.BaseDraftVersion != nil && *body.BaseDraftVersion < 1) || len(body.Document) == 0 || len(body.ImageReference) > 16384 {
		replyImageExportError(w, "IMAGE_EXPORT_INVALID", 400)
		return
	}
	id := common.PathVar(r, "artifact_id")
	target, err := prepareImageExport(r.Context(), owner, id, body)
	if err != nil {
		var failure documentActionFailure
		if errors.As(err, &failure) {
			replyImageExportError(w, failure.code, failure.status)
		} else {
			replyImageExportError(w, "IMAGE_EXPORT_FAILED", 500)
		}
		return
	}
	if !validPortableSnapshot(body.Document, target.content.Representation) {
		replyImageExportError(w, "IMAGE_EXPORT_INVALID", 400)
		return
	}
	refs, title, err := documentImageReferences(json.RawMessage(body.Document))
	if err != nil {
		replyImageExportError(w, "IMAGE_EXPORT_INVALID", 422)
		return
	}
	if len(refs) == 0 {
		replyImageExportError(w, "IMAGE_EXPORT_EMPTY", 422)
		return
	}
	images, err := documentImageSources(r.Context(), target, refs)
	if err != nil {
		replyImageExportError(w, "IMAGE_EXPORT_FAILED", 500)
		return
	}
	if body.ImageReference != "" {
		selected := []imageExportSource{}
		key := imageReferenceKey(body.ImageReference)
		for _, image := range images {
			matches := key == imageReferenceKey(image.Source)
			for _, reference := range image.References {
				matches = matches || key == imageReferenceKey(reference) || key == "sha256:"+imageExportHash([]byte(reference))
			}
			if image.Local {
				matches = matches || key == doc.StaticFileReferenceFromAnyStoragePath(image.Source)
			}
			if matches {
				image.ID = "image-001"
				selected = append(selected, image)
				break
			}
		}
		if len(selected) == 0 {
			replyImageExportError(w, "IMAGE_NOT_IN_DOCUMENT", 422)
			return
		}
		images = selected
	}
	if title == "" {
		title = "document"
	}
	payload := imageExportPayload{SessionID: target.session.ID, ArtifactID: id, SourceHash: imageExportHash(body.Document), Title: title, Single: body.ImageReference != "", Images: images}
	identity, _ := json.Marshal([]any{owner, id, body.BaseRevision, body.BaseDraftVersion, payload})
	idempotencyKey := imageExportHash(identity)
	payload.CacheKey = strings.ReplaceAll(uuid.NewString(), "-", "")
	payload.ExpiresAt = time.Now().UTC().Add(documentImageExportTTL)
	job, err := enqueueDocumentImageExport(r.Context(), target.db, asyncjob.EnqueueRequest{JobType: documentImageJobType, ResourceType: "document_image_export", ResourceID: id,
		IdempotencyKey: idempotencyKey, Payload: payload, CreateUserID: owner, MaxAttempts: 2})
	if err != nil {
		replyImageEnqueueError(w, err)
		return
	}
	if json.Unmarshal(job.PayloadJSON, &payload) != nil {
		replyImageExportError(w, "IMAGE_EXPORT_FAILED", 500)
		return
	}
	common.ReplyOK(w, imageExportState(job, payload))
}

func ownedDocumentImageExport(w http.ResponseWriter, r *http.Request) (*orm.AsyncJob, imageExportPayload) {
	payload := imageExportPayload{}
	owner := imageExportOwner(w, r)
	if owner == "" {
		return nil, payload
	}
	db := corestore.DB()
	if db == nil {
		replyImageExportError(w, "IMAGE_EXPORT_FAILED", 500)
		return nil, payload
	}
	job, err := asyncjob.Get(r.Context(), db, common.PathVar(r, "export_id"))
	if err != nil || job.JobType != documentImageJobType || job.CreateUserID != owner || job.ResourceID != common.PathVar(r, "artifact_id") ||
		json.Unmarshal(job.PayloadJSON, &payload) != nil || !validImageExportPayload(payload) ||
		workflowstore.New(db).AuthorizeSession(r.Context(), payload.SessionID, owner) != nil {
		replyImageExportError(w, "IMAGE_EXPORT_NOT_FOUND", 404)
		return nil, payload
	}
	session, err := GetSession(r.Context(), db, payload.SessionID)
	if err != nil || session.Dismissed {
		replyImageExportError(w, "IMAGE_EXPORT_NOT_FOUND", 404)
		return nil, payload
	}
	return job, payload
}

func ReadDocumentImageExportHTTP(w http.ResponseWriter, r *http.Request) {
	job, payload := ownedDocumentImageExport(w, r)
	if job == nil {
		return
	}
	common.ReplyOK(w, imageExportState(job, payload))
}

func RetryDocumentImageExportHTTP(w http.ResponseWriter, r *http.Request) {
	job, payload := ownedDocumentImageExport(w, r)
	if job == nil {
		return
	}
	state := imageExportState(job, payload)
	if state.Status == "expired" {
		replyImageExportError(w, "IMAGE_EXPORT_EXPIRED", 410)
		return
	}
	if state.Status == "pending" || state.Status == "running" || state.Status == "ready" {
		common.ReplyOK(w, state)
		return
	}
	// A new job has its own ZIP; immutable successful images are reused from the
	// original private cache. Retrying never reads a newer article or media slot.
	retry, err := enqueueDocumentImageExport(r.Context(), corestore.DB(), asyncjob.EnqueueRequest{JobType: documentImageJobType, ResourceType: job.ResourceType, ResourceID: job.ResourceID,
		IdempotencyKey: "image-retry:" + job.ID, Payload: payload, CreateUserID: job.CreateUserID, MaxAttempts: 2})
	if err != nil {
		replyImageEnqueueError(w, err)
		return
	}
	common.ReplyOK(w, imageExportState(retry, payload))
}

func DownloadDocumentImageExportHTTP(w http.ResponseWriter, r *http.Request) {
	job, payload := ownedDocumentImageExport(w, r)
	if job == nil {
		return
	}
	state := imageExportState(job, payload)
	if state.Status == "expired" {
		replyImageExportError(w, "IMAGE_EXPORT_EXPIRED", 410)
		return
	}
	if state.Status != "ready" && state.Status != "partial" {
		replyImageExportError(w, "IMAGE_EXPORT_NOT_READY", 409)
		return
	}
	fileID := common.PathVar(r, "file_id")
	path := ""
	filename := ""
	contentType := "application/octet-stream"
	directory := imageExportDirectory(job.CreateUserID, payload)
	if fileID == "bundle" && !payload.Single {
		path = filepath.Join(directory, job.ID+".zip")
		filename = state.Filename
		contentType = "application/zip"
	} else {
		for _, item := range state.Items {
			if item.ID == fileID && item.Status == "ready" {
				path = filepath.Join(directory, item.ID+".image")
				filename = item.Filename
				break
			}
		}
	}
	if path == "" {
		replyImageExportError(w, "IMAGE_EXPORT_NOT_FOUND", 404)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		replyImageExportError(w, "IMAGE_EXPORT_EXPIRED", 410)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		replyImageExportError(w, "IMAGE_EXPORT_EXPIRED", 410)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

// Serialize admission per user so simultaneous submissions and retries share
// one capacity limit. The worker performs all file/network IO outside this lock.
func enqueueDocumentImageExport(ctx context.Context, db *gorm.DB, request asyncjob.EnqueueRequest) (*orm.AsyncJob, error) {
	var job *orm.AsyncJob
	err := common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "image-exports:"+request.CreateUserID).Error; err != nil {
				return err
			}
		}
		var previous orm.AsyncJob
		found := tx.Where("job_type = ? AND idempotency_key = ?", documentImageJobType, request.IdempotencyKey).First(&previous).Error
		if found != nil && !errors.Is(found, gorm.ErrRecordNotFound) {
			return found
		}
		if found == nil {
			var payload imageExportPayload
			if json.Unmarshal(previous.PayloadJSON, &payload) == nil && !time.Now().Before(payload.ExpiresAt) {
				// Retain the expired receipt, but release its key so the generic
				// queue does not reset a canceled job that an old worker may hold.
				if err := tx.Model(&orm.AsyncJob{}).Where("id = ?", previous.ID).Update("idempotency_key", previous.IdempotencyKey+":expired:"+previous.ID).Error; err != nil {
					return err
				}
				if err := tx.Model(&orm.AsyncJob{}).Where("id = ? AND status IN ?", previous.ID, []string{"pending", "running"}).Updates(map[string]any{"status": "canceled", "error_code": "IMAGE_EXPORT_EXPIRED"}).Error; err != nil {
					return err
				}
			} else if previous.Status == "pending" || previous.Status == "running" || previous.Status == "succeeded" {
				job = &previous
				return nil
			}
		}
		var active int64
		if err := tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND create_user_id = ? AND status IN ?", documentImageJobType, request.CreateUserID, []string{"pending", "running"}).Count(&active).Error; err != nil {
			return err
		}
		if active >= 3 {
			return documentFailure("IMAGE_EXPORT_BUSY", 429)
		}
		var err error
		job, err = asyncjob.EnqueueInTransaction(ctx, tx, request)
		return err
	})
	return job, err
}

func replyImageEnqueueError(w http.ResponseWriter, err error) {
	var failure documentActionFailure
	if errors.As(err, &failure) {
		replyImageExportError(w, failure.code, failure.status)
	} else {
		replyImageExportError(w, "IMAGE_EXPORT_FAILED", 500)
	}
}
