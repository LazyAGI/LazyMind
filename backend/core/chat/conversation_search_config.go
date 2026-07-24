package chat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxConversationSearchConfigBodyBytes = 16 << 10

type conversationSearchConfigPatch struct {
	DatasetIDs *[]string `json:"dataset_ids"`
}

// PatchConversationSearchConfig changes only the knowledge bases persisted on an
// existing conversation. Other search settings remain intact.
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
	if patch.DatasetIDs == nil {
		common.ReplyErr(w, "dataset_ids is required", http.StatusBadRequest)
		return
	}

	datasetIDs := uniqueNonEmptyStrings(*patch.DatasetIDs)
	if len(datasetIDs) > 20 {
		common.ReplyErr(w, "at most 20 knowledge bases are allowed", http.StatusBadRequest)
		return
	}

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if len(datasetIDs) > 0 {
		var datasets []orm.Dataset
		if err := db.WithContext(r.Context()).
			Where("id IN ? AND deleted_at IS NULL", datasetIDs).
			Find(&datasets).Error; err != nil {
			common.ReplyErr(w, "load knowledge bases failed", http.StatusInternalServerError)
			return
		}
		byID := make(map[string]orm.Dataset, len(datasets))
		for _, dataset := range datasets {
			byID[dataset.ID] = dataset
		}
		for _, datasetID := range datasetIDs {
			dataset, ok := byID[datasetID]
			if !ok || (dataset.CreateUserID != userID &&
				!acl.Can(userID, acl.ResourceTypeDB, datasetID, acl.PermRead)) {
				common.ReplyErr(w, "knowledge base is not readable", http.StatusForbidden)
				return
			}
		}
	}

	selectors := make([]map[string]string, 0, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		selectors = append(selectors, map[string]string{"id": datasetID})
	}
	searchConfig := map[string]any{}
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Take(&conversation).Error; err != nil {
			return err
		}
		if len(conversation.SearchConfig) > 0 {
			_ = json.Unmarshal(conversation.SearchConfig, &searchConfig)
		}
		if searchConfig == nil {
			searchConfig = map[string]any{}
		}
		// dataset_ids is the legacy representation and takes precedence in the
		// chat read path. Remove it so this patch cannot be silently ignored.
		delete(searchConfig, "dataset_ids")
		searchConfig["dataset_list"] = selectors
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
	if err != nil {
		common.ReplyErr(w, "update search config failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"conversation_id": conversationID,
		"search_config":   searchConfig,
	})
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
