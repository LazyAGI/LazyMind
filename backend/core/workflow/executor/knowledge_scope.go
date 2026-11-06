package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// KnowledgeScope is captured once in the grant transaction. Nil means a legacy
// contract without a selection snapshot, never permission to read live choices.
type KnowledgeScope struct {
	DatasetIDs []string               `json:"dataset_ids,omitempty"`
	Documents  []KnowledgeDocumentRef `json:"documents,omitempty"`
}

type KnowledgeDocumentRef struct {
	DatasetID       string `json:"dataset_id"`
	DocumentID      string `json:"document_id"`
	ResourceVersion int64  `json:"resource_version,omitempty"`
}

func freezeKnowledgeScope(ctx context.Context, db *gorm.DB, session orm.WorkflowSession, value *AttemptContext) error {
	if value.ExternalExecution == nil || value.KnowledgeScope != nil {
		return nil
	}
	scope := &KnowledgeScope{}
	datasets := map[string]bool{}
	documents := map[string]KnowledgeDocumentRef{}
	var visit func(any) error
	visit = func(raw any) error {
		switch item := raw.(type) {
		case []any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []map[string]any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		case map[string]any:
			id, _ := item["source_id"].(string)
			if id == "" {
				return nil
			}
			switch item["source_type"] {
			case "dataset":
				datasets[id] = true
			case "document":
				var document orm.Document
				if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).Take(&document).Error; err != nil {
					return err
				}
				if revision, _ := item["source_revision"].(string); revision != "" {
					if version, err := strconv.ParseInt(revision, 10, 64); err == nil && version != document.ResourceVersion {
						return fmt.Errorf("selected document %q changed before execution", id)
					}
				}
				documents[id] = KnowledgeDocumentRef{DatasetID: document.DatasetID, DocumentID: id, ResourceVersion: document.ResourceVersion}
			}
		}
		return nil
	}
	for _, input := range value.Inputs {
		if err := visit(input); err != nil {
			return err
		}
	}
	if session.ConversationID != "" && db.Migrator().HasTable(&orm.Conversation{}) {
		var conversation orm.Conversation
		err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", session.ConversationID, session.CreateUserID).Take(&conversation).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && len(conversation.SearchConfig) > 0 {
			var config struct {
				DatasetIDs  []string `json:"dataset_ids"`
				DatasetList []struct {
					ID string `json:"id"`
				} `json:"dataset_list"`
			}
			if err := json.Unmarshal(conversation.SearchConfig, &config); err != nil {
				return fmt.Errorf("invalid selected knowledge configuration: %w", err)
			}
			for _, id := range config.DatasetIDs {
				if id != "" {
					datasets[id] = true
				}
			}
			for _, row := range config.DatasetList {
				if row.ID != "" {
					datasets[row.ID] = true
				}
			}
		}
	}
	for id := range datasets {
		scope.DatasetIDs = append(scope.DatasetIDs, id)
	}
	for _, ref := range documents {
		scope.Documents = append(scope.Documents, ref)
	}
	sort.Strings(scope.DatasetIDs)
	sort.Slice(scope.Documents, func(i, j int) bool { return scope.Documents[i].DocumentID < scope.Documents[j].DocumentID })
	value.KnowledgeScope = scope
	return nil
}
