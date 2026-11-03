package chat

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// A boundary is an index into ChatHistory.Result, not another copy of it.
// Result and these offsets must be committed in the same database update.
type steeringBoundary struct {
	IntentUpdated *IntentUpdatedEvent `json:"intent_updated,omitempty"`
	ID            string              `json:"id"`
	Text          string              `json:"text"`
	AcceptedAt    float64             `json:"accepted_at"`
	Offset        int                 `json:"offset"`
}

type steeringTimeline struct {
	RunID string             `json:"run_id"`
	Items []steeringBoundary `json:"items"`
}

type steeringApplied struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	AcceptedAt float64 `json:"accepted_at"`
}

type steeringReplayItem struct {
	steeringApplied
	IntentUpdated    *IntentUpdatedEvent `json:"intent_updated,omitempty"`
	AssistantSegment string              `json:"assistant_segment"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
}

func intentForSteeringSegment(updated *IntentUpdatedEvent, result string, ext json.RawMessage, runID string) *IntentUpdatedEvent {
	copy := *updated
	segmentID := ""
	timeline := steeringTimelineFromHistory(orm.ChatHistory{Result: result, Ext: ext, RunID: runID})
	if len(timeline.Items) > 0 {
		segmentID = timeline.Items[len(timeline.Items)-1].ID
	}
	copy.SteeringSegmentID = &segmentID
	copy.RunID = runID
	return &copy
}

func replaceSteeringTailExport(result string, ext json.RawMessage, runID, body string) string {
	timeline := steeringTimelineFromHistory(orm.ChatHistory{Result: result, Ext: ext, RunID: runID})
	start := 0
	if len(timeline.Items) > 0 {
		start = timeline.Items[len(timeline.Items)-1].Offset
	}
	return result[:start] + replaceChatExportResult(result[start:], body)
}

func steeringTail(result string, ext json.RawMessage, runID string) string {
	timeline := steeringTimelineFromHistory(orm.ChatHistory{Result: result, Ext: ext, RunID: runID})
	if len(timeline.Items) == 0 {
		return result
	}
	return result[timeline.Items[len(timeline.Items)-1].Offset:]
}

// Reconnect uses the same boundaries as live delivery. Only the final segment
// is replaced by Delta; earlier segments are addressed by their stable IDs.
func steeringResumeChunk(chunks []*ChatChunkResponse, merged *ChatChunkResponse) *ChatChunkResponse {
	if merged == nil {
		return nil
	}
	var items []steeringReplayItem
	var text, reasoning string
	var intent *IntentUpdatedEvent
	seen := map[string]bool{}
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if chunk.IntentUpdated != nil {
			intent = chunk.IntentUpdated
		}
		if chunk.DeltaMode == ChatDeltaModeReplace {
			text = chunk.Delta
		} else {
			text += chunk.Delta
		}
		if chunk.DeltaMode == ChatDeltaModeReplace && chunk.ModelOutput != nil {
			reasoning = chunk.ReasoningContent
		} else {
			reasoning += chunk.ReasoningContent
		}
		for _, item := range chunk.SteeringApplied {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			items = append(items, steeringReplayItem{steeringApplied: item, AssistantSegment: text, ReasoningContent: reasoning, IntentUpdated: intent})
			text, reasoning = "", ""
			intent = nil
		}
	}
	if len(items) == 0 {
		return merged
	}
	copy := *merged
	copy.SteeringReplay = items
	copy.Delta, copy.ReasoningContent = text, reasoning
	return &copy
}

func resolveSteeringIDs(ctx context.Context, db *gorm.DB, convID, historyID, runID string, items []steeringApplied) error {
	for i := range items {
		var row orm.ConversationFollowUp
		// The endpoint stores routing before contacting the runtime. Do not
		// depend on the HTTP acknowledgement winning the race with this stream.
		if err := db.WithContext(ctx).Where(
			"conversation_id = ? AND target_history_id = ? AND target_run_id = ? AND steer_id = ? AND deleted_at IS NULL",
			convID, historyID, runID, items[i].ID,
		).Take(&row).Error; err != nil {
			return err
		}
		items[i].ID = row.ID
		items[i].Text = row.Text
	}
	return nil
}

func steeringTimelineFromHistory(h orm.ChatHistory) steeringTimeline {
	var ext struct {
		Timeline steeringTimeline `json:"steering_timeline"`
	}
	_ = json.Unmarshal(h.Ext, &ext)
	timeline := ext.Timeline
	if timeline.RunID != h.RunID {
		return steeringTimeline{}
	}
	last := 0
	seen := map[string]bool{}
	for _, item := range timeline.Items {
		if item.ID == "" || seen[item.ID] || item.Offset < last || item.Offset > len(h.Result) {
			return steeringTimeline{}
		}
		seen[item.ID] = true
		last = item.Offset
	}
	return timeline
}

func withSteeringBoundary(ext json.RawMessage, runID string, offset int, items []steeringApplied) json.RawMessage {
	var data map[string]json.RawMessage
	_ = json.Unmarshal(ext, &data)
	if data == nil {
		data = map[string]json.RawMessage{}
	}
	var timeline steeringTimeline
	_ = json.Unmarshal(data["steering_timeline"], &timeline)
	if timeline.RunID != runID {
		timeline = steeringTimeline{RunID: runID}
	}
	seen := map[string]bool{}
	for _, item := range timeline.Items {
		seen[item.ID] = true
	}
	for _, item := range items {
		if item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		// Freeze only an update made in the segment being closed. The latest
		// conversation intent remains in ext; badges belong to their own replies.
		segmentID := ""
		if len(timeline.Items) > 0 {
			segmentID = timeline.Items[len(timeline.Items)-1].ID
		}
		var intent *IntentUpdatedEvent
		_ = json.Unmarshal(data["intent_updated"], &intent)
		if intent != nil && (intent.RunID != runID || intent.SteeringSegmentID == nil || *intent.SteeringSegmentID != segmentID) {
			intent = nil
		}
		timeline.Items = append(timeline.Items, steeringBoundary{
			ID: item.ID, Text: item.Text, AcceptedAt: item.AcceptedAt, Offset: offset, IntentUpdated: intent,
		})
	}
	data["steering_timeline"], _ = json.Marshal(timeline)
	encoded, _ := json.Marshal(data)
	return encoded
}

func hydrateSteeringTimeline(h orm.ChatHistory, item map[string]any) {
	timeline := steeringTimelineFromHistory(h)
	if len(timeline.Items) == 0 {
		return
	}
	messages := make([]conversationSteeringHistoryDTO, 0, len(timeline.Items))
	start := 0
	for _, boundary := range timeline.Items {
		segment := h.Result[start:boundary.Offset]
		acceptedAt := time.UnixMilli(int64(boundary.AcceptedAt * 1000)).UTC()
		visible := stripThinkTags(stripToolTags(segment))
		messages = append(messages, conversationSteeringHistoryDTO{
			ID: boundary.ID, Text: boundary.Text, TargetHistoryID: h.ID,
			IntentUpdated:    boundary.IntentUpdated,
			AssistantSegment: &visible, ReasoningContent: extractThinkContent(segment), AcceptedAt: &acceptedAt,
		})
		start = boundary.Offset
	}
	item["steering_messages"] = messages
	item["assistant_remainder"] = stripThinkTags(stripToolTags(h.Result[start:]))
	item["reasoning_content"] = extractThinkContent(h.Result[start:])
}

// A database-only reconnect needs the same ordered replay as the stream cache,
// rather than replacing the last assistant segment with the whole run's text.
func addSteeringResumeSnapshot(item map[string]any, h orm.ChatHistory) {
	timeline := steeringTimelineFromHistory(h)
	if len(timeline.Items) == 0 {
		return
	}
	replay := make([]steeringReplayItem, 0, len(timeline.Items))
	start := 0
	for _, boundary := range timeline.Items {
		segment := h.Result[start:boundary.Offset]
		replay = append(replay, steeringReplayItem{
			steeringApplied:  steeringApplied{ID: boundary.ID, Text: boundary.Text, AcceptedAt: boundary.AcceptedAt},
			IntentUpdated:    boundary.IntentUpdated,
			AssistantSegment: stripThinkTags(stripToolTags(segment)), ReasoningContent: extractThinkContent(segment),
		})
		start = boundary.Offset
	}
	item["steering_replay"] = replay
	item["delta"] = stripThinkTags(stripToolTags(h.Result[start:]))
	item["message"] = item["delta"]
	item["reasoning_content"] = extractThinkContent(h.Result[start:])
}
