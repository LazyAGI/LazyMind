package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

var (
	errArchiveFolderNotEmpty = errors.New("archive folder is not empty; choose a move target")
	errArchiveFolderMoveSelf = errors.New("archive folder move target must differ from source")
)

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

func isArchiveFolderNameConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
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

	q := db.Model(&orm.Conversation{}).Where("create_user_id = ? AND is_ephemeral = ?", userID, false)
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
	parentNames := parentDisplayNames(r.Context(), db, userID, rows)
	for _, row := range rows {
		item := conversationRecoveryItem(row, folderNames)
		parentName := ""
		if row.ParentConversationID != nil {
			parentName = parentNames[*row.ParentConversationID]
		}
		mergeConversationRelationMetadata(item, row, parentName, false)
		items = append(items, item)
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).First(&conversation).Error; err != nil {
			return err
		}
		if conversation.ParentConversationID != nil {
			return errChildGroupOperation
		}
		conversationIDs, err := ownedConversationFamilyIDs(r.Context(), tx, userID, conversationID)
		if err != nil {
			return err
		}
		updates := map[string]any{"archive_folder_id": folderID, "archived_at": now, "updated_at": now}
		if err := taskcenter.ArchiveTasksForConversations(r.Context(), tx, userID, conversationIDs, taskcenter.ArchivedReasonConversationArchive, now); err != nil {
			return err
		}
		return tx.Model(&orm.Conversation{}).
			Where("id IN ? AND create_user_id = ? AND deleted_at IS NULL", conversationIDs, userID).
			Updates(updates).Error
	})
	if errors.Is(err, errChildGroupOperation) {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
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
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND create_user_id = ? AND deleted_at IS NULL AND archived_at IS NOT NULL",
			conversationID, userID,
		).Take(&conversation).Error; err != nil {
			return err
		}
		if conversation.ParentConversationID != nil {
			return errChildGroupOperation
		}
		conversationIDs, err := ownedConversationFamilyIDs(r.Context(), tx, userID, conversationID)
		if err != nil {
			return err
		}
		result := tx.Model(&orm.Conversation{}).
			Where("id IN ? AND create_user_id = ? AND deleted_at IS NULL AND archived_at IS NOT NULL", conversationIDs, userID).
			Updates(map[string]any{"archived_at": nil, "archive_folder_id": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return taskcenter.RestoreTasksForConversations(r.Context(), tx, userID, conversationIDs, taskcenter.ArchivedReasonConversationArchive, now)
	})
	if errors.Is(err, errChildGroupOperation) {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
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
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND create_user_id = ? AND deleted_at IS NOT NULL",
			conversationID, userID,
		).Take(&conversation).Error; err != nil {
			return err
		}
		conversationIDs := []string{conversation.ID}
		if conversation.ParentConversationID != nil {
			var parent orm.Conversation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
				"id = ? AND create_user_id = ? AND deleted_at IS NULL AND archived_at IS NULL",
				*conversation.ParentConversationID, userID,
			).Take(&parent).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return errChildGroupOperation
			} else if err != nil {
				return err
			}
		} else if conversation.DeletedAt != nil {
			var childIDs []string
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&orm.Conversation{}).
				Where(
					"parent_conversation_id = ? AND create_user_id = ? AND deleted_at = ?",
					conversation.ID, userID, *conversation.DeletedAt,
				).Pluck("id", &childIDs).Error; err != nil {
				return err
			}
			conversationIDs = append(conversationIDs, childIDs...)
		}
		result := tx.Model(&orm.Conversation{}).
			Where("id IN ? AND create_user_id = ? AND deleted_at IS NOT NULL", conversationIDs, userID).
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
		return taskcenter.RestoreTasksForConversations(r.Context(), tx, userID, conversationIDs, taskcenter.ArchivedReasonConversationTrash, now)
	})
	if errors.Is(err, errChildGroupOperation) {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
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
	ctx := ctxDB.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var root orm.Conversation
	if err := ctxDB.Where(
		"id = ? AND create_user_id = ? AND (deleted_at IS NOT NULL OR is_ephemeral = ?)",
		conversationID, userID, true,
	).First(&root).Error; err != nil {
		return err
	}

	family := []orm.Conversation{root}
	if root.ParentConversationID == nil {
		var children []orm.Conversation
		if err := ctxDB.Where(
			"parent_conversation_id = ? AND create_user_id = ?", root.ID, userID,
		).Find(&children).Error; err != nil {
			return err
		}
		family = append(family, children...)
	}
	sort.Slice(family, func(i, j int) bool { return family[i].ID < family[j].ID })

	guards := make([]*sidechatNextRequestGuard, 0, len(family))
	for _, conversation := range family {
		if !isSidechatConversation(conversation) {
			continue
		}
		guard, err := acquireSidechatNextRequestGuard(ctx, store.State(), conversation.ID, "")
		if err != nil {
			for index := len(guards) - 1; index >= 0; index-- {
				guards[index].Release()
			}
			return err
		}
		guards = append(guards, guard)
	}
	defer func() {
		for index := len(guards) - 1; index >= 0; index-- {
			guards[index].Release()
		}
	}()

	expected := make(map[string]struct{}, len(family))
	conversationIDs := make([]string, 0, len(family))
	for _, conversation := range family {
		expected[conversation.ID] = struct{}{}
		conversationIDs = append(conversationIDs, conversation.ID)
	}
	err := ctxDB.Transaction(func(tx *gorm.DB) error {
		var lockedRoot orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND create_user_id = ? AND (deleted_at IS NOT NULL OR is_ephemeral = ?)",
			root.ID, userID, true,
		).Take(&lockedRoot).Error; err != nil {
			return err
		}
		var lockedFamily []orm.Conversation
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("create_user_id = ?", userID)
		if lockedRoot.ParentConversationID == nil {
			query = query.Where("id = ? OR parent_conversation_id = ?", lockedRoot.ID, lockedRoot.ID)
		} else {
			query = query.Where("id = ?", lockedRoot.ID)
		}
		if err := query.Find(&lockedFamily).Error; err != nil {
			return err
		}
		if len(lockedFamily) != len(expected) {
			return errSidechatRequestBusy
		}
		for _, conversation := range lockedFamily {
			if _, ok := expected[conversation.ID]; !ok {
				return errSidechatRequestBusy
			}
		}

		deletions := []struct {
			model any
			where string
			args  []any
		}{
			{&orm.ChatRunPerformance{}, "conversation_id IN ? AND user_id = ?", []any{conversationIDs, userID}},
			{&orm.ChatHistory{}, "conversation_id IN ?", []any{conversationIDs}},
			{&orm.MultiAnswersChatHistory{}, "conversation_id IN ?", []any{conversationIDs}},
			{&orm.ConversationArtifact{}, "conversation_id IN ? AND create_user_id = ?", []any{conversationIDs, userID}},
			{&orm.ConversationIdleEvent{}, "session_id IN ? AND user_id = ?", []any{conversationIDs, userID}},
			{&orm.EpisodeMemory{}, "conversation_id IN ? AND user_id = ?", []any{conversationIDs, userID}},
		}
		for _, deletion := range deletions {
			if err := tx.Where(deletion.where, deletion.args...).Delete(deletion.model).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&orm.SkillV2Draft{}).
			Where("conversation_id IN ?", conversationIDs).
			Update("conversation_id", nil).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable("external_agent_bindings") {
			if err := tx.Exec("DELETE FROM external_agent_bindings WHERE conversation_id IN ? AND created_by_user_id = ?", conversationIDs, userID).Error; err != nil {
				return err
			}
		}
		for _, id := range conversationIDs {
			if err := taskcenter.MarkConversationPurged(ctx, tx, userID, id, time.Now().UTC()); err != nil {
				return err
			}
		}
		result := tx.Where("id IN ? AND create_user_id = ?", conversationIDs, userID).
			Delete(&orm.Conversation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(conversationIDs)) {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	notifySessionEnvClear(conversationIDs...)
	// Only remove files after the database transaction commits. A rollback or
	// process exit before commit therefore leaves every live conversation and its
	// workspace intact; a post-commit interruption can only leave unreachable
	// files, never a live conversation with missing artifacts.
	for _, id := range conversationIDs {
		if err := removeConversationArtifactFiles(userID, id); err != nil {
			return err
		}
	}
	return nil
}

func PurgeConversation(w http.ResponseWriter, r *http.Request) {
	conversationID, ok := recoveryConversationID(w, r)
	if !ok {
		return
	}
	err := purgeConversation(store.DB().WithContext(r.Context()), conversationID, recoveryUserID(r))
	if errors.Is(err, errSidechatRequestBusy) {
		common.ReplyErr(w, "conversation is busy", http.StatusConflict)
		return
	}
	if errors.Is(err, errSidechatStateUnavailable) {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
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
	q := db.Model(&orm.Conversation{}).Where("create_user_id = ? AND deleted_at IS NOT NULL AND is_ephemeral = ?", userID, false)
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
		if err := purgeConversation(db, conversationID, userID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
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
		if isArchiveFolderNameConflict(err) {
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

func UpdateConversationArchiveFolder(w http.ResponseWriter, r *http.Request) {
	folderID := strings.TrimSpace(common.PathVar(r, "folder_id"))
	if folderID == "" {
		common.ReplyErr(w, "invalid archive folder id", http.StatusBadRequest)
		return
	}
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
	result := store.DB().WithContext(r.Context()).Model(&orm.ConversationArchiveFolder{}).
		Where("id = ? AND user_id = ?", folderID, recoveryUserID(r)).
		Updates(map[string]any{"name": name, "normalized_name": normalized, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		if isArchiveFolderNameConflict(result.Error) {
			common.ReplyErr(w, "archive folder name already exists", http.StatusConflict)
			return
		}
		common.ReplyErr(w, "update archive folder failed", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected == 0 {
		common.ReplyErr(w, "archive folder not found", http.StatusNotFound)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{})
}

func DeleteConversationArchiveFolder(w http.ResponseWriter, r *http.Request) {
	folderID := strings.TrimSpace(common.PathVar(r, "folder_id"))
	if folderID == "" {
		common.ReplyErr(w, "invalid archive folder id", http.StatusBadRequest)
		return
	}
	moveTarget := strings.TrimSpace(r.URL.Query().Get("move_to_folder_id"))
	userID := recoveryUserID(r)
	var movedCount int64
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var folder orm.ConversationArchiveFolder
		if err := tx.Where("id = ? AND user_id = ?", folderID, userID).First(&folder).Error; err != nil {
			return err
		}

		if err := tx.Model(&orm.Conversation{}).
			Where("create_user_id = ? AND archive_folder_id = ?", userID, folderID).
			Count(&movedCount).Error; err != nil {
			return err
		}
		if movedCount > 0 {
			if moveTarget == "" {
				return errArchiveFolderNotEmpty
			}
			if moveTarget == folderID {
				return errArchiveFolderMoveSelf
			}
			targetID, err := resolveArchiveFolderID(tx, userID, &moveTarget)
			if err != nil {
				return err
			}
			if err := tx.Model(&orm.Conversation{}).
				Where("create_user_id = ? AND archive_folder_id = ?", userID, folderID).
				Updates(map[string]any{"archive_folder_id": targetID, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}

		result := tx.Where("id = ? AND user_id = ?", folderID, userID).Delete(&orm.ConversationArchiveFolder{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "archive folder not found", http.StatusNotFound)
	case errors.Is(err, errArchiveFolderNotEmpty):
		common.ReplyErr(w, errArchiveFolderNotEmpty.Error(), http.StatusConflict)
	case errors.Is(err, errArchiveFolderMoveSelf):
		common.ReplyErr(w, errArchiveFolderMoveSelf.Error(), http.StatusBadRequest)
	case err != nil:
		common.ReplyErr(w, "delete archive folder failed", http.StatusInternalServerError)
	default:
		writeConversationJSON(w, http.StatusOK, map[string]any{"moved_count": movedCount})
	}
}

// PurgeExpiredConversationTrash removes every expired item independently so a
// single corrupt conversation cannot block retention cleanup for the rest.
func PurgeExpiredConversationTrash(ctx context.Context, db *gorm.DB, now time.Time) (purged, failed int) {
	var rows []orm.Conversation
	if err := db.WithContext(ctx).
		Where(
			"(deleted_at IS NOT NULL AND trash_expires_at IS NOT NULL AND trash_expires_at <= ?) OR (is_ephemeral = ? AND ephemeral_expires_at IS NOT NULL AND ephemeral_expires_at <= ?)",
			now, true, now,
		).
		Find(&rows).Error; err != nil {
		return 0, 1
	}
	for _, row := range rows {
		if err := purgeConversation(db.WithContext(ctx), row.ID, row.CreateUserID); errors.Is(err, errSidechatRequestBusy) {
			continue
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			failed++
			continue
		}
		purged++
	}
	return purged, failed
}
