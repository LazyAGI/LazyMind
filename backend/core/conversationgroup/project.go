package conversationgroup

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/store"
)

const KindGroup = "group"
const KindProject = "project"

func projectError(reason string, status int) *common.AppError {
	return common.ResolveAppError("conversation project "+reason, status)
}

func projectName(name, path string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(path)
		if filepath.Dir(path) == path {
			name = path
		}
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > 255 {
		return "", projectError("invalid_name", 400)
	}
	return name, nil
}

// EnsureProject runs under UserTransaction. Directory identity is checked even
// when a new authorization row refers to an existing project's path.
func EnsureProject(ctx context.Context, tx *gorm.DB, uid, workspaceID, name string) (orm.ConversationGroup, error) {
	var project orm.ConversationGroup
	if !localworkspace.Enabled() {
		return project, localworkspace.ModeError()
	}
	workspace, err := localworkspace.ResolveActiveForBinding(ctx, tx, uid, workspaceID)
	if err != nil {
		return project, err
	}
	err = tx.Where("user_id=? AND kind=? AND project_path=?", uid, KindProject, workspace.CanonicalPath).Take(&project).Error
	if err == nil {
		if project.WorkspaceID == nil {
			return project, projectError("directory_conflict", 409)
		}
		var previous orm.LocalWorkspace
		if err := tx.Where("id=? AND create_user_id=?", *project.WorkspaceID, uid).Take(&previous).Error; err != nil {
			return project, err
		}
		if previous.DirectoryIdentity != workspace.DirectoryIdentity {
			return project, projectError("directory_conflict", 409)
		}
		project.WorkspaceID = &workspace.ID
		project.DeletedAt = nil
		err = tx.Model(&project).Updates(map[string]any{"workspace_id": workspace.ID, "deleted_at": nil, "updated_at": time.Now().UTC()}).Error
		return project, err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return project, err
	}
	name, err = projectName(name, workspace.CanonicalPath)
	if err != nil {
		return project, err
	}
	now := time.Now().UTC()
	project = orm.ConversationGroup{ID: uuid.NewString(), UserID: uid, Kind: KindProject, Name: name, NormalizedName: normalizeName(name), WorkspaceID: &workspace.ID, ProjectPath: &workspace.CanonicalPath, Version: 1, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	return project, tx.Create(&project).Error
}

func createProject(w http.ResponseWriter, r *http.Request, input groupInput) {
	if input.WorkspaceID == nil || input.Scope != nil || input.OrganizerRunID != nil {
		replyNotFoundOrError(w, projectError("invalid_input", 400))
		return
	}
	name := ""
	if input.Name != nil {
		name = *input.Name
		if strings.TrimSpace(name) == "" {
			replyNotFoundOrError(w, projectError("invalid_name", 400))
			return
		}
	}
	var project orm.ConversationGroup
	err := UserTransaction(r.Context(), store.DB(), userID(r), func(tx *gorm.DB) error {
		var err error
		project, err = EnsureProject(r.Context(), tx, userID(r), *input.WorkspaceID, name)
		return err
	})
	if err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"group": groupDTO(project, 0)})
}

func updateProject(w http.ResponseWriter, r *http.Request, input groupInput) {
	if input.Name == nil || strings.TrimSpace(*input.Name) == "" || input.Scope != nil || input.Kind != "" || input.WorkspaceID != nil || input.OrganizerRunID != nil {
		replyNotFoundOrError(w, projectError("invalid_input", 400))
		return
	}
	name, err := projectName(*input.Name, "")
	if err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	var project orm.ConversationGroup
	err = UserTransaction(r.Context(), store.DB(), userID(r), func(tx *gorm.DB) error {
		if err := tx.Where("id=? AND user_id=? AND kind=? AND deleted_at IS NULL", common.PathVar(r, "group_id"), userID(r), KindProject).Take(&project).Error; err != nil {
			return err
		}
		return tx.Model(&project).Updates(map[string]any{"name": name, "normalized_name": normalizeName(name), "updated_at": time.Now().UTC(), "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		replyNotFoundOrError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"group": groupDTO(project, 0)})
}

// InheritProject is only called while creating a fork or a side conversation.
// Legacy directory-bound conversations deliberately retain their old behavior.
func InheritProject(ctx context.Context, tx *gorm.DB, uid, sourceID, targetID string) error {
	if !tx.Migrator().HasTable(&orm.ConversationGroupMember{}) {
		return nil
	}
	var project orm.ConversationGroup
	err := tx.Table("conversation_groups g").Select("g.*").Joins("JOIN conversation_group_members m ON m.group_id=g.id").Where("m.conversation_id=? AND g.user_id=? AND g.kind=?", sourceID, uid, KindProject).Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if project.DeletedAt != nil {
		return projectError("directory_conflict", 409)
	}
	var binding orm.ConversationWorkspaceBinding
	if err := tx.Where("conversation_id=?", sourceID).Take(&binding).Error; err != nil {
		return err
	}
	binding.ConversationID = targetID
	binding.CreatedAt = time.Now().UTC()
	binding.UpdatedAt = binding.CreatedAt
	if err := tx.Create(&binding).Error; err != nil {
		return err
	}
	_, err = writeMembershipTx(tx, uid, targetID, &project.ID, CreatedByUser, "")
	return err
}

func RestoreProjects(tx *gorm.DB, uid string, ids []string) error {
	if !tx.Migrator().HasTable(&orm.ConversationGroupMember{}) {
		return nil
	}
	return tx.Model(&orm.ConversationGroup{}).Where("user_id=? AND kind=? AND id IN (SELECT group_id FROM conversation_group_members WHERE conversation_id IN ? AND user_id=?)", uid, KindProject, ids, uid).Updates(map[string]any{"deleted_at": nil, "updated_at": time.Now().UTC()}).Error
}
