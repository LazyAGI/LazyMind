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
	if source == "library-backup" && len(snapshots) > 0 {
		var incoming paperlibrary.Data
		if err = json.Unmarshal(snapshots[0], &incoming); err != nil {
			return err
		}
		requested := map[string]bool{}
		for _, p := range incoming.Papers {
			requested["id:"+p.ID] = true
			if p.Metadata.Identifiers.DOI != "" {
				requested["doi:"+strings.ToLower(p.Metadata.Identifiers.DOI)] = true
			}
			if p.Metadata.Identifiers.Arxiv != "" {
				requested["arxiv:"+p.Metadata.Identifiers.Arxiv] = true
			}
		}
		for _, p := range view.Data.Papers {
			if requested["id:"+p.ID] || (p.Metadata.Identifiers.DOI != "" && requested["doi:"+strings.ToLower(p.Metadata.Identifiers.DOI)]) || (p.Metadata.Identifiers.Arxiv != "" && requested["arxiv:"+p.Metadata.Identifiers.Arxiv]) {
				ids = append(ids, p.ID)
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
	}
	batch := orm.PaperImportBatch{ID: uuid.NewString(), EntryType: kind, TargetDatasetID: dataset, CreatedBy: user, IdempotencyKey: key, Status: "succeeded", TotalItems: len(ids), CompletedItems: len(ids), SourceContextJSON: marshal(map[string]string{"title": source}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{}), CreatedAt: now, UpdatedAt: now}
	if err = db.Create(&batch).Error; err != nil {
		return err
	}
	for _, id := range ids {
		var entry orm.PaperLibraryEntry
		if err = db.Where("dataset_id=? AND id=? AND removed_at IS NULL", dataset, id).Take(&entry).Error; err != nil {
			return err
		}
		var work orm.AcademicWork
		if err = db.Where("id=?", entry.AcademicWorkID).Take(&work).Error; err != nil {
			return err
		}
		item := orm.PaperImportItem{TitleSnapshot: work.CanonicalTitle, ID: uuid.NewString(), BatchID: batch.ID, AcademicWorkID: entry.AcademicWorkID, LibraryPaperID: id, Status: "succeeded", Stage: "done", ResultKind: "metadata_only", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{}), CreatedAt: now, UpdatedAt: now}
		if err = db.Create(&item).Error; err != nil {
			return err
		}
	}
	return nil
}
