package chat

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/subagent"
)

const (
	chatModelPreparedBodyKey  = "_chat_model_prepared"
	chatModelFallbackBodyKey  = "_chat_model_fallback"
	RuntimeEventModelSwitched = "model_switched"
)

type ModelSwitchIdentity = common.ModelSwitchIdentity
type ModelSwitchedData = common.ModelSwitchedData
type ModelSwitchRecord = common.ModelSwitchRecord
type chatFallbackInitialSwitch = common.ModelFallbackInitialSwitch

type chatFallbackState struct {
	candidates    []modelconfig.ChatModelCandidate
	models        map[string]availableChatModel
	currentID     string
	initialSwitch *chatFallbackInitialSwitch
	lastIndex     int
	records       []ModelSwitchRecord
	seen          map[string]string
}

func clearChatFallbackInput(body map[string]any) {
	for key := range body {
		if strings.HasPrefix(key, "model_fallback_") || strings.HasPrefix(key, "_model_fallback") || key == chatModelFallbackBodyKey {
			delete(body, key)
		}
	}
}
func newChatFallbackState(prepared *modelconfig.ChatRuntimeConfig, currentID string) *chatFallbackState {
	state := &chatFallbackState{candidates: prepared.Candidates, models: map[string]availableChatModel{}, currentID: currentID, seen: map[string]string{}}
	for _, model := range prepared.Models {
		state.models[modelconfig.ChatCandidateID(model.Source, model.ID)] = model
	}
	return state
}
func (state *chatFallbackState) candidate(id string) *modelconfig.ChatModelCandidate {
	for i := range state.candidates {
		if state.candidates[i].CandidateID == id {
			return &state.candidates[i]
		}
	}
	return nil
}
func (state *chatFallbackState) identity(id string) ModelSwitchIdentity {
	model := state.models[id]
	return ModelSwitchIdentity{CandidateID: id, Source: model.Source, ModelID: model.ID, ProviderGroupID: model.ProviderGroupID, ProviderName: model.ProviderName, ModelName: model.ModelName}
}
func chatModelRouteFromSnapshot(conversation *orm.Conversation, mode string) *chatModelRoute {
	var snapshot chatModelSnapshot
	_ = json.Unmarshal(conversation.ChatModelSnapshot, &snapshot)
	// A stored arbitrary initial ID is not evidence that a model was ever valid.
	if snapshot.ModelID == "" || conversation.ChatModelID != nil && *conversation.ChatModelID != snapshot.ModelID {
		return nil
	}
	if snapshot.Source == "" && conversation.ChatModelSource != nil {
		snapshot.Source = *conversation.ChatModelSource
	}
	if snapshot.Source != "own" && snapshot.Source != "shared" && snapshot.Source != "cloud" {
		return nil
	}
	return &chatModelRoute{Mode: mode, Strategy: mode, TaskClass: mode, Reason: "model_unavailable", ModelID: snapshot.ModelID, ProviderID: snapshot.ProviderID, ProviderName: snapshot.ProviderName, ModelName: snapshot.ModelName, Source: snapshot.Source, snapshot: &snapshot}
}
func initialFallbackChatCandidate(prepared *modelconfig.ChatRuntimeConfig, original *availableChatModel) (*availableChatModel, *modelconfig.ChatModelCandidate) {
	var window *int64
	if original != nil {
		window = modelconfig.ConfiguredChatWindow(original.MaxInputTokens)
	}
	return modelconfig.InitialChatCandidate(prepared, window)
}
func (state *chatFallbackState) setInitialSwitch(from *chatModelRoute, window *int64) {
	if from == nil {
		return
	}
	identity := ModelSwitchIdentity{CandidateID: modelconfig.ChatCandidateID(from.Source, from.ModelID), Source: from.Source, ModelID: from.ModelID, ProviderName: from.ProviderName, ModelName: from.ModelName}
	if from.snapshot != nil {
		identity.ProviderGroupID = from.snapshot.ProviderGroupID
	}
	state.initialSwitch = &chatFallbackInitialSwitch{From: identity, ReasonCode: "model_config_unavailable", FromMaxInputTokens: window}
}
func validateModelSwitched(raw json.RawMessage) error { return common.ValidateModelSwitched(raw) }
func sameModelSwitchIdentity(got, expected ModelSwitchIdentity) bool {
	return common.SameModelSwitchIdentity(got, expected)
}

// Stage a switch without advancing the live route before its checkpoint commits.
func copyChatFallbackBody(body map[string]any) map[string]any {
	next := maps.Clone(body)
	if state, _ := body[chatModelFallbackBodyKey].(*chatFallbackState); state != nil {
		copy := *state
		copy.records = append([]ModelSwitchRecord(nil), state.records...)
		copy.seen = maps.Clone(state.seen)
		next[chatModelFallbackBodyKey] = &copy
	}
	return next
}

// The Host emits this fact only after a candidate call and local commit succeed.
// Rebuild public fields from Core-authorized identities before projecting it.
func acceptChatFallbackEvent(body map[string]any, event *ChatRuntimeEvent, runID string, ext *json.RawMessage) (bool, error) {
	if err := event.Validate(runID); err != nil {
		return false, err
	}
	if event.Type == RuntimeEventModelCallFinished {
		var data ModelCallFinishedData
		_ = json.Unmarshal(event.Data, &data)
		event.Data, _ = json.Marshal(data)
		return true, nil
	}
	if event.Type != RuntimeEventModelSwitched {
		return true, nil
	}
	state, _ := body[chatModelFallbackBodyKey].(*chatFallbackState)
	if state == nil {
		return false, errors.New("model switch has no authorized candidates")
	}
	var data ModelSwitchedData
	_ = json.Unmarshal(event.Data, &data)
	candidate := state.candidate(data.To.CandidateID)
	from := state.identity(data.From.CandidateID)
	var fromWindow *int64
	if previous := state.candidate(data.From.CandidateID); previous != nil {
		fromWindow = previous.MaxInputTokens
	}
	if initial := state.initialSwitch; initial != nil && data.From.CandidateID == initial.From.CandidateID {
		from, fromWindow = initial.From, initial.FromMaxInputTokens
	}
	if candidate == nil || !sameModelSwitchIdentity(data.To, state.identity(candidate.CandidateID)) || !sameModelSwitchIdentity(data.From, from) {
		return false, errors.New("model switch candidate mismatch")
	}
	data.From, data.To = from, state.identity(candidate.CandidateID)
	data.FromMaxInputTokens, data.ToMaxInputTokens = fromWindow, candidate.MaxInputTokens
	event.Data, _ = json.Marshal(data)
	fingerprint := event.Type + string(event.Data)
	if prior, ok := state.seen[event.EventID]; ok {
		if prior != fingerprint {
			return false, errors.New("conflicting runtime event")
		}
		return false, nil
	}
	expectedFrom := state.currentID
	if state.lastIndex == 0 && state.initialSwitch != nil {
		expectedFrom = state.initialSwitch.From.CandidateID
		if data.ReasonCode != state.initialSwitch.ReasonCode {
			return false, errors.New("model switch initial reason mismatch")
		}
	}
	if data.SwitchIndex != state.lastIndex+1 || data.From.CandidateID != expectedFrom {
		return false, errors.New("model switch sequence mismatch")
	}
	model := state.models[candidate.CandidateID]
	previous := chatModelRouteFromBody(body)
	route := fixedChatModelRoute(&model)
	if previous != nil {
		route.Mode, route.Strategy, route.TaskClass = previous.Mode, previous.Strategy, previous.TaskClass
		route.SelectionVersion = previous.SelectionVersion
	}
	route.Reason = "model_fallback"
	route.snapshot = chatModelSnapshotForModel(&model)
	body[chatModelRouteBodyKey] = route
	config := cloneChatLLMConfig(llmConfigFromBody(body))
	if config == nil {
		config = map[string]any{}
	}
	config["llm"] = cloneChatLLMConfig(candidate.LLMConfig)["llm"]
	body["llm_config"] = config
	state.currentID, state.lastIndex = candidate.CandidateID, data.SwitchIndex
	state.records = append(state.records, ModelSwitchRecord{ModelSwitchedData: data, EventID: event.EventID})
	state.seen[event.EventID] = fingerprint
	*ext = mergeChatModelRouteIntoExt(*ext, body)
	*ext = mergeModelSwitchesIntoExt(*ext, state.records)
	return true, nil
}

// A confirmed Host switch is invocation evidence even when a native SDK path
// has no call lifecycle event. Keep the older no-switch behavior unchanged.
func chatModelWasInvoked(terminal *RunTerminal, route *chatModelRoute, records []ModelSwitchRecord) bool {
	if terminal.modelWasInvoked() {
		return true
	}
	if terminal == nil || terminal.Status != "completed" || route == nil || len(records) == 0 {
		return false
	}
	last := records[len(records)-1]
	return last.EventID != "" && last.SwitchIndex > 0 && last.To.ModelID == route.ModelID && last.To.Source == route.Source
}

func cloneChatLLMConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	raw, _ := json.Marshal(config)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}
func mergeModelSwitchesIntoExt(raw json.RawMessage, records []ModelSwitchRecord) json.RawMessage {
	var ext map[string]any
	_ = json.Unmarshal(raw, &ext)
	if ext == nil {
		ext = map[string]any{}
	}
	if len(records) == 0 {
		delete(ext, "model_switches")
	} else {
		ext["model_switches"] = records
	}
	return marshalChatHistoryExt(ext)
}
func modelSwitchesFromBody(body map[string]any) []ModelSwitchRecord {
	if state, ok := body[chatModelFallbackBodyKey].(*chatFallbackState); ok {
		return append([]ModelSwitchRecord(nil), state.records...)
	}
	return nil
}
func persistChatFallbackProgress(ctx context.Context, db *gorm.DB, convID, historyID, runID, query, algorithmID, result string, seq int, ext json.RawMessage, retrieval json.RawMessage, selectionBody map[string]any) error {
	values := map[string]any{"ext": ext, "result": result, "update_time": time.Now()}
	if retrieval != nil {
		values["retrieval_result"] = retrieval
	}
	return conversationCheckpoint(ctx, db, convID, func(tx *gorm.DB) error {
		var history orm.ChatHistory
		err := tx.Select("run_id").Where("id = ? AND conversation_id = ?", historyID, convID).Take(&history).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Create(&orm.ChatHistory{ID: historyID, ConversationID: convID, RunID: runID, RunStatus: "generating", RawContent: query, Content: query, AlgorithmID: algorithmID, Seq: seq, Result: result, Ext: ext, RetrievalResult: retrieval, TimeMixin: orm.TimeMixin{CreateTime: time.Now(), UpdateTime: time.Now()}}).Error
		} else if err == nil {
			if history.RunID != runID {
				return gorm.ErrRecordNotFound
			}
			err = tx.Model(&orm.ChatHistory{}).Where("id = ? AND run_id = ?", historyID, runID).Updates(values).Error
		}
		if err != nil || selectionBody == nil {
			return err
		}
		route := chatModelRouteFromBody(selectionBody)
		if route == nil || route.snapshot == nil || route.SelectionVersion == nil || len(modelSwitchesFromBody(selectionBody)) == 0 {
			return nil
		}
		snapshot := *route.snapshot
		snapshot.SuccessfulRunID = runID
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		updates := map[string]any{"chat_model_snapshot": raw}
		if route.Mode == chatModelModeFixed {
			updates["chat_model_mode"], updates["chat_model_id"], updates["chat_model_source"] = route.Mode, route.ModelID, route.Source
		}
		latest := tx.Model(&orm.ChatHistory{}).Select("id").Where("conversation_id = ?", convID).Order("seq DESC, create_time DESC, id DESC").Limit(1)
		// Automatic recovery keeps the explicit-selection generation; a newer user choice wins.
		return tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND COALESCE(NULLIF(chat_model_mode, ''), 'fixed') = ? AND chat_model_version = ?", convID, userIDFromChatRequestBody(selectionBody), route.Mode, *route.SelectionVersion).
			Where("? = (?)", historyID, latest).Updates(updates).Error
	})
}

func modelSwitchesFromExt(raw json.RawMessage) []ModelSwitchRecord {
	var ext struct {
		ModelSwitches []ModelSwitchRecord `json:"model_switches"`
	}
	_ = json.Unmarshal(raw, &ext)
	return ext.ModelSwitches
}

// The child receives a value snapshot of the confirmed route, then reauthorizes it.
func inheritedTaskModelOrigin(body map[string]any) *subagent.InheritedModelOrigin {
	route := chatModelRouteFromBody(body)
	if route == nil || route.ModelID == "" || route.Source != "own" && route.Source != "shared" && route.Source != "cloud" {
		return nil
	}
	identity := ModelSwitchIdentity{CandidateID: modelconfig.ChatCandidateID(route.Source, route.ModelID), Source: route.Source, ModelID: route.ModelID, ProviderName: route.ProviderName, ModelName: route.ModelName}
	if route.snapshot != nil {
		identity.ProviderGroupID = route.snapshot.ProviderGroupID
	}
	var window *int64
	if state, _ := body[chatModelFallbackBodyKey].(*chatFallbackState); state != nil {
		if candidate := state.candidate(identity.CandidateID); candidate != nil {
			identity = state.identity(identity.CandidateID)
			if candidate.MaxInputTokens != nil {
				value := *candidate.MaxInputTokens
				window = &value
			}
		}
	}
	return &subagent.InheritedModelOrigin{Identity: identity, MaxInputTokens: window}
}
