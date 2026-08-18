package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

const (
	archiveFolderUnfiled = "unfiled"
	maxArchiveFolderName = 30
	trashRetention       = 30 * 24 * time.Hour
)

var errConversationInTrash = errors.New("conversation is in trash")

type conversationArchiveRequest struct {
	FolderID *string `json:"folder_id"`
}

type archiveFolderRequest struct {
	Name string `json:"name"`
}

func recoveryUserID(r *http.Request) string {
	userID := store.UserID(r)
	if userID == "" {
		return "0"
	}
	return userID
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func normalizedArchiveFolderName(raw string) (string, string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "", errors.New("folder name is required")
	}
	if utf8.RuneCountInString(name) > maxArchiveFolderName {
		return "", "", fmt.Errorf("folder name must be at most %d characters", maxArchiveFolderName)
	}
	return name, strings.ToLower(name), nil
}

func resolveArchiveFolderID(db *gorm.DB, userID string, raw *string) (*string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" || strings.EqualFold(strings.TrimSpace(*raw), archiveFolderUnfiled) {
		return nil, nil
	}
	folderID := strings.TrimSpace(*raw)
	var count int64
	if err := db.Model(&orm.ConversationArchiveFolder{}).
		Where("id = ? AND user_id = ?", folderID, userID).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &folderID, nil
}

func conversationRecoveryItem(c orm.Conversation, folderNames map[string]string) map[string]any {
	item := map[string]any{
		"name":            "conversations/" + c.ID,
		"conversation_id": c.ID,
		"display_name":    c.DisplayName,
		"kind":            map[bool]string{true: "task", false: "dialog"}[c.IsTaskConv],
		"create_time":     c.CreatedAt.UTC().Format(time.RFC3339),
		"update_time":     c.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if c.ArchivedAt != nil {
		item["archived_at"] = c.ArchivedAt.UTC().Format(time.RFC3339)
	}
	if c.DeletedAt != nil {
		item["deleted_at"] = c.DeletedAt.UTC().Format(time.RFC3339)
	}
	if c.TrashExpiresAt != nil {
		item["trash_expires_at"] = c.TrashExpiresAt.UTC().Format(time.RFC3339)
	}
	if c.ArchiveFolderID != nil {
		item["folder_id"] = *c.ArchiveFolderID
		item["archive_folder_name"] = folderNames[*c.ArchiveFolderID]
	} else {
		item["folder_id"] = nil
	}
	return item
}

func listRecoveryConversations(w http.ResponseWriter, r *http.Request, archived bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := recoveryUserID(r)
	page := 1
	pageSize := 20
	if value, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && value > 0 {
		page = value
	}
	if value, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && value > 0 && value <= 100 {
		pageSize = value
	}

	q := db.Model(&orm.Conversation{}).Where("create_user_id = ?", userID)
	if archived {
		q = q.Where("deleted_at IS NULL AND archived_at IS NOT NULL")
		folderID := strings.TrimSpace(r.URL.Query().Get("folder_id"))
		switch {
		case folderID == "", strings.EqualFold(folderID, "all"):
		case strings.EqualFold(folderID, archiveFolderUnfiled):
			q = q.Where("archive_folder_id IS NULL")
		default:
			q = q.Where("archive_folder_id = ?", folderID)
		}
	} else {
		q = q.Where("deleted_at IS NOT NULL")
	}
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		q = q.Where("LOWER(display_name) LIKE ?", "%"+strings.ToLower(keyword)+"%")
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = strings.TrimSpace(r.URL.Query().Get("is_task_conv"))
	}
	switch kind {
	case "task", "true":
		q = q.Where("is_task_conv = ?", true)
	case "dialog", "false":
		q = q.Where("is_task_conv = ? OR is_task_conv IS NULL", false)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query conversations failed", http.StatusInternalServerError)
		return
	}
	var rows []orm.Conversation
	orderColumn := "deleted_at"
	if archived {
		orderColumn = "archived_at"
	}
	if err := q.Order(orderColumn + " DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query conversations failed", http.StatusInternalServerError)
		return
	}

	folderNames := map[string]string{}
	if archived {
		var folders []orm.ConversationArchiveFolder
		if err := db.Where("user_id = ?", userID).Find(&folders).Error; err != nil {
			common.ReplyErr(w, "query archive folders failed", http.StatusInternalServerError)
			return
		}
		for _, folder := range folders {
			folderNames[folder.ID] = folder.Name
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, conversationRecoveryItem(row, folderNames))
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "page_size": pageSize,
	})
}

func ListArchivedConversations(w http.ResponseWriter, r *http.Request) {
	listRecoveryConversations(w, r, true)
}

func ListTrashedConversations(w http.ResponseWriter, r *http.Request) {
	listRecoveryConversations(w, r, false)
}

func recoveryConversationID(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := common.PathVar(r, "conversation_id")
	if raw == "" {
		raw = conversationNameFromPath(r)
	}
	conversationID := conversationIDFromName(raw)
	if conversationID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return "", false
	}
	return conversationID, true
}

func ArchiveConversation(w http.ResponseWriter, r *http.Request) {
	conversationID, ok := recoveryConversationID(w, r)
	if !ok {
		return
	}
	var body conversationArchiveRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	db := store.DB()
	userID := recoveryUserID(r)
	folderID, err := resolveArchiveFolderID(db, userID, body.FolderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "archive folder not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "query archive folder failed", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	var conversation orm.Conversation
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).First(&conversation).Error; err != nil {
			return err
		}
		updates := map[string]any{"archive_folder_id": folderID, "updated_at": now}
		if conversation.ArchivedAt == nil {
			updates["archived_at"] = now
			if err := taskcenter.ArchiveTasksForConversations(r.Context(), tx, userID, []string{conversationID}, taskcenter.ArchivedReasonConversationArchive, now); err != nil {
				return err
			}
		}
		return tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Updates(updates).Error
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "archive conversation failed", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

func UnarchiveConversation(w http.ResponseWriter, r *http.Request) {
	conversationID, ok := recoveryConversationID(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	db := store.DB().WithContext(r.Context())
	userID := recoveryUserID(r)
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND archived_at IS NOT NULL", conversationID, userID).
			Updates(map[string]any{"archived_at": nil, "archive_folder_id": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return taskcenter.RestoreTasksForConversations(r.Context(), tx, userID, []string{conversationID}, taskcenter.ArchivedReasonConversationArchive, now)
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "unarchive conversation failed", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "archived conversation not found", http.StatusNotFound)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

func RestoreConversation(w http.ResponseWriter, r *http.Request) {
	conversationID, ok := recoveryConversationID(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	db := store.DB().WithContext(r.Context())
	userID := recoveryUserID(r)
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NOT NULL", conversationID, userID).
			Updates(map[string]any{
				"deleted_at": nil, "trash_expires_at": nil, "archived_at": nil,
				"archive_folder_id": nil, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return taskcenter.RestoreTasksForConversations(r.Context(), tx, userID, []string{conversationID}, taskcenter.ArchivedReasonConversationTrash, now)
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "restore conversation failed", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "trashed conversation not found", http.StatusNotFound)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

func purgeConversation(ctxDB *gorm.DB, conversationID, userID string) error {
	var conversation orm.Conversation
	if err := ctxDB.Where("id = ? AND create_user_id = ? AND deleted_at IS NOT NULL", conversationID, userID).
		First(&conversation).Error; err != nil {
		return err
	}
	if err := removeConversationArtifactFiles(userID, conversationID); err != nil {
		return err
	}
	return ctxDB.Transaction(func(tx *gorm.DB) error {
		deletions := []struct {
			model any
			where string
			args  []any
		}{
			{&orm.ChatHistory{}, "conversation_id = ?", []any{conversationID}},
			{&orm.MultiAnswersChatHistory{}, "conversation_id = ?", []any{conversationID}},
			{&orm.ConversationArtifact{}, "conversation_id = ? AND create_user_id = ?", []any{conversationID, userID}},
			{&orm.ConversationIdleEvent{}, "session_id = ? AND user_id = ?", []any{conversationID, userID}},
			{&orm.EpisodeMemory{}, "conversation_id = ? AND user_id = ?", []any{conversationID, userID}},
		}
		for _, deletion := range deletions {
			if err := tx.Where(deletion.where, deletion.args...).Delete(deletion.model).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&orm.SkillV2Draft{}).
			Where("conversation_id = ?", conversationID).
			Update("conversation_id", nil).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable("external_agent_bindings") {
			if err := tx.Exec("DELETE FROM external_agent_bindings WHERE conversation_id = ? AND created_by_user_id = ?", conversationID, userID).Error; err != nil {
				return err
			}
		}
		if err := taskcenter.MarkConversationPurged(context.Background(), tx, userID, conversationID, time.Now().UTC()); err != nil {
			return err
		}
		result := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NOT NULL", conversationID, userID).
			Delete(&orm.Conversation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func PurgeConversation(w http.ResponseWriter, r *http.Request) {
	conversationID, ok := recoveryConversationID(w, r)
	if !ok {
		return
	}
	err := purgeConversation(store.DB().WithContext(r.Context()), conversationID, recoveryUserID(r))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "trashed conversation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "purge conversation failed", http.StatusInternalServerError)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

func EmptyConversationTrash(w http.ResponseWriter, r *http.Request) {
	db := store.DB().WithContext(r.Context())
	userID := recoveryUserID(r)
	q := db.Model(&orm.Conversation{}).Where("create_user_id = ? AND deleted_at IS NOT NULL", userID)
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = strings.TrimSpace(r.URL.Query().Get("is_task_conv"))
	}
	switch kind {
	case "task", "true":
		q = q.Where("is_task_conv = ?", true)
	case "dialog", "false":
		q = q.Where("is_task_conv = ? OR is_task_conv IS NULL", false)
	}
	var ids []string
	if err := q.Pluck("id", &ids).Error; err != nil {
		common.ReplyErr(w, "query conversation trash failed", http.StatusInternalServerError)
		return
	}
	for _, conversationID := range ids {
		if err := purgeConversation(db, conversationID, userID); err != nil {
			common.ReplyErr(w, "empty conversation trash failed", http.StatusInternalServerError)
			return
		}
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"deleted_count": len(ids)})
}

func ListConversationArchiveFolders(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	userID := recoveryUserID(r)
	var folders []orm.ConversationArchiveFolder
	if err := db.Where("user_id = ?", userID).Order("created_at ASC").Find(&folders).Error; err != nil {
		common.ReplyErr(w, "query archive folders failed", http.StatusInternalServerError)
		return
	}
	type folderCounts struct{ Dialog, Task int64 }
	counts := map[string]folderCounts{}
	var countRows []struct {
		ArchiveFolderID string
		IsTaskConv      bool
		Count           int64
	}
	if err := db.Model(&orm.Conversation{}).
		Select("archive_folder_id, is_task_conv, COUNT(*) AS count").
		Where("create_user_id = ? AND deleted_at IS NULL AND archived_at IS NOT NULL AND archive_folder_id IS NOT NULL", userID).
		Group("archive_folder_id, is_task_conv").Scan(&countRows).Error; err != nil {
		common.ReplyErr(w, "query archive folder counts failed", http.StatusInternalServerError)
		return
	}
	for _, row := range countRows {
		value := counts[row.ArchiveFolderID]
		if row.IsTaskConv {
			value.Task = row.Count
		} else {
			value.Dialog = row.Count
		}
		counts[row.ArchiveFolderID] = value
	}
	items := make([]map[string]any, 0, len(folders))
	for _, folder := range folders {
		count := counts[folder.ID]
		items = append(items, map[string]any{
			"id": folder.ID, "name": folder.Name,
			"dialog_count": count.Dialog, "task_count": count.Task, "total_count": count.Dialog + count.Task,
			"created_at": folder.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": folder.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	var unfiledDialogCount, unfiledTaskCount int64
	baseUnfiled := "create_user_id = ? AND deleted_at IS NULL AND archived_at IS NOT NULL AND archive_folder_id IS NULL"
	db.Model(&orm.Conversation{}).Where(baseUnfiled+" AND (is_task_conv = ? OR is_task_conv IS NULL)", userID, false).Count(&unfiledDialogCount)
	db.Model(&orm.Conversation{}).Where(baseUnfiled+" AND is_task_conv = ?", userID, true).Count(&unfiledTaskCount)
	writeConversationJSON(w, http.StatusOK, map[string]any{
		"folders": items, "unfiled_dialog_count": unfiledDialogCount,
		"unfiled_task_count": unfiledTaskCount, "unfiled_total_count": unfiledDialogCount + unfiledTaskCount,
	})
}

func CreateConversationArchiveFolder(w http.ResponseWriter, r *http.Request) {
	var body archiveFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	name, normalized, err := normalizedArchiveFolderName(body.Name)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	folder := orm.ConversationArchiveFolder{
		ID: uuid.NewString(), UserID: recoveryUserID(r), Name: name, NormalizedName: normalized,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.DB().Create(&folder).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			common.ReplyErr(w, "archive folder name already exists", http.StatusConflict)
			return
		}
		common.ReplyErr(w, "create archive folder failed", http.StatusInternalServerError)
		return
	}
	writeConversationJSON(w, http.StatusCreated, map[string]any{"folder": map[string]any{
		"id": folder.ID, "name": folder.Name, "dialog_count": 0, "task_count": 0, "total_count": 0,
		"created_at": folder.CreatedAt.UTC().Format(time.RFC3339), "updated_at": folder.UpdatedAt.UTC().Format(time.RFC3339),
	}})
}

// PurgeExpiredConversationTrash removes every expired item independently so a
// single corrupt conversation cannot block retention cleanup for the rest.
func PurgeExpiredConversationTrash(ctx context.Context, db *gorm.DB, now time.Time) (purged, failed int) {
	var rows []orm.Conversation
	if err := db.WithContext(ctx).
		Where("deleted_at IS NOT NULL AND trash_expires_at IS NOT NULL AND trash_expires_at <= ?", now).
		Find(&rows).Error; err != nil {
		return 0, 1
	}
	for _, row := range rows {
		if err := purgeConversation(db.WithContext(ctx), row.ID, row.CreateUserID); err != nil {
			failed++
			continue
		}
		purged++
	}
	return purged, failed
}
