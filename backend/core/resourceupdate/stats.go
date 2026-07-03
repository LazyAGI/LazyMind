package resourceupdate

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	historyToolCallTagPattern   = regexp.MustCompile(`(?s)<tool_call(?:[^>]*)>(.*?)</tool_call>`)
	historyToolResultTagPattern = regexp.MustCompile(`(?s)<tool_result(?:[^>]*)>(.*?)</tool_result>`)
)

type skillReviewHistoryStatsRow struct {
	ConversationID string `gorm:"column:conversation_id"`
	Content        string `gorm:"column:content"`
	Result         string `gorm:"column:result"`
}

type skillReviewConversationStats struct {
	UserTurnCount    int
	ToolCallCount    int
	KnownToolCallIDs map[string]struct{}
}

func CountSkillReviewHistoryStats(ctx context.Context, db *gorm.DB, userID string, start, end time.Time, minUserTurns, minToolTurns int) (HistoryStats, error) {
	var stats HistoryStats
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return stats, nil
	}
	var rows []skillReviewHistoryStatsRow
	err := db.WithContext(ctx).
		Table("chat_histories AS ch").
		Select("ch.conversation_id, ch.content, ch.result").
		Joins("JOIN conversations AS c ON c.id = ch.conversation_id").
		Where("c.create_user_id = ?", userID).
		Where("c.deleted_at IS NULL").
		Where("c.updated_at >= ? AND c.updated_at < ?", start, end).
		Order("ch.conversation_id ASC, ch.create_time ASC, ch.seq ASC").
		Scan(&rows).Error
	if err != nil {
		return stats, err
	}

	perConversation := make(map[string]skillReviewConversationStats)
	for _, row := range rows {
		conversationID := strings.TrimSpace(row.ConversationID)
		if conversationID == "" {
			continue
		}
		item := perConversation[conversationID]
		if item.KnownToolCallIDs == nil {
			item.KnownToolCallIDs = map[string]struct{}{}
		}
		if strings.TrimSpace(row.Content) != "" {
			item.UserTurnCount++
			stats.UserTurnCount++
		}
		toolTurns := countHistoryResultToolTurns(row.Result, item.KnownToolCallIDs)
		item.ToolCallCount += toolTurns
		stats.ToolCallCount += toolTurns
		perConversation[conversationID] = item
	}

	for _, item := range perConversation {
		if item.UserTurnCount >= minUserTurns && item.ToolCallCount >= minToolTurns {
			stats.QualifiedSessionCount++
		}
	}
	stats.QuantityThreshold = 0
	return stats, nil
}

func countHistoryResultToolTurns(result string, knownToolCallIDs map[string]struct{}) int {
	count := 0
	for _, match := range historyToolCallTagPattern.FindAllStringSubmatch(result, -1) {
		payload := parseHistoryToolTagPayload(match)
		if payload == nil {
			continue
		}
		toolCallID, _ := payload["id"].(string)
		toolName, _ := payload["name"].(string)
		toolCallID = strings.TrimSpace(toolCallID)
		toolName = strings.TrimSpace(toolName)
		if toolCallID == "" || toolName == "" {
			continue
		}
		knownToolCallIDs[toolCallID] = struct{}{}
		count++
	}
	for _, match := range historyToolResultTagPattern.FindAllStringSubmatch(result, -1) {
		payload := parseHistoryToolTagPayload(match)
		if payload == nil {
			continue
		}
		toolCallID, _ := payload["id"].(string)
		toolCallID = strings.TrimSpace(toolCallID)
		if _, ok := knownToolCallIDs[toolCallID]; ok && toolCallID != "" {
			continue
		}
		count++
	}
	return count
}

func parseHistoryToolTagPayload(match []string) map[string]any {
	if len(match) < 2 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(match[1]), &payload); err != nil {
		return nil
	}
	return payload
}
