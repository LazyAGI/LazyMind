package academic

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// ImportZoteroSnapshot commits confirmed metadata before any optional PDF uploads.
// Stable source IDs make retries additive; normalized user edits are never replaced.
func ImportZoteroSnapshot(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 48<<20)
	var input struct {
		SourceKeys     []string        `json:"source_keys"`
		Data           json.RawMessage `json:"data"`
		SourceID       string          `json:"source_id"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Data) == 0 || len(input.SourceID) > 512 || len(input.IdempotencyKey) > 255 || strings.TrimSpace(input.IdempotencyKey) == "" {
		replyError(w, 400, "INVALID_REQUEST", "Zotero 导入内容无效或超过大小限制")
		return
	}
	if err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockZoteroDataset(tx, dataset); err != nil {
			return err
		}
		if err := paperlibrary.ImportSnapshot(r.Context(), tx, dataset, store.UserID(r), input.Data); err != nil {
			return err
		}
		return recordZoteroImport(r.Context(), tx, dataset, store.UserID(r), input.SourceID, input.IdempotencyKey, input.SourceKeys, input.Data)
	}); err != nil {
		replyLibraryError(w, err)
		return
	}
	result, err := zoteroView(r.Context(), store.DB(), dataset, store.UserID(r))
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	common.ReplyJSON(w, result)
}

// Receipts live with the library, so a browser refresh cannot erase import history.
func recordZoteroImport(ctx context.Context, db *gorm.DB, dataset, user, source, key string, keys []string, snapshots ...json.RawMessage) error {
	var count int64
	if err := db.Model(&orm.PaperImportBatch{}).Where("target_dataset_id=? AND created_by=? AND idempotency_key=?", dataset, user, key).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	view, err := paperlibrary.Load(ctx, db, dataset, user, nil, true)
	if err != nil {
		return err
	}
	var graph struct {
		Papers []struct {
			ID            string
			ZoteroSources []string
		}
	}
	if len(view.Data.Extra) > 0 {
		if err = json.Unmarshal(view.Data.Extra, &graph); err != nil {
			return err
		}
	}
	selected := map[string]bool{}
	for _, k := range keys {
		selected[source+":"+k] = true
	}
	ids := []string{}
	// Unkeyed snapshots (backup, browser migration, older clients) still need a
	// receipt. Match only the requested papers against the normalized saved view.
	if len(keys) == 0 && len(snapshots) > 0 {
		var incoming paperlibrary.Data
		if err = json.Unmarshal(snapshots[0], &incoming); err != nil {
			return err
		}
		// The importer keeps an existing paper ID before consulting scholarly
		// identifiers. Do not let stale identifiers pull unrelated papers into
		// this receipt, and count a deduplicated destination only once.
		byID := make(map[string]paperlibrary.Paper, len(view.Data.Papers))
		byDOI, byArxiv := map[string]string{}, map[string]string{}
		for _, p := range view.Data.Papers {
			byID[p.ID] = p
			if doi := paperlibrary.DOI(p.Metadata.Identifiers.DOI); doi != "" {
				byDOI[doi] = p.ID
			}
			if arxiv := paperlibrary.ArxivBase(p.Metadata.Identifiers.Arxiv); arxiv != "" {
				byArxiv[arxiv] = p.ID
			}
		}
		matched := map[string]bool{}
		for _, p := range incoming.Papers {
			id := ""
			if _, exists := byID[p.ID]; exists {
				id = p.ID
			} else if doi := paperlibrary.DOI(p.Metadata.Identifiers.DOI); doi != "" && byDOI[doi] != "" {
				id = byDOI[doi]
			} else if arxiv := paperlibrary.ArxivBase(p.Metadata.Identifiers.Arxiv); arxiv != "" {
				id = byArxiv[arxiv]
			}
			if id != "" && !matched[id] {
				matched[id] = true
				ids = append(ids, id)
			}
		}
	}
	for _, p := range graph.Papers {
		match := false
		for _, identity := range p.ZoteroSources {
			if selected[identity] {
				match = true
			}
		}
		if match {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	kind := "zotero"
	if source == "library-backup" {
		kind = "library_backup"
	} else if source == "browser-local-v3" {
		kind = "library_migration"
	}
	batch := orm.PaperImportBatch{ID: uuid.NewString(), EntryType: kind, TargetDatasetID: dataset, CreatedBy: user, IdempotencyKey: key, Status: "succeeded", TotalItems: len(ids), CompletedItems: len(ids), SourceContextJSON: marshal(map[string]string{"title": source}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{}), CreatedAt: now, UpdatedAt: now}
	if err = db.Create(&batch).Error; err != nil {
		return err
	}
	attachments := map[string]paperlibrary.Attachment{}
	for _, attachment := range view.Data.Attachments {
		if attachment.Kind == "pdf" && attachment.ContentRef != "" && attachment.Available {
			if _, exists := attachments[attachment.PaperID]; !exists || attachment.IsPrimary {
				attachments[attachment.PaperID] = attachment
			}
		}
	}
	// Resolve titles in bounded batches, rather than two queries and one insert per paper.
	for offset := 0; offset < len(ids); offset += 200 {
		chunk := ids[offset:min(offset+200, len(ids))]
		var entries []orm.PaperLibraryEntry
		if err = db.Where("dataset_id=? AND id IN ? AND removed_at IS NULL", dataset, chunk).Find(&entries).Error; err != nil {
			return err
		}
		byID := make(map[string]orm.PaperLibraryEntry, len(entries))
		workIDs := make([]string, 0, len(entries))
		for _, entry := range entries {
			byID[entry.ID] = entry
			workIDs = append(workIDs, entry.AcademicWorkID)
		}
		var works []orm.AcademicWork
		if err = db.Where("id IN ?", workIDs).Find(&works).Error; err != nil {
			return err
		}
		byWork := make(map[string]orm.AcademicWork, len(works))
		for _, work := range works {
			byWork[work.ID] = work
		}
		items := make([]orm.PaperImportItem, 0, len(chunk))
		for _, id := range chunk {
			entry, exists := byID[id]
			if !exists {
				return gorm.ErrRecordNotFound
			}
			work, exists := byWork[entry.AcademicWorkID]
			if !exists {
				return gorm.ErrRecordNotFound
			}
			resultKind, documentID := "metadata_only", ""
			if attachment, exists := attachments[id]; exists {
				resultKind, documentID = "fulltext", attachment.ContentRef
			}
			items = append(items, orm.PaperImportItem{DocumentID: documentID, TitleSnapshot: work.CanonicalTitle, ID: uuid.NewString(), BatchID: batch.ID, AcademicWorkID: entry.AcademicWorkID, LibraryPaperID: id, Status: "succeeded", Stage: "done", ResultKind: resultKind, ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{}), CreatedAt: now, UpdatedAt: now})
		}
		if err = db.CreateInBatches(items, 200).Error; err != nil {
			return err
		}
	}
	return nil
}
