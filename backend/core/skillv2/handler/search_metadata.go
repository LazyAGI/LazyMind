package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	skillservice "lazymind/core/skillv2/service"
)

type metadataIdentity struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

func metadataUser(ctx context.Context, db *gorm.DB, r *http.Request, identity metadataIdentity) (string, error) {
	if identity.SessionID != "" {
		id, _, err := evolution.ResolveSessionUser(ctx, db, identity.SessionID)
		return id, err
	}
	id := strings.TrimSpace(identity.UserID)
	if id == "" {
		id = strings.TrimSpace(r.Header.Get("X-User-Id"))
	}
	if id == "" {
		return "", fmt.Errorf("user_id required")
	}
	return id, nil
}
func ownedMetadataSkill(ctx context.Context, db *gorm.DB, userID, key string) (orm.SkillV2Skill, error) {
	category, name, ok := strings.Cut(strings.TrimSpace(key), "/")
	if !ok || category == "" || name == "" {
		return orm.SkillV2Skill{}, fmt.Errorf("invalid skill_key")
	}
	var row orm.SkillV2Skill
	err := db.WithContext(ctx).Where("owner_user_id = ? AND category = ? AND skill_name = ? AND deleted_at IS NULL", userID, category, name).Take(&row).Error
	return row, err
}
func metadataStrings(raw []byte) []string {
	values := []string{}
	_ = json.Unmarshal(raw, &values)
	if values == nil {
		return []string{}
	}
	return values
}

// InternalMetadata reads system search metadata without copying it into L2.
func InternalMetadata(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	var req struct {
		metadataIdentity
		SkillKeys []string `json:"skill_keys"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, err := metadataUser(r.Context(), db, r, req.metadataIdentity)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	records := []map[string]any{}
	for _, key := range compactStrings(req.SkillKeys) {
		row, err := ownedMetadataSkill(r.Context(), db, userID, key)
		if err == gorm.ErrRecordNotFound {
			continue
		}
		if err != nil {
			replyServiceError(w, err)
			return
		}
		records = append(records, map[string]any{"skill_id": row.ID, "skill_key": row.Category + "/" + row.SkillName, "field": row.Field, "tags": metadataStrings(row.Tags), "aliases": metadataStrings(row.Aliases), "keywords": metadataStrings(row.Keywords)})
	}
	common.ReplyOK(w, map[string]any{"skills": records})
}

type metadataUpdate struct {
	SkillKey string    `json:"skill_key"`
	Field    *string   `json:"field"`
	Tags     *[]string `json:"tags"`
	Aliases  *[]string `json:"aliases"`
	Keywords *[]string `json:"keywords"`
}

// InternalMetadataUpdate applies a trusted organizer batch atomically. Only
// system search columns are writable; original and execution revisions persist.
func InternalMetadataUpdate(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	var req struct {
		metadataIdentity
		Updates []metadataUpdate `json:"updates"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, err := metadataUser(r.Context(), db, r, req.metadataIdentity)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		svc := newSkillService(tx)
		for _, update := range req.Updates {
			row, err := ownedMetadataSkill(r.Context(), tx, userID, update.SkillKey)
			if err != nil {
				return err
			}
			_, err = svc.PatchSkill(r.Context(), skillservice.PatchSkillRequest{SkillID: row.ID, UserID: userID, Field: update.Field, Tags: update.Tags, Aliases: update.Aliases, Keywords: update.Keywords})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"count": len(req.Updates)})
}
