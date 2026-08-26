package chat

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	corestore "lazymind/core/store"
)

func recordDatasetUsageForChat(ctx context.Context, raw, reqBody map[string]any, userID, userName string, isRegeneration bool) {
	if isRegeneration {
		return
	}
	if runInBackground, _ := raw["run_in_background"].(bool); runInBackground {
		return
	}
	if syntheticSourceFromChatReq(reqBody) != "" {
		return
	}

	datasetIDs := datasetIDsForChatTurn(reqBody)
	if len(datasetIDs) == 0 {
		return
	}

	db := corestore.DB()
	if db == nil {
		return
	}
	now := time.Now().UTC()
	for _, datasetID := range datasetIDs {
		state := orm.DatasetUserState{
			ID:             common.GeneratePrefixedID("dus_", 64),
			DatasetID:      datasetID,
			UsageCount:     1,
			LastUsedAt:     &now,
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := db.WithContext(ctx).Clauses(datasetUsageConflictClause(now)).Create(&state).Error; err != nil {
			log.Logger.Error().Err(err).Str("dataset_id", datasetID).Msg("record dataset usage failed")
		}
	}
}

func datasetUsageConflictClause(now time.Time) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "create_user_id"}, {Name: "dataset_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"usage_count": gorm.Expr(
				"? + ?",
				clause.Column{Table: orm.DatasetUserState{}.TableName(), Name: "usage_count"},
				1,
			),
			"last_used_at": now,
			"updated_at":   now,
			"deleted_at":   nil,
		}),
	}
}

func syntheticSourceFromChatReq(reqBody map[string]any) string {
	workflowContext, _ := reqBody["workflow_context"].(map[string]any)
	if workflowContext == nil {
		return ""
	}
	value, _ := workflowContext["synthetic_source"].(string)
	return strings.TrimSpace(value)
}

func datasetIDsForChatTurn(reqBody map[string]any) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	appendDatasetIDs := func(value any) {
		for _, id := range datasetIDsFromValue(value) {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}

	if filters, _ := reqBody["filters"].(map[string]any); filters != nil {
		appendDatasetIDs(filters["kb_id"])
	}
	if bindings, _ := reqBody["explicit_resource_bindings"].(map[string]any); bindings != nil {
		appendDatasetIDs(bindings["knowledge_base_ids"])
	}
	return ids
}

func datasetIDsFromValue(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if id, ok := item.(string); ok {
				out = append(out, id)
			}
		}
		return out
	default:
		return nil
	}
}
