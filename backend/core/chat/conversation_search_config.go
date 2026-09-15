package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxConversationSearchConfigBodyBytes  = 16 << 10
	maxConversationSearchConfigListItems  = 20
	maxConversationSearchConfigValueRunes = 255
)

var errKnowledgeBaseNotReadable = errors.New("knowledge base is not readable")

type conversationSearchConfigPatch struct {
	DatasetIDs *[]string `json:"dataset_ids"`
	Creators   *[]string `json:"creators"`
	Tags       *[]string `json:"tags"`
}

// PatchConversationSearchConfig changes supplied retrieval filters on an
// existing conversation. Omitted search settings remain intact.
func PatchConversationSearchConfig(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromName(conversationNameFromPath(r))
	userID := strings.TrimSpace(store.UserID(r))
	if conversationID == "" || userID == "" {
		common.ReplyErr(w, "conversation and X-User-Id are required", http.StatusBadRequest)
		return
	}

	var patch conversationSearchConfigPatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConversationSearchConfigBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		common.ReplyErr(w, "invalid search config patch", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid search config patch", http.StatusBadRequest)
		return
	}
	if patch.DatasetIDs == nil && patch.Creators == nil && patch.Tags == nil {
		common.ReplyErr(w, "at least one search config field is required", http.StatusBadRequest)
		return
	}

	datasetIDs, err := normalizeConversationSearchConfigValues(patch.DatasetIDs, "dataset_ids")
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	creators, err := normalizeConversationSearchConfigValues(patch.Creators, "creators")
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	tags, err := normalizeConversationSearchConfigValues(patch.Tags, "tags")
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if datasetIDs != nil {
		for _, datasetID := range datasetIDs {
			if err := authorizeKnowledgeBaseID(r.Context(), db, userID, datasetID); err != nil {
				if errors.Is(err, errKnowledgeBaseNotReadable) {
					common.ReplyErr(w, errKnowledgeBaseNotReadable.Error(), http.StatusForbidden)
				} else {
					common.ReplyErr(w, "load knowledge bases failed", http.StatusInternalServerError)
				}
				return
			}
		}
	}

	var selectors []map[string]string
	if datasetIDs != nil {
		selectors = make([]map[string]string, 0, len(datasetIDs))
		for _, datasetID := range datasetIDs {
			selectors = append(selectors, map[string]string{"id": datasetID})
		}
	}
	searchConfig := map[string]any{}
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Take(&conversation).Error; err != nil {
			return err
		}
		if isSidechatConversation(conversation) {
			return errSidechatKnowledgeInherited
		}
		if len(conversation.SearchConfig) > 0 {
			_ = json.Unmarshal(conversation.SearchConfig, &searchConfig)
		}
		if searchConfig == nil {
			searchConfig = map[string]any{}
		}
		if datasetIDs != nil {
			// dataset_ids is the legacy representation and takes precedence in the
			// chat read path. Remove it so this patch cannot be silently ignored.
			delete(searchConfig, "dataset_ids")
			searchConfig["dataset_list"] = selectors
		}
		if creators != nil {
			searchConfig["creators"] = creators
		}
		if tags != nil {
			searchConfig["tags"] = tags
		}
		encoded, err := json.Marshal(searchConfig)
		if err != nil {
			return err
		}
		return tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Updates(map[string]any{
				"search_config": encoded,
				"updated_at":    time.Now().UTC(),
			}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errSidechatKnowledgeInherited) {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, "update search config failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"conversation_id": conversationID,
		"search_config":   searchConfig,
	})
}

func normalizeConversationSearchConfigValues(values *[]string, field string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	normalized := uniqueNonEmptyStrings(*values)
	if len(normalized) > maxConversationSearchConfigListItems {
		return nil, fmt.Errorf("at most %d %s are allowed", maxConversationSearchConfigListItems, field)
	}
	for _, value := range normalized {
		if utf8.RuneCountInString(value) > maxConversationSearchConfigValueRunes {
			return nil, fmt.Errorf("%s value is too long", field)
		}
	}
	return normalized, nil
}

func authorizeKnowledgeBaseID(ctx context.Context, db *gorm.DB, userID, datasetID string) error {
	datasetID = strings.TrimSpace(datasetID)
	if db == nil || strings.TrimSpace(userID) == "" || datasetID == "" {
		return errKnowledgeBaseNotReadable
	}

	var dataset orm.Dataset
	if err := db.WithContext(ctx).
		Select("id", "create_user_id").
		Where("id = ? AND deleted_at IS NULL", datasetID).
		Take(&dataset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errKnowledgeBaseNotReadable
		}
		return err
	}
	if dataset.CreateUserID == userID || acl.Can(userID, acl.ResourceTypeDB, datasetID, acl.PermRead) {
		return nil
	}
	return errKnowledgeBaseNotReadable
}

func conversationSearchConfigDatasetIDs(searchConfig any) []string {
	sc, ok := searchConfig.(map[string]any)
	if !ok || sc == nil {
		return nil
	}

	ids := make([]string, 0)
	switch values := sc["dataset_ids"].(type) {
	case []any:
		for _, value := range values {
			if id, ok := value.(string); ok {
				ids = append(ids, id)
			}
		}
	case []string:
		ids = append(ids, values...)
	}

	switch selectors := sc["dataset_list"].(type) {
	case []any:
		for _, value := range selectors {
			switch selector := value.(type) {
			case map[string]any:
				if id, ok := selector["id"].(string); ok {
					ids = append(ids, id)
				}
			case map[string]string:
				ids = append(ids, selector["id"])
			}
		}
	case []map[string]any:
		for _, selector := range selectors {
			if id, ok := selector["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	case []map[string]string:
		for _, selector := range selectors {
			ids = append(ids, selector["id"])
		}
	}
	return uniqueNonEmptyStrings(ids)
}

func authorizeConversationSearchConfig(
	ctx context.Context,
	db *gorm.DB,
	userID, conversationID string,
	requestSearchConfig any,
) error {
	ids := conversationSearchConfigDatasetIDs(requestSearchConfig)

	var conversation orm.Conversation
	err := db.WithContext(ctx).
		Select("search_config").
		Where("id = ? AND create_user_id = ?", conversationID, userID).
		Take(&conversation).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && len(conversation.SearchConfig) > 0 {
		var persisted map[string]any
		if json.Unmarshal(conversation.SearchConfig, &persisted) == nil {
			ids = append(ids, conversationSearchConfigDatasetIDs(persisted)...)
		}
	}

	for _, datasetID := range uniqueNonEmptyStrings(ids) {
		if err := authorizeKnowledgeBaseID(ctx, db, userID, datasetID); err != nil {
			return err
		}
	}
	return nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
