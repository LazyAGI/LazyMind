package conversationgroup

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
)

const (
	CreatedByUser                  = "user"
	CreatedByOrganizer             = "organizer"
	invalidOrganizerRunMessage     = "invalid organizer run"
	groupNoLongerControlledMessage = "conversation group is no longer controlled by organizer run"
)

var ErrConversationGroupNotFound = errors.New("conversation group not found")
var ErrConversationOrganizing = errors.New("conversation is locked by organizer")

type GroupDTO struct {
	Kind             string    `json:"kind"`
	WorkspaceID      *string   `json:"workspace_id,omitempty"`
	Path             *string   `json:"path,omitempty"`
	TotalMemberCount int64     `json:"total_member_count"`
	Pinned           bool      `json:"pinned"`
	SortOrder        int64     `json:"sort_order"`
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Scope            string    `json:"scope"`
	Version          int64     `json:"version"`
	MemberCount      int64     `json:"member_count"`
	CreatedBy        string    `json:"created_by"`
	CreatedRunID     string    `json:"created_run_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type groupInput struct {
	Kind           string  `json:"kind"`
	WorkspaceID    *string `json:"workspace_id"`
	Name           *string `json:"name"`
	Scope          *string `json:"scope"`
	OrganizerRunID *string `json:"organizer_run_id"`
}

func user(r *http.Request) (string, string) {
	id := strings.TrimSpace(store.UserID(r))
	if id == "" {
		id = "0"
	}
	return id, strings.TrimSpace(store.UserName(r))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func validateGroupInput(input groupInput, creating bool) (string, string, error) {
	name, scope := "", ""
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Scope != nil {
		scope = strings.TrimSpace(*input.Scope)
	}
	if creating || input.Name != nil {
		if n := utf8.RuneCountInString(name); n < 1 || n > 24 {
			return "", "", errors.New("conversation group name must contain 1 to 24 characters")
		}
	}
	if input.Scope != nil && utf8.RuneCountInString(scope) > 500 {
		return "", "", errors.New("conversation group scope must not exceed 500 characters")
	}
	return name, scope, nil
}

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// UserTransaction serializes this user's short group mutations. In particular,
// removing a group must not race with a newly inserted member that was absent
// from its initial member query. Model calls always happen outside this lock.
func UserTransaction(ctx context.Context, db *gorm.DB, uid string, fn func(*gorm.DB) error) error {
	return common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "conversation-groups:"+uid).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func CreateGroup(w http.ResponseWriter, r *http.Request) {
	var input groupInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Kind == KindProject {
		createProject(w, r, input)
		return
	}
	if input.Kind != "" && input.Kind != KindGroup || input.WorkspaceID != nil {
		replyNotFoundOrError(w, projectError("invalid_input", 400))
		return
	}
	name, scope, err := validateGroupInput(input, true)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	uid, _ := user(r)
	now := time.Now().UTC()
	row := orm.ConversationGroup{ID: uuid.NewString(), UserID: uid, Name: name, NormalizedName: normalizeName(name), Scope: scope, Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	if err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		if err := requireOrganizerNamesUnlocked(tx, uid); err != nil {
			return err
		}
		return tx.Create(&row).Error
	}); err != nil {
		if strings.Contains(err.Error(), "group names are locked") {
			common.ReplyErr(w, err.Error(), http.StatusConflict)
			return
		}
		if isUnique(err) {
			common.ReplyErr(w, "conversation group name already exists", http.StatusConflict)
			return
		}
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"group": groupDTO(row, 0)})
}

func ListGroups(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	type row struct {
		orm.ConversationGroup
		MemberCount      int64 `gorm:"column:member_count"`
		TotalMemberCount int64 `gorm:"column:total_member_count"`
	}
	var rows []row
	query := store.DB().WithContext(r.Context()).Table("conversation_groups g").
		Select("g.*, COUNT(c.id) AS member_count, (SELECT COUNT(*) FROM conversation_group_members tm JOIN conversations tc ON tc.id=tm.conversation_id WHERE tm.group_id=g.id AND tc.deleted_at IS NULL AND tc.parent_conversation_id IS NULL) AS total_member_count").
		Joins("LEFT JOIN conversation_group_members m ON m.group_id = g.id").
		Joins("LEFT JOIN conversations c ON c.id=m.conversation_id AND c.deleted_at IS NULL AND c.archived_at IS NULL AND c.parent_conversation_id IS NULL").
		Where("g.user_id = ? AND g.deleted_at IS NULL", uid).Group("g.id").Order("g.pinned DESC, g.sort_order ASC, g.created_at ASC, g.id")
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		query = query.Where("LOWER(g.name) LIKE ? OR LOWER(g.project_path) LIKE ? OR EXISTS (SELECT 1 FROM conversation_group_members sm JOIN conversations sc ON sc.id=sm.conversation_id LEFT JOIN conversation_opening_metadata so ON so.conversation_id=sc.id WHERE sm.group_id=g.id AND sc.deleted_at IS NULL AND sc.archived_at IS NULL AND (LOWER(sc.display_name) LIKE ? OR LOWER(so.summary) LIKE ?))", pattern, pattern, pattern, pattern)
	}
	err := query.Scan(&rows).Error
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	items := make([]GroupDTO, 0, len(rows))
	for _, item := range rows {
		dto := groupDTO(item.ConversationGroup, item.MemberCount)
		dto.TotalMemberCount = item.TotalMemberCount
		items = append(items, dto)
	}
	writeJSON(w, 200, map[string]any{"groups": items, "total_size": len(items)})
}

func GetGroup(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id := common.PathVar(r, "group_id")
	var group orm.ConversationGroup
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND user_id = ? AND deleted_at IS NULL", id, uid).Take(&group).Error; err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	pageSize := int64(20)
	if n, err := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64); err == nil && n > 0 && n <= 100 {
		pageSize = n
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("page_token"), 10, 64)
	if offset < 0 {
		offset = 0
	}
	var total int64
	db := store.DB().WithContext(r.Context())
	base := db.Table("conversation_group_members m").Joins("JOIN conversations c ON c.id = m.conversation_id").Where("m.group_id = ? AND m.user_id = ? AND c.deleted_at IS NULL AND c.archived_at IS NULL", id, uid)
	if err := base.Count(&total).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	items := make([]struct {
		ConversationID     string     `json:"conversation_id"`
		DisplayName        string     `json:"display_name"`
		IsTaskConv         bool       `json:"is_task_conv"`
		PinnedAt           *time.Time `json:"pinned_at"`
		CreatedAt          time.Time  `json:"created_at"`
		UpdatedAt          time.Time  `json:"updated_at"`
		Summary            *string    `json:"summary"`
		MembershipRevision int64      `json:"membership_revision"`
	}, 0)
	base = base.Joins("LEFT JOIN conversation_opening_metadata o ON o.conversation_id=c.id")
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		base = base.Where("LOWER(c.display_name) LIKE ? OR LOWER(o.summary) LIKE ?", pattern, pattern)
	}
	var filtered int64
	if err := base.Count(&filtered).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	if err := base.Select("c.id AS conversation_id, c.display_name, c.is_task_conv, c.pinned_at, c.created_at, c.updated_at, o.summary, m.revision AS membership_revision").Order("CASE WHEN c.history_order IS NULL THEN 0 ELSE 1 END, c.history_order ASC, c.updated_at DESC, c.id ASC").Offset(int(offset)).Limit(int(pageSize)).Find(&items).Error; err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	next := ""
	if offset+int64(len(items)) < filtered {
		next = strconv.FormatInt(offset+int64(len(items)), 10)
	}
	dto := groupDTO(group, total)
	if group.Kind == KindProject {
		if err := db.Table("conversation_group_members m").Joins("JOIN conversations c ON c.id=m.conversation_id").Where("m.group_id=? AND m.user_id=? AND c.deleted_at IS NULL AND c.parent_conversation_id IS NULL", id, uid).Count(&dto.TotalMemberCount).Error; err != nil {
			replyNotFoundOrError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"group": dto, "conversations": items, "total_size": total, "next_page_token": next})
}

func UpdateGroup(w http.ResponseWriter, r *http.Request) {
	var input groupInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if input.Name == nil && input.Scope == nil {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	var current orm.ConversationGroup
	if err := store.DB().Where("id=? AND user_id=? AND deleted_at IS NULL", common.PathVar(r, "group_id"), userID(r)).Take(&current).Error; err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	if current.Kind == KindProject {
		updateProject(w, r, input)
		return
	}
	if input.Kind != "" || input.WorkspaceID != nil {
		replyNotFoundOrError(w, projectError("invalid_input", 400))
		return
	}
	name, scope, err := validateGroupInput(input, false)
	if err != nil {
		common.ReplyErr(w, err.Error(), 400)
		return
	}
	uid, _ := user(r)
	id := common.PathVar(r, "group_id")
	var updated orm.ConversationGroup
	err = UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var controlledRun *orm.ConversationOrganizerRun
		var controlledResult organizerResult
		if input.OrganizerRunID != nil {
			runID := strings.TrimSpace(*input.OrganizerRunID)
			if runID == "" {
				return fmt.Errorf("%s", invalidOrganizerRunMessage)
			}
			var run orm.ConversationOrganizerRun
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND status=?", runID, uid, "succeeded").Take(&run).Error; err != nil {
				return err
			}
			if err := json.Unmarshal(run.ResultJSON, &controlledResult); err != nil {
				return err
			}
			controlledRun = &run
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND deleted_at IS NULL", id, uid).Take(&updated).Error; err != nil {
			return err
		}
		if input.Name != nil && name != updated.Name {
			if err := requireOrganizerNamesUnlocked(tx, uid); err != nil {
				return err
			}
		}
		if controlledRun != nil {
			version, ok := controlledResult.ControlledGroupVersions[id]
			if !ok || updated.CreatedBy != CreatedByOrganizer || updated.CreatedRunID != controlledRun.ID || updated.Version != version {
				return fmt.Errorf("%s", groupNoLongerControlledMessage)
			}
		}
		values := map[string]any{"updated_at": time.Now().UTC(), "version": gorm.Expr("version + 1")}
		if input.Name != nil {
			values["name"] = name
			values["normalized_name"] = normalizeName(name)
		}
		if input.Scope != nil {
			values["scope"] = scope
		}
		if err := tx.Model(&updated).Updates(values).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Take(&updated).Error; err != nil {
			return err
		}
		if controlledRun != nil {
			controlledResult.ControlledGroupVersions[id] = updated.Version
			raw, err := json.Marshal(controlledResult)
			if err != nil {
				return err
			}
			if err := tx.Model(controlledRun).Update("result_json", raw).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if isUnique(err) {
			common.ReplyErr(w, "conversation group name already exists", 409)
		} else if strings.Contains(err.Error(), "group names are locked") || strings.Contains(err.Error(), "no longer controlled") || strings.Contains(err.Error(), "invalid organizer run") {
			common.ReplyErr(w, err.Error(), 409)
		} else {
			replyNotFoundOrError(w, err)
		}
		return
	}
	var count int64
	store.DB().Table("conversation_group_members m").Joins("JOIN conversations c ON c.id=m.conversation_id").Where("m.group_id=? AND c.deleted_at IS NULL AND c.archived_at IS NULL", id).Count(&count)
	writeJSON(w, 200, map[string]any{"group": groupDTO(updated, count)})
}

func DeleteGroup(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	id := common.PathVar(r, "group_id")
	now := time.Now().UTC()
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var memberIDs []string
		if err := tx.Model(&orm.ConversationGroupMember{}).Where("group_id=? AND user_id=?", id, uid).Pluck("conversation_id", &memberIDs).Error; err != nil {
			return err
		}
		sort.Strings(memberIDs)
		if len(memberIDs) > 0 {
			var conversations []orm.Conversation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND create_user_id=?", memberIDs, uid).Order("id").Find(&conversations).Error; err != nil {
				return err
			}
		}
		var group orm.ConversationGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", id, uid).Take(&group).Error; err != nil {
			return err
		}
		if group.Kind == KindProject {
			return projectError("membership_locked", 409)
		}
		for _, cid := range memberIDs {
			if _, err := moveMembershipTx(tx, uid, cid, nil, CreatedByUser, ""); err != nil {
				return err
			}
		}
		return tx.Model(&group).Updates(map[string]any{"deleted_at": now, "normalized_name": group.NormalizedName + "#deleted#" + group.ID, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{})
}

type memberInput struct {
	ConversationID string `json:"conversation_id"`
	TargetID       string `json:"target_conversation_id"`
	Position       string `json:"position"`
}

func AddMember(w http.ResponseWriter, r *http.Request) {
	var in memberInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(in.ConversationID) == "" || len(in.ConversationID) > 255 || len(in.TargetID) > 255 ||
		(in.TargetID == "" && in.Position != "") || (in.TargetID != "" && (in.TargetID == strings.TrimSpace(in.ConversationID) || (in.Position != "before" && in.Position != "after"))) {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if err := moveConversationAt(r.Context(), store.DB(), userID(r), strings.TrimSpace(in.ConversationID), common.PathVar(r, "group_id"), CreatedByUser, "", in.TargetID, in.Position); err != nil {
		replyMembershipError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"conversation_id": strings.TrimSpace(in.ConversationID), "group_id": common.PathVar(r, "group_id")})
}
func RemoveMember(w http.ResponseWriter, r *http.Request) {
	cid := common.PathVar(r, "conversation_id")
	uid := userID(r)
	gid := common.PathVar(r, "group_id")
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=?", cid, uid).Take(&conv).Error; err != nil {
			return err
		}
		if err := RequireOrganizerUnlocked(r.Context(), tx, uid, []string{cid}, ""); err != nil {
			return err
		}
		var group orm.ConversationGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", gid, uid).Take(&group).Error; err != nil {
			return err
		}
		var member orm.ConversationGroupMember
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=? AND group_id=?", cid, uid, gid).Take(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("conversation membership changed")
			}
			return err
		}
		if _, err := moveMembershipTx(tx, uid, cid, nil, CreatedByUser, ""); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		replyMembershipError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"conversation_id": cid, "group_id": nil})
}

func MoveConversation(ctx context.Context, db *gorm.DB, uid, conversationID, groupID, source, runID string) error {
	return moveConversationAt(ctx, db, uid, conversationID, groupID, source, runID, "", "")
}

func moveConversationAt(ctx context.Context, db *gorm.DB, uid, conversationID, groupID, source, runID, targetID, position string) error {
	return UserTransaction(ctx, db, uid, func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=? AND deleted_at IS NULL AND archived_at IS NULL", conversationID, uid).Take(&conv).Error; err != nil {
			return err
		}
		if conv.ParentConversationID != nil {
			return errors.New("child conversation cannot be grouped independently")
		}
		if err := RequireOrganizerUnlocked(ctx, tx, uid, []string{conversationID}, runID); err != nil {
			return err
		}
		if groupID != "" {
			var group orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", groupID, uid).Take(&group).Error; err != nil {
				return err
			}
		}
		change, err := moveMembershipTx(tx, uid, conversationID, groupIDOrNil(groupID), source, runID)
		if err != nil {
			return err
		}
		if targetID != "" {
			return reorderGroupMemberTx(tx, uid, groupID, conversationID, targetID, position)
		}
		if groupID != "" && (change.BeforeGroupID == nil || *change.BeforeGroupID != groupID) {
			if conv.PinnedAt != nil || conv.IsEphemeral {
				return tx.Model(&conv).UpdateColumn("history_order", nil).Error
			}
			return reorderGroupMemberTx(tx, uid, groupID, conversationID, "", "")
		}
		return nil
	})
}

// AttachNewConversation adds the group membership inside the caller's
// conversation-creation transaction. It deliberately has no fallback to free.
func AttachNewConversation(ctx context.Context, tx *gorm.DB, uid, conversationID, groupID string) error {
	var group orm.ConversationGroup
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", groupID, uid).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConversationGroupNotFound
		}
		return err
	}
	if group.Kind == KindProject {
		var binding orm.ConversationWorkspaceBinding
		if err := tx.Where("conversation_id=?", conversationID).Take(&binding).Error; err != nil {
			return err
		}
		if group.WorkspaceID == nil || binding.WorkspaceID != *group.WorkspaceID {
			return projectError("directory_conflict", 409)
		}
		var count int64
		if err := tx.Model(&orm.ConversationGroupMember{}).Where("conversation_id=?", conversationID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return projectError("membership_locked", 409)
		}
		_, err := writeMembershipTx(tx, uid, conversationID, &groupID, CreatedByUser, "")
		return err
	}
	_, err := moveMembershipTx(tx, uid, conversationID, &groupID, CreatedByUser, "")
	return err
}

func IsDeleteLocked(ctx context.Context, tx *gorm.DB, uid string, ids []string) (bool, error) {
	return isOrganizerLocked(ctx, tx, uid, ids, "")
}

func isOrganizerLocked(ctx context.Context, tx *gorm.DB, uid string, ids []string, exceptRunID string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var count int64
	if !tx.Migrator().HasTable(&orm.ConversationOrganizerSnapshotItem{}) {
		return false, nil
	}
	query := tx.WithContext(ctx).Table("conversation_organizer_snapshot_items i").Joins("JOIN conversation_organizer_runs r ON r.id=i.run_id").Where("i.user_id=? AND i.conversation_id IN ? AND r.status IN ?", uid, ids, []string{"pending", "running", "applying"})
	query = query.Where("NOT EXISTS (SELECT 1 FROM conversation_group_members pm JOIN conversation_groups pg ON pg.id=pm.group_id WHERE pm.conversation_id=i.conversation_id AND pg.kind=?)", KindProject)
	if exceptRunID != "" {
		query = query.Where("r.id<>?", exceptRunID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}

func RequireOrganizerUnlocked(ctx context.Context, tx *gorm.DB, uid string, ids []string, exceptRunID string) error {
	locked, err := isOrganizerLocked(ctx, tx, uid, ids, exceptRunID)
	if err != nil {
		return err
	}
	if locked {
		return ErrConversationOrganizing
	}
	return nil
}

func groupDTO(g orm.ConversationGroup, count int64) GroupDTO {
	return GroupDTO{Kind: g.Kind, WorkspaceID: g.WorkspaceID, Path: g.ProjectPath, TotalMemberCount: count, Pinned: g.Pinned, SortOrder: g.SortOrder, ID: g.ID, Name: g.Name, Scope: g.Scope, Version: g.Version, MemberCount: count, CreatedBy: g.CreatedBy, CreatedRunID: g.CreatedRunID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}
func userID(r *http.Request) string { u, _ := user(r); return u }
func isUnique(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate")
}
func replyNotFoundOrError(w http.ResponseWriter, err error) {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		common.ReplyAppErr(w, appErr)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "conversation group not found", 404)
	} else {
		common.ReplyErr(w, err.Error(), 500)
	}
}
func replyMembershipError(w http.ResponseWriter, err error) {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		common.ReplyAppErr(w, appErr)
		return
	}
	if errors.Is(err, ErrConversationOrganizing) {
		common.ReplyErr(w, err.Error(), 409)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "conversation or conversation group not found", 404)
	} else if strings.Contains(err.Error(), "cannot be grouped") || strings.Contains(err.Error(), "membership changed") {
		common.ReplyErr(w, err.Error(), 409)
	} else {
		common.ReplyErr(w, err.Error(), 500)
	}
}

// Called under UserTransaction, shared with StartOrganizer and apply.
func requireOrganizerNamesUnlocked(tx *gorm.DB, uid string) error {
	var count int64
	if err := tx.Model(&orm.ConversationOrganizerRun{}).Where("user_id=? AND status IN ?", uid, []string{"pending", "running", "applying"}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("conversation organizer group names are locked")
	}
	return nil
}
