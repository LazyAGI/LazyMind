package remotefs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	skillhttperr "lazymind/core/skillv2/httperr"
	"lazymind/core/skillv2/revision"
	skillsearch "lazymind/core/skillv2/search"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/taskguard"
	"lazymind/core/state"
)

type LocalObjectStore struct {
	service  *skillservice.LocalObjectStore
	revision *revision.LocalObjectStore
}

func NewLocalObjectStore(root string) *LocalObjectStore {
	return &LocalObjectStore{
		service:  skillservice.NewLocalObjectStore(root),
		revision: revision.NewLocalObjectStore(root),
	}
}

type BlobStore struct {
	service  *skillservice.BlobStore
	revision revision.BlobStore
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) *BlobStore {
	return &BlobStore{
		service:  skillservice.NewBlobStore(db, objects.service),
		revision: revision.NewBlobStore(db, objects.revision),
	}
}

type HandlerDeps struct {
	DB         *gorm.DB
	BlobStore  *BlobStore
	StateStore state.Store
}

type Handler struct {
	db         *gorm.DB
	blobStore  *BlobStore
	stateStore state.Store
	clock      clock
}

func NewHandler(deps HandlerDeps) *Handler {
	relaxSQLiteFixtureIndexes(deps.DB)
	return &Handler{db: deps.DB, blobStore: deps.BlobStore, stateStore: deps.StateStore, clock: systemClock{}}
}

type CommitterDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type Committer struct {
	db      *gorm.DB
	service *revision.Service
}

type CommitDraftRequest = revision.CommitDraftRequest
type CommitDraftResponse = revision.CommitDraftResponse

func NewCommitter(deps CommitterDeps) *Committer {
	relaxSQLiteFixtureIndexes(deps.DB)
	return &Committer{
		db:      deps.DB,
		service: revision.NewService(revision.ServiceDeps{DB: deps.DB, BlobStore: deps.BlobStore.revision}),
	}
}

func (c *Committer) CommitDraft(ctx context.Context, req CommitDraftRequest) (CommitDraftResponse, error) {
	return c.service.CommitDraft(ctx, req)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	parsed, err := parseRemotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.level == pathLevelRoot {
		h.listCategories(w, r, userID)
		return
	}
	if parsed.level == pathLevelCategory {
		h.listSkills(w, r, userID, parsed.category)
		return
	}
	task, ok := requireTask(w, r)
	if !ok {
		return
	}
	skill, err := h.skillForPath(r.Context(), userID, parsed)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	entries, err := h.entriesForSkillView(r.Context(), h.db, skill.ID, task)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	items := listChildren(parsed.relPath, entries)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	parsed, err := parseRemotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.level != pathLevelSkill {
		writeJSON(w, http.StatusOK, map[string]any{"path": parsed.raw, "type": "dir"})
		return
	}
	task, ok := requireTask(w, r)
	if !ok {
		return
	}
	skill, err := h.skillForPath(r.Context(), userID, parsed)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	entry, err := h.entryForPath(r.Context(), h.db, skill.ID, parsed.relPath, task)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      parsed.raw,
		"type":      entry.EntryType,
		"size":      entry.Size,
		"mime":      entry.Mime,
		"file_type": entry.FileType,
		"binary":    entry.Binary,
	})
}

func (h *Handler) Exists(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	parsed, err := parseRemotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	exists := true
	if parsed.level == pathLevelSkill {
		task, ok := requireTask(w, r)
		if !ok {
			return
		}
		skill, err := h.skillForPath(r.Context(), userID, parsed)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exists = false
			} else {
				writeHTTPError(w, err)
				return
			}
		} else if _, err := h.entryForPath(r.Context(), h.db, skill.ID, parsed.relPath, task); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exists = false
			} else {
				writeHTTPError(w, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": exists})
}

func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.readContent(w, r)
	case http.MethodPut:
		h.writeContent(w, r)
	default:
		skillhttperr.Reply(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) Dir(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := requireWriteParams(w, r)
	if !ok {
		return
	}
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	parsed, err := parseRemotePath(body.Path)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.level == pathLevelRoot || parsed.level == pathLevelCategory {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		skill, err := h.skillForPathInDB(r.Context(), tx, userID, parsed)
		if parsed.relPath == "" {
			if err == nil {
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return h.createEmptyPackage(r.Context(), tx, userID, parsed, task)
		}
		if err != nil {
			return err
		}
		if err := h.claimTask(r.Context(), tx, skill.ID, userID, task); err != nil {
			return err
		}
		entries, err := h.entriesForSkillView(r.Context(), tx, skill.ID, task)
		if err != nil {
			return err
		}
		if existing, ok := entries[parsed.relPath]; ok && existing.EntryType == "file" {
			return conflict("target is a file")
		}
		if !body.Recursive {
			parent := path.Dir(parsed.relPath)
			if parent != "." && !directoryExists(parent, entries) {
				return badRequest("parent directory does not exist")
			}
		}
		return h.materializeDirs(tx, skill.ID, parsed.relPath, entries)
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) DeletePath(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	parsed, err := parseRemotePath(r.URL.Query().Get("path"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.level != pathLevelSkill {
		writeHTTPError(w, badRequest("path must include category and skill name"))
		return
	}
	if parsed.relPath == "" {
		if !truthy(r.URL.Query().Get("permanent")) || !truthy(r.URL.Query().Get("confirm")) {
			writeHTTPError(w, badRequest("permanent delete requires permanent=true and confirm=true"))
			return
		}
		skill, err := h.trashedSkillForPath(r.Context(), userID, parsed)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if _, activeErr := h.skillForPath(r.Context(), userID, parsed); activeErr == nil {
					writeHTTPError(w, badRequest("skill must be in trash before permanent delete"))
					return
				}
			}
			writeHTTPError(w, err)
			return
		}
		svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
			DB:        h.db,
			BlobStore: h.blobStore.service,
			Clock:     h.clock,
		})
		if err := svc.PurgeSkill(r.Context(), skillservice.PurgeSkillRequest{SkillID: skill.ID, UserID: userID}); err != nil {
			writeHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	task, ok := requireTask(w, r)
	if !ok {
		return
	}
	skill, err := h.skillForPath(r.Context(), userID, parsed)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := h.claimTask(r.Context(), tx, skill.ID, userID, task); err != nil {
			return err
		}
		entries, err := h.entriesForSkillView(r.Context(), tx, skill.ID, task)
		if err != nil {
			return err
		}
		target, ok := entries[parsed.relPath]
		if !ok {
			return gorm.ErrRecordNotFound
		}
		if target.FromDraft && !target.FromHead {
			return tx.Where("skill_id = ? AND (path = ? OR path LIKE ?)", skill.ID, parsed.relPath, parsed.relPath+"/%").Delete(&skillDraftEntryRow{}).Error
		}
		return h.deleteMergedPath(tx, skill.ID, parsed.relPath, entries)
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) Copy(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := requireWriteParams(w, r)
	if !ok {
		return
	}
	var body struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Overwrite {
		writeHTTPError(w, badRequest("overwrite is not supported"))
		return
	}
	from, err := parseRemotePath(body.From)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	to, err := parseRemotePath(body.To)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if from.level != pathLevelSkill || to.level != pathLevelSkill || from.relPath == "" || to.relPath == "" {
		writeHTTPError(w, badRequest("copy requires package-internal paths"))
		return
	}
	if samePackage(from, to) && to.relPath != from.relPath && strings.HasPrefix(to.relPath, from.relPath+"/") {
		writeHTTPError(w, badRequest("cannot copy directory into its child"))
		return
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sourceSkill, err := h.skillForPathInDB(r.Context(), tx, userID, from)
		if err != nil {
			return err
		}
		targetSkill, err := h.skillForPathInDB(r.Context(), tx, userID, to)
		if err != nil {
			return err
		}
		if err := h.claimTask(r.Context(), tx, targetSkill.ID, userID, task); err != nil {
			return err
		}
		sourceEntries, err := h.entriesForSkillView(r.Context(), tx, sourceSkill.ID, task)
		if err != nil {
			return err
		}
		targetEntries := sourceEntries
		if sourceSkill.ID != targetSkill.ID {
			targetEntries, err = h.entriesForSkillView(r.Context(), tx, targetSkill.ID, task)
			if err != nil {
				return err
			}
		}
		return h.copyMergedPath(tx, targetSkill.ID, from.relPath, to.relPath, sourceEntries, targetEntries)
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := requireWriteParams(w, r)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	from, err := parseRemotePath(body.From)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	to, err := parseRemotePath(body.To)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if from.level != pathLevelSkill || to.level != pathLevelSkill {
		writeHTTPError(w, badRequest("move requires skill paths"))
		return
	}
	if from.relPath == "" || to.relPath == "" {
		if from.relPath == "" && to.relPath == "" {
			if err := h.movePackageRoot(r.Context(), userID, from, to); err != nil {
				writeHTTPError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		writeHTTPError(w, badRequest("cannot move between package root and file path"))
		return
	}
	if samePackage(from, to) && to.relPath != from.relPath && strings.HasPrefix(to.relPath, from.relPath+"/") {
		writeHTTPError(w, badRequest("cannot move directory into its child"))
		return
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sourceSkill, err := h.skillForPathInDB(r.Context(), tx, userID, from)
		if err != nil {
			return err
		}
		targetSkill, err := h.skillForPathInDB(r.Context(), tx, userID, to)
		if err != nil {
			return err
		}
		if err := h.claimTask(r.Context(), tx, sourceSkill.ID, userID, task); err != nil {
			return err
		}
		if sourceSkill.ID != targetSkill.ID {
			if err := h.claimTask(r.Context(), tx, targetSkill.ID, userID, task); err != nil {
				return err
			}
		}
		sourceEntries, err := h.entriesForSkillView(r.Context(), tx, sourceSkill.ID, task)
		if err != nil {
			return err
		}
		targetEntries := sourceEntries
		if sourceSkill.ID != targetSkill.ID {
			targetEntries, err = h.entriesForSkillView(r.Context(), tx, targetSkill.ID, task)
			if err != nil {
				return err
			}
		}
		if err := h.copyMergedPath(tx, targetSkill.ID, from.relPath, to.relPath, sourceEntries, targetEntries); err != nil {
			return err
		}
		return h.deleteMergedPath(tx, sourceSkill.ID, from.relPath, sourceEntries)
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) Trash(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	pathValue := r.URL.Query().Get("path")
	if pathValue == "" {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
			return
		}
		pathValue = body.Path
	}
	parsed, err := parseRemotePath(pathValue)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.level != pathLevelSkill || parsed.relPath != "" {
		writeHTTPError(w, badRequest("trash requires a skill package root"))
		return
	}
	skill, err := h.skillForPath(r.Context(), userID, parsed)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        h.db,
		BlobStore: h.blobStore.service,
		Clock:     h.clock,
	})
	if err := svc.TrashSkill(r.Context(), skillservice.DeleteSkillRequest{SkillID: skill.ID, UserID: userID}); err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) readContent(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	task, ok := requireTask(w, r)
	if !ok {
		return
	}
	parsed, skill, err := h.resolveSkillPath(r, userID)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	entry, err := h.entryForPath(r.Context(), h.db, skill.ID, parsed.relPath, task)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if entry.EntryType == "dir" {
		writeHTTPError(w, badRequest("path is a directory"))
		return
	}
	if entry.BlobHash == nil {
		writeHTTPError(w, gorm.ErrRecordNotFound)
		return
	}
	var blob skillBlobRow
	if err := h.db.WithContext(r.Context()).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		writeHTTPError(w, err)
		return
	}
	data, err := h.blobData(blob)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	switch r.URL.Query().Get("encoding") {
	case "base64":
		writeJSON(w, http.StatusOK, map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(data)})
	default:
		if blob.Mime != "" {
			w.Header().Set("Content-Type", blob.Mime)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

func (h *Handler) writeContent(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := requireWriteParams(w, r)
	if !ok {
		return
	}
	parsed, skill, err := h.resolveSkillPath(r, userID)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if parsed.relPath == "" {
		writeHTTPError(w, badRequest("cannot write skill root"))
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := h.claimTask(r.Context(), tx, skill.ID, userID, task); err != nil {
			return err
		}
		entries, err := h.entriesForSkillView(r.Context(), tx, skill.ID, task)
		if err != nil {
			return err
		}
		if existing, ok := entries[parsed.relPath]; ok && existing.EntryType == "dir" {
			return badRequest("cannot write file over directory")
		}
		parent := path.Dir(parsed.relPath)
		if parent != "." {
			if err := h.materializeDirs(tx, skill.ID, parent, entries); err != nil {
				return err
			}
		}
		blob, err := h.blobStore.service.Put(r.Context(), tx, parsed.relPath, data, h.clock)
		if err != nil {
			return err
		}
		hash := blob.Hash
		return tx.Save(&skillDraftEntryRow{
			SkillID:   skill.ID,
			Path:      parsed.relPath,
			Op:        "upsert",
			EntryType: "file",
			BlobHash:  &hash,
			Size:      blob.Size,
			Mime:      blob.Mime,
			FileType:  blob.FileType,
			Binary:    blob.Binary,
			Mode:      0o644,
			UpdatedAt: h.clock.Now(),
		}).Error
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) resolveSkillPath(r *http.Request, userID string) (remotePath, skillRow, error) {
	parsed, err := parseRemotePath(r.URL.Query().Get("path"))
	if err != nil {
		return remotePath{}, skillRow{}, err
	}
	if parsed.level != pathLevelSkill {
		return remotePath{}, skillRow{}, badRequest("path must include category and skill name")
	}
	skill, err := h.skillForPath(r.Context(), userID, parsed)
	return parsed, skill, err
}

func (h *Handler) skillForPath(ctx context.Context, userID string, parsed remotePath) (skillRow, error) {
	return h.skillForPathInDB(ctx, h.db, userID, parsed)
}

func (h *Handler) skillForPathInDB(ctx context.Context, db *gorm.DB, userID string, parsed remotePath) (skillRow, error) {
	var skill skillRow
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL", userID, parsed.packageRoot()).
		Take(&skill).Error
	return skill, err
}

func (h *Handler) trashedSkillForPath(ctx context.Context, userID string, parsed remotePath) (skillRow, error) {
	var skill skillRow
	err := h.db.WithContext(ctx).
		Where("owner_user_id = ? AND relative_root = ? AND deleted_at IS NOT NULL", userID, parsed.packageRoot()).
		Take(&skill).Error
	return skill, err
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request, userID string) {
	var rows []skillRow
	if err := h.db.WithContext(r.Context()).Where("owner_user_id = ? AND deleted_at IS NULL", userID).Order("category ASC").Find(&rows).Error; err != nil {
		writeHTTPError(w, err)
		return
	}
	seen := map[string]bool{}
	items := make([]listItem, 0)
	for _, row := range rows {
		if !seen[row.Category] {
			seen[row.Category] = true
			items = append(items, listItem{Name: row.Category, Path: "skills/" + row.Category, Type: "dir"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request, userID, category string) {
	var rows []skillRow
	if err := h.db.WithContext(r.Context()).Where("owner_user_id = ? AND category = ? AND deleted_at IS NULL", userID, category).Order("skill_name ASC").Find(&rows).Error; err != nil {
		writeHTTPError(w, err)
		return
	}
	items := make([]listItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, listItem{Name: row.SkillName, Path: "skills/" + row.Category + "/" + row.SkillName, Type: "dir"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createEmptyPackage(ctx context.Context, tx *gorm.DB, userID string, parsed remotePath, task remoteTask) error {
	var conflicts int64
	if err := tx.WithContext(ctx).Model(&skillRow{}).
		Where("owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL", userID, parsed.packageRoot()).
		Count(&conflicts).Error; err != nil {
		return err
	}
	if conflicts > 0 {
		return conflict("skill package already exists")
	}
	now := h.clock.Now()
	skillID := uuid.NewString()
	draftStatus := "pending_confirm"
	if task.AutoCommit {
		draftStatus = "auto_pending"
	}
	if err := tx.WithContext(ctx).Create(&skillRow{
		ID:                 skillID,
		OwnerUserID:        userID,
		CreateUserID:       userID,
		Category:           parsed.category,
		SkillName:          parsed.skillName,
		Tags:               []byte("[]"),
		RelativeRoot:       parsed.packageRoot(),
		SkillMDPath:        "SKILL.md",
		Version:            1,
		AutoEvo:            task.AutoCommit,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          false,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&skillRow{}).Where("id = ?", skillID).Update("is_enabled", false).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Create(&skillDraftRow{
		SkillID:     skillID,
		DraftStatus: draftStatus,
		TaskID:      task.ID,
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error
}

func (h *Handler) claimTask(ctx context.Context, tx *gorm.DB, skillID, userID string, task remoteTask) error {
	decision, err := taskguard.EvaluateSkillOperation(ctx, tx, h.stateStore, taskguard.SkillOperationRequest{
		UserID:        userID,
		SkillID:       skillID,
		TaskID:        task.ID,
		Operation:     taskguard.WriteSkillDraft,
		TriggerSource: "remote_fs",
	})
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return conflict(decision.Message)
	}
	now := h.clock.Now()
	updates := map[string]any{
		"version":          gorm.Expr("version + 1"),
		"updated_at":       now,
		"draft_updated_at": now,
		"task_id":          task.ID,
		"conversation_id":  nil,
	}
	if task.Mode == remoteTaskModeEditor {
		updates["conversation_id"] = task.ID
	}
	if userID != "" {
		updates["updated_by"] = userID
	}
	query := tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ?", skillID)
	if task.Mode != remoteTaskModeReview {
		query = query.Where("task_id = ? OR NOT EXISTS (SELECT 1 FROM skill_draft_entries WHERE skill_draft_entries.skill_id = skill_drafts.skill_id)", task.ID)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return conflict("草稿属于其他 Skill 编辑任务")
	}
	return nil
}

func (h *Handler) entryForPath(ctx context.Context, db *gorm.DB, skillID, relPath string, task remoteTask) (mergedEntry, error) {
	entries, err := h.entriesForSkillView(ctx, db, skillID, task)
	if err != nil {
		return mergedEntry{}, err
	}
	return entryFromEntries(entries, relPath)
}

func entryFromEntries(entries map[string]mergedEntry, relPath string) (mergedEntry, error) {
	if relPath == "" {
		return mergedEntry{Path: "", EntryType: "dir", FileType: "directory"}, nil
	}
	entry, ok := entries[relPath]
	if !ok {
		return mergedEntry{}, gorm.ErrRecordNotFound
	}
	return entry, nil
}

func (h *Handler) entriesForSkillView(ctx context.Context, db *gorm.DB, skillID string, task remoteTask) (map[string]mergedEntry, error) {
	useDraft, err := h.useDraftView(ctx, db, skillID, task)
	if err != nil {
		return nil, err
	}
	if useDraft {
		return h.entriesForSkill(ctx, db, skillID)
	}
	return h.publishedEntriesForSkill(ctx, db, skillID)
}

func (h *Handler) useDraftView(ctx context.Context, db *gorm.DB, skillID string, task remoteTask) (bool, error) {
	var draft skillDraftRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error; err != nil {
		return false, err
	}
	overlayCount, err := h.draftOverlayCount(ctx, db, skillID)
	if err != nil {
		return false, err
	}
	if overlayCount == 0 {
		return false, nil
	}
	if task.Mode == remoteTaskModeReview {
		return true, nil
	}
	return draft.TaskID == task.ID, nil
}

func (h *Handler) draftOverlayCount(ctx context.Context, db *gorm.DB, skillID string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error
	return count, err
}

func (h *Handler) entriesForSkill(ctx context.Context, db *gorm.DB, skillID string) (map[string]mergedEntry, error) {
	entries, err := h.publishedEntriesForSkill(ctx, db, skillID)
	if err != nil {
		return nil, err
	}
	var overlays []skillDraftEntryRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, err
	}
	for _, overlay := range overlays {
		if overlay.Op == "delete" {
			for p := range entries {
				if p == overlay.Path || isDescendantPath(overlay.Path, p) {
					delete(entries, p)
				}
			}
			continue
		}
		hash := overlay.BlobHash
		previous := entries[overlay.Path]
		entries[overlay.Path] = mergedEntry{
			Path:      overlay.Path,
			EntryType: overlay.EntryType,
			BlobHash:  hash,
			Size:      overlay.Size,
			Mime:      overlay.Mime,
			FileType:  overlay.FileType,
			Binary:    overlay.Binary,
			Mode:      overlay.Mode,
			FromHead:  previous.FromHead,
			FromDraft: true,
		}
	}
	return entries, nil
}

func (h *Handler) publishedEntriesForSkill(ctx context.Context, db *gorm.DB, skillID string) (map[string]mergedEntry, error) {
	var skill skillRow
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return nil, err
	}
	if skill.HeadRevisionID == nil {
		return map[string]mergedEntry{}, nil
	}
	var rows []skillRevisionEntryRow
	if err := db.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make(map[string]mergedEntry, len(rows))
	for _, row := range rows {
		hash := row.BlobHash
		entries[row.Path] = mergedEntry{
			Path:      row.Path,
			EntryType: row.EntryType,
			BlobHash:  hash,
			Size:      row.Size,
			Mime:      row.Mime,
			FileType:  row.FileType,
			Binary:    row.Binary,
			Mode:      row.Mode,
			FromHead:  true,
		}
	}
	return entries, nil
}

func (h *Handler) materializeDirs(tx *gorm.DB, skillID, dir string, entries map[string]mergedEntry) error {
	if dir == "" || dir == "." {
		return nil
	}
	parts := strings.Split(dir, "/")
	current := ""
	for _, part := range parts {
		if current == "" {
			current = part
		} else {
			current = current + "/" + part
		}
		if entry, ok := entries[current]; ok {
			if entry.EntryType != "dir" {
				return conflict("parent path is a file")
			}
			continue
		}
		row := &skillDraftEntryRow{
			SkillID:   skillID,
			Path:      current,
			Op:        "upsert",
			EntryType: "dir",
			FileType:  "directory",
			Mode:      0o755,
			UpdatedAt: h.clock.Now(),
		}
		if err := tx.Save(row).Error; err != nil {
			return err
		}
		entries[current] = mergedEntry{Path: current, EntryType: "dir", FileType: "directory", Mode: 0o755, FromDraft: true}
	}
	return nil
}

func (h *Handler) copyMergedPath(tx *gorm.DB, targetSkillID, from, to string, sourceEntries, targetEntries map[string]mergedEntry) error {
	source, ok := sourceEntries[from]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	parent := path.Dir(to)
	if parent != "." {
		if err := h.materializeDirs(tx, targetSkillID, parent, targetEntries); err != nil {
			return err
		}
	}
	moved := h.entriesUnderPath(from, to, source, sourceEntries)
	for _, entry := range moved {
		if _, exists := targetEntries[entry.Path]; exists {
			return conflict("target already exists")
		}
	}
	for _, entry := range moved {
		if err := tx.Save(draftEntryFromMerged(targetSkillID, entry.Path, entry, h.clock.Now())).Error; err != nil {
			return err
		}
		targetEntries[entry.Path] = entry
	}
	return nil
}

func (h *Handler) entriesUnderPath(from, to string, source mergedEntry, entries map[string]mergedEntry) []mergedEntry {
	moved := []mergedEntry{}
	for p, entry := range entries {
		if p != from && !isDescendantPath(from, p) {
			continue
		}
		rel := strings.TrimPrefix(p, from)
		entry.Path = to + rel
		moved = append(moved, entry)
	}
	if len(moved) == 0 {
		source.Path = to
		moved = append(moved, source)
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i].Path < moved[j].Path })
	return moved
}

func (h *Handler) deleteMergedPath(tx *gorm.DB, skillID, relPath string, entries map[string]mergedEntry) error {
	target, ok := entries[relPath]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	if target.FromDraft && !target.FromHead {
		return tx.Where("skill_id = ? AND (path = ? OR path LIKE ?)", skillID, relPath, relPath+"/%").Delete(&skillDraftEntryRow{}).Error
	}
	if err := tx.Where("skill_id = ? AND path LIKE ?", skillID, relPath+"/%").Delete(&skillDraftEntryRow{}).Error; err != nil {
		return err
	}
	return tx.Save(&skillDraftEntryRow{
		SkillID:   skillID,
		Path:      relPath,
		Op:        "delete",
		UpdatedAt: h.clock.Now(),
	}).Error
}

func (h *Handler) movePackageRoot(ctx context.Context, userID string, from, to remotePath) error {
	return h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		skill, err := h.skillForPathInDB(ctx, tx, userID, from)
		if err != nil {
			return err
		}
		var conflicts int64
		if err := tx.Model(&skillRow{}).
			Where("owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL AND id <> ?", userID, to.packageRoot(), skill.ID).
			Count(&conflicts).Error; err != nil {
			return err
		}
		if conflicts > 0 {
			return conflict("target skill package already exists")
		}
		if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", skill.ID).Updates(map[string]any{
			"category":      to.category,
			"skill_name":    to.skillName,
			"relative_root": to.packageRoot(),
			"updated_at":    h.clock.Now(),
		}).Error; err != nil {
			return err
		}
		return skillsearch.RebuildSkillTx(ctx, tx, skill.ID, h.clock.Now())
	})
}

func (h *Handler) blobData(blob skillBlobRow) ([]byte, error) {
	if !blob.Binary {
		return blob.Content, nil
	}
	if blob.StorageKey == nil {
		return nil, fmt.Errorf("binary blob has no storage key")
	}
	rawURL := h.blobStore.service.DownloadURL(*blob.StorageKey)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "file" {
		return nil, fmt.Errorf("unsupported storage url: %s", rawURL)
	}
	return os.ReadFile(u.Path)
}

func relaxSQLiteFixtureIndexes(db *gorm.DB) {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return
	}
	_ = db.Exec("DROP INDEX IF EXISTS uk_skills_owner_identity").Error
	_ = db.Exec("DROP INDEX IF EXISTS uk_skills_owner_relative_root").Error
}

type pathLevel int

const (
	pathLevelRoot pathLevel = iota
	pathLevelCategory
	pathLevelSkill
)

type remoteTaskMode int

const (
	remoteTaskModeEditor remoteTaskMode = iota
	remoteTaskModeReview
	remoteTaskModeOrg
)

type remoteTask struct {
	ID         string
	Mode       remoteTaskMode
	AutoCommit bool
}

func parseRemoteTask(taskID string) remoteTask {
	switch {
	case strings.HasPrefix(taskID, "review_"):
		return remoteTask{ID: taskID, Mode: remoteTaskModeReview}
	case strings.HasPrefix(taskID, "org_"):
		return remoteTask{ID: taskID, Mode: remoteTaskModeOrg}
	default:
		return remoteTask{ID: taskID, Mode: remoteTaskModeEditor}
	}
}

type remotePath struct {
	raw       string
	level     pathLevel
	category  string
	skillName string
	relPath   string
}

func (p remotePath) packageRoot() string {
	return path.Join(p.category, p.skillName)
}

func samePackage(a, b remotePath) bool {
	return a.category == b.category && a.skillName == b.skillName
}

func parseRemotePath(raw string) (remotePath, error) {
	if raw == "" {
		return remotePath{}, badRequest("path is required")
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, `\`) || strings.Contains(raw, "//") {
		return remotePath{}, badRequest("unsafe path")
	}
	cleaned := path.Clean(raw)
	if cleaned != raw || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return remotePath{}, badRequest("unsafe path")
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return remotePath{}, badRequest("unsafe path")
		}
	}
	if parts[0] != "skills" {
		return remotePath{}, badRequest("path must start with skills")
	}
	switch len(parts) {
	case 1:
		return remotePath{raw: raw, level: pathLevelRoot}, nil
	case 2:
		return remotePath{raw: raw, level: pathLevelCategory, category: parts[1]}, nil
	default:
		return remotePath{
			raw:       raw,
			level:     pathLevelSkill,
			category:  parts[1],
			skillName: parts[2],
			relPath:   strings.Join(parts[3:], "/"),
		}, nil
	}
}

type listItem struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size,omitempty"`
	Mime     string `json:"mime,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Binary   bool   `json:"binary,omitempty"`
}

func listChildren(parent string, entries map[string]mergedEntry) []listItem {
	prefix := ""
	if parent != "" {
		prefix = parent + "/"
	}
	byName := map[string]listItem{}
	for p, entry := range entries {
		if parent != "" && p != parent && !strings.HasPrefix(p, prefix) {
			continue
		}
		if p == parent {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name := rest
		itemType := entry.EntryType
		if idx := strings.Index(rest, "/"); idx >= 0 {
			name = rest[:idx]
			itemType = "dir"
		}
		itemPath := name
		if parent != "" {
			itemPath = parent + "/" + name
		}
		item := listItem{Name: name, Path: itemPath, Type: itemType}
		if itemType == entry.EntryType && itemPath == entry.Path {
			item.Size = entry.Size
			item.Mime = entry.Mime
			item.FileType = entry.FileType
			item.Binary = entry.Binary
		}
		byName[name] = item
	}
	items := make([]listItem, 0, len(byName))
	for _, item := range byName {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func directoryExists(dir string, entries map[string]mergedEntry) bool {
	if dir == "" || dir == "." {
		return true
	}
	if entry, ok := entries[dir]; ok {
		return entry.EntryType == "dir"
	}
	prefix := dir + "/"
	for p := range entries {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func draftEntryFromMerged(skillID, newPath string, entry mergedEntry, now time.Time) *skillDraftEntryRow {
	hash := entry.BlobHash
	return &skillDraftEntryRow{
		SkillID:   skillID,
		Path:      newPath,
		Op:        "upsert",
		EntryType: entry.EntryType,
		BlobHash:  hash,
		Size:      entry.Size,
		Mime:      entry.Mime,
		FileType:  entry.FileType,
		Binary:    entry.Binary,
		Mode:      entry.Mode,
		UpdatedAt: now,
	}
}

func hashRemoteTree(entries []skillRevisionEntryRow) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		hash := ""
		if entry.BlobHash != nil {
			hash = *entry.BlobHash
		}
		lines = append(lines, entry.Path+"\x00"+entry.EntryType+"\x00"+hash)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func truthy(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		skillhttperr.ReplyWithCode(w, "user_id is required", http.StatusUnauthorized, skillhttperr.CodeUnauthenticated)
		return "", false
	}
	return userID, true
}

func requireTask(w http.ResponseWriter, r *http.Request) (remoteTask, bool) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		skillhttperr.Reply(w, "task_id is required", http.StatusBadRequest)
		return remoteTask{}, false
	}
	task := parseRemoteTask(taskID)
	task.AutoCommit = strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("mode")), "auto")
	return task, true
}

func requireWriteParams(w http.ResponseWriter, r *http.Request) (string, remoteTask, bool) {
	userID, ok := requireUser(w, r)
	if !ok {
		return "", remoteTask{}, false
	}
	task, ok := requireTask(w, r)
	if !ok {
		return "", remoteTask{}, false
	}
	return userID, task, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeHTTPError(w http.ResponseWriter, err error) {
	var httpErr httpError
	switch {
	case errors.As(err, &httpErr):
		skillhttperr.Reply(w, httpErr.Error(), httpErr.status)
	case errors.Is(err, gorm.ErrRecordNotFound):
		skillhttperr.ReplyWithCode(w, "not found", http.StatusNotFound, skillhttperr.CodeNotFound)
	default:
		skillhttperr.Reply(w, err.Error(), http.StatusInternalServerError)
	}
}

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string { return e.message }

func badRequest(message string) error {
	return httpError{status: http.StatusBadRequest, message: message}
}

func conflict(message string) error { return httpError{status: http.StatusConflict, message: message} }

func isDescendantPath(parent, child string) bool {
	return child != parent && strings.HasPrefix(child, parent+"/")
}

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type skillRow struct {
	ID                 string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID        string     `gorm:"column:owner_user_id;type:text;not null"`
	OwnerUserName      string     `gorm:"column:owner_user_name;type:text;not null;default:''"`
	CreateUserID       string     `gorm:"column:create_user_id;type:text;not null"`
	CreateUserName     string     `gorm:"column:create_user_name;type:text;not null;default:''"`
	Category           string     `gorm:"column:category;type:text;not null"`
	SkillName          string     `gorm:"column:skill_name;type:text;not null"`
	Description        string     `gorm:"column:description;type:text"`
	Tags               []byte     `gorm:"column:tags;type:json"`
	RelativeRoot       string     `gorm:"column:relative_root;type:text;not null"`
	SkillMDPath        string     `gorm:"column:skill_md_path;type:text;not null;default:'SKILL.md'"`
	HeadRevisionID     *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version            int64      `gorm:"column:version;not null;default:1"`
	AutoEvo            bool       `gorm:"column:auto_evo;not null;default:false"`
	AutoEvoApplyStatus string     `gorm:"column:auto_evo_apply_status;type:text;not null;default:'idle'"`
	AutoEvoGeneration  int64      `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt   *time.Time `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt  *time.Time `gorm:"column:auto_evo_finished_at"`
	AutoEvoError       string     `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled          bool       `gorm:"column:is_enabled;not null;default:true"`
	UpdateStatus       string     `gorm:"column:update_status;type:text;not null;default:'up_to_date'"`
	Ext                []byte     `gorm:"column:ext;type:json"`
	DeletedAt          *time.Time `gorm:"column:deleted_at"`
	DeletedBy          *string    `gorm:"column:deleted_by;type:text"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
}

func (skillRow) TableName() string { return "skills" }

type skillBlobRow struct {
	Hash           string    `gorm:"column:hash;type:text;primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:text"`
	FileType       string    `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false"`
	StorageBackend string    `gorm:"column:storage_backend;type:text;not null"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:blob"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (skillBlobRow) TableName() string { return "skill_blobs" }

type skillRevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:text;not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:text;not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(36)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (skillRevisionRow) TableName() string { return "skill_revisions" }

type skillRevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (skillRevisionEntryRow) TableName() string { return "skill_revision_entries" }

type skillDraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	DraftStatus    string     `gorm:"column:draft_status;type:text;not null;default:''"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(36)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
}

func (skillDraftRow) TableName() string { return "skill_drafts" }

type skillDraftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:text;primaryKey"`
	Op        string    `gorm:"column:op;type:text;not null"`
	EntryType string    `gorm:"column:entry_type;type:text"`
	BlobHash  *string   `gorm:"column:blob_hash;type:text"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:text"`
	FileType  string    `gorm:"column:file_type"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (skillDraftEntryRow) TableName() string { return "skill_draft_entries" }

type mergedEntry struct {
	Path      string
	EntryType string
	BlobHash  *string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
	FromHead  bool
	FromDraft bool
}
