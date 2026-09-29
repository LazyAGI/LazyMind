package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
)

type skillAmbiguityResolution struct {
	Query        string
	DisplayQuery string
}

func buildSkillAmbiguityAskPending(query string, ambiguity *skillNameAmbiguousError) *AskPendingEvent {
	if ambiguity == nil || len(ambiguity.Candidates) == 0 {
		return nil
	}
	candidates := append([]string{}, ambiguity.Candidates...)
	sort.Strings(candidates)
	requested := strings.TrimSpace(ambiguity.RequestedName)
	if requested == "" {
		requested = "Skill"
	}
	return &AskPendingEvent{
		AskID:       newID("ask_"),
		Title:       "请选择要使用的 Skill",
		Description: fmt.Sprintf("存在多个 %s，请选择一个后继续任务。", requested),
		Questions: []AskQuestion{{
			Text:    fmt.Sprintf("存在多个 %s，请选择要使用的 Skill", requested),
			Type:    "single",
			Choices: candidates,
		}},
		SkillAmbiguity: &SkillAmbiguitySelection{
			RequestedName: requested,
			Candidates:    candidates,
			OriginalQuery: query,
		},
	}
}

func writeSkillAmbiguityAskPending(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	convID, query string,
	target chatPersistTarget,
	raw map[string]any,
	histories []orm.ChatHistory,
	ask *AskPendingEvent,
	stream bool,
) {
	historyExt := buildChatHistoryExtWithTrail(raw, query, histories, target)
	historyExt = mergeAskPendingIntoExt(historyExt, ask)
	historyID := target.HistoryID
	if historyID == "" {
		historyID = newID("h_")
	}
	runID := newID("run_")
	notInvoked := false
	terminal := &RunTerminal{
		Status:        "completed",
		Reason:        "awaiting_user_input",
		PartialOutput: false,
		ModelInvoked:  &notInvoked,
	}
	persistImmediateRunTerminal(ctx, db, convID, historyID, query, runID, target, historyExt, terminal)

	if !stream {
		writeConversationJSON(w, http.StatusOK, map[string]any{
			"conversation_id": convID,
			"history_id":      historyID,
			"seq":             target.Seq,
			"ask_pending":     ask,
			"runtime_event":   runFinishedEvent(runID, *terminal),
		})
		return
	}
	askChunk := &ChatChunkResponse{
		ConversationID: convID,
		Seq:            int32(target.Seq),
		HistoryID:      historyID,
		AskPending:     ask,
	}
	writeSSEChunk(w, flusher, askChunk)
	runtimeChunk := &ChatChunkResponse{
		ConversationID: convID,
		Seq:            int32(target.Seq),
		HistoryID:      historyID,
		RuntimeEvent:   runFinishedEvent(runID, *terminal),
	}
	writeSSEChunk(w, flusher, runtimeChunk)
	if stateStore != nil {
		_ = appendChatChunk(ctx, stateStore, convID, historyID, askChunk)
		_ = appendChatChunk(ctx, stateStore, convID, historyID, runtimeChunk)
		_ = setChatRuntimeStatus(ctx, stateStore, convID, historyID, terminal.Status, "", runID, terminal)
		_ = AppendConvEvent(ctx, stateStore, convID, &ConvEvent{Type: "ask_pending", Payload: ask})
	}
}

func submitSkillAmbiguitySelection(
	ctx context.Context,
	db *gorm.DB,
	histories []orm.ChatHistory,
	structured any,
) (*skillAmbiguityResolution, error) {
	payload, ok := structured.(map[string]any)
	if !ok {
		return nil, nil
	}
	for index := len(histories) - 1; index >= 0; index-- {
		history := &histories[index]
		var ext map[string]any
		if len(history.Ext) == 0 || json.Unmarshal(history.Ext, &ext) != nil {
			continue
		}
		pending, _ := ext["ask_pending"].(map[string]any)
		if pending == nil {
			continue
		}
		if pending["skill_ambiguity"] == nil {
			return nil, nil
		}
		var resolved *skillAmbiguityResolution
		var updated []byte
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current orm.ChatHistory
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND conversation_id = ?", history.ID, history.ConversationID).First(&current).Error; err != nil {
				return err
			}
			ext = nil
			if err := json.Unmarshal(current.Ext, &ext); err != nil {
				return err
			}
			pending, _ = ext["ask_pending"].(map[string]any)
			if ext["ask_answered"] == true || pending == nil || !validAskSubmission(pending, payload) {
				return errors.New("skill selection is invalid or already answered")
			}
			metadata, err := json.Marshal(pending["skill_ambiguity"])
			if err != nil {
				return err
			}
			var target SkillAmbiguitySelection
			if json.Unmarshal(metadata, &target) != nil || len(target.Candidates) == 0 || target.OriginalQuery == "" {
				return errors.New("skill selection metadata is invalid")
			}
			choice, err := selectedSkillChoice(payload)
			if err != nil {
				return err
			}
			if !containsString(target.Candidates, choice) {
				return errors.New("skill selection is not one of the available candidates")
			}
			resolved = &skillAmbiguityResolution{
				Query:        replaceFirstCaseInsensitive(target.OriginalQuery, target.RequestedName, choice),
				DisplayQuery: target.OriginalQuery,
			}
			ext["ask_answered"] = true
			ext["ask_saved_answers"] = submittedAskAnswers(structured)
			updated, err = json.Marshal(ext)
			if err != nil {
				return err
			}
			return tx.Model(&current).Update("ext", updated).Error
		})
		if err != nil {
			return nil, err
		}
		history.Ext = updated
		return resolved, nil
	}
	return nil, nil
}

func selectedSkillChoice(payload map[string]any) (string, error) {
	questions, _ := payload["questions"].([]any)
	if len(questions) != 1 {
		return "", errors.New("skill selection requires one answer")
	}
	question, _ := questions[0].(map[string]any)
	answer, _ := question["answer"].(map[string]any)
	choice := strings.TrimSpace(fmt.Sprint(answer["value"]))
	if question["type"] != "single" || choice == "" || choice == "<nil>" {
		return "", errors.New("skill selection requires a selected Skill")
	}
	return choice, nil
}

func replaceFirstCaseInsensitive(value, old, replacement string) string {
	old = strings.TrimSpace(old)
	if old == "" {
		return value
	}
	index := strings.Index(strings.ToLower(value), strings.ToLower(old))
	if index < 0 {
		return value
	}
	return value[:index] + replacement + value[index+len(old):]
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func maybeReplySkillAmbiguity(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	w http.ResponseWriter,
	flusher http.Flusher,
	convID, query string,
	target chatPersistTarget,
	raw map[string]any,
	histories []orm.ChatHistory,
	err error,
	stream bool,
) bool {
	var ambiguity *skillNameAmbiguousError
	if !errors.As(err, &ambiguity) {
		return false
	}
	ask := buildSkillAmbiguityAskPending(query, ambiguity)
	if ask == nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return true
	}
	writeSkillAmbiguityAskPending(ctx, db, stateStore, w, flusher, convID, query, target, raw, histories, ask, stream)
	return true
}
