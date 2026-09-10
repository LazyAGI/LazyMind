package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

type runPerformanceRecord struct {
	RunID          string
	ConversationID string
	HistoryID      string
	UserID         string
	Status         string
	ObservedAt     time.Time
	Metrics        *RunPerformanceMetrics
}

func persistRunPerformance(ctx context.Context, db *gorm.DB, record runPerformanceRecord) error {
	if db == nil {
		return errors.New("performance database is nil")
	}
	if strings.TrimSpace(record.RunID) == "" || strings.TrimSpace(record.ConversationID) == "" ||
		strings.TrimSpace(record.HistoryID) == "" || strings.TrimSpace(record.UserID) == "" {
		return errors.New("performance ownership fields are required")
	}
	if err := record.Metrics.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	observedAt := record.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = now
	}
	metrics := record.Metrics
	row := orm.ChatRunPerformance{
		RunID:              strings.TrimSpace(record.RunID),
		ConversationID:     strings.TrimSpace(record.ConversationID),
		HistoryID:          strings.TrimSpace(record.HistoryID),
		UserID:             strings.TrimSpace(record.UserID),
		TurnSeq:            metrics.TurnSeq,
		SchemaVersion:      metrics.SchemaVersion,
		Status:             strings.TrimSpace(record.Status),
		Model:              strings.TrimSpace(metrics.Model),
		Steps:              metrics.Steps,
		ModelSteps:         metrics.ModelSteps,
		ToolSteps:          metrics.ToolSteps,
		WallMS:             metrics.WallMS,
		ModelMS:            metrics.ModelMS,
		ToolMS:             metrics.ToolMS,
		TTFTMS:             metrics.TTFTMS,
		InputTokens:        metrics.InputTokens,
		OutputTokens:       metrics.OutputTokens,
		TotalTokens:        metrics.TotalTokens,
		CachedTokens:       metrics.CachedTokens,
		CacheInputTokens:   metrics.CacheInputTokens,
		ReasoningTokens:    metrics.ReasoningTokens,
		MaxInputTokens:     metrics.MaxInputTokens,
		ContextInputTokens: metrics.ContextInputTokens,
		ObservedAt:         observedAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	result := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "run_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"conversation_id", "history_id", "user_id", "turn_seq", "schema_version", "status", "model",
			"steps", "model_steps", "tool_steps", "wall_ms", "model_ms", "tool_ms", "ttft_ms",
			"input_tokens", "output_tokens", "total_tokens", "cached_tokens", "cache_input_tokens", "reasoning_tokens",
			"max_input_tokens", "context_input_tokens", "observed_at", "updated_at",
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: "chat_run_performance", Name: "conversation_id"}, Value: row.ConversationID},
			clause.Eq{Column: clause.Column{Table: "chat_run_performance", Name: "history_id"}, Value: row.HistoryID},
			clause.Eq{Column: clause.Column{Table: "chat_run_performance", Name: "user_id"}, Value: row.UserID},
		}},
	}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("performance run ownership does not match existing row")
	}
	return nil
}

func loadRunPerformance(
	ctx context.Context,
	db *gorm.DB,
	userID, conversationID string,
	runIDs []string,
) (map[string]*RunPerformanceMetrics, error) {
	result := make(map[string]*RunPerformanceMetrics)
	if db == nil || len(runIDs) == 0 {
		return result, nil
	}
	var rows []orm.ChatRunPerformance
	if err := db.WithContext(ctx).
		Where("run_id IN ? AND user_id = ? AND conversation_id = ?", runIDs, userID, conversationID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		metrics := &RunPerformanceMetrics{
			SchemaVersion:      row.SchemaVersion,
			TurnSeq:            row.TurnSeq,
			ModelSteps:         row.ModelSteps,
			ToolSteps:          row.ToolSteps,
			Steps:              row.Steps,
			WallMS:             row.WallMS,
			ModelMS:            row.ModelMS,
			ToolMS:             row.ToolMS,
			TTFTMS:             row.TTFTMS,
			Model:              row.Model,
			InputTokens:        row.InputTokens,
			OutputTokens:       row.OutputTokens,
			TotalTokens:        row.TotalTokens,
			CachedTokens:       row.CachedTokens,
			CacheInputTokens:   row.CacheInputTokens,
			ReasoningTokens:    row.ReasoningTokens,
			MaxInputTokens:     row.MaxInputTokens,
			ContextInputTokens: row.ContextInputTokens,
		}
		derivePerformanceRates(metrics)
		result[row.RunID] = metrics
	}
	return result, nil
}

func hydrateHistoryPerformance(
	ctx context.Context,
	db *gorm.DB,
	userID, conversationID string,
	items []map[string]any,
) error {
	runIDs := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	var collect func(map[string]any)
	collect = func(item map[string]any) {
		runID, _ := item["run_id"].(string)
		runID = strings.TrimSpace(runID)
		if runID != "" {
			if _, exists := seen[runID]; !exists {
				seen[runID] = struct{}{}
				runIDs = append(runIDs, runID)
			}
		}
		switch answers := item["answers"].(type) {
		case []map[string]any:
			for _, answer := range answers {
				collect(answer)
			}
		case []any:
			for _, raw := range answers {
				if answer, ok := raw.(map[string]any); ok {
					collect(answer)
				}
			}
		}
	}
	for _, item := range items {
		collect(item)
	}
	metricsByRun, err := loadRunPerformance(ctx, db, userID, conversationID, runIDs)
	if err != nil {
		return err
	}
	var attach func(map[string]any)
	attach = func(item map[string]any) {
		if runID, ok := item["run_id"].(string); ok {
			if metrics := metricsByRun[strings.TrimSpace(runID)]; metrics != nil {
				item["performance_metrics"] = metrics
			}
		}
		switch answers := item["answers"].(type) {
		case []map[string]any:
			for _, answer := range answers {
				attach(answer)
			}
		case []any:
			for _, raw := range answers {
				if answer, ok := raw.(map[string]any); ok {
					attach(answer)
				}
			}
		}
	}
	for _, item := range items {
		attach(item)
	}
	return nil
}

func derivePerformanceRates(metrics *RunPerformanceMetrics) {
	if metrics == nil {
		return
	}
	if metrics.CachedTokens != nil && metrics.CacheInputTokens != nil && *metrics.CacheInputTokens > 0 {
		value := float64(*metrics.CachedTokens) / float64(*metrics.CacheInputTokens)
		metrics.CacheHitRate = &value
	}
	if metrics.OutputTokens != nil && metrics.ModelMS != nil && *metrics.ModelMS > 0 {
		value := float64(*metrics.OutputTokens) / (float64(*metrics.ModelMS) / 1000)
		metrics.TokS = &value
	}
	if metrics.ContextInputTokens != nil && metrics.MaxInputTokens != nil && *metrics.MaxInputTokens > 0 {
		value := float64(*metrics.ContextInputTokens) / float64(*metrics.MaxInputTokens)
		metrics.ContextRatio = &value
	}
}
