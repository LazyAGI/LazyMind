package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

const (
	chatModelModeFixed            = "fixed"
	chatModelModeAuto             = "auto"
	maxConversationModelBodyBytes = 16 << 10

	chatModelAvailabilityAvailable   = "available"
	chatModelAvailabilityUnavailable = "unavailable"

	chatModelRouteBodyKey      = "_chat_model_route"
	chatModelRetryRouteBodyKey = "_chat_model_retry_route"
	chatModelRouteStrategy     = "session_sticky_v1"
)

var (
	errInvalidChatModelSelection = errors.New("invalid chat model selection")
	errChatModelUnavailable      = errors.New("model config unavailable")
	errChatModelVersionConflict  = errors.New("conversation model selection changed")
	errChatModelBusy             = errors.New("conversation is busy")
)

type initialChatModelSelection struct {
	Mode    string `json:"mode"`
	ModelID string `json:"model_id,omitempty"`
	Source  string `json:"source,omitempty"`
}

type patchConversationModelRequest struct {
	Mode            string `json:"mode"`
	ModelID         string `json:"model_id,omitempty"`
	Source          string `json:"source,omitempty"`
	ExpectedVersion *int64 `json:"expected_version"`
}

type chatModelSnapshot struct {
	ModelID         string `json:"model_id"`
	ProviderID      string `json:"provider_id"`
	ProviderName    string `json:"provider_name"`
	ProviderGroupID string `json:"provider_group_id"`
	GroupName       string `json:"group_name"`
	ModelName       string `json:"model_name"`
	Source          string `json:"source"`
	SuccessfulRunID string `json:"successful_run_id,omitempty"`
}

type availableChatModel = modelconfig.AvailableChatModel

type chatModelRoute struct {
	Mode             string `json:"mode"`
	Strategy         string `json:"strategy"`
	TaskClass        string `json:"task_class"`
	Reason           string `json:"reason"`
	ModelID          string `json:"model_id"`
	ProviderID       string `json:"provider_id"`
	ProviderName     string `json:"provider_name"`
	ModelName        string `json:"model_name"`
	Source           string `json:"source"`
	SelectionVersion *int64 `json:"selection_version,omitempty"`
	snapshot         *chatModelSnapshot
}

type chatModelSelectionResponse struct {
	Mode         string `json:"mode"`
	ModelID      string `json:"model_id,omitempty"`
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	GroupID      string `json:"group_id,omitempty"`
	GroupName    string `json:"group_name,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	Source       string `json:"source,omitempty"`
	Version      int64  `json:"version"`
	Availability string `json:"availability"`
}

type chatModelListItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	GroupID      string   `json:"group_id"`
	GroupName    string   `json:"group_name"`
	Source       string   `json:"source"`
	Capabilities []string `json:"capabilities"`
	Badges       []string `json:"badges"`
	Availability string   `json:"availability"`
	Lifecycle    string   `json:"lifecycle"`
	Current      bool     `json:"current"`
	Default      bool     `json:"default"`
	Shared       bool     `json:"shared"`
}

type chatModelProviderItem struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Source string              `json:"source"`
	Models []chatModelListItem `json:"models"`
}

type chatModelsResponse struct {
	Selection           chatModelSelectionResponse `json:"selection"`
	DefaultSelection    chatModelSelectionResponse `json:"default_selection"`
	Providers           []chatModelProviderItem    `json:"providers"`
	SwitchAllowed       bool                       `json:"switch_allowed"`
	SwitchBlockedReason string                     `json:"switch_blocked_reason,omitempty"`
	AutoAvailable       bool                       `json:"auto_available"`
}

type resolvedChatModelBinding struct {
	Mode     string
	ModelID  *string
	Source   *string
	Snapshot json.RawMessage
	Version  int64
}

func parseInitialChatModelSelection(raw map[string]any) (*initialChatModelSelection, error) {
	value, present := raw["initial_model_selection"]
	if !present || value == nil {
		return nil, nil
	}
	selection, ok := value.(map[string]any)
	if !ok {
		return nil, errInvalidChatModelSelection
	}
	mode, ok := selection["mode"].(string)
	if !ok {
		return nil, errInvalidChatModelSelection
	}
	out := &initialChatModelSelection{
		Mode: strings.ToLower(strings.TrimSpace(mode)),
	}
	if modelID, ok := selection["model_id"].(string); ok {
		out.ModelID = strings.TrimSpace(modelID)
	} else if _, present := selection["model_id"]; present {
		return nil, errInvalidChatModelSelection
	}
	if source, ok := selection["source"].(string); ok {
		out.Source = strings.ToLower(strings.TrimSpace(source))
	} else if _, present := selection["source"]; present {
		return nil, errInvalidChatModelSelection
	}
	if !validChatModelSelection(out.Mode, out.ModelID) {
		return nil, errInvalidChatModelSelection
	}
	if out.Mode == chatModelModeAuto && out.Source != "" ||
		out.Mode == chatModelModeFixed && out.Source != "" && out.Source != "own" && out.Source != "shared" && out.Source != "cloud" {
		return nil, errInvalidChatModelSelection
	}
	return out, nil
}

func validChatModelSelection(mode, modelID string) bool {
	switch mode {
	case chatModelModeFixed:
		return strings.TrimSpace(modelID) != ""
	case chatModelModeAuto:
		return strings.TrimSpace(modelID) == ""
	default:
		return false
	}
}

func loadAvailableChatModels(ctx context.Context, db *gorm.DB, userID string) ([]availableChatModel, error) {
	return modelconfig.LoadAvailableChatModels(ctx, db, userID)
}

func findAvailableChatModelBySource(models []availableChatModel, modelID, source string) *availableChatModel {
	modelID = strings.TrimSpace(modelID)
	source = strings.ToLower(strings.TrimSpace(source))
	for index := range models {
		if models[index].ID != modelID {
			continue
		}
		if source == "" && models[index].Source != "cloud" || models[index].Source == source {
			return &models[index]
		}
	}
	return nil
}

func chatModelUsable(model *availableChatModel) bool {
	return model != nil && (model.Availability == "" || model.Availability == chatModelAvailabilityAvailable || model.Availability == "degraded")
}

func chatModelSelectable(model *availableChatModel) bool {
	return chatModelUsable(model) && (model.Lifecycle == "" || model.Lifecycle == "active")
}

func resolveOwnDefaultChatModel(ctx context.Context, db *gorm.DB, userID string, models []availableChatModel) (*availableChatModel, error) {
	return modelconfig.ResolveOwnDefaultChatModel(ctx, db, userID, models)
}
func resolveDefaultChatModel(ctx context.Context, db *gorm.DB, userID string, models []availableChatModel) (*availableChatModel, error) {
	return modelconfig.ResolveDefaultChatModel(ctx, db, userID, models)
}

type chatModelHistory struct {
	RunID       string
	RunTerminal json.RawMessage
	Ext         json.RawMessage
}

func loadConversationChatModelHistory(ctx context.Context, db *gorm.DB, conversationID string) ([]chatModelHistory, error) {
	var histories []chatModelHistory
	err := db.WithContext(ctx).Raw(`
		SELECT run_id, run_terminal, ext FROM (
			SELECT run_id, run_terminal, ext, update_time FROM chat_histories
			WHERE conversation_id = ? AND run_status IN ('completed', 'failed', 'interrupted', 'cancelled')
				AND (algorithm_id IS NULL OR algorithm_id NOT LIKE 'external:%')
			UNION ALL
			SELECT run_id, run_terminal, ext, update_time FROM multi_answers_chat_histories
			WHERE conversation_id = ? AND run_status IN ('completed', 'failed', 'interrupted', 'cancelled')
		) AS outcomes ORDER BY update_time DESC, run_id DESC`, conversationID, conversationID).Scan(&histories).Error
	return histories, err
}

func successfulChatModelSnapshot(conversation *orm.Conversation, histories []chatModelHistory) *chatModelSnapshot {
	var snapshot chatModelSnapshot
	if json.Unmarshal(conversation.ChatModelSnapshot, &snapshot) == nil && snapshot.SuccessfulRunID != "" {
		return &snapshot
	}
	// Older snapshots were written before the model ran. Recover the successful
	// model from completed history, excluding responses that never invoked it.
	for _, history := range histories {
		terminal, err := parseRunTerminal(history.RunTerminal)
		route := chatModelRouteFromHistoryExt(history.Ext)
		if err != nil || !chatModelWasInvoked(terminal, route, modelSwitchesFromExt(history.Ext)) || terminal.Status != "completed" || route == nil || route.ModelID == "" {
			continue
		}
		if !terminal.modelWasInvoked() && (route.SelectionVersion == nil || *route.SelectionVersion != conversation.ChatModelVersion) {
			continue
		}
		if snapshot.ModelID != route.ModelID || snapshot.Source != route.Source {
			snapshot = chatModelSnapshot{}
		}
		snapshot.ModelID = route.ModelID
		snapshot.ProviderID = route.ProviderID
		snapshot.ProviderName = route.ProviderName
		snapshot.ModelName = route.ModelName
		snapshot.Source = route.Source
		snapshot.SuccessfulRunID = history.RunID
		return &snapshot
	}
	return nil
}

// lastSuccessfulChatModelSnapshot never treats the selected or attempted model
// as a successful call and never changes conversation state.
func lastSuccessfulChatModelSnapshot(ctx context.Context, db *gorm.DB, conversation *orm.Conversation) (*chatModelSnapshot, error) {
	if conversation == nil {
		return nil, nil
	}
	if snapshot := successfulChatModelSnapshot(conversation, nil); snapshot != nil {
		return snapshot, nil
	}
	histories, err := loadConversationChatModelHistory(ctx, db, conversation.ID)
	if err != nil {
		return nil, err
	}
	return successfulChatModelSnapshot(conversation, histories), nil
}

func chatModelRouteMatchesSelection(route *chatModelRoute, conversation *orm.Conversation) bool {
	if route == nil || route.Mode != chatModelModeAuto {
		return false
	}
	if route.SelectionVersion != nil {
		return *route.SelectionVersion == conversation.ChatModelVersion
	}
	// Unversioned routes belong to the legacy binding. A newly saved or
	// successful binding after an explicit reset must not reuse those failures.
	return conversation.ChatModelVersion <= 1 ||
		(len(conversation.ChatModelSnapshot) > 0 && successfulChatModelSnapshot(conversation, nil) == nil)
}

func chatModelIdentityKey(source, modelID string) string {
	if source == "cloud" {
		return source + "\x00" + modelID
	}
	return modelID
}

func unavailableAutoChatModels(conversation *orm.Conversation, histories []chatModelHistory) map[string]bool {
	unavailable := map[string]bool{}
	seen := map[string]bool{}
	for _, history := range histories {
		route := chatModelRouteFromHistoryExt(history.Ext)
		if route == nil || !chatModelRouteMatchesSelection(route, conversation) || route.ModelID == "" {
			continue
		}
		key := chatModelIdentityKey(route.Source, route.ModelID)
		if seen[key] {
			continue
		}
		terminal, err := parseRunTerminal(history.RunTerminal)
		if err != nil || !chatModelWasInvoked(terminal, route, modelSwitchesFromExt(history.Ext)) {
			continue
		}
		if terminal.Status == "completed" {
			seen[key] = true
			continue
		}
		if terminal.Reason != "model_failure" {
			continue
		}
		seen[key] = true
		if validModelFailureCodes[terminal.Code] {
			unavailable[key] = true
		}
	}
	return unavailable
}

// Auto selects by availability only. The algorithm's final context budget and
// compression remain authoritative; catalog capacities are not a fit guarantee.
func initialAutoChatModel(models []availableChatModel, defaultModel *availableChatModel) *availableChatModel {
	hasLocal := false
	for index := range models {
		if models[index].Source != "cloud" && chatModelUsable(&models[index]) {
			hasLocal = true
			break
		}
	}
	if chatModelUsable(defaultModel) && (!hasLocal || defaultModel.Source != "cloud") {
		return defaultModel
	}
	for index := range models {
		if chatModelUsable(&models[index]) && models[index].Source == "own" {
			return &models[index]
		}
	}
	for index := range models {
		if chatModelSelectable(&models[index]) && (hasLocal && models[index].Source != "cloud" || !hasLocal && models[index].Source == "cloud") {
			return &models[index]
		}
	}
	return nil
}

func fixedChatModelRoute(model *availableChatModel) *chatModelRoute {
	if model == nil {
		return nil
	}
	return &chatModelRoute{
		Mode: chatModelModeFixed, Strategy: "fixed", TaskClass: "fixed", Reason: "fixed",
		ModelID: model.ID, ProviderID: model.ProviderID, ProviderName: model.ProviderName,
		ModelName: model.ModelName, Source: model.Source,
	}
}

func chatModelRouteFromBody(body map[string]any) *chatModelRoute {
	if body == nil {
		return nil
	}
	switch route := body[chatModelRouteBodyKey].(type) {
	case *chatModelRoute:
		return route
	case chatModelRoute:
		copy := route
		return &copy
	default:
		return nil
	}
}

func chatModelRouteFromHistoryExt(raw json.RawMessage) *chatModelRoute {
	if len(raw) == 0 {
		return nil
	}
	var ext struct {
		ModelRoute *chatModelRoute `json:"model_route"`
	}
	if json.Unmarshal(raw, &ext) != nil {
		return nil
	}
	return ext.ModelRoute
}

func mergeChatModelRouteIntoExt(raw json.RawMessage, body map[string]any) json.RawMessage {
	route := chatModelRouteFromBody(body)
	if route == nil {
		return raw
	}
	ext := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &ext)
	}
	ext["model_route"] = route
	return marshalChatHistoryExt(ext)
}

func chatModelSnapshotForModel(model *availableChatModel) *chatModelSnapshot {
	if model == nil {
		return nil
	}
	return &chatModelSnapshot{
		ModelID: model.ID, ProviderID: model.ProviderID, ProviderName: model.ProviderName,
		ProviderGroupID: model.ProviderGroupID, GroupName: model.GroupName,
		ModelName: model.ModelName, Source: model.Source,
	}
}

func snapshotForChatModel(model *availableChatModel) (json.RawMessage, error) {
	if model == nil {
		return nil, nil
	}
	return json.Marshal(chatModelSnapshotForModel(model))
}

func resolveInitialChatModelBinding(ctx context.Context, db *gorm.DB, userID string, requested *initialChatModelSelection) (*resolvedChatModelBinding, error) {
	models, err := loadAvailableChatModels(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	defaultModel, err := resolveDefaultChatModel(ctx, db, userID, models)
	if err != nil {
		return nil, err
	}
	if requested == nil {
		if defaultModel == nil {
			return nil, nil
		}
		requested = &initialChatModelSelection{Mode: chatModelModeFixed, ModelID: defaultModel.ID, Source: defaultModel.Source}
	}

	if requested.Mode == chatModelModeAuto {
		return &resolvedChatModelBinding{Mode: chatModelModeAuto, Version: 1}, nil
	}
	model := findAvailableChatModelBySource(models, requested.ModelID, requested.Source)
	if model == nil {
		modelID := requested.ModelID
		source := requested.Source
		return &resolvedChatModelBinding{
			Mode: chatModelModeFixed, ModelID: &modelID, Source: &source, Version: 1,
		}, nil
	}
	snapshot, err := snapshotForChatModel(model)
	if err != nil {
		return nil, err
	}
	modelID := model.ID
	source := model.Source
	return &resolvedChatModelBinding{
		Mode: chatModelModeFixed, ModelID: &modelID, Source: &source, Snapshot: snapshot, Version: 1,
	}, nil
}

func applyResolvedChatModelBinding(conversation *orm.Conversation, binding *resolvedChatModelBinding) {
	if conversation == nil || binding == nil {
		return
	}
	mode := binding.Mode
	conversation.ChatModelMode = &mode
	conversation.ChatModelID = binding.ModelID
	conversation.ChatModelSource = binding.Source
	conversation.ChatModelSnapshot = binding.Snapshot
	conversation.ChatModelVersion = binding.Version
}

func selectionFromModel(mode string, model *availableChatModel, version int64) chatModelSelectionResponse {
	selection := chatModelSelectionResponse{
		Mode: mode, Version: version, Availability: chatModelAvailabilityUnavailable,
	}
	if mode == chatModelModeAuto {
		selection.Availability = chatModelAvailabilityAvailable
		return selection
	}
	if model == nil {
		return selection
	}
	selection.ModelID = model.ID
	selection.ProviderID = model.ProviderID
	selection.ProviderName = model.ProviderName
	selection.GroupID = model.ProviderGroupID
	selection.GroupName = model.GroupName
	selection.ModelName = model.ModelName
	selection.Source = model.Source
	if chatModelUsable(model) {
		selection.Availability = chatModelAvailabilityAvailable
	}
	return selection
}

func selectionFromSnapshot(conversation *orm.Conversation) chatModelSelectionResponse {
	mode := chatModelModeFixed
	if conversation != nil && conversation.ChatModelMode != nil && strings.TrimSpace(*conversation.ChatModelMode) != "" {
		mode = strings.ToLower(strings.TrimSpace(*conversation.ChatModelMode))
	}
	selection := chatModelSelectionResponse{
		Mode: mode, Availability: chatModelAvailabilityUnavailable,
	}
	if conversation == nil {
		return selection
	}
	selection.Version = conversation.ChatModelVersion
	if conversation.ChatModelID != nil {
		selection.ModelID = strings.TrimSpace(*conversation.ChatModelID)
	}
	if conversation.ChatModelSource != nil {
		selection.Source = strings.ToLower(strings.TrimSpace(*conversation.ChatModelSource))
	}
	var snapshot chatModelSnapshot
	if len(conversation.ChatModelSnapshot) > 0 && json.Unmarshal(conversation.ChatModelSnapshot, &snapshot) == nil {
		if selection.ModelID == "" {
			selection.ModelID = snapshot.ModelID
		}
		selection.ProviderID = snapshot.ProviderID
		selection.ProviderName = snapshot.ProviderName
		selection.GroupID = snapshot.ProviderGroupID
		selection.GroupName = snapshot.GroupName
		selection.ModelName = snapshot.ModelName
		selection.Source = snapshot.Source
	}
	if mode == chatModelModeAuto {
		selection.ModelID = ""
		selection.Availability = chatModelAvailabilityAvailable
	}
	return selection
}

func conversationChatModelSource(conversation *orm.Conversation) string {
	if conversation == nil {
		return ""
	}
	if conversation.ChatModelSource != nil && strings.TrimSpace(*conversation.ChatModelSource) != "" {
		return strings.ToLower(strings.TrimSpace(*conversation.ChatModelSource))
	}
	var snapshot chatModelSnapshot
	if len(conversation.ChatModelSnapshot) > 0 && json.Unmarshal(conversation.ChatModelSnapshot, &snapshot) == nil {
		return strings.ToLower(strings.TrimSpace(snapshot.Source))
	}
	return ""
}

func resolvedSelectionForConversation(conversation *orm.Conversation, models []availableChatModel, defaultModel *availableChatModel) chatModelSelectionResponse {
	if conversation == nil || conversation.ChatModelMode == nil || strings.TrimSpace(*conversation.ChatModelMode) == "" {
		return selectionFromModel(chatModelModeFixed, defaultModel, 0)
	}
	mode := strings.ToLower(strings.TrimSpace(*conversation.ChatModelMode))
	if mode == chatModelModeAuto {
		selection := selectionFromSnapshot(conversation)
		selection.Mode = chatModelModeAuto
		selection.Version = conversation.ChatModelVersion
		selection.Availability = chatModelAvailabilityUnavailable
		for index := range models {
			if chatModelUsable(&models[index]) {
				selection.Availability = chatModelAvailabilityAvailable
				break
			}
		}
		return selection
	}
	if conversation.ChatModelID != nil {
		source := conversationChatModelSource(conversation)
		if model := findAvailableChatModelBySource(models, strings.TrimSpace(*conversation.ChatModelID), source); model != nil {
			return selectionFromModel(chatModelModeFixed, model, conversation.ChatModelVersion)
		}
	}
	return selectionFromSnapshot(conversation)
}

func conversationModelSwitchBlock(ctx context.Context, db *gorm.DB, userID, conversationID string) (string, error) {
	var externalRuns int64
	if err := db.WithContext(ctx).Model(&orm.ExternalChatRun{}).
		Where("actor_user_id = ? AND conversation_id = ? AND status IN ?", userID, conversationID, []string{"pending", "running"}).
		Count(&externalRuns).Error; err != nil {
		return "", err
	}
	if externalRuns > 0 {
		return "generating", nil
	}
	if stateStore := store.State(); stateStore != nil {
		locked, err := stateStore.Exists(ctx, sidechatRequestLockKey(conversationID))
		if err != nil {
			return "", err
		}
		if locked {
			return "generating", nil
		}
		ids, err := reconcileGeneratingExternalChatStatuses(ctx, db, stateStore, userID, conversationID)
		if err != nil {
			return "", err
		}
		if len(ids) > 0 {
			return "generating", nil
		}
	}

	var activeWorkflows int64
	if err := db.WithContext(ctx).Model(&orm.WorkflowSession{}).
		Where("conversation_id = ? AND dismissed = ? AND status = ?", conversationID, false, workflow.SessionStatusActive).
		Count(&activeWorkflows).Error; err != nil {
		return "", err
	}
	if activeWorkflows > 0 {
		return "workflow_running", nil
	}

	var activeTasks int64
	if err := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where(
			"user_id = ? AND conversation_id = ? AND status IN ? AND task_type IN ?",
			userID, conversationID, []string{"pending", "running"}, []string{"background_chat", "workflow_run"},
		).
		Count(&activeTasks).Error; err != nil {
		return "", err
	}
	if activeTasks > 0 {
		return "background_task_running", nil
	}
	return "", nil
}

func buildChatModelsResponse(ctx context.Context, db *gorm.DB, userID string, conversation *orm.Conversation) (chatModelsResponse, error) {
	models, err := loadAvailableChatModels(ctx, db, userID)
	if err != nil {
		return chatModelsResponse{}, err
	}
	defaultModel, err := resolveDefaultChatModel(ctx, db, userID, models)
	if err != nil {
		return chatModelsResponse{}, err
	}
	selection := resolvedSelectionForConversation(conversation, models, defaultModel)
	defaultSelection := selectionFromModel(chatModelModeFixed, defaultModel, 0)

	providerByKey := make(map[string]*chatModelProviderItem)
	providerKeys := make([]string, 0)
	for index := range models {
		model := &models[index]
		key := model.ProviderID + "\x00" + model.Source
		provider := providerByKey[key]
		if provider == nil {
			provider = &chatModelProviderItem{
				ID: model.ProviderID, Name: model.ProviderName, Source: model.Source, Models: []chatModelListItem{},
			}
			providerByKey[key] = provider
			providerKeys = append(providerKeys, key)
		}
		current := selection.Mode == chatModelModeFixed && selection.ModelID == model.ID && selection.Source == model.Source
		isDefault := defaultModel != nil && defaultModel.ID == model.ID && defaultModel.Source == model.Source
		badges := make([]string, 0, 3)
		if current {
			badges = append(badges, "current")
		}
		if isDefault {
			badges = append(badges, "default")
		}
		if model.Source == "shared" {
			badges = append(badges, "shared")
		}
		if model.Source == "cloud" {
			badges = append(badges, "cloud")
		}
		if model.Availability == "degraded" {
			badges = append(badges, "degraded")
		}
		provider.Models = append(provider.Models, chatModelListItem{
			ID: model.ID, Name: model.ModelName, GroupID: model.ProviderGroupID, GroupName: model.GroupName,
			Source: model.Source, Capabilities: append([]string(nil), model.Capabilities...), Badges: badges,
			Availability: model.Availability, Lifecycle: model.Lifecycle,
			Current: current, Default: isDefault, Shared: model.Source == "shared",
		})
	}
	sort.Slice(providerKeys, func(i, j int) bool {
		left, right := providerByKey[providerKeys[i]], providerByKey[providerKeys[j]]
		if left.Name == right.Name {
			if left.Source == right.Source {
				return left.ID < right.ID
			}
			return left.Source < right.Source
		}
		return left.Name < right.Name
	})
	providers := make([]chatModelProviderItem, 0, len(providerKeys))
	for _, key := range providerKeys {
		providers = append(providers, *providerByKey[key])
	}

	autoAvailable := initialAutoChatModel(models, defaultModel) != nil
	response := chatModelsResponse{
		Selection: selection, DefaultSelection: defaultSelection, Providers: providers,
		SwitchAllowed: true, AutoAvailable: autoAvailable,
	}
	if conversation != nil {
		reason, err := conversationModelSwitchBlock(ctx, db, userID, conversation.ID)
		if err != nil {
			return chatModelsResponse{}, err
		}
		if reason != "" {
			response.SwitchAllowed = false
			response.SwitchBlockedReason = reason
		}
	}
	return response, nil
}

// ListChatModels returns the current user's usable chat LLMs without exposing credentials.
func ListChatModels(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var conversation *orm.Conversation
	if conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id")); conversationID != "" {
		if len(conversationID) > maxConversationIDLength {
			common.ReplyErr(w, "conversation_id too long", http.StatusBadRequest)
			return
		}
		var row orm.Conversation
		if err := db.WithContext(r.Context()).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				common.ReplyErr(w, "conversation not found", http.StatusNotFound)
				return
			}
			common.ReplyErr(w, "load model configs", http.StatusInternalServerError)
			return
		}
		conversation = &row
	}

	response, err := buildChatModelsResponse(r.Context(), db, userID, conversation)
	if err != nil {
		common.ReplyErr(w, "load model configs", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, response)
}

// PatchConversationModel updates a conversation-scoped LLM binding with optimistic locking.
func PatchConversationModel(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	conversationID := conversationIDFromName(mux.Vars(r)["conversation_id"])
	if conversationID == "" || len(conversationID) > maxConversationIDLength {
		common.ReplyErr(w, "invalid chat model selection", http.StatusBadRequest)
		return
	}

	var request patchConversationModelRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConversationModelBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.Source = strings.ToLower(strings.TrimSpace(request.Source))
	if request.ExpectedVersion == nil || *request.ExpectedVersion < 0 || !validChatModelSelection(request.Mode, request.ModelID) ||
		request.Mode == chatModelModeAuto && request.Source != "" ||
		request.Mode == chatModelModeFixed && request.Source != "" && request.Source != "own" && request.Source != "shared" && request.Source != "cloud" {
		common.ReplyErr(w, "invalid chat model selection", http.StatusBadRequest)
		return
	}

	var updated orm.Conversation
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).
			Take(&conversation).Error; err != nil {
			return err
		}
		if conversation.ChatModelVersion != *request.ExpectedVersion {
			return errChatModelVersionConflict
		}
		if reason, err := conversationModelSwitchBlock(r.Context(), tx, userID, conversationID); err != nil {
			return err
		} else if reason != "" {
			return errChatModelBusy
		}

		models, err := loadAvailableChatModels(r.Context(), tx, userID)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"chat_model_mode":     request.Mode,
			"chat_model_id":       nil,
			"chat_model_source":   nil,
			"chat_model_snapshot": nil,
			"chat_model_version":  gorm.Expr("chat_model_version + ?", 1),
			"updated_at":          time.Now().UTC(),
		}
		if request.Mode == chatModelModeAuto {
			defaultModel, err := resolveDefaultChatModel(r.Context(), tx, userID, models)
			if err != nil {
				return err
			}
			if initialAutoChatModel(models, defaultModel) == nil {
				return errChatModelUnavailable
			}
		} else {
			model := findAvailableChatModelBySource(models, request.ModelID, request.Source)
			if !chatModelSelectable(model) {
				return errChatModelUnavailable
			}
			snapshot, err := snapshotForChatModel(model)
			if err != nil {
				return err
			}
			updates["chat_model_id"] = model.ID
			updates["chat_model_source"] = model.Source
			updates["chat_model_snapshot"] = snapshot
		}

		result := tx.Model(&orm.Conversation{}).
			Where(
				"id = ? AND create_user_id = ? AND deleted_at IS NULL AND chat_model_version = ?",
				conversationID, userID, *request.ExpectedVersion,
			).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errChatModelVersionConflict
		}
		return tx.Where("id = ? AND create_user_id = ?", conversationID, userID).Take(&updated).Error
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		case errors.Is(err, errChatModelVersionConflict):
			common.ReplyErr(w, errChatModelVersionConflict.Error(), http.StatusConflict)
		case errors.Is(err, errChatModelBusy):
			common.ReplyErr(w, errChatModelBusy.Error(), http.StatusConflict)
		case errors.Is(err, errChatModelUnavailable):
			common.ReplyErr(w, errChatModelUnavailable.Error(), http.StatusServiceUnavailable)
		default:
			common.ReplyErr(w, "save conversation model failed", http.StatusInternalServerError)
		}
		return
	}

	models, err := loadAvailableChatModels(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "load model configs", http.StatusInternalServerError)
		return
	}
	defaultModel, err := resolveDefaultChatModel(r.Context(), db, userID, models)
	if err != nil {
		common.ReplyErr(w, "load model configs", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"selection": resolvedSelectionForConversation(&updated, models, defaultModel),
	})
}

func applyConversationChatModelConfig(ctx context.Context, db *gorm.DB, userID string, body map[string]any) error {
	conversationID, _ := body["conversation_id"].(string)
	conversationID = strings.TrimSpace(conversationID)
	if db == nil {
		return nil
	}
	var conversation orm.Conversation
	if conversationID != "" {
		if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, strings.TrimSpace(userID)).Take(&conversation).Error; err != nil {
			return err
		}
	}
	prepared, _ := body[chatModelPreparedBodyKey].(*modelconfig.ChatRuntimeConfig)
	if prepared == nil {
		var err error
		prepared, err = modelconfig.LoadChatModelCandidates(ctx, db, userID)
		if err != nil {
			return err
		}
	}
	clearChatFallbackInput(body)
	mode := chatModelModeFixed
	legacy := conversation.ChatModelMode == nil || strings.TrimSpace(*conversation.ChatModelMode) == ""
	if !legacy {
		mode = strings.ToLower(strings.TrimSpace(*conversation.ChatModelMode))
	}
	if legacy {
		if config, ok := body["llm_config"].(map[string]any); ok && config["llm"] != nil {
			return nil
		}
	}
	if mode != chatModelModeAuto && mode != chatModelModeFixed {
		return errChatModelUnavailable
	}
	models := prepared.Models
	var model *availableChatModel
	var route *chatModelRoute
	var unavailableSticky *chatModelSnapshot
	if legacy {
		var err error
		model, err = resolveDefaultChatModel(ctx, db, userID, models)
		if err != nil {
			return err
		}
		route = fixedChatModelRoute(model)
	} else if mode == chatModelModeAuto {
		reason := "initial_selection"
		retryRoute, _ := body[chatModelRetryRouteBodyKey].(*chatModelRoute)
		if chatModelRouteMatchesSelection(retryRoute, &conversation) {
			model = findAvailableChatModelBySource(models, retryRoute.ModelID, retryRoute.Source)
			reason = "retry_same_model"
			if model == nil {
				copy := *retryRoute
				route = &copy
			}
		} else {
			histories, historyErr := loadConversationChatModelHistory(ctx, db, conversation.ID)
			if historyErr != nil {
				return historyErr
			}
			unavailable := unavailableAutoChatModels(&conversation, histories)
			usable := make([]availableChatModel, 0, len(models))
			for _, candidate := range models {
				key := chatModelIdentityKey(candidate.Source, candidate.ID)
				if chatModelUsable(&candidate) && !unavailable[key] {
					usable = append(usable, candidate)
				}
			}
			if len(conversation.ChatModelSnapshot) > 0 {
				if previous := successfulChatModelSnapshot(&conversation, histories); previous != nil {
					model = findAvailableChatModelBySource(usable, previous.ModelID, previous.Source)
					reason = "session_sticky"
					if model == nil {
						reason = "model_unavailable"
						prior := findAvailableChatModelBySource(models, previous.ModelID, previous.Source)
						if !chatModelUsable(prior) || unavailable[chatModelIdentityKey(previous.Source, previous.ModelID)] {
							unavailableSticky = previous
						}
					}
				}
			}
			if model == nil {
				for _, history := range histories {
					previousRoute := chatModelRouteFromHistoryExt(history.Ext)
					if !chatModelRouteMatchesSelection(previousRoute, &conversation) {
						continue
					}
					terminal, err := parseRunTerminal(history.RunTerminal)
					if err != nil || !chatModelWasInvoked(terminal, previousRoute, modelSwitchesFromExt(history.Ext)) {
						continue
					}
					model = findAvailableChatModelBySource(usable, previousRoute.ModelID, previousRoute.Source)
					if model != nil {
						if reason == "initial_selection" {
							reason = "session_sticky"
						}
						break
					}
				}
			}
			if model == nil {
				if inherited := forkConfigForConversation(conversation); inherited != nil && inherited.Model != nil && conversation.ChatModelVersion == 1 {
					model = findAvailableChatModelBySource(usable, inherited.Model.ModelID, inherited.Model.Source)
					if model != nil {
						reason = "fork_selection"
					}
				}
			}
			if model == nil {
				// Restore a successful Cloud binding before applying the existing
				// local-first policy to a new selection. Catalog visibility alone
				// must not enable Cloud when local models have failed this session.
				hasLocalCandidates := false
				for index := range models {
					if models[index].Source != "cloud" && chatModelUsable(&models[index]) {
						hasLocalCandidates = true
						break
					}
				}
				if hasLocalCandidates {
					local := usable[:0]
					for _, candidate := range usable {
						if candidate.Source != "cloud" {
							local = append(local, candidate)
						}
					}
					usable = local
				}
				defaultModel, defaultErr := resolveDefaultChatModel(ctx, db, userID, usable)
				if defaultErr != nil {
					return defaultErr
				}
				model = initialAutoChatModel(usable, defaultModel)
				if len(unavailable) > 0 {
					reason = "model_unavailable"
				}
			}
		}
		if model != nil {
			route = fixedChatModelRoute(model)
			route.Mode = chatModelModeAuto
			route.Strategy = chatModelRouteStrategy
			route.TaskClass = "auto"
			route.Reason = reason
		}
	} else if conversation.ChatModelID != nil {
		source := conversationChatModelSource(&conversation)
		model = findAvailableChatModelBySource(models, strings.TrimSpace(*conversation.ChatModelID), source)
		route = fixedChatModelRoute(model)
	}
	original := model
	originalRoute := route
	if originalRoute == nil {
		originalRoute = chatModelRouteFromSnapshot(&conversation, mode)
	}
	if !legacy && mode == chatModelModeFixed && conversation.ChatModelID != nil && original == nil && originalRoute == nil {
		return errChatModelUnavailable
	}
	var current *modelconfig.ChatModelCandidate
	if model != nil {
		current = prepared.PrepareCandidate(ctx, *model)
	}
	if current == nil {
		model, current = initialFallbackChatCandidate(prepared, original)
	}
	if current == nil {
		if legacy {
			return nil
		}
		return errChatModelUnavailable
	}
	fixedLLM := current.LLMConfig["llm"]
	if route == nil {
		route = originalRoute
	}
	if route == nil {
		route = fixedChatModelRoute(model)
		route.Mode = mode
	}
	if inherited := forkConfigForConversation(conversation); inherited != nil && inherited.Model != nil && inherited.Model.ModelID == model.ID && conversation.ChatModelVersion == 1 {
		if limit, err := strconv.ParseInt(inherited.MaxInputTokens, 10, 64); err == nil && limit > 0 {
			if cfg, ok := fixedLLM.(map[string]any); ok {
				currentWindow, _ := cfg["max_input_tokens"].(string)
				currentLimit, _ := strconv.ParseInt(currentWindow, 10, 64)
				if currentLimit <= 0 || limit < currentLimit {
					cfg["max_input_tokens"] = inherited.MaxInputTokens
					current.MaxInputTokens = modelconfig.ConfiguredChatWindow(&inherited.MaxInputTokens)
				}
			}
		}
	}
	config, _ := body["llm_config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	config["llm"] = fixedLLM
	body["llm_config"] = config
	body[chatModelRouteBodyKey] = route
	route.SelectionVersion = &conversation.ChatModelVersion
	if original != nil {
		route.snapshot = chatModelSnapshotForModel(original)
	}
	if originalRoute == nil {
		originalRoute = route
	}
	state := newChatFallbackState(prepared, current.CandidateID)
	if unavailableSticky != nil && modelconfig.ChatCandidateID(unavailableSticky.Source, unavailableSticky.ModelID) != current.CandidateID {
		from := &chatModelRoute{Mode: mode, Strategy: route.Strategy, TaskClass: route.TaskClass, Reason: "model_unavailable", SelectionVersion: route.SelectionVersion, ModelID: unavailableSticky.ModelID, ProviderID: unavailableSticky.ProviderID, ProviderName: unavailableSticky.ProviderName, ModelName: unavailableSticky.ModelName, Source: unavailableSticky.Source, snapshot: unavailableSticky}
		var window *int64
		if prior := findAvailableChatModelBySource(models, from.ModelID, from.Source); prior != nil {
			window = modelconfig.ConfiguredChatWindow(prior.MaxInputTokens)
		}
		state.setInitialSwitch(from, window)
		body[chatModelRouteBodyKey] = from
	} else if originalRoute != nil && modelconfig.ChatCandidateID(originalRoute.Source, originalRoute.ModelID) != current.CandidateID {
		var window *int64
		if original != nil {
			window = modelconfig.ConfiguredChatWindow(original.MaxInputTokens)
		}
		state.setInitialSwitch(originalRoute, window)
	}
	body[chatModelFallbackBodyKey] = state
	if route != nil {
		log.Logger.Info().
			Str("conversation_id", conversation.ID).
			Str("mode", route.Mode).
			Str("strategy", route.Strategy).
			Str("task_class", route.TaskClass).
			Str("reason", route.Reason).
			Str("provider_id", route.ProviderID).
			Str("model_id", route.ModelID).
			Msg("resolved chat model")
	}
	return nil
}

func persistSuccessfulChatModel(ctx context.Context, db *gorm.DB, userID, conversationID, runID string, body map[string]any, terminal *RunTerminal) {
	route := chatModelRouteFromBody(body)
	if db == nil || route == nil || route.Mode != chatModelModeAuto || !chatModelWasInvoked(terminal, route, modelSwitchesFromBody(body)) || terminal.Status != "completed" || route.snapshot == nil || route.SelectionVersion == nil || runID == "" {
		return
	}
	snapshot := *route.snapshot
	snapshot.SuccessfulRunID = runID
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	writeCtx, cancel := terminalWriteContext(ctx)
	defer cancel()
	if err := conversationCheckpoint(writeCtx, db, conversationID, func(tx *gorm.DB) error {
		var latest orm.ChatHistory
		err := tx.Where("conversation_id = ?", conversationID).Order("seq DESC, create_time DESC, id DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && latest.RunID != runID {
			return nil
		}
		return tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND chat_model_mode = ? AND chat_model_version = ?",
				conversationID, strings.TrimSpace(userID), route.Mode, *route.SelectionVersion).
			UpdateColumn("chat_model_snapshot", raw).Error
	}); err != nil {
		log.Logger.Warn().Err(err).Str("conversation_id", conversationID).Str("run_id", runID).Msg("failed to save successful chat model")
	}
}

// LoadOrganizerChatLLMConfig reuses Main Chat selection, including a conversation override.
// Resolve credentials at execution time instead of persisting them in batch inputs.
func LoadOrganizerChatLLMConfig(ctx context.Context, db *gorm.DB, userID, conversationID string) (map[string]any, error) {
	if db == nil {
		return nil, errChatModelUnavailable
	}
	body := map[string]any{"conversation_id": conversationID}
	if err := applyConversationChatModelConfig(ctx, db, userID, body); err != nil {
		return nil, err
	}
	if config, ok := body["llm_config"].(map[string]any); ok {
		return config, nil
	}
	role, err := LoadDefaultChatLLMConfig(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"llm": role}, nil
}

// LoadDefaultChatLLMConfig returns the same llm role that chat would use when
// no conversation overlay is available: the user's current default chat model.
func LoadDefaultChatLLMConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	if db == nil {
		return nil, errChatModelUnavailable
	}
	models, err := loadAvailableChatModels(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	model, err := resolveDefaultChatModel(ctx, db, userID, models)
	if err != nil {
		return nil, err
	}
	if !chatModelUsable(model) {
		return nil, errChatModelUnavailable
	}
	fixed, err := buildChatLLMConfig(ctx, model)
	if err != nil {
		return nil, err
	}
	cfg, _ := fixed.(map[string]any)
	if cfg == nil {
		return nil, errChatModelUnavailable
	}
	return cfg, nil
}

func buildChatLLMConfig(ctx context.Context, model *availableChatModel) (any, error) {
	if model == nil {
		return nil, errChatModelUnavailable
	}
	if model.Source == "cloud" {
		resolved, available, err := modelconfig.ResolveCloudRuntimeModel(ctx, "llm", model.ID)
		if err != nil || !available {
			return nil, errChatModelUnavailable
		}
		fixed := modelconfig.BuildLLMConfig([]modelconfig.SelectedRuntimeModel{resolved})
		fixedLLM, _ := fixed["llm"]
		if fixedLLM == nil {
			return nil, errChatModelUnavailable
		}
		return fixedLLM, nil
	}
	apiKey, err := modelprovider.ResolveAPIKey(model.APIKey, model.APIKeyCiphertext)
	if err != nil {
		return nil, errChatModelUnavailable
	}
	fixed := modelconfig.BuildLLMConfig([]modelconfig.SelectedRuntimeModel{{
		ModelType: "llm", TechnicalModelType: model.ModelType, Vision: model.Vision, ProviderName: model.ProviderName, ModelName: model.ModelName,
		BaseURL: model.BaseURL, APIKey: apiKey, MaxInputTokens: model.MaxInputTokens,
	}})
	fixedLLM, _ := fixed["llm"]
	if fixedLLM == nil {
		return nil, errChatModelUnavailable
	}
	return fixedLLM, nil
}
