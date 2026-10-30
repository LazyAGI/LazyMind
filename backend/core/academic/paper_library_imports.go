package academic

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var arxivVersionOnlyPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

// All import kinds share these membership operations and the existing batch receipt.
func importedLibraryEntry(ctx context.Context, tx *gorm.DB, dataset string, work orm.AcademicWork, draft *paperlibrary.Paper, folder, method string, explicit bool) (orm.PaperLibraryEntry, error) {
	var row orm.PaperLibraryEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND academic_work_id=?", dataset, work.ID).Take(&row).Error
	if err == nil {
		if row.RemovedAt != nil && explicit {
			row.RemovedAt = nil
			if e := paperlibrary.CheckFolder(tx, dataset, row.FolderID); e != nil {
				if !errors.Is(e, gorm.ErrRecordNotFound) {
					return row, e
				}
				row.FolderID = ""
			}
			err = paperlibrary.SaveEntry(tx, &row)
		}
		return row, err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return row, err
	}
	if err = paperlibrary.CheckFolder(tx, dataset, folder); err != nil {
		return row, err
	}
	now := time.Now().UTC()
	row = orm.PaperLibraryEntry{DatasetID: dataset, ID: "work-" + work.ID, AcademicWorkID: work.ID, FolderID: folder, TagsJSON: marshal([]string{}), MetadataJSON: marshal(map[string]any{}), Source: "unknown", ImportMethod: method, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if draft != nil {
		if !paperlibrary.ValidID(draft.ID) {
			return row, invalidLibraryMutation("INVALID_PAPER_LIBRARY", "论文条目 ID 无效")
		}
		row.ID = draft.ID
		row.MetadataJSON = confirmedMetadata(draft.Metadata)
		row.Source = draft.Source
		row.SourceURL = draft.SourceURL
		row.ArxivVersion = draft.ArxivVersion
		row.TagsJSON = marshal(draft.Tags)
	}
	if row.Source == "" {
		row.Source = "unknown"
	}
	if work.ArxivIDBase != "" && row.Source == "unknown" {
		row.Source = "arxiv"
	} else if work.DOINormalized != "" && row.Source == "unknown" {
		row.Source = "doi"
	}
	if err = tx.Create(&row).Error; err != nil {
		return row, err
	}
	return row, nil
}

// Recognition may add a newly discovered PDF once, but cannot restore removed membership.
func registerRecognizedLibraryDocument(ctx context.Context, tx *gorm.DB, document orm.Document, work orm.AcademicWork, explicit bool, folder, itemID, method string) (string, error) {
	var ds orm.Dataset
	if err := tx.Select("id,scene").Where("id=? AND deleted_at IS NULL", document.DatasetID).Take(&ds).Error; err != nil {
		return "", err
	}
	if ds.Scene != "academic_papers" {
		return "", nil
	}
	if !explicit {
		var pending int64
		if e := tx.Model(&orm.PaperImportItem{}).Where("document_id=? AND stage='awaiting_association'", document.ID).Count(&pending).Error; e != nil {
			return "", e
		}
		if pending > 0 {
			return "", nil
		}
	}
	var prior orm.PaperLibraryAttachment
	err := tx.Where("dataset_id=? AND document_id=?", document.DatasetID, document.ID).Order("created_at").Take(&prior).Error
	if err == nil && !explicit {
		return prior.PaperID, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	row, err := importedLibraryEntry(ctx, tx, document.DatasetID, work, nil, folder, method, explicit)
	if err != nil {
		return "", err
	}
	if row.RemovedAt != nil {
		return row.ID, nil
	}
	if prior.ID != "" && prior.DetachedAt == nil {
		if prior.PaperID != row.ID {
			return "", invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "PDF 已关联其他论文")
		}
		return prior.PaperID, nil
	}
	var active int64
	if err = tx.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id=? AND paper_id=? AND kind='pdf' AND detached_at IS NULL", document.DatasetID, row.ID).Count(&active).Error; err != nil {
		return "", err
	}
	now := time.Now().UTC()
	member := orm.PaperLibraryAttachment{DatasetID: document.DatasetID, ID: "document-" + document.ID, PaperID: row.ID, DocumentID: document.ID, ImportItemID: itemID, Name: document.DisplayName, Kind: "pdf", IsPrimary: active == 0, CreatedAt: now, UpdatedAt: now}
	if prior.ID != "" {
		member.ID = prior.ID
		member.CreatedAt = prior.CreatedAt
		if err = tx.Model(&prior).Select("paper_id", "import_item_id", "detached_at", "is_primary", "updated_at").Updates(&member).Error; err != nil {
			return "", err
		}
	} else if err = tx.Create(&member).Error; err != nil {
		return "", err
	}
	return row.ID, paperlibrary.SaveEntry(tx, &row)
}
func applyImportFolders(tx *gorm.DB, dataset string, folders []paperlibrary.Folder) error {
	if len(folders) > 500 {
		return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "导入目录过多")
	}
	pending := append([]paperlibrary.Folder{}, folders...)
	for len(pending) > 0 {
		next := []paperlibrary.Folder{}
		progress := false
		for _, f := range pending {
			if !paperlibrary.ValidID(f.ID) || strings.TrimSpace(f.Name) == "" || len(f.Name) > 255 {
				return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "导入目录无效")
			}
			var existing orm.PaperLibraryFolder
			if e := tx.Where("dataset_id=? AND id=?", dataset, f.ID).Take(&existing).Error; e == nil {
				progress = true
				continue
			} else if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			parent := ""
			if f.ParentID != nil {
				parent = *f.ParentID
			}
			if e := paperlibrary.CheckFolder(tx, dataset, parent); errors.Is(e, gorm.ErrRecordNotFound) {
				next = append(next, f)
				continue
			} else if e != nil {
				return e
			}
			row := orm.PaperLibraryFolder{DatasetID: dataset, ID: f.ID, ParentID: parent, Name: strings.TrimSpace(f.Name), NameKey: strings.ToLower(strings.TrimSpace(f.Name)), Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if e := tx.Create(&row).Error; e != nil {
				return e
			}
			progress = true
		}
		if !progress {
			return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "导入目录的上级不存在或包含循环")
		}
		pending = next
	}
	return nil
}
func createLibraryImport(w http.ResponseWriter, r *http.Request, user string, input createImportRequest) {
	catalog, err := libraryImportDataset(r, input.TargetDatasetID)
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	_ = catalog
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		replyError(w, 400, "INVALID_REQUEST", "导入需要有效的幂等标识")
		return
	}
	if input.EntryType == "bibliography" && input.Format != "bib" && input.Format != "ris" && input.Format != "csv" {
		replyError(w, 400, "INVALID_REQUEST", "题录格式无效")
		return
	}
	guard, err := lockArxivImport(r.Context(), store.DB(), "request:"+user+":"+input.IdempotencyKey)
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	defer guard.release()
	r = r.WithContext(guard.ctx)
	requestHash := sourceHash([]string{string(marshal(struct {
		Items                      []selectedImportItem
		Folders                    []paperlibrary.Folder
		FolderID, Format, FileName string
	}{input.Items, input.Folders, input.FolderID, input.Format, input.FileName}))})
	var batch orm.PaperImportBatch
	ids := []string{}
	err = store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("created_by=? AND idempotency_key=?", user, input.IdempotencyKey).Take(&batch).Error
		if e == nil {
			if batch.TargetDatasetID != input.TargetDatasetID || batch.EntryType != input.EntryType {
				return invalidLibraryMutation("INVALID_REQUEST", "导入标识已用于其他请求")
			}
			if input.EntryType == "pdf_upload" {
				id, e := confirmLibraryUpload(r.Context(), tx, user, batch, input)
				if e != nil {
					return e
				}
				ids = append(ids, id)
				return nil
			}
			var policy struct {
				RequestHash string `json:"request_hash"`
			}
			if e = json.Unmarshal(batch.PolicySnapshotJSON, &policy); e != nil {
				return e
			}
			if policy.RequestHash != requestHash {
				return invalidLibraryMutation("INVALID_REQUEST", "导入请求与首次提交不同")
			}
			var items []orm.PaperImportItem
			if e = tx.Where("batch_id=?", batch.ID).Find(&items).Error; e != nil {
				return e
			}
			if len(items) != len(input.Items) {
				return invalidLibraryMutation("INVALID_REQUEST", "导入请求与首次提交不同")
			}
			for _, item := range items {
				ids = append(ids, item.LibraryPaperID)
			}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if input.EntryType == "pdf_upload" {
			return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "原 PDF 上传回执不存在，请重新选择文件")
		}
		if e = applyImportFolders(tx, input.TargetDatasetID, input.Folders); e != nil {
			return e
		}
		var receipt importPreviewReceipt
		if input.EntryType == "paper_link" {
			receipt, e = loadImportPreview(r.Context(), input.PreviewToken, user, input.TargetDatasetID)
			if e != nil {
				return &libraryMutationError{409, "IMPORT_PREVIEW_EXPIRED", e.Error()}
			}
		}
		now := time.Now().UTC()
		batch = orm.PaperImportBatch{ID: uuid.NewString(), TargetDatasetID: input.TargetDatasetID, CreatedBy: user, IdempotencyKey: input.IdempotencyKey, EntryType: input.EntryType, Status: "succeeded", SourceContextJSON: marshal(map[string]string{"format": input.Format, "file_name": input.FileName}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{"request_hash": requestHash}), CreatedAt: now, UpdatedAt: now}
		items := []orm.PaperImportItem{}
		for _, selected := range input.Items {
			draft := selected.Paper
			var work orm.AcademicWork
			if input.EntryType == "paper_link" {
				var match *PreviewItem
				for i := range receipt.Items {
					if receipt.Items[i].WorkID == selected.WorkID {
						match = &receipt.Items[i]
						break
					}
				}
				if match == nil || len(match.Candidates) > 0 && match.Disposition != "already_present" {
					return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "链接导入与确认预览不同")
				}
				if e = tx.Where("id=?", selected.WorkID).Take(&work).Error; e != nil {
					return e
				}
				m := paperlibrary.WorkMetadata(work)
				ref := match.Paper
				if ref.Title != "" {
					m.Title = ref.Title
				}
				if len(ref.Authors) > 0 {
					m.Authors = ref.Authors
				}
				if ref.Venue != "" {
					m.Venue = ref.Venue
				}
				if ref.Year > 0 {
					m.Year = ref.Year
				}
				if ref.Abstract != nil {
					m.Abstract = *ref.Abstract
				}
				m.CitationCount = ref.CitationCount
				draft = &paperlibrary.Paper{ID: "work-" + work.ID, Metadata: m, Tags: []string{}, Source: "doi", SourceURL: ref.PaperURL, ArxivVersion: ref.SourceVersion}
				if work.ArxivIDBase != "" {
					draft.Source = "arxiv"
				}

			} else {
				if draft == nil {
					return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "导入缺少题录")
				}
				if e = validateLibraryMetadata(&draft.Metadata); e != nil {
					return e
				}
				work, e = resolveLibraryWork(r.Context(), tx, input.TargetDatasetID, draft.ID, draft.Metadata)
				if e != nil {
					return e
				}
			}
			folder := input.FolderID
			if draft != nil && draft.FolderID != nil {
				folder = *draft.FolderID
			}
			var existing int64
			if e = tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND academic_work_id=? AND removed_at IS NULL", input.TargetDatasetID, work.ID).Count(&existing).Error; e != nil {
				return e
			}
			row, e := importedLibraryEntry(r.Context(), tx, input.TargetDatasetID, work, draft, folder, paperlibrary.ImportMethod(input.EntryType), true)
			if e != nil {
				return e
			}
			kind, status := "metadata_only", "succeeded"
			if existing > 0 {
				kind, status = "already_present", "skipped"
				batch.SkippedItems++
			} else {
				batch.CompletedItems++
			}
			snapshot := marshal(map[string]any{})
			if input.EntryType == "paper_link" {
				for _, preview := range receipt.Items {
					if preview.WorkID == work.ID {
						ref := preview.Paper
						snapshot = marshal(savedImportCandidate{FulltextCandidate: FulltextCandidate{URL: ref.PDFURL, SourceProvider: ref.SourceProvider, Version: ref.SourceVersion}, VerifiedPaper: &ref})
						break
					}
				}
			}
			items = append(items, orm.PaperImportItem{ID: uuid.NewString(), BatchID: batch.ID, LibraryPaperID: row.ID, AcademicWorkID: work.ID, TitleSnapshot: work.CanonicalTitle, ResultKind: kind, Status: status, Stage: "done", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: snapshot, ErrorDetailsJSON: marshal(map[string]any{}), CreatedAt: now, UpdatedAt: now})
			ids = append(ids, row.ID)
		}
		batch.TotalItems = len(items)
		if e = tx.Create(&batch).Error; e != nil {
			return e
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	out, e := libraryResult(r, store.DB(), input.TargetDatasetID, ids)
	if e != nil {
		replyLibraryError(w, e)
		return
	}
	for _, f := range input.Folders {
		var row orm.PaperLibraryFolder
		if e = store.DB().Where("dataset_id=? AND id=?", input.TargetDatasetID, f.ID).Take(&row).Error; e != nil {
			replyLibraryError(w, e)
			return
		}
		out.Data.Folders = append(out.Data.Folders, paperlibrary.Folder{ID: row.ID, Name: row.Name, ParentID: paperlibrary.Pointer(row.ParentID), Revision: row.Revision})
	}
	common.ReplyJSON(w, struct {
		libraryChanges
		BatchID string `json:"batch_id"`
		Status  string `json:"status"`
	}{out, batch.ID, "succeeded"})
}
func libraryImportDataset(r *http.Request, dataset string) (orm.Dataset, error) {
	var row orm.Dataset
	err := store.DB().WithContext(r.Context()).Where("id=? AND scene='academic_papers' AND deleted_at IS NULL", dataset).Take(&row).Error
	return row, err
}
func confirmLibraryUpload(ctx context.Context, tx *gorm.DB, user string, batch orm.PaperImportBatch, input createImportRequest) (string, error) {
	if len(input.Items) != 1 {
		return "", invalidLibraryMutation("INVALID_REQUEST", "一次只能确认一份 PDF")
	}
	selected := input.Items[0]
	var item orm.PaperImportItem
	if err := tx.Where("batch_id=? AND document_id=?", batch.ID, selected.DocumentID).Take(&item).Error; err != nil {
		return "", err
	}
	if !selected.Confirmed {
		return "", invalidLibraryMutation("ATTACHMENT_CONFIRMATION_REQUIRED", "请先核对并确认 PDF 关联")
	}
	if item.Stage == "done" {
		var policy struct {
			PaperID string `json:"confirmation_paper_id"`
		}
		if e := json.Unmarshal(batch.PolicySnapshotJSON, &policy); e != nil {
			return "", e
		}
		if selected.Paper == nil || (policy.PaperID != selected.Paper.ID && item.LibraryPaperID != selected.Paper.ID) {
			return "", invalidLibraryMutation("INVALID_REQUEST", "PDF 确认请求与首次提交不同")
		}
		return item.LibraryPaperID, nil
	}
	if item.Stage != "awaiting_association" || selected.Paper == nil || selected.Paper.ID != item.LibraryPaperID {
		return "", invalidLibraryMutation("INVALID_REQUEST", "确认目标与上传请求不同")
	}
	var document orm.Document
	if err := tx.Where("dataset_id=? AND id=? AND deleted_at IS NULL", batch.TargetDatasetID, selected.DocumentID).Take(&document).Error; err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(document.DisplayName), ".pdf") {
		return "", invalidLibraryMutation("PDF_REQUIRED", "请选择 PDF 文件")
	}
	if v := selected.Candidate.Version; v != "" && (len(v) > 16 || !arxivVersionOnlyPattern.MatchString(v)) {
		return "", invalidLibraryMutation("INVALID_REQUEST", "PDF 来源版本无效")
	}
	draft := selected.Paper
	if err := validateLibraryMetadata(&draft.Metadata); err != nil {
		return "", err
	}
	var entry orm.PaperLibraryEntry
	err := tx.Where("dataset_id=? AND id=? AND removed_at IS NULL", batch.TargetDatasetID, draft.ID).Take(&entry).Error
	var work orm.AcademicWork
	if err == nil {
		entry, err = paperlibrary.Entry(tx, batch.TargetDatasetID, draft.ID, selected.Revision)
		if err != nil {
			return "", err
		}
		if err = tx.Where("id=?", entry.AcademicWorkID).Take(&work).Error; err != nil {
			return "", err
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		work, err = resolveLibraryWork(ctx, tx, batch.TargetDatasetID, draft.ID, draft.Metadata)
		if err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	var state orm.AcademicDocumentState
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id=? AND dataset_id=?", document.ID, document.DatasetID).Take(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if state.Kind == "other" && state.Status == "succeeded" {
		return "", invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "文件被识别为非论文，请先核对识别结果")
	}
	if state.Kind == "paper" && state.PaperID != "" {
		var identified orm.AcademicWork
		if err = tx.Where("id=?", state.PaperID).Take(&identified).Error; err != nil {
			return "", err
		}
		if paperlibrary.IdentityConflict(paperlibrary.WorkMetadata(work).Identifiers, paperlibrary.WorkMetadata(identified).Identifiers) {
			return "", invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "这个 PDF 属于另一篇论文，未关联")
		}
	}
	folder := input.FolderID
	if draft.FolderID != nil {
		folder = *draft.FolderID
	}
	entry, err = importedLibraryEntry(ctx, tx, batch.TargetDatasetID, work, draft, folder, "upload", true)
	if err != nil {
		return "", err
	}
	hash := item.ContentSHA256
	if hash == "" {
		hash = documentFileHash(document)
	}
	if hash != "" {
		var other int64
		if err = tx.Table("paper_library_attachments a").Joins("JOIN paper_library_entries e ON e.dataset_id=a.dataset_id AND e.id=a.paper_id AND e.removed_at IS NULL").Joins("JOIN academic_work_documents l ON l.dataset_id=a.dataset_id AND l.document_id=a.document_id").Where("a.dataset_id=? AND a.paper_id<>? AND a.detached_at IS NULL AND l.content_sha256=?", batch.TargetDatasetID, entry.ID, hash).Count(&other).Error; err != nil {
			return "", err
		}
		if other > 0 {
			return "", invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "相同 PDF 已关联另一篇论文，请核对归属")
		}
	}
	var duplicate orm.PaperLibraryAttachment
	q := tx.Table("paper_library_attachments a").Select("a.*").Joins("JOIN documents d ON d.id=a.document_id AND d.dataset_id=a.dataset_id AND d.deleted_at IS NULL").Joins("LEFT JOIN academic_work_documents l ON l.document_id=a.document_id AND l.dataset_id=a.dataset_id").Where("a.dataset_id=? AND a.paper_id=? AND a.detached_at IS NULL", batch.TargetDatasetID, entry.ID)
	if hash != "" {
		q = q.Where("a.document_id=? OR l.content_sha256=?", document.ID, hash)
	} else {
		q = q.Where("a.document_id=?", document.ID)
	}
	e := q.Take(&duplicate).Error
	result := "imported"
	if e == nil {
		result = "already_present"
		if duplicate.DocumentID != document.ID {
			now := time.Now().UTC()
			discarded := orm.PaperLibraryAttachment{DatasetID: document.DatasetID, ID: "document-" + document.ID, PaperID: entry.ID, DocumentID: document.ID, ImportItemID: item.ID, Name: document.DisplayName, Kind: "pdf", DetachedAt: &now, CreatedAt: now, UpdatedAt: now}
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&discarded).Error; err != nil {
				return "", err
			}
		}
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return "", e
	} else {
		link := orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: document.DatasetID, DocumentID: document.ID, SourceProvider: "upload", SourceVersion: selected.Candidate.Version, ContentSHA256: hash, MatchMethod: "manual", MatchConfidence: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err = saveDocumentWorkLink(tx, link); err != nil {
			return "", err
		}
		state.DocumentID, state.DatasetID, state.PaperID = document.ID, document.DatasetID, work.ID
		state.Kind, state.Status, state.Manual, state.Error = "paper", "succeeded", true, ""
		state.Revision++
		state.MetadataJSON = marshal(map[string]any{})
		state.UpdatedAt = time.Now().UTC()
		if err = tx.Save(&state).Error; err != nil {
			return "", err
		}
		if _, err = registerRecognizedLibraryDocument(ctx, tx, document, work, true, entry.FolderID, item.ID, "upload"); err != nil {
			return "", err
		}
	}
	if err = tx.Model(&item).Updates(map[string]any{"library_paper_id": entry.ID, "academic_work_id": work.ID, "stage": "done", "status": "succeeded", "result_kind": result, "updated_at": time.Now().UTC()}).Error; err != nil {
		return "", err
	}
	err = tx.Model(&batch).Updates(map[string]any{"status": "succeeded", "completed_items": 1, "needs_action_items": 0, "policy_snapshot_json": marshal(map[string]any{"confirmation_paper_id": selected.Paper.ID}), "updated_at": time.Now().UTC()}).Error
	return entry.ID, err
}
