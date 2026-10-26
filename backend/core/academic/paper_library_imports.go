package academic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type LibraryImportItem struct {
	PaperID string `json:"paper_id"`
}
type LibraryImportContext struct {
	IdempotencyKey string              `json:"idempotency_key"`
	EntryType      string              `json:"entry_type"`
	Format         string              `json:"format,omitempty"`
	FileName       string              `json:"file_name,omitempty"`
	PreviewToken   string              `json:"preview_token,omitempty"`
	Items          []LibraryImportItem `json:"items"`
}
type importLibraryPaper struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	AcademicWorkID string `json:"academicWorkId"`
}

func importLibraryPapers(data json.RawMessage) (map[string]importLibraryPaper, error) {
	var graph struct {
		Papers []importLibraryPaper `json:"papers"`
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	result := map[string]importLibraryPaper{}
	for _, paper := range graph.Papers {
		result[paper.ID] = paper
	}
	return result, nil
}

// Saving the graph and its import receipt is one compare-and-swap transaction.
// A replay returns the current saved graph without applying its old snapshot.
func savePaperLibraryImport(ctx context.Context, db *gorm.DB, dataset, user string, revision int64, data json.RawMessage, input *LibraryImportContext) (saved, replay bool, err error) {
	if input == nil {
		saved, err = savePaperLibrary(ctx, db, dataset, revision, data)
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 || len(input.Items) == 0 || (input.EntryType != "bibliography" && input.EntryType != "paper_link") {
		return false, false, errors.New("invalid library import context")
	}
	if input.EntryType == "bibliography" && input.Format != "bib" && input.Format != "ris" && input.Format != "csv" {
		return false, false, errors.New("invalid bibliography format")
	}
	var receipt importPreviewReceipt
	if input.EntryType == "paper_link" {
		var count int64
		if err = db.WithContext(ctx).Model(&orm.PaperImportBatch{}).Where("created_by=? AND idempotency_key=?", user, input.IdempotencyKey).Count(&count).Error; err != nil {
			return
		}
		if count == 0 {
			receipt, err = loadImportPreview(ctx, input.PreviewToken, user, dataset)
			if err != nil {
				return
			}
		}
	}

	next, err := importLibraryPapers(data)
	if err != nil {
		return false, false, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.PaperImportBatch
		findErr := tx.Where("created_by=? AND idempotency_key=?", user, input.IdempotencyKey).Take(&existing).Error
		if findErr == nil {
			if existing.TargetDatasetID != dataset || existing.EntryType != input.EntryType {
				return errors.New("import request key is already in use")
			}
			var items []orm.PaperImportItem
			if e := tx.Where("batch_id=?", existing.ID).Find(&items).Error; e != nil {
				return e
			}
			ids := map[string]bool{}
			for _, item := range items {
				ids[item.LibraryPaperID] = true
			}
			for _, item := range input.Items {
				if !ids[item.PaperID] {
					return errors.New("import request does not match its original papers")
				}
			}
			saved, replay = true, true
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		var previous orm.PaperLibrary
		findErr = tx.Where("dataset_id=?", dataset).Take(&previous).Error
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		before := map[string]importLibraryPaper{}
		if len(previous.DataJSON) > 0 {
			var e error
			before, e = importLibraryPapers(previous.DataJSON)
			if e != nil {
				return e
			}
		}
		now := time.Now().UTC()
		source := map[string]string{"format": input.Format, "file_name": input.FileName}
		batch := orm.PaperImportBatch{ID: uuid.NewString(), EntryType: input.EntryType, TargetDatasetID: dataset, SourceContextJSON: marshal(source), SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`), Status: "succeeded", IdempotencyKey: input.IdempotencyKey, CreatedBy: user, CreatedAt: now, UpdatedAt: now}
		items := []orm.PaperImportItem{}
		seen := map[string]bool{}
		for _, selected := range input.Items {
			if seen[selected.PaperID] {
				continue
			}
			seen[selected.PaperID] = true
			paper, ok := next[selected.PaperID]
			if !ok {
				return errors.New("imported paper is missing from the saved library")
			}
			item := orm.PaperImportItem{ID: uuid.NewString(), BatchID: batch.ID, LibraryPaperID: paper.ID, TitleSnapshot: paper.Title, AcademicWorkID: paper.AcademicWorkID, ResultKind: "metadata_only", Status: "succeeded", Stage: "done", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
			if _, exists := before[paper.ID]; exists {
				item.Status = "skipped"
				item.ResultKind = "already_present"
			}
			if input.EntryType == "paper_link" {
				var preview *PreviewItem
				for i := range receipt.Items {
					if receipt.Items[i].WorkID == paper.AcademicWorkID {
						preview = &receipt.Items[i]
						break
					}
				}
				if preview == nil {
					return errors.New("linked paper does not match the import preview")
				}
				if len(preview.Candidates) > 0 && preview.Disposition != "already_present" {
					return errors.New("downloadable papers must use the import queue")
				}
				if preview.Disposition == "already_present" {
					for _, document := range preview.Presence.CurrentDataset {
						if document.DatasetID == dataset {
							item.DocumentID = document.DocumentID
							item.SelectedCandidateJSON = marshal(FulltextCandidate{SourceProvider: document.SourceProvider, Version: document.SourceVersion})
							item.ResultKind = "already_present"
							item.Status = "skipped"
							break
						}
					}
				}
			}
			if item.Status == "skipped" {
				batch.SkippedItems++
			} else {
				batch.CompletedItems++
			}
			items = append(items, item)
		}
		batch.TotalItems = len(items)
		var e error
		saved, e = savePaperLibrary(ctx, tx, dataset, revision, data)
		if e != nil || !saved {
			return e
		}
		if e = tx.Create(&batch).Error; e != nil {
			return e
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		saved = false
	}
	return
}
