package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const (
	followUpModeQueued = "queued"
	followUpModeSteer  = "steer"

	followUpPending     = "pending"
	followUpSteering    = "steering"
	followUpAccepted    = "accepted"
	followUpFailed      = "failed"
	followUpDispatching = "dispatching"
	followUpSent        = "sent"

	followUpDispatchLease      = 2 * time.Minute
	followUpSteeringLease      = 2 * time.Minute
	followUpAcceptedVisibility = 5 * time.Second
	followUpSentRetention      = 24 * time.Hour

	followUpAssistantSnapshotKey = "_steering_assistant_snapshot"
)

type conversationFollowUpDTO struct {
	ID              string          `json:"id"`
	ConversationID  string          `json:"conversation_id"`
	Content         json.RawMessage `json:"content"`
	Text            string          `json:"text"`
	Mode            string          `json:"mode"`
	Status          string          `json:"status"`
	SortOrder       int64           `json:"sort_order"`
	Revision        int64           `json:"revision"`
	TargetRunID     string          `json:"target_run_id,omitempty"`
	TargetHistoryID string          `json:"target_history_id,omitempty"`
	SteerID         string          `json:"steer_id,omitempty"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	AcceptedAt      *time.Time      `json:"accepted_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type activeFollowUpRun struct {
	RunID       string
	HistoryID   string
	AlgorithmID string
}

type conversationSteeringHistoryDTO struct {
	IntentUpdated     *IntentUpdatedEvent `json:"intent_updated,omitempty"`
	ID                string              `json:"id"`
	Text              string              `json:"text"`
	TargetHistoryID   string              `json:"target_history_id"`
	AssistantSnapshot string              `json:"assistant_snapshot,omitempty"`
	AssistantSegment  *string             `json:"assistant_segment,omitempty"`
	ReasoningContent  string              `json:"reasoning_content,omitempty"`
	AcceptedAt        *time.Time          `json:"accepted_at,omitempty"`
}

func followUpDTO(row orm.ConversationFollowUp) conversationFollowUpDTO {
	return conversationFollowUpDTO{
		ID: row.ID, ConversationID: row.ConversationID, Content: publicFollowUpContent(row.Content), Text: row.Text,
		Mode: row.Mode, Status: row.Status, SortOrder: row.SortOrder, Revision: row.Revision,
		TargetRunID: row.TargetRunID, TargetHistoryID: row.TargetHistoryID, SteerID: row.SteerID,
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, AcceptedAt: row.AcceptedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func publicFollowUpContent(content json.RawMessage) json.RawMessage {
	var payload map[string]any
	if len(content) == 0 || json.Unmarshal(content, &payload) != nil {
		return content
	}
	if _, exists := payload[followUpAssistantSnapshotKey]; !exists {
		return content
	}
	delete(payload, followUpAssistantSnapshotKey)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return content
	}
	return encoded
}

func followUpUserID(r *http.Request) string {
	if userID := strings.TrimSpace(store.UserID(r)); userID != "" {
		return userID
	}
	return "0"
}

func authorizeFollowUpConversation(ctx context.Context, db *gorm.DB, conversationID, userID string) error {
	if conversationID == "" {
		return gorm.ErrRecordNotFound
	}
	return db.WithContext(ctx).Where(
		"id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID,
	).Take(&orm.Conversation{}).Error
}

func currentFollowUpRun(ctx context.Context, db *gorm.DB, conversationID, userID string) (*activeFollowUpRun, error) {
	stateStore := store.State()
	if stateStore == nil {
		return nil, nil
	}
	historyIDs, err := getGeneratingHistoryIDs(ctx, stateStore, conversationID)
	if err != nil || len(historyIDs) == 0 {
		return nil, err
	}
	var histories []orm.ChatHistory
	if err := db.WithContext(ctx).Where("conversation_id = ? AND id IN ?", conversationID, historyIDs).
		Order("seq DESC").Find(&histories).Error; err != nil {
		return nil, err
	}
	var statusLookupErr error
	for _, history := range histories {
		status, statusErr := getChatStatus(ctx, stateStore, conversationID, history.ID)
		if statusErr != nil {
			if statusLookupErr == nil {
				statusLookupErr = statusErr
			}
			continue
		}
		if status == nil || status.Status != "generating" || status.RunID == "" {
			continue
		}
		return &activeFollowUpRun{
			RunID: status.RunID, HistoryID: history.ID, AlgorithmID: strings.TrimSpace(history.AlgorithmID),
		}, nil
	}
	return nil, statusLookupErr
}

// conversationFollowUpContextReady closes the small gap between the runtime
// becoming idle and its final ChatHistory update being committed. Dispatching a
// queued follow-up inside that gap would build the next model request without
// the just-finished turn, so the downgraded guidance would lose conversation
// context.
func conversationFollowUpContextReady(ctx context.Context, db *gorm.DB, conversationID string) (bool, error) {
	var latest orm.ChatHistory
	err := db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("seq DESC, update_time DESC").
		Take(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(latest.RunStatus) != "generating", nil
}

func loadOwnedFollowUp(ctx context.Context, db *gorm.DB, conversationID, followUpID, userID string, lock bool) (orm.ConversationFollowUp, error) {
	var row orm.ConversationFollowUp
	query := db.WithContext(ctx)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where(
		"id = ? AND conversation_id = ? AND create_user_id = ? AND deleted_at IS NULL",
		followUpID, conversationID, userID,
	).Take(&row).Error
	return row, err
}

func maintainConversationFollowUps(ctx context.Context, db *gorm.DB, conversationID, userID string) error {
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ConversationFollowUp{}).Where(
			"conversation_id = ? AND create_user_id = ? AND status = ? AND updated_at < ? AND deleted_at IS NULL",
			conversationID, userID, followUpDispatching, now.Add(-followUpDispatchLease),
		).Updates(map[string]any{
			"status":        followUpFailed,
			"error_code":    "dispatch_interrupted",
			"error_message": "The client stopped before the queued message was sent",
			"revision":      gorm.Expr("revision + 1"),
			"updated_at":    now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.ConversationFollowUp{}).Where(
			"conversation_id = ? AND create_user_id = ? AND status = ? AND updated_at < ? AND deleted_at IS NULL",
			conversationID, userID, followUpSteering, now.Add(-followUpSteeringLease),
		).Updates(map[string]any{
			"status":        followUpFailed,
			"error_code":    "steering_interrupted",
			"error_message": "Guidance delivery was interrupted before it could be confirmed",
			"revision":      gorm.Expr("revision + 1"),
			"updated_at":    now,
		}).Error; err != nil {
			return err
		}
		// Successfully sent ordinary follow-ups are already represented by their
		// own ChatHistory rows and can be discarded after the idempotency window.
		// Accepted steering rows are durable transcript metadata: keep them for as
		// long as the conversation so a history reload can restore the guidance.
		return tx.Unscoped().Where(
			"conversation_id = ? AND create_user_id = ? AND status = ? AND updated_at < ?",
			conversationID, userID, followUpSent, now.Add(-followUpSentRetention),
		).Delete(&orm.ConversationFollowUp{}).Error
	})
}

func acceptedConversationSteeringRows(
	ctx context.Context,
	db *gorm.DB,
	conversationID string,
	userID string,
	historyIDs []string,
) ([]orm.ConversationFollowUp, error) {
	if db == nil || len(historyIDs) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(userID) == "" {
		userID = "0"
	}
	var rows []orm.ConversationFollowUp
	err := db.WithContext(ctx).Where(
		"conversation_id = ? AND create_user_id = ? AND target_history_id IN ? AND mode = ? AND status = ? AND deleted_at IS NULL",
		conversationID, userID, historyIDs, followUpModeSteer, followUpAccepted,
	).Order("accepted_at ASC, created_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

func followUpAssistantSnapshot(content json.RawMessage) string {
	var payload map[string]any
	if len(content) == 0 || json.Unmarshal(content, &payload) != nil {
		return ""
	}
	snapshot, _ := payload[followUpAssistantSnapshotKey].(string)
	return snapshot
}

// injectConversationSteeringMessages makes accepted in-run guidance durable
// model context. A steering message is not a separate ChatHistory row, but it
// is still a real user instruction and must be visible to every later turn.
func injectConversationSteeringMessages(
	ctx context.Context,
	db *gorm.DB,
	conversationID string,
	userID string,
	histories []orm.ChatHistory,
	messages []map[string]any,
) []map[string]any {
	if len(histories) == 0 || len(messages) == 0 {
		return messages
	}
	historyIDs := make([]string, 0, len(histories))
	seqByHistoryID := make(map[string]int, len(histories))
	runByHistoryID := make(map[string]string, len(histories))
	applied := make(map[string]bool)
	for _, history := range histories {
		for _, item := range steeringTimelineFromHistory(history).Items {
			applied[item.ID] = true
		}
		historyIDs = append(historyIDs, history.ID)
		seqByHistoryID[history.ID] = history.Seq
		runByHistoryID[history.ID] = history.RunID
	}
	rows, err := acceptedConversationSteeringRows(ctx, db, conversationID, userID, historyIDs)
	if err != nil || len(rows) == 0 {
		return messages
	}
	rowsBySeq := make(map[int][]orm.ConversationFollowUp)
	for _, row := range rows {
		if applied[row.ID] || (row.TargetRunID != "" && runByHistoryID[row.TargetHistoryID] != "" && row.TargetRunID != runByHistoryID[row.TargetHistoryID]) {
			continue
		}
		seq, ok := seqByHistoryID[row.TargetHistoryID]
		if !ok {
			continue
		}
		rowsBySeq[seq] = append(rowsBySeq[seq], row)
	}
	if len(rowsBySeq) == 0 {
		return messages
	}
	out := make([]map[string]any, 0, len(messages)+len(rows))
	lastAssistant := make(map[int]int)
	for i, message := range messages {
		if seq, ok := historyMessageSeq(message["history_seq"]); ok && message["role"] == "assistant" {
			lastAssistant[seq] = i
		}
	}
	for i, message := range messages {
		out = append(out, message)
		role, _ := message["role"].(string)
		seq, ok := historyMessageSeq(message["history_seq"])
		if role != "assistant" || !ok || lastAssistant[seq] != i {
			continue
		}
		for _, row := range rowsBySeq[seq] {
			out = append(out, map[string]any{
				"role": "user", "content": row.Text, "history_seq": seq,
			})
		}
	}
	return out
}

func historyMessageSeq(value any) (int, bool) {
	switch seq := value.(type) {
	case int:
		return seq, true
	case int32:
		return int(seq), true
	case int64:
		return int(seq), true
	case float64:
		return int(seq), true
	default:
		return 0, false
	}
}

func hydrateConversationSteeringHistory(
	ctx context.Context,
	db *gorm.DB,
	conversationID string,
	userID string,
	historyItems []map[string]any,
) error {
	if db == nil || len(historyItems) == 0 {
		return nil
	}
	if strings.TrimSpace(userID) == "" {
		userID = "0"
	}
	historyIDs := make([]string, 0, len(historyItems))
	itemByHistoryID := make(map[string]map[string]any, len(historyItems))
	for _, item := range historyItems {
		historyID, _ := item["id"].(string)
		if historyID == "" {
			continue
		}
		historyIDs = append(historyIDs, historyID)
		itemByHistoryID[historyID] = item
	}
	if len(historyIDs) == 0 {
		return nil
	}

	rows, err := acceptedConversationSteeringRows(ctx, db, conversationID, userID, historyIDs)
	if err != nil {
		return err
	}

	for _, row := range rows {
		item := itemByHistoryID[row.TargetHistoryID]
		if item == nil {
			continue
		}
		if runID, _ := item["run_id"].(string); runID != "" && row.TargetRunID != "" && runID != row.TargetRunID {
			continue
		}
		messages, _ := item["steering_messages"].([]conversationSteeringHistoryDTO)
		alreadyApplied := false
		for _, message := range messages {
			alreadyApplied = alreadyApplied || message.ID == row.ID
		}
		if alreadyApplied {
			continue
		}
		if remainder, ordered := item["assistant_remainder"].(string); ordered {
			// A stopped worker may have accepted another guide just before its
			// next boundary. Preserve that durable input after the final partial
			// reply, but do not invent a boundary for a still-running worker.
			if item["run_status"] == "generating" {
				continue
			}
			reasoning, _ := item["reasoning_content"].(string)
			item["steering_messages"] = append(messages, conversationSteeringHistoryDTO{
				ID: row.ID, Text: row.Text, TargetHistoryID: row.TargetHistoryID,
				AssistantSegment: &remainder, ReasoningContent: reasoning, AcceptedAt: row.AcceptedAt,
			})
			item["assistant_remainder"], item["reasoning_content"] = "", ""
			continue
		}
		item["steering_messages"] = append(messages, conversationSteeringHistoryDTO{
			ID: row.ID, Text: row.Text, TargetHistoryID: row.TargetHistoryID,
			AssistantSnapshot: followUpAssistantSnapshot(row.Content), AcceptedAt: row.AcceptedAt,
		})
	}
	return nil
}

// CreateConversationFollowUp handles POST /conversations/{conversation_id}/follow-ups.
func CreateConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	userID := followUpUserID(r)
	var body struct {
		Text           string          `json:"text"`
		Content        json.RawMessage `json:"content"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid follow-up body", http.StatusBadRequest)
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.Text == "" || len(body.Text) > 100000 {
		common.ReplyErr(w, "follow-up text is required", http.StatusBadRequest)
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = uuid.NewString()
	}
	if len(body.Content) == 0 || !json.Valid(body.Content) {
		body.Content, _ = json.Marshal(map[string]any{"text": body.Text})
	}
	db := store.DB()
	if err := authorizeFollowUpConversation(r.Context(), db, conversationID, userID); err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err := maintainConversationFollowUps(r.Context(), db, conversationID, userID); err != nil {
		common.ReplyErr(w, "failed to maintain follow-up queue", http.StatusInternalServerError)
		return
	}
	var row orm.ConversationFollowUp
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(
			"create_user_id = ? AND conversation_id = ? AND idempotency_key = ? AND deleted_at IS NULL",
			userID, conversationID, body.IdempotencyKey,
		).Take(&row).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Serialize sort-order allocation per conversation. Without this row
		// lock, concurrent tabs can observe the same MAX(sort_order).
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID,
		).Take(&conversation).Error; err != nil {
			return err
		}
		var maxOrder int64
		if err := tx.Model(&orm.ConversationFollowUp{}).
			Where("conversation_id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		row = orm.ConversationFollowUp{
			ID: uuid.NewString(), ConversationID: conversationID, Content: body.Content, Text: body.Text,
			Mode: followUpModeQueued, Status: followUpPending, SortOrder: maxOrder + 1024, Revision: 1,
			IdempotencyKey: body.IdempotencyKey,
			BaseModel:      orm.BaseModel{CreateUserID: userID, CreateUserName: userID, CreatedAt: now, UpdatedAt: now},
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		// Two identical clicks can race between the read and insert. The unique
		// index is authoritative; return the winner as the idempotent result.
		if lookupErr := db.WithContext(r.Context()).Where(
			"create_user_id = ? AND conversation_id = ? AND idempotency_key = ? AND deleted_at IS NULL",
			userID, conversationID, body.IdempotencyKey,
		).Take(&row).Error; lookupErr == nil {
			common.ReplyOK(w, followUpDTO(row))
			return
		}
		common.ReplyErr(w, "failed to create follow-up", http.StatusConflict)
		return
	}
	common.ReplyOK(w, followUpDTO(row))
}

// ListConversationFollowUps returns all actionable items plus the active run
// snapshot used by clients to avoid steering a successor run by mistake.
func ListConversationFollowUps(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	userID := followUpUserID(r)
	db := store.DB()
	if err := authorizeFollowUpConversation(r.Context(), db, conversationID, userID); err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err := maintainConversationFollowUps(r.Context(), db, conversationID, userID); err != nil {
		common.ReplyErr(w, "failed to maintain follow-up queue", http.StatusInternalServerError)
		return
	}
	var rows []orm.ConversationFollowUp
	if err := db.WithContext(r.Context()).Where(
		"conversation_id = ? AND create_user_id = ? AND deleted_at IS NULL AND status <> ? AND (status <> ? OR accepted_at >= ?)",
		conversationID, userID, followUpSent, followUpAccepted, time.Now().UTC().Add(-followUpAcceptedVisibility),
	).Order("sort_order ASC, created_at ASC, id ASC").Find(&rows).Error; err != nil {
		common.ReplyErr(w, "failed to list follow-ups", http.StatusInternalServerError)
		return
	}
	items := make([]conversationFollowUpDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, followUpDTO(row))
	}
	active, activeErr := currentFollowUpRun(r.Context(), db, conversationID, userID)
	if activeErr != nil {
		common.ReplyErr(w, "failed to inspect active run", http.StatusServiceUnavailable)
		return
	}
	activeRunID := ""
	supportsSteering := false
	if active != nil {
		activeRunID = active.RunID
		supportsSteering = !strings.HasPrefix(active.AlgorithmID, "external:")
	}
	common.ReplyOK(w, map[string]any{
		"items": items, "active_run_id": activeRunID, "supports_steering": supportsSteering,
	})
}

// UpdateConversationFollowUp handles PATCH on a pending or failed item.
func UpdateConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	followUpID := common.PathVar(r, "follow_up_id")
	userID := followUpUserID(r)
	var body struct {
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid follow-up body", http.StatusBadRequest)
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		common.ReplyErr(w, "follow-up text is required", http.StatusBadRequest)
		return
	}
	if len(body.Content) == 0 || !json.Valid(body.Content) {
		body.Content, _ = json.Marshal(map[string]any{"text": body.Text})
	}
	db := store.DB()
	var updated orm.ConversationFollowUp
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		row, err := loadOwnedFollowUp(r.Context(), tx, conversationID, followUpID, userID, true)
		if err != nil {
			return err
		}
		if row.Status != followUpPending && row.Status != followUpFailed {
			return errFollowUpConflict
		}
		row.Text, row.Content, row.Status = body.Text, body.Content, followUpPending
		row.Mode, row.ErrorCode, row.ErrorMessage = followUpModeQueued, "", ""
		row.Revision++
		row.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		updated = row
		return nil
	})
	if errors.Is(err, errFollowUpConflict) {
		common.ReplyErr(w, "follow-up is no longer editable", http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, "follow-up not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, followUpDTO(updated))
}

var errFollowUpConflict = errors.New("follow-up state conflict")

func beginFollowUpSteering(
	ctx context.Context,
	db *gorm.DB,
	row orm.ConversationFollowUp,
	active *activeFollowUpRun,
	steerID string,
) error {
	result := db.WithContext(ctx).Model(&orm.ConversationFollowUp{}).Where(
		"id = ? AND conversation_id = ? AND create_user_id = ? AND status IN ? AND revision = ?",
		row.ID, row.ConversationID, row.CreateUserID,
		[]string{followUpPending, followUpFailed}, row.Revision,
	).Updates(map[string]any{
		"mode": followUpModeSteer, "status": followUpSteering, "target_run_id": active.RunID,
		"target_history_id": active.HistoryID, "steer_id": steerID, "error_code": "", "error_message": "",
		"revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errFollowUpConflict
	}
	return nil
}

// DeleteConversationFollowUp only removes input that is not owned by a runtime.
func DeleteConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	followUpID := common.PathVar(r, "follow_up_id")
	userID := followUpUserID(r)
	result := store.DB().WithContext(r.Context()).Unscoped().Where(
		"id = ? AND conversation_id = ? AND create_user_id = ? AND status IN ? AND deleted_at IS NULL",
		followUpID, conversationID, userID, []string{followUpPending, followUpFailed},
	).Delete(&orm.ConversationFollowUp{})
	if result.Error != nil || result.RowsAffected != 1 {
		common.ReplyErr(w, "follow-up is no longer removable", http.StatusConflict)
		return
	}
	common.ReplyOK(w, map[string]any{"id": followUpID})
}

// ReorderConversationFollowUps replaces the complete pending queue order.
func ReorderConversationFollowUps(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	userID := followUpUserID(r)
	var body struct {
		OrderedIDs []string `json:"ordered_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid reorder body", http.StatusBadRequest)
		return
	}
	seen := make(map[string]struct{}, len(body.OrderedIDs))
	for _, id := range body.OrderedIDs {
		if id = strings.TrimSpace(id); id == "" {
			common.ReplyErr(w, "ordered_ids contains an empty id", http.StatusBadRequest)
			return
		} else if _, exists := seen[id]; exists {
			common.ReplyErr(w, "ordered_ids contains a duplicate id", http.StatusBadRequest)
			return
		} else {
			seen[id] = struct{}{}
		}
	}
	db := store.DB()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var rows []orm.ConversationFollowUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"conversation_id = ? AND create_user_id = ? AND status = ? AND deleted_at IS NULL",
			conversationID, userID, followUpPending,
		).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(body.OrderedIDs) {
			return errFollowUpConflict
		}
		for _, row := range rows {
			if _, ok := seen[row.ID]; !ok {
				return errFollowUpConflict
			}
		}
		now := time.Now().UTC()
		for index, id := range body.OrderedIDs {
			if err := tx.Model(&orm.ConversationFollowUp{}).Where(
				"id = ? AND conversation_id = ? AND create_user_id = ? AND status = ?",
				id, conversationID, userID, followUpPending,
			).Updates(map[string]any{"sort_order": int64(index+1) * 1024, "revision": gorm.Expr("revision + 1"), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errFollowUpConflict) {
		common.ReplyErr(w, "follow-up queue changed; reload before reordering", http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, "failed to reorder follow-ups", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"ordered_ids": body.OrderedIDs})
}

// SteerConversationFollowUp submits a pending item to the current run. If the
// expected run ended, the item remains queued and the successful response tells
// the client that it was downgraded to an ordinary next-turn message.
func SteerConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	followUpID := common.PathVar(r, "follow_up_id")
	userID := followUpUserID(r)
	var body struct {
		ExpectedRunID string `json:"expected_run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid steer body", http.StatusBadRequest)
		return
	}
	db := store.DB()
	row, err := loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
	if err != nil {
		common.ReplyErr(w, "follow-up not found", http.StatusNotFound)
		return
	}
	if row.Status == followUpAccepted {
		common.ReplyOK(w, map[string]any{"item": followUpDTO(row), "delivery_mode": followUpModeSteer})
		return
	}
	if row.Status != followUpPending && row.Status != followUpFailed {
		common.ReplyErr(w, "follow-up is already being processed", http.StatusConflict)
		return
	}
	active, activeErr := currentFollowUpRun(r.Context(), db, conversationID, userID)
	if activeErr != nil {
		common.ReplyErr(w, "failed to inspect active run", http.StatusServiceUnavailable)
		return
	}
	expectedRunID := strings.TrimSpace(body.ExpectedRunID)
	if active == nil || (expectedRunID != "" && active.RunID != expectedRunID) {
		result := db.Model(&orm.ConversationFollowUp{}).Where(
			"id = ? AND conversation_id = ? AND create_user_id = ? AND status IN ? AND revision = ?",
			row.ID, conversationID, userID, []string{followUpPending, followUpFailed}, row.Revision,
		).Updates(map[string]any{
			"mode": followUpModeQueued, "status": followUpPending, "target_run_id": "", "target_history_id": "",
			"error_code": "", "error_message": "", "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			common.ReplyErr(w, "failed to queue guidance after the active run ended", http.StatusInternalServerError)
			return
		}
		if result.RowsAffected != 1 {
			common.ReplyErr(w, "follow-up state changed", http.StatusConflict)
			return
		}
		row, err = loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
		if err != nil {
			common.ReplyErr(w, "failed to reload queued guidance", http.StatusInternalServerError)
			return
		}
		common.ReplyOK(w, map[string]any{
			"item": followUpDTO(row), "delivery_mode": followUpModeQueued, "reason": "active_run_finished",
		})
		return
	}
	if strings.HasPrefix(active.AlgorithmID, "external:") {
		result := db.Model(&orm.ConversationFollowUp{}).Where(
			"id = ? AND conversation_id = ? AND create_user_id = ? AND status IN ? AND revision = ?",
			row.ID, conversationID, userID, []string{followUpPending, followUpFailed}, row.Revision,
		).Updates(map[string]any{
			"mode": followUpModeSteer, "status": followUpFailed, "target_run_id": active.RunID,
			"target_history_id": active.HistoryID, "error_code": "steering_not_supported",
			"error_message": "This executor does not support steering", "revision": gorm.Expr("revision + 1"),
			"updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			common.ReplyErr(w, "failed to persist unsupported steering state", http.StatusInternalServerError)
			return
		}
		if result.RowsAffected != 1 {
			common.ReplyErr(w, "follow-up state changed", http.StatusConflict)
			return
		}
		row, err = loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
		if err != nil {
			common.ReplyErr(w, "failed to reload unsupported guidance", http.StatusInternalServerError)
			return
		}
		common.ReplyOK(w, map[string]any{"item": followUpDTO(row), "delivery_mode": followUpModeSteer})
		return
	}
	steerID := row.SteerID
	if steerID == "" {
		steerID = "steer_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if err := beginFollowUpSteering(r.Context(), db, row, active, steerID); errors.Is(err, errFollowUpConflict) {
		common.ReplyErr(w, "follow-up state changed", http.StatusConflict)
		return
	} else if err != nil {
		common.ReplyErr(w, "failed to begin guidance delivery", http.StatusInternalServerError)
		return
	}
	service := NewChatServiceWithEndpoint(chatServiceURL())
	upstream, statusCode, steerErr := service.SteerChat(r.Context(), LazyChatSteerRequest{
		AlgorithmID: active.AlgorithmID, ConversationID: conversationID, RunID: active.RunID,
		SteerID: steerID, Input: row.Text,
	})
	if steerErr != nil || upstream == nil || statusCode != http.StatusOK || !upstream.Accepted {
		code, detail := "steer_delivery_failed", "The runtime did not accept the guidance"
		if upstream != nil {
			if upstream.Code != "" {
				code = upstream.Code
			}
			if upstream.Message != "" {
				detail = upstream.Message
			}
		}
		if code == "response_already_completed" || code == "response_not_active" {
			result := db.Model(&orm.ConversationFollowUp{}).Where("id = ? AND status = ?", row.ID, followUpSteering).
				Updates(map[string]any{"mode": followUpModeQueued, "status": followUpPending, "target_run_id": "", "target_history_id": "", "error_code": "", "error_message": "", "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
			if result.Error != nil || result.RowsAffected != 1 {
				common.ReplyErr(w, "failed to queue guidance after the active run ended", http.StatusInternalServerError)
				return
			}
			row, err = loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
			if err != nil {
				common.ReplyErr(w, "failed to reload queued guidance", http.StatusInternalServerError)
				return
			}
			common.ReplyOK(w, map[string]any{"item": followUpDTO(row), "delivery_mode": followUpModeQueued, "reason": "active_run_finished"})
			return
		}
		result := db.Model(&orm.ConversationFollowUp{}).Where("id = ? AND status = ?", row.ID, followUpSteering).
			Updates(map[string]any{"status": followUpFailed, "error_code": code, "error_message": detail, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil || result.RowsAffected != 1 {
			common.ReplyErr(w, "failed to persist steering delivery failure", http.StatusInternalServerError)
			return
		}
		row, err = loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
		if err != nil {
			common.ReplyErr(w, "failed to reload failed guidance", http.StatusInternalServerError)
			return
		}
		common.ReplyOK(w, map[string]any{"item": followUpDTO(row), "delivery_mode": followUpModeSteer})
		return
	}
	now := time.Now().UTC()
	result := db.Model(&orm.ConversationFollowUp{}).Where("id = ? AND status = ?", row.ID, followUpSteering).
		Updates(map[string]any{
			"status": followUpAccepted, "accepted_at": now,
			"revision": gorm.Expr("revision + 1"), "updated_at": now,
		})
	if result.Error != nil || result.RowsAffected != 1 {
		common.ReplyErr(w, "guidance was accepted but its acknowledgement could not be persisted", http.StatusInternalServerError)
		return
	}
	row, err = loadOwnedFollowUp(r.Context(), db, conversationID, followUpID, userID, false)
	if err != nil {
		common.ReplyErr(w, "failed to reload accepted guidance", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"item": followUpDTO(row), "delivery_mode": followUpModeSteer})
}

// ClaimNextConversationFollowUp atomically assigns the queue head to one client.
func ClaimNextConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	userID := followUpUserID(r)
	db := store.DB()
	if err := maintainConversationFollowUps(r.Context(), db, conversationID, userID); err != nil {
		common.ReplyErr(w, "failed to maintain follow-up queue", http.StatusInternalServerError)
		return
	}
	active, activeErr := currentFollowUpRun(r.Context(), db, conversationID, userID)
	if activeErr != nil {
		common.ReplyErr(w, "failed to inspect active run", http.StatusServiceUnavailable)
		return
	}
	if active != nil {
		common.ReplyOK(w, map[string]any{"item": nil})
		return
	}
	contextReady, contextErr := conversationFollowUpContextReady(r.Context(), db, conversationID)
	if contextErr != nil {
		common.ReplyErr(w, "failed to confirm conversation history", http.StatusServiceUnavailable)
		return
	}
	if !contextReady {
		common.ReplyOK(w, map[string]any{"item": nil})
		return
	}
	var claimed *orm.ConversationFollowUp
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var rows []orm.ConversationFollowUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"conversation_id = ? AND create_user_id = ? AND mode = ? AND status = ? AND deleted_at IS NULL",
			conversationID, userID, followUpModeQueued, followUpPending,
		).Order("sort_order ASC, created_at ASC, id ASC").Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
			return err
		}
		row := rows[0]
		result := tx.Model(&orm.ConversationFollowUp{}).Where("id = ? AND status = ?", row.ID, followUpPending).
			Updates(map[string]any{"status": followUpDispatching, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil || result.RowsAffected != 1 {
			return errFollowUpConflict
		}
		row.Status = followUpDispatching
		row.Revision++
		claimed = &row
		return nil
	})
	if err != nil && !errors.Is(err, errFollowUpConflict) {
		common.ReplyErr(w, "failed to claim follow-up", http.StatusInternalServerError)
		return
	}
	if claimed == nil {
		common.ReplyOK(w, map[string]any{"item": nil})
		return
	}
	// The active-run snapshot above is outside the SQL transaction. Close the
	// narrow race where another tab starts a turn while this queue head is being
	// claimed, and leave the item pending for the next terminal transition.
	active, activeErr = currentFollowUpRun(r.Context(), db, conversationID, userID)
	contextReady, contextErr = conversationFollowUpContextReady(r.Context(), db, conversationID)
	if activeErr != nil || contextErr != nil || active != nil || !contextReady {
		reset := db.WithContext(r.Context()).Model(&orm.ConversationFollowUp{}).Where(
			"id = ? AND conversation_id = ? AND create_user_id = ? AND status = ?",
			claimed.ID, conversationID, userID, followUpDispatching,
		).Updates(map[string]any{
			"status": followUpPending, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC(),
		})
		if reset.Error != nil || reset.RowsAffected != 1 {
			common.ReplyErr(w, "failed to release follow-up claim", http.StatusInternalServerError)
			return
		}
		if activeErr != nil || contextErr != nil {
			common.ReplyErr(w, "failed to confirm the queue is idle", http.StatusServiceUnavailable)
			return
		}
		common.ReplyOK(w, map[string]any{"item": nil})
		return
	}
	common.ReplyOK(w, map[string]any{"item": followUpDTO(*claimed)})
}

// CompleteConversationFollowUp acknowledges whether a claimed ordinary send
// opened successfully. Failed input is retained verbatim for retry.
func CompleteConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	followUpID := common.PathVar(r, "follow_up_id")
	userID := followUpUserID(r)
	var body struct {
		Success      bool   `json:"success"`
		Requeue      bool   `json:"requeue"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid completion body", http.StatusBadRequest)
		return
	}
	updates := map[string]any{"updated_at": time.Now().UTC(), "revision": gorm.Expr("revision + 1")}
	if body.Requeue {
		updates["mode"] = followUpModeQueued
		updates["status"] = followUpPending
		updates["target_run_id"], updates["target_history_id"] = "", ""
		updates["error_code"], updates["error_message"] = "", ""
	} else if body.Success {
		updates["status"] = followUpSent
		updates["error_code"], updates["error_message"] = "", ""
	} else {
		updates["status"] = followUpFailed
		updates["error_code"], updates["error_message"] = strings.TrimSpace(body.ErrorCode), strings.TrimSpace(body.ErrorMessage)
	}
	result := store.DB().WithContext(r.Context()).Model(&orm.ConversationFollowUp{}).Where(
		"id = ? AND conversation_id = ? AND create_user_id = ? AND status = ?",
		followUpID, conversationID, userID, followUpDispatching,
	).Updates(updates)
	if result.Error != nil || result.RowsAffected != 1 {
		common.ReplyErr(w, "follow-up is no longer dispatching", http.StatusConflict)
		return
	}
	common.ReplyOK(w, map[string]any{"id": followUpID, "status": updates["status"]})
}

// RetryConversationFollowUp puts failed input back into the pending queue.
func RetryConversationFollowUp(w http.ResponseWriter, r *http.Request) {
	conversationID := conversationIDFromPath(r)
	followUpID := common.PathVar(r, "follow_up_id")
	userID := followUpUserID(r)
	result := store.DB().WithContext(r.Context()).Model(&orm.ConversationFollowUp{}).Where(
		"id = ? AND conversation_id = ? AND create_user_id = ? AND status = ?",
		followUpID, conversationID, userID, followUpFailed,
	).Updates(map[string]any{
		"mode": followUpModeQueued, "status": followUpPending,
		"target_run_id": "", "target_history_id": "", "error_code": "", "error_message": "",
		"revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC(),
	})
	if result.Error != nil || result.RowsAffected != 1 {
		common.ReplyErr(w, "follow-up is not retryable", http.StatusConflict)
		return
	}
	row, err := loadOwnedFollowUp(r.Context(), store.DB(), conversationID, followUpID, userID, false)
	if err != nil {
		common.ReplyErr(w, "failed to reload retried follow-up", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, followUpDTO(row))
}
