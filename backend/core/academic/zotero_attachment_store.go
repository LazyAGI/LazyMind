package academic

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

const zoteroStoredPDFLimit = 128 << 20

// StoreZoteroAttachment runs only after metadata confirmation. The graph must
// already contain the target attachment; uploads cannot create arbitrary papers.
func StoreZoteroAttachment(w http.ResponseWriter, r *http.Request) {
	datasetID, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	if !canWrite {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的编辑权限")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, zoteroStoredPDFLimit+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		replyError(w, 400, "INVALID_REQUEST", "PDF 上传无效或超过 128 MiB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	paperID, attachmentID := r.FormValue("paper_id"), r.FormValue("attachment_id")
	if paperID == "" || len(paperID) > 255 || attachmentID == "" || len(attachmentID) > 255 {
		replyError(w, 400, "INVALID_REQUEST", "文献或附件标识无效")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		replyError(w, 400, "INVALID_REQUEST", "请选择 PDF 附件")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "lazymind-zotero-*.pdf")
	if err != nil {
		replyError(w, 500, "ZOTERO_STORE_FAILED", "无法暂存 PDF 附件")
		return
	}
	defer os.Remove(tmp.Name())
	hash := md5.New() // Zotero's published attachment checksum uses MD5.
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(file, zoteroStoredPDFLimit+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > zoteroStoredPDFLimit {
		replyError(w, 400, "INVALID_REQUEST", "PDF 上传失败或超过 128 MiB")
		return
	}
	if expected := strings.ToLower(strings.TrimSpace(r.FormValue("expected_md5"))); expected != "" && expected != hex.EncodeToString(hash.Sum(nil)) {
		replyError(w, 409, "ZOTERO_ATTACHMENT_CHANGED", "附件校验值与 Zotero 题录不一致，请重新读取题录并选择对应文件")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := storeZoteroFile(ctx, store.DB(), datasetID, paperID, attachmentID, tmp.Name(), filepath.Base(header.Filename), store.UserID(r), store.UserName(r))
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "ZOTERO_STORE_FAILED", "message": err.Error(), "library": result})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

// Metadata and documents share one authoritative normalized library.
func zoteroTarget(db *gorm.DB, dataset, paperID, attachmentID string) (orm.PaperLibraryEntry, orm.PaperLibraryAttachment, error) {
	var entry orm.PaperLibraryEntry
	var member orm.PaperLibraryAttachment
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=? AND removed_at IS NULL", dataset, paperID).Take(&entry).Error
	if err == nil {
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND paper_id=? AND id=? AND detached_at IS NULL", dataset, paperID, attachmentID).Take(&member).Error
	}
	return entry, member, err
}

func zoteroView(ctx context.Context, db *gorm.DB, dataset, user string) (paperlibrary.View, error) {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/internal/zotero", nil)
	r.Header.Set("X-User-Id", user)
	view, err := loadLibraryView(r, db, dataset, nil, true)
	if err != nil {
		return view, err
	}
	counts, err := paperlibrary.Statistics(ctx, db, []string{dataset})
	view.Counts = counts[dataset]
	view.CanWrite = true
	return view, err
}

func storeZoteroFile(ctx context.Context, db *gorm.DB, datasetID, paperID, attachmentID, path, name, userID, userName string) (paperlibrary.View, error) {
	return storeZoteroFileResult(ctx, db, datasetID, paperID, attachmentID, path, name, userID, userName, false)
}

func storeZoteroFileResult(ctx context.Context, db *gorm.DB, datasetID, paperID, attachmentID, path, name, userID, userName string, compact bool) (paperlibrary.View, error) {
	var empty paperlibrary.View
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	info, statErr := file.Stat()
	header := make([]byte, 1024)
	n, _ := file.Read(header)
	file.Close()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > zoteroStoredPDFLimit || !bytes.Contains(header[:n], []byte("%PDF-")) {
		return empty, errors.New("Zotero 附件不是有效 PDF 或超过 128 MiB")
	}
	identity := sha256.Sum256([]byte(datasetID + "\x00" + attachmentID))
	documentID := "zotero_" + hex.EncodeToString(identity[:])
	guard, err := lockArxivImport(ctx, db, "zotero:"+documentID)
	if err != nil {
		return empty, err
	}
	defer guard.release()
	ctx = guard.ctx
	if err = paperlibrary.EnsureLegacy(ctx, db, datasetID); err != nil {
		return empty, err
	}
	// Source membership is checked before acquisition, and again under the commit lock.
	// Compact transfers already checked the source before acquiring bytes. The
	// target is still checked below and again under the commit lock.
	if !compact {
		view, err := paperlibrary.Load(ctx, db, datasetID, userID, []string{paperID}, false)
		if err != nil {
			return empty, err
		}
		valid := false
		for _, a := range view.Data.Attachments {
			if a.ID == attachmentID && a.PaperID == paperID && a.Source == "zotero" {
				valid = true
			}
		}
		if !valid {
			return empty, errors.New("Zotero 文献或附件已移除，请重新读取论文库")
		}
	}
	_, member, err := zoteroTarget(db.WithContext(ctx), datasetID, paperID, attachmentID)
	if err != nil {
		return empty, err
	}
	if member.DocumentID != "" {
		documentID = member.DocumentID
	}
	var document orm.Document
	findErr := db.WithContext(ctx).Where("id=? AND dataset_id=?", documentID, datasetID).Take(&document).Error
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return empty, findErr
	}
	if (findErr == nil && document.DeletedAt != nil) || (errors.Is(findErr, gorm.ErrRecordNotFound) && member.DocumentID != "") {
		return empty, doc.ErrImportedDocumentUnavailable
	}
	var imported doc.LocalFileImportResult
	var importErr error
	if errors.Is(findErr, gorm.ErrRecordNotFound) {
		var dataset orm.Dataset
		if err = db.WithContext(ctx).Where("id=? AND deleted_at IS NULL", datasetID).Take(&dataset).Error; err != nil {
			return empty, err
		}
		caller := doc.DatasetCatalogCaller{UserID: userID, UserName: userName, TenantID: dataset.TenantID, Permissions: []string{"document.write"}}
		svc, e := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db})
		if e != nil {
			return empty, e
		}
		input, e := os.Open(path)
		if e != nil {
			return empty, e
		}
		defer input.Close()
		source, e := svc.AcquireOriginal(ctx, caller, doc.AcquireOriginalRequest{Filename: name, ContentType: "application/pdf", Reader: input})
		if e != nil {
			return empty, e
		}
		// Acquired source is temporary; its bound copy becomes durable with membership.
		defer svc.ReleaseOriginal(context.WithoutCancel(ctx), caller, source.FileRef)
		displayName := name
		var collisions int64
		if e = db.WithContext(ctx).Model(&orm.Document{}).Where("dataset_id=? AND display_name=? AND deleted_at IS NULL", datasetID, name).Count(&collisions).Error; e != nil {
			return empty, e
		}
		if collisions > 0 {
			displayName = strings.TrimSuffix(name, filepath.Ext(name)) + "-" + hex.EncodeToString(identity[:4]) + filepath.Ext(name)
		}
		prepared, e := svc.PrepareOriginalBinding(ctx, caller, doc.ImportOriginalRequest{FileRef: source.FileRef, DatasetID: datasetID, DocumentID: documentID, DisplayName: displayName, DataSourceType: "ZOTERO"})
		if e != nil {
			return empty, e
		}
		committed := false
		defer func() {
			if !committed {
				_ = svc.CleanupPreparedBinding(prepared)
			}
		}()
		ctx, e = doc.PrepareResourceAuthorization(ctx, caller, prepared.AuthorizationRequests())
		if e != nil {
			return empty, e
		}
		var bound doc.OriginalImportResult
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := lockZoteroDataset(tx, datasetID); e != nil {
				return e
			}
			entry, target, e := zoteroTarget(tx, datasetID, paperID, attachmentID)
			if e != nil {
				return e
			}
			if target.DocumentID != "" {
				return paperlibrary.ErrConflict
			}
			bound, e = svc.BindOriginalTx(ctx, tx, caller, prepared)
			if e != nil {
				return e
			}
			if e = guard.check(); e != nil {
				return e
			}
			return bindZoteroAttachment(ctx, tx, &entry, &target, bound.Document, bound.ContentSHA256, info.Size())
		})
		if e != nil {
			return empty, e
		}
		committed = true
		bound = svc.SubmitOriginalProcessing(ctx, caller, bound)
		imported = doc.LocalFileImportResult{DocumentID: bound.Document.ID, TaskID: bound.Task.ID, ContentSHA256: bound.ContentSHA256, Start: bound.Start}
		if bound.SubmissionStatus == "failed" {
			importErr = fmt.Errorf("PDF 已保存，但处理任务未能启动，请重试: %s", bound.SubmissionError)
		}
	} else {
		imported, importErr = doc.ResumeLocalFileImport(ctx, doc.ResumeLocalFileImportRequest{DatasetID: datasetID, DocumentID: documentID, UserID: userID, UserName: userName})
		if errors.Is(importErr, doc.ErrImportedDocumentUnavailable) {
			return empty, importErr
		}
		// Recover a document created by the pre-normalization importer before its graph commit.
		if imported.DocumentID != "" && member.DocumentID == "" {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if e := lockZoteroDataset(tx, datasetID); e != nil {
					return e
				}
				entry, target, e := zoteroTarget(tx, datasetID, paperID, attachmentID)
				if e != nil {
					return e
				}
				if target.DocumentID != "" && target.DocumentID != documentID {
					return paperlibrary.ErrConflict
				}
				return bindZoteroAttachment(ctx, tx, &entry, &target, document, documentFileHash(document), info.Size())
			})
			if err != nil {
				return empty, err
			}
		}
	}
	if importErr == nil && imported.Start.Status != "STARTED" {
		importErr = errors.New("PDF 已保存，但处理任务未能启动，请重试")
	}
	// The new binding already clears stale errors; avoid a second whole-graph rewrite.
	if compact && importErr == nil {
		return empty, nil
	}
	// Keep the saved PDF and report task rejection, never turn it into false success.
	fields := map[string]any{"processingError": nil}
	if importErr != nil {
		fields["processingError"] = importErr.Error()
	}
	if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockZoteroDataset(tx, datasetID); e != nil {
			return e
		}
		return paperlibrary.MergeEntityExtras(ctx, tx, datasetID, "attachments", attachmentID, fields)
	}); err != nil {
		return empty, err
	}
	if compact {
		return empty, importErr
	}
	result, err := zoteroView(ctx, db, datasetID, userID)
	if err != nil {
		return empty, err
	}
	return result, importErr
}

func bindZoteroAttachment(ctx context.Context, tx *gorm.DB, entry *orm.PaperLibraryEntry, member *orm.PaperLibraryAttachment, document orm.Document, hash string, size int64) error {
	now := time.Now().UTC()
	if err := tx.Model(member).Updates(map[string]any{"document_id": document.ID, "updated_at": now}).Error; err != nil {
		return err
	}
	link := orm.AcademicWorkDocument{AcademicWorkID: entry.AcademicWorkID, DatasetID: entry.DatasetID, DocumentID: document.ID, SourceProvider: "zotero", SourceLocator: member.SourceURL, ContentSHA256: hash, MatchMethod: "manual", MatchConfidence: 1, CreatedAt: now, UpdatedAt: now}
	if err := saveDocumentWorkLink(tx, link); err != nil {
		return err
	}
	state := orm.AcademicDocumentState{DocumentID: document.ID, DatasetID: entry.DatasetID, Kind: "paper", Status: "succeeded", PaperID: entry.AcademicWorkID, Revision: 1, Manual: true, MetadataJSON: marshal(map[string]any{}), UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "document_id"}}, DoUpdates: clause.AssignmentColumns([]string{"kind", "status", "paper_id", "manual", "metadata_json", "updated_at"})}).Create(&state).Error; err != nil {
		return err
	}
	if err := paperlibrary.SaveEntry(tx, entry); err != nil {
		return err
	}
	// Link the persisted import receipt to the saved document; parsing is a
	// separate state observed by the notification center.
	if err := tx.Model(&orm.PaperImportItem{}).Where("library_paper_id=? AND batch_id IN (?)", entry.ID,
		tx.Model(&orm.PaperImportBatch{}).Select("id").Where("target_dataset_id=? AND entry_type IN ?", entry.DatasetID, []string{"zotero", "library_backup"})).
		Updates(map[string]any{"document_id": document.ID, "result_kind": "fulltext", "updated_at": now}).Error; err != nil {
		return err
	}
	return paperlibrary.MergeEntityExtras(ctx, tx, entry.DatasetID, "attachments", member.ID, map[string]any{"size": size, "mimeType": "application/pdf", "source": "zotero", "processingError": nil})
}

func lockZoteroDataset(tx *gorm.DB, dataset string) error {
	var ds orm.Dataset
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", dataset).Take(&ds).Error
}
