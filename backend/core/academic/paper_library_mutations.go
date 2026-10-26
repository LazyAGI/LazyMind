package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

type LibraryAttachmentContext struct {
	Operation      string `json:"operation"`
	PaperID        string `json:"paper_id"`
	DocumentID     string `json:"document_id"`
	AttachmentID   string `json:"attachment_id"`
	Confirmed      bool   `json:"confirmed"`
	IdempotencyKey string `json:"idempotency_key"`
}

type libraryMutationError struct {
	Status        int
	Code, Message string
}

func (e *libraryMutationError) Error() string { return e.Message }
func invalidLibraryMutation(code, message string) error {
	return &libraryMutationError{Status: 422, Code: code, Message: message}
}

var errLibraryRevision = errors.New("paper library revision conflict")

func activeLibraryAttachments(graph paperlibrary.Graph) map[string]paperlibrary.Entity {
	attachments := map[string]paperlibrary.Entity{}
	for _, attachment := range paperlibrary.Objects(graph, "attachments") {
		attachments[paperlibrary.Text(attachment, "id")] = attachment
	}
	active := map[string]paperlibrary.Entity{}
	for _, paper := range paperlibrary.Objects(graph, "papers") {
		for _, id := range paperlibrary.StringList(paper["attachmentIds"]) {
			if attachment := attachments[id]; attachment != nil {
				active[paperlibrary.Text(paper, "id")+"\x00"+id] = attachment
			}
		}
	}
	return active
}

func libraryWork(ctx context.Context, tx *gorm.DB, datasetID string, paper paperlibrary.Entity) (orm.AcademicWork, error) {
	if id := paperlibrary.Text(paper, "academicWorkId"); id != "" {
		var work orm.AcademicWork
		err := tx.Where("id=?", id).Take(&work).Error
		return work, err
	}
	authors := strings.FieldsFunc(paperlibrary.Text(paper, "authors"), func(r rune) bool { return r == '、' || r == ';' || r == '；' })
	year, _ := paper["year"].(float64)
	input := WorkInput{Title: paperlibrary.Text(paper, "title"), Authors: authors, Year: int(year), Venue: paperlibrary.Text(paper, "venue"), Abstract: paperlibrary.Text(paper, "abstract"), DOI: paperlibrary.Identifier(paper, "doi"), ArxivID: paperlibrary.Identifier(paper, "arxiv")}
	if input.DOI != "" || input.ArxivID != "" {
		work, _, err := findOrCreateWork(ctx, tx, input)
		return work, err
	}
	// A metadata-only record without identifiers does not prove identity with
	// another record sharing a title. Its stable identity is dataset-scoped.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("library-paper:"+datasetID+":"+paperlibrary.Text(paper, "id"))).String()
	work := orm.AcademicWork{ID: id, CanonicalTitle: input.Title, NormalizedTitle: NormalizeTitle(input.Title), AuthorsJSON: marshal(authors), PublicationYear: input.Year, Venue: input.Venue, Abstract: input.Abstract, ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{"source": "library"}), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if len(authors) > 0 {
		work.FirstAuthorNormalized = normalizeAuthor(authors[0])
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work).Error; err != nil {
		return work, err
	}
	return work, tx.Where("id=?", id).Take(&work).Error
}

func saveLibraryMutation(ctx context.Context, db *gorm.DB, datasetID, userID string, revision int64, raw json.RawMessage, importContext *LibraryImportContext, attachmentContext *LibraryAttachmentContext) (bool, bool, error) {
	var saved, replay bool
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if importContext != nil && importContext.IdempotencyKey != "" {
			var existing orm.PaperImportBatch
			if err := tx.Where("created_by=? AND idempotency_key=?", userID, importContext.IdempotencyKey).Take(&existing).Error; err == nil {
				var err error
				saved, replay, err = savePaperLibraryImport(ctx, tx, datasetID, userID, revision, raw, importContext)
				return err
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var previous orm.PaperLibrary
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=?", datasetID).Take(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.Revision != revision {
			if attachmentContext != nil && attachmentContext.Confirmed {
				if attachmentContext.Operation == "attach" {
					var acknowledged int64
					if err := tx.Model(&orm.PaperImportItem{}).Joins("JOIN paper_import_batches b ON b.id=paper_import_items.batch_id").Where("b.target_dataset_id=? AND b.created_by=? AND b.idempotency_key=? AND b.entry_type=? AND paper_import_items.document_id=? AND paper_import_items.library_paper_id=? AND paper_import_items.stage=? AND paper_import_items.result_kind=?", datasetID, userID, attachmentContext.IdempotencyKey, "pdf_upload", attachmentContext.DocumentID, attachmentContext.PaperID, "done", "already_present").Count(&acknowledged).Error; err != nil {
						return err
					}
					if acknowledged > 0 {
						saved, replay = true, true
						return nil
					}
				}
				graph, decodeErr := paperlibrary.Decode(previous.DataJSON)
				if decodeErr != nil {
					return decodeErr
				}
				key := attachmentContext.PaperID + "\x00" + attachmentContext.AttachmentID
				active := activeLibraryAttachments(graph)
				if attachmentContext.Operation == "detach" && active[key] == nil && containsString(paperlibrary.StringList(graph["hiddenDocumentIds"]), attachmentContext.DocumentID) {
					saved, replay = true, true
					return nil
				}
				if attachmentContext.Operation == "attach" && active[key] != nil && paperlibrary.Text(active[key], "contentRef") == attachmentContext.DocumentID {
					var count int64
					if err := tx.Model(&orm.PaperImportBatch{}).Where("target_dataset_id=? AND created_by=? AND entry_type=? AND idempotency_key=? AND status=?", datasetID, userID, "pdf_upload", attachmentContext.IdempotencyKey, "succeeded").Count(&count).Error; err != nil {
						return err
					}
					if count > 0 {
						saved, replay = true, true
						return nil
					}
				}
			}
			return errLibraryRevision
		}
		views, err := paperlibrary.LoadMany(ctx, tx, []string{datasetID}, "")
		if err != nil {
			return err
		}
		before, err := paperlibrary.Decode(views[datasetID].Data)
		if err != nil {
			return err
		}
		next, err := paperlibrary.Decode(raw)
		if err != nil {
			return err
		}
		papers := map[string]paperlibrary.Entity{}
		for _, paper := range paperlibrary.Objects(next, "papers") {
			work, err := libraryWork(ctx, tx, datasetID, paper)
			if err != nil {
				return err
			}
			paper["academicWorkId"] = work.ID
			paper["important"] = false
			papers[paperlibrary.Text(paper, "id")] = paper
		}
		oldActive, newActive := activeLibraryAttachments(before), activeLibraryAttachments(next)
		previousPapers := map[string]paperlibrary.Entity{}
		for _, paper := range paperlibrary.Objects(before, "papers") {
			previousPapers[paperlibrary.Text(paper, "id")] = paper
		}
		for key, attachment := range newActive {
			old := oldActive[key]
			if old != nil && paperlibrary.Text(old, "contentRef") == paperlibrary.Text(attachment, "contentRef") {
				paperID := paperlibrary.Text(attachment, "paperId")
				if paperlibrary.Text(papers[paperID], "academicWorkId") != paperlibrary.Text(previousPapers[paperID], "academicWorkId") {
					if err := checkAttachmentIdentity(tx, datasetID, papers[paperID], paperlibrary.Text(attachment, "contentRef")); err != nil {
						return err
					}
				}
				continue
			}
			if paperlibrary.Text(attachment, "contentRef") == "" {
				continue
			}
			if attachmentContext == nil || attachmentContext.Operation != "attach" || attachmentContext.PaperID != paperlibrary.Text(attachment, "paperId") || attachmentContext.DocumentID != paperlibrary.Text(attachment, "contentRef") || attachmentContext.AttachmentID != paperlibrary.Text(attachment, "id") || !attachmentContext.Confirmed || strings.TrimSpace(attachmentContext.IdempotencyKey) == "" || len(attachmentContext.IdempotencyKey) > 128 {
				return invalidLibraryMutation("ATTACHMENT_CONFIRMATION_REQUIRED", "新增 PDF 关联需要确认目标题录，请重新添加 PDF")
			}
			if err := confirmLibraryAttachment(ctx, tx, datasetID, userID, papers[attachmentContext.PaperID], attachment, attachmentContext.IdempotencyKey, before, next); err != nil {
				return err
			}
			for _, prior := range oldActive {
				if paperlibrary.Text(prior, "paperId") == attachmentContext.PaperID && prior["isPrimary"] == true && prior["available"] != false && paperlibrary.Text(prior, "kind") == "pdf" && paperlibrary.Text(prior, "contentRef") != "" {
					attachment["isPrimary"] = false
					break
				}
			}
		}
		for key, attachment := range oldActive {
			if newActive[key] != nil || papers[paperlibrary.Text(attachment, "paperId")] == nil {
				continue
			}
			// Link-only supplemental records do not change a stored original's identity.
			if paperlibrary.Text(attachment, "contentRef") == "" {
				continue
			}
			if attachmentContext == nil || attachmentContext.Operation != "detach" || attachmentContext.PaperID != paperlibrary.Text(attachment, "paperId") || attachmentContext.AttachmentID != paperlibrary.Text(attachment, "id") || attachmentContext.DocumentID != paperlibrary.Text(attachment, "contentRef") || !attachmentContext.Confirmed || strings.TrimSpace(attachmentContext.IdempotencyKey) == "" {
				return invalidLibraryMutation("ATTACHMENT_CONFIRMATION_REQUIRED", "移除附件需要确认，请从论文详情操作")
			}
			hidden := paperlibrary.StringList(next["hiddenDocumentIds"])
			if !containsString(hidden, paperlibrary.Text(attachment, "contentRef")) {
				next["hiddenDocumentIds"] = append(hidden, paperlibrary.Text(attachment, "contentRef"))
			}
		}
		if err := paperlibrary.ValidateRelations(next); err != nil {
			return invalidLibraryMutation("INVALID_PAPER_LIBRARY", err.Error())
		}
		data, err := json.Marshal(next)
		if err != nil {
			return err
		}
		saved, replay, err = savePaperLibraryImport(ctx, tx, datasetID, userID, revision, data, importContext)
		if err != nil {
			return err
		}
		if !saved {
			return errLibraryRevision
		}
		return nil
	})
	if errors.Is(err, errLibraryRevision) {
		return false, false, nil
	}
	return saved, replay, err
}

func checkAttachmentIdentity(tx *gorm.DB, datasetID string, paper paperlibrary.Entity, documentID string) error {
	if documentID == "" {
		return nil
	}
	var state orm.AcademicDocumentState
	if err := tx.Where("document_id=? AND dataset_id=?", documentID, datasetID).Take(&state).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if state.Kind == "paper" && state.PaperID != "" {
		var work orm.AcademicWork
		if err := tx.Where("id=?", state.PaperID).Take(&work).Error; err != nil {
			return err
		}
		if paperlibrary.MatchIdentity(paper, paperlibrary.WorkEntity(work)) == "conflict" {
			return invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "论文身份与已有附件不一致，请核对后再修改")
		}
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func confirmLibraryAttachment(ctx context.Context, tx *gorm.DB, datasetID, userID string, paper, attachment paperlibrary.Entity, requestKey string, before, next paperlibrary.Graph) error {
	if paper == nil {
		return invalidLibraryMutation("PAPER_NOT_FOUND", "目标论文已被移除，请重新选择")
	}
	id := paperlibrary.Text(attachment, "contentRef")
	var document orm.Document
	if err := tx.Where("id=? AND dataset_id=? AND deleted_at IS NULL", id, datasetID).Take(&document).Error; err != nil {
		return invalidLibraryMutation("PDF_UNAVAILABLE", "PDF 不存在或不属于当前论文集")
	}
	if !strings.HasSuffix(strings.ToLower(document.DisplayName), ".pdf") {
		return invalidLibraryMutation("PDF_REQUIRED", "请选择 PDF 文件")
	}
	var state orm.AcademicDocumentState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id=? AND dataset_id=?", id, datasetID).Take(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if state.Kind == "other" && state.Status == "succeeded" {
		return invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "文件被识别为非论文，请先核对识别结果")
	}
	if state.Kind == "paper" && state.PaperID != "" {
		var identified orm.AcademicWork
		if err := tx.Where("id=?", state.PaperID).Take(&identified).Error; err != nil {
			return err
		}
		if paperlibrary.MatchIdentity(paper, paperlibrary.WorkEntity(identified)) == "conflict" {
			return invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "这个 PDF 属于另一篇论文，未关联；请更换文件或作为新论文导入")
		}
	}
	var links []orm.AcademicWorkDocument
	if err := tx.Where("dataset_id=? AND academic_work_id=?", datasetID, paperlibrary.Text(paper, "academicWorkId")).Find(&links).Error; err != nil {
		return err
	}
	var receipt orm.PaperImportItem
	if err := tx.Where("document_id=? AND content_sha256<>?", id, "").Order("created_at DESC").Take(&receipt).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	hash := receipt.ContentSHA256
	if hash == "" {
		hash = documentFileHash(document)
	}
	for _, prior := range activeLibraryAttachments(before) {
		if paperlibrary.Text(prior, "paperId") != paperlibrary.Text(paper, "id") || prior["available"] == false {
			continue
		}
		priorHash := paperlibrary.Text(prior, "contentHash")
		for _, link := range links {
			if link.DocumentID == paperlibrary.Text(prior, "contentRef") && priorHash == "" {
				priorHash = link.ContentSHA256
			}
		}
		if paperlibrary.Text(prior, "contentRef") != id && (hash == "" || priorHash != hash) {
			continue
		}
		incomingID, priorID := paperlibrary.Text(attachment, "id"), paperlibrary.Text(prior, "id")
		ids := []string{}
		for _, attachmentID := range paperlibrary.StringList(paper["attachmentIds"]) {
			if attachmentID == incomingID {
				attachmentID = priorID
			}
			if !containsString(ids, attachmentID) {
				ids = append(ids, attachmentID)
			}
		}
		paper["attachmentIds"] = ids
		kept := []paperlibrary.Entity{}
		for _, candidate := range paperlibrary.Objects(next, "attachments") {
			if paperlibrary.Text(candidate, "id") != incomingID || incomingID == priorID {
				kept = append(kept, candidate)
			}
		}
		paperlibrary.SetObjects(next, "attachments", kept)
		if id != paperlibrary.Text(prior, "contentRef") {
			hidden := paperlibrary.StringList(next["hiddenDocumentIds"])
			if !containsString(hidden, id) {
				next["hiddenDocumentIds"] = append(hidden, id)
			}
		}
		return acknowledgeLibraryUpload(tx, datasetID, userID, requestKey, id, paperlibrary.Text(paper, "id"), paperlibrary.Text(paper, "academicWorkId"), "already_present")
	}
	var existing orm.AcademicWorkDocument
	if err := tx.Where("document_id=?", id).Take(&existing).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	link := existing
	link.AcademicWorkID, link.DatasetID, link.DocumentID = paperlibrary.Text(paper, "academicWorkId"), datasetID, id
	if link.SourceProvider == "" {
		link.SourceProvider = "upload"
	}
	if link.SourceVersion == "" {
		link.SourceVersion = paperlibrary.Text(attachment, "sourceVersion")
	}
	link.ContentSHA256, link.MatchMethod, link.MatchConfidence = hash, "manual", 1
	if link.CreatedAt.IsZero() {
		link.CreatedAt = time.Now().UTC()
	}
	link.UpdatedAt = time.Now().UTC()
	if err := tx.Where("document_id=?", id).Delete(&orm.AcademicWorkDocument{}).Error; err != nil {
		return err
	}
	if err := tx.Create(&link).Error; err != nil {
		return err
	}
	state.DocumentID, state.DatasetID, state.PaperID = id, datasetID, link.AcademicWorkID
	state.Kind, state.Status, state.Manual, state.Error = "paper", "succeeded", true, ""
	state.Revision++
	state.UpdatedAt = time.Now().UTC()
	if len(state.MetadataJSON) == 0 {
		state.MetadataJSON = marshal(map[string]any{})
	}
	if err := tx.Save(&state).Error; err != nil {
		return err
	}
	attachment["contentHash"] = hash
	return acknowledgeLibraryUpload(tx, datasetID, userID, requestKey, id, paperlibrary.Text(paper, "id"), link.AcademicWorkID, "imported")
}

func acknowledgeLibraryUpload(tx *gorm.DB, datasetID, userID, requestKey, documentID, paperID, workID, resultKind string) error {
	// The upload receipt and graph are acknowledged by this same transaction.
	var batches []orm.PaperImportBatch
	if err := tx.Where("target_dataset_id=? AND created_by=? AND entry_type=? AND idempotency_key=?", datasetID, userID, "pdf_upload", requestKey).Find(&batches).Error; err != nil {
		return err
	}
	for _, batch := range batches {
		if err := tx.Model(&orm.PaperImportItem{}).Where("batch_id=? AND document_id=?", batch.ID, documentID).Updates(map[string]any{"library_paper_id": paperID, "academic_work_id": workID, "stage": "done", "status": "succeeded", "result_kind": resultKind, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&batch).Updates(map[string]any{"status": "succeeded", "completed_items": 1, "needs_action_items": 0, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// SetLibraryImportance permits personal marks on metadata-only papers without
// requiring a PDF or storing another user's mark in the shared graph.
func SetLibraryImportance(w http.ResponseWriter, r *http.Request) {
	datasetID, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		IsImportant bool   `json:"is_important"`
		Revision    *int64 `json:"revision"`
	}
	if !parseJSON(r, &input) || input.Revision == nil {
		replyError(w, 400, "INVALID_REQUEST", "revision is required")
		return
	}
	var result paperLibraryResponse
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var library orm.PaperLibrary
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=?", datasetID).Take(&library).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if library.Revision != *input.Revision {
			return errLibraryRevision
		}
		views, err := paperlibrary.LoadMany(r.Context(), tx, []string{datasetID}, "")
		if err != nil {
			return err
		}
		graph, err := paperlibrary.Decode(views[datasetID].Data)
		if err != nil {
			return err
		}
		var paper paperlibrary.Entity
		for _, candidate := range paperlibrary.Objects(graph, "papers") {
			if paperlibrary.Text(candidate, "id") == mux.Vars(r)["library_paper_id"] {
				paper = candidate
				break
			}
		}
		if paper == nil {
			return invalidLibraryMutation("PAPER_NOT_FOUND", "论文不存在")
		}
		work, err := libraryWork(r.Context(), tx, datasetID, paper)
		if err != nil {
			return err
		}
		if paperlibrary.Text(paper, "academicWorkId") == "" {
			paper["academicWorkId"] = work.ID
			data, err := json.Marshal(graph)
			if err != nil {
				return err
			}
			saved, err := savePaperLibrary(r.Context(), tx, datasetID, library.Revision, data)
			if err != nil {
				return err
			}
			if !saved {
				return errLibraryRevision
			}
		}
		mark := orm.AcademicUserPaperState{UserID: store.UserID(r), PaperID: work.ID, IsImportant: input.IsImportant, UpdatedAt: time.Now().UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "paper_id"}}, DoUpdates: clause.AssignmentColumns([]string{"is_important", "updated_at"})}).Create(&mark).Error
	})
	if errors.Is(err, errLibraryRevision) {
		replyError(w, 409, "PAPER_LIBRARY_CONFLICT", "论文库已更新，请重新加载后标记")
		return
	}
	if err != nil {
		var invalid *libraryMutationError
		if errors.As(err, &invalid) {
			replyError(w, invalid.Status, invalid.Code, invalid.Message)
		} else {
			replyError(w, 500, "PAPER_LIBRARY_SAVE_FAILED", "重点标记保存失败")
		}
		return
	}
	result, err = resolvePaperLibrary(r.Context(), store.DB(), datasetID, store.UserID(r))
	if err != nil {
		replyError(w, 500, "PAPER_LIBRARY_LOAD_FAILED", "无法读取最新论文库")
		return
	}
	result.CanWrite = canWrite
	common.ReplyJSON(w, result)
}
