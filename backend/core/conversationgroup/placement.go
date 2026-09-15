package conversationgroup

import (
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
)

// Placement is navigation state, independent of organizer scope/version fences.
// An anchor, rather than a client-supplied full list, preserves concurrent new groups.
func UpdateGroupPlacement(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Pinned        *bool   `json:"pinned"`
		BeforeGroupID *string `json:"before_group_id"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Pinned == nil && input.BeforeGroupID == nil) {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	uid, id := userID(r), common.PathVar(r, "group_id")
	err := UserTransaction(r.Context(), store.DB(), uid, func(tx *gorm.DB) error {
		var current orm.ConversationGroup
		if err := tx.Where("id=? AND user_id=? AND deleted_at IS NULL", id, uid).Take(&current).Error; err != nil {
			return err
		}
		pinned := current.Pinned
		if input.Pinned != nil {
			pinned = *input.Pinned
		}
		var groups []orm.ConversationGroup
		if err := tx.Where("user_id=? AND deleted_at IS NULL AND pinned=? AND id<>?", uid, pinned, id).Order("sort_order ASC, created_at ASC, id ASC").Find(&groups).Error; err != nil {
			return err
		}
		index := 0
		if input.BeforeGroupID != nil {
			index = len(groups)
			if *input.BeforeGroupID == id {
				return nil
			}
			if *input.BeforeGroupID != "" {
				index = -1
				for i, group := range groups {
					if group.ID == *input.BeforeGroupID {
						index = i
						break
					}
				}
				if index < 0 {
					return gorm.ErrRecordNotFound
				}
			}
		}
		groups = append(groups, orm.ConversationGroup{})
		copy(groups[index+1:], groups[index:])
		current.Pinned = pinned
		groups[index] = current
		for i, group := range groups {
			if err := tx.Model(&orm.ConversationGroup{}).Where("id=? AND user_id=?", group.ID, uid).UpdateColumns(map[string]any{"pinned": pinned, "sort_order": i + 1}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	ListGroups(w, r)
}
