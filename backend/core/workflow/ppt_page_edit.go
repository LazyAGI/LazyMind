package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type pptNotesFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Only completed files in the owner's upload directory can replace page notes.
func (file pptNotesFile) verify(owner string) error {
	if owner == "" || !filepath.IsAbs(file.Path) {
		return ErrConflict
	}
	directory := doc.TempUserFilesRoot(owner)
	relative, err := filepath.Rel(directory, file.Path)
	if err != nil || !filepath.IsLocal(relative) {
		return ErrConflict
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrConflict
	}
	defer root.Close()
	info, err := root.Stat(relative)
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
		return ErrConflict
	}
	return nil
}

type pptPageField struct {
	File         *pptNotesFile `json:"file"`
	ListIndex    *int          `json:"list_index"`
	Revision     int           `json:"revision"`
	DraftVersion *int64        `json:"draft_version"`
	Text         *string       `json:"text"`
}

type pptPageEdit struct {
	Slide pptPageField  `json:"slide"`
	Notes *pptPageField `json:"notes"`
}

type pptPageResult struct {
	Revision      int       `json:"revision"`
	NotesRevision int       `json:"notes_revision,omitempty"`
	StateVersion  int64     `json:"state_version"`
	Changes       []slotDTO `json:"changes"`
}

func handlePPTPageEdit(w http.ResponseWriter, r *http.Request, sid string, idx int, edit pptPageEdit) {
	if edit.Slide.File != nil || (edit.Notes != nil && edit.Notes.File != nil &&
		(edit.Notes.Text != nil || edit.Notes.File.Path == "" || edit.Notes.File.Size < 0)) {
		common.ReplyErr(w, "invalid page edit", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	result, err := savePPTPage(r.Context(), db, sid, store.UserID(r), idx, edit)
	if replyDraftVersionPreconditionError(w, err) {
		return
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "page changed or is not editable", 409)
		return
	}
	if err != nil {
		common.ReplyErr(w, "page save failed", 500)
		return
	}
	common.ReplyOK(w, result)
}

func savePPTPage(ctx context.Context, db *gorm.DB, sid, owner string, idx int, edit pptPageEdit) (pptPageResult, error) {
	if edit.Notes != nil && edit.Notes.File != nil {
		if err := edit.Notes.File.verify(owner); err != nil {
			return pptPageResult{}, err
		}
	}
	var result pptPageResult
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		result = pptPageResult{Changes: []slotDTO{}}
		session, err := lockArtifactMutationSession(tx, sid)
		if err != nil {
			return err
		}
		if !isPPTPreviewSlot(session, "preview_html") {
			return ErrConflict
		}
		step, err := GetLatestStep(ctx, tx, sid, "generate_ppt")
		if err != nil {
			return err
		}
		if step == nil || step.Status != StepStatusSucceeded {
			return ErrConflict
		}
		get := func(slot string, index int, field pptPageField) (orm.WorkflowSlotRevision, error) {
			var row orm.WorkflowSlotRevision
			err := tx.Where("session_id = ? AND slot_id = ? AND list_index = ? AND selected = ? AND validity = ?", sid, slot, index, true, "effective").First(&row).Error
			if err != nil {
				return row, err
			}
			if row.Revision != field.Revision || row.StepID != step.StepID || row.Attempt != step.Attempt {
				return row, ErrConflict
			}
			if row.HumanArtifactID != nil {
				var art orm.WorkflowHumanArtifact
				if err := tx.Select("draft_version").First(&art, "id = ?", *row.HumanArtifactID).Error; err != nil {
					return row, err
				}
				if field.DraftVersion == nil || *field.DraftVersion != art.DraftVersion {
					return row, ErrDraftVersionConflict
				}
			}
			return row, nil
		}
		slide, err := get("preview_html", idx, edit.Slide)
		if err != nil {
			return err
		}
		write := func(row orm.WorkflowSlotRevision, field pptPageField, force bool) (orm.WorkflowSlotRevision, error) {
			previous, err := LoadSlotRevisionValue(ctx, tx, row)
			if err != nil {
				return row, err
			}
			next := previous
			if field.File != nil {
				var carrier map[string]any
				if json.Unmarshal(previous, &carrier) != nil || carrier["path"] == nil ||
					(carrier["type"] != "text" && carrier["type"] != "json") {
					return row, ErrConflict
				}
				if carrier["path"] != field.File.Path {
					carrier["type"], carrier["path"], carrier["size"] = "text", field.File.Path, field.File.Size
					delete(carrier, "url")
					next, err = json.Marshal(carrier)
					if err != nil {
						return row, err
					}
					force = true
				}
			} else if field.Text != nil {
				var content struct {
					Text *string `json:"text"`
				}
				_ = json.Unmarshal(common.CanonicalizeTextArtifactValue("text", previous), &content)
				if content.Text == nil || *content.Text != *field.Text {
					next, err = json.Marshal(map[string]string{"text": *field.Text})
					if err != nil {
						return row, err
					}
					force = true
				}
			}
			if !force {
				return row, nil
			}
			saved, err := writeSlotRevisionWithHumanArtifact(ctx, tx, sid, row.SlotID, row.Slot, row.StepID, row.Attempt, "list", row.ListIndex, "text", next, nil, "human", &field.Revision, field.DraftVersion, false, nil, slotWriteOptions{preserveNotesFile: field.File != nil})
			if err != nil {
				return row, err
			}
			change := toSlotDTO(saved)
			change.DraftVersion = 1 // New human artifact checkpoints start at version one.
			result.Changes = append(result.Changes, change)
			return *saved, nil
		}
		var oldNotes, newNotes orm.WorkflowSlotRevision
		if edit.Notes != nil {
			order, err := ListIndexToSortOrder(ctx, tx, sid, slide.SlotID, idx)
			if err != nil {
				return err
			}
			notesIndex := idx
			if order > 0 {
				notesIndex, err = SortOrderToListIndex(ctx, tx, sid, "preview_notes", order)
				if err != nil {
					return err
				}
				if notesIndex < 0 {
					return ErrConflict
				}
			}
			if edit.Notes.ListIndex == nil || *edit.Notes.ListIndex != notesIndex {
				return ErrConflict
			}
			oldNotes, err = get("preview_notes", notesIndex, *edit.Notes)
			if err != nil {
				return err
			}
			newNotes, err = write(oldNotes, *edit.Notes, false)
			if err != nil {
				return err
			}
			result.NotesRevision = newNotes.Revision
		}
		saved, err := write(slide, edit.Slide, oldNotes.ID != newNotes.ID)
		if err != nil {
			return err
		}
		if saved.ID != slide.ID && oldNotes.ID != "" {
			for id, notesID := range map[string]string{slide.ID: oldNotes.ID, saved.ID: newNotes.ID} {
				if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ? AND ppt_notes_revision_id IS NULL", id).UpdateColumn("ppt_notes_revision_id", notesID).Error; err != nil {
					return err
				}
			}
		}
		result.Revision = saved.Revision
		return tx.Model(&orm.WorkflowSession{}).Where("id = ?", sid).Select("state_version").Scan(&result.StateVersion).Error
	})
	if err != nil {
		return pptPageResult{}, err
	}
	for _, change := range result.Changes {
		NotifyWorkflowArtifactUpdated(ctx, db, sid, change.StepID, change.SlotID, change.Slot, change.Revision, change.ListIndex, "human")
	}
	return result, nil
}
