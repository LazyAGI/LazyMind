package chat

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/mcp"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

// ToolConfigurationAction contains references and state only, never credentials.
type ToolConfigurationAction struct {
	ID               string    `json:"id" gorm:"primaryKey"`
	UserID           string    `json:"-"`
	ConversationID   string    `json:"conversation_id"`
	HistoryID        string    `json:"history_id"`
	RunID            string    `json:"-"`
	Service          string    `json:"service"`
	Label            string    `json:"label"`
	Status           string    `json:"status"`
	Revision         string    `json:"-"`
	Version          int64     `json:"version"`
	DeliveredVersion int64     `json:"-"`
	RequestID        string    `json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

func (ToolConfigurationAction) TableName() string { return "tool_configuration_actions" }

type ToolConfigurationListResponse struct {
	Actions []ToolConfigurationAction `json:"actions"`
}

type ToolConfigurationRequest struct {
	Operation string   `json:"operation"`
	HistoryID string   `json:"history_id"`
	RunID     string   `json:"run_id"`
	Service   string   `json:"service"`
	ActionIDs []string `json:"action_ids"`
	ActionID  string   `json:"action_id"`
	Version   int64    `json:"version"`
	RequestID string   `json:"request_id"`
}

type toolConfigurationSnapshot struct {
	Label  string
	Status string
	Config map[string]any
	MCP    *mcp.RuntimeConfig
}

func (snapshot toolConfigurationSnapshot) revision() string {
	encoded, _ := json.Marshal(struct {
		Status string
		Config map[string]any
		MCP    *mcp.RuntimeConfig
	}{
		snapshot.Status, snapshot.Config, snapshot.MCP,
	})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

var configurationLabels = map[string]string{
	"mail": "邮箱", "googledrive": "Google Drive", "feishu": "飞书", "notion": "Notion",
	"web_search": "网页搜索", "academic_search": "学术搜索",
}

func probeToolConfiguration(ctx context.Context, db *gorm.DB, userID, service string) (toolConfigurationSnapshot, error) {
	disabled, err := listDisabledToolNames(ctx, db, userID)
	if err != nil {
		return toolConfigurationSnapshot{}, err
	}
	baseService, target, targeted := strings.Cut(service, "/")
	if targeted && (baseService == "web_search" || baseService == "academic_search" || baseService == "mail") {
		service = baseService
	}
	group := service
	if modelconfig.IsCloudToolProvider(service) {
		group = "cloud_files"
	}
	for _, name := range disabled {
		if name == group {
			return toolConfigurationSnapshot{Status: "forbidden"}, nil
		}
	}
	if strings.HasPrefix(service, "mcp:") {
		item, err := mcp.LoadCapability(ctx, db, userID, service)
		if err != nil {
			return toolConfigurationSnapshot{}, err
		}
		if item != nil {
			return toolConfigurationSnapshot{Label: item.Label, Status: item.Status, MCP: item.Runtime}, nil
		}
		return toolConfigurationSnapshot{Status: "forbidden"}, nil
	}
	if service != "mail" && service != "web_search" && service != "academic_search" && !modelconfig.IsCloudToolProvider(service) {
		return toolConfigurationSnapshot{}, errors.New("unknown service")
	}
	label := configurationLabels[service]
	if label == "" {
		label = service
	}
	var config map[string]any
	requiresReauthorization := false
	if service == "web_search" {
		config, err = searchToolConfigEntry(ctx, db, userID)
	} else if service == "academic_search" {
		config, err = academicSearchToolConfigEntry(ctx, db, userID)
	} else {
		var tokens []string
		if service == "mail" {
			tokens, err = modelconfig.LoadMailToolConfig(ctx, userID)
			valid := make([]string, 0, len(tokens))
			for _, raw := range tokens {
				var account map[string]string
				if json.Unmarshal([]byte(raw), &account) != nil {
					continue
				}
				switch strings.ToUpper(account["status"]) {
				case "REVOKED", "DISCONNECTED", "EXPIRED", "ERROR":
					requiresReauthorization = true
				default:
					valid = append(valid, raw)
				}
			}
			tokens = valid
		} else {
			tokens, err = modelconfig.LoadCloudProviderTokens(ctx, service, userID)
		}
		config = map[string]any{service: normalizeToolConfigValue(tokens)}
	}
	if err != nil {
		if service == "web_search" || service == "academic_search" {
			return toolConfigurationSnapshot{}, err
		}
		return toolConfigurationSnapshot{Label: label, Status: "unavailable"}, nil
	}
	originalConfig := config
	if targeted {
		if service == "mail" {
			selected := []string{}
			var credentials []string
			switch values := config["mail"].(type) {
			case string:
				credentials = []string{values}
			case []string:
				credentials = values
			}
			for _, raw := range credentials {
				var account map[string]any
				if json.Unmarshal([]byte(raw), &account) != nil {
					continue
				}
				for _, field := range []string{"email", "connection_id", "provider"} {
					value, _ := account[field].(string)
					if strings.EqualFold(strings.TrimSpace(value), target) {
						selected = append(selected, raw)
						break
					}
				}
			}
			config = map[string]any{"mail": selected}
		} else {
			config = map[string]any{target: config[target]}
		}
		label += " (" + target + ")"
	}
	ready := false
	for _, value := range config {
		if normalizeToolConfigValue(value) != nil {
			ready = true
		}
	}
	status := "needs_configuration"
	if requiresReauthorization {
		status = "needs_authorization"
	}
	if ready {
		status = "ready"
	}
	if service == "mail" {
		config = originalConfig
	}
	return toolConfigurationSnapshot{Label: label, Status: status, Config: config}, nil
}

func validateConfigurationOwner(ctx context.Context, db *gorm.DB, userID, conversationID string) error {
	if userID == "" || conversationID == "" {
		return gorm.ErrRecordNotFound
	}
	var count int64
	err := db.WithContext(ctx).Model(&orm.Conversation{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", conversationID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func prepareToolConfiguration(ctx context.Context, db *gorm.DB, userID, conversationID string, req ToolConfigurationRequest) (ToolConfigurationAction, error) {
	if err := validateConfigurationOwner(ctx, db, userID, conversationID); err != nil {
		return ToolConfigurationAction{}, err
	}
	if req.RunID == "" || req.HistoryID == "" || len(req.Service) > 255 {
		return ToolConfigurationAction{}, gorm.ErrRecordNotFound
	}
	var active int64
	scope := "id = ? AND conversation_id = ? AND run_id = ? AND run_status = ?"
	if err := db.WithContext(ctx).Model(&orm.ChatHistory{}).Where(scope, req.HistoryID, conversationID, req.RunID, "generating").Count(&active).Error; err != nil {
		return ToolConfigurationAction{}, err
	}
	if active == 0 {
		if err := db.WithContext(ctx).Model(&orm.MultiAnswersChatHistory{}).Where(scope, req.HistoryID, conversationID, req.RunID, "generating").Count(&active).Error; err != nil {
			return ToolConfigurationAction{}, err
		}
	}
	if active == 0 {
		return ToolConfigurationAction{}, gorm.ErrRecordNotFound
	}
	snapshot, err := probeToolConfiguration(ctx, db, userID, req.Service)
	if err != nil {
		return ToolConfigurationAction{}, err
	}
	// The key is task-scoped; repeated calls cannot create repeated configuration cards.
	encoded, _ := json.Marshal([]string{userID, conversationID, req.HistoryID, req.RunID, req.Service})
	hash := sha256.Sum256(encoded)
	action := ToolConfigurationAction{ID: hex.EncodeToString(hash[:]), UserID: userID, ConversationID: conversationID,
		HistoryID: req.HistoryID, RunID: req.RunID, Service: req.Service, Label: snapshot.Label,
		Status: snapshot.Status, Revision: snapshot.revision(), Version: 1}
	err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&action).Error
	if err != nil {
		return action, err
	}
	err = db.WithContext(ctx).Where("id = ? AND user_id = ?", action.ID, userID).First(&action).Error
	return action, err
}

func refreshToolConfiguration(ctx context.Context, db *gorm.DB, action *ToolConfigurationAction) (toolConfigurationSnapshot, error) {
	snapshot, err := probeToolConfiguration(ctx, db, action.UserID, action.Service)
	if err != nil {
		return snapshot, err
	}
	return snapshot, applyToolConfigurationSnapshot(ctx, db, action, snapshot)
}

func applyToolConfigurationSnapshot(ctx context.Context, db *gorm.DB, action *ToolConfigurationAction, snapshot toolConfigurationSnapshot) error {
	revision := snapshot.revision()
	if action.Status != snapshot.Status || action.Revision != revision {
		result := db.WithContext(ctx).Model(&ToolConfigurationAction{}).
			Where("id = ? AND user_id = ? AND version = ?", action.ID, action.UserID, action.Version).
			Updates(map[string]any{"status": snapshot.Status, "revision": revision, "version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("configuration changed concurrently; retry")
		}
		action.Status, action.Revision, action.Version = snapshot.Status, revision, action.Version+1
	}
	return nil
}

func refreshToolConfigurations(ctx context.Context, db *gorm.DB, actions []ToolConfigurationAction) (map[string]toolConfigurationSnapshot, error) {
	snapshots := map[string]toolConfigurationSnapshot{}
	for i := range actions {
		action := &actions[i]
		snapshot, ok := snapshots[action.Service]
		if !ok {
			var err error
			snapshot, err = probeToolConfiguration(ctx, db, action.UserID, action.Service)
			if err != nil {
				return nil, err
			}
			snapshots[action.Service] = snapshot
		}
		if err := applyToolConfigurationSnapshot(ctx, db, action, snapshot); err != nil {
			return nil, err
		}
	}
	return snapshots, nil
}

func InternalToolConfiguration(w http.ResponseWriter, r *http.Request) {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	supplied := r.Header.Get("X-LazyMind-Internal-Token")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) != 1 {
		common.ReplyErr(w, "forbidden", http.StatusUnauthorized)
		return
	}
	db, userID := store.DB(), store.UserID(r)
	conversationID := common.PathVar(r, "conversation_id")
	var req ToolConfigurationRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil || db == nil {
		common.ReplyErr(w, "invalid request", 400)
		return
	}
	if validateConfigurationOwner(r.Context(), db, userID, conversationID) != nil {
		common.ReplyErr(w, "not found", 404)
		return
	}
	if req.Operation == "list" {
		var actions []ToolConfigurationAction
		if err := db.WithContext(r.Context()).Where("user_id = ? AND conversation_id = ?", userID, conversationID).
			Order("created_at DESC").Limit(100).Find(&actions).Error; err != nil {
			common.ReplyErr(w, "configuration unavailable", 503)
			return
		}
		common.ReplyOK(w, ToolConfigurationListResponse{Actions: actions})
		return
	}
	if req.Operation == "check" {
		snapshot, err := probeToolConfiguration(r.Context(), db, userID, req.Service)
		if err != nil {
			common.ReplyErr(w, "configuration unavailable", 503)
			return
		}
		common.ReplyOK(w, map[string]any{"status": snapshot.Status, "mcp_config": snapshot.MCP, "tool_config": snapshot.Config})
		return
	}
	if req.Operation == "prepare" {
		action, err := prepareToolConfiguration(r.Context(), db, userID, conversationID, req)
		if err != nil {
			common.ReplyErr(w, "cannot prepare configuration", 400)
			return
		}
		common.ReplyOK(w, action)
		return
	}
	if req.Operation == "poll_batch" {
		if len(req.ActionIDs) > 100 {
			common.ReplyErr(w, "too many actions", 400)
			return
		}
		var actions []ToolConfigurationAction
		if err := db.WithContext(r.Context()).Where("id IN ? AND user_id = ? AND conversation_id = ?", req.ActionIDs, userID, conversationID).Find(&actions).Error; err != nil {
			common.ReplyErr(w, "configuration unavailable", 503)
			return
		}
		byID := map[string]int{}
		for i := range actions {
			byID[actions[i].ID] = i
		}
		// Check every requested action before any probes or updates, including foreign IDs.
		for _, id := range req.ActionIDs {
			if _, ok := byID[id]; !ok {
				common.ReplyErr(w, "not found", 404)
				return
			}
		}
		snapshots, err := refreshToolConfigurations(r.Context(), db, actions)
		if err != nil {
			common.ReplyErr(w, "configuration unavailable", 503)
			return
		}
		results := make([]map[string]any, 0, len(req.ActionIDs))
		for _, id := range req.ActionIDs {
			action := actions[byID[id]]
			snapshot := snapshots[action.Service]
			results = append(results, map[string]any{"action": action, "tool_config": snapshot.Config, "mcp_config": snapshot.MCP, "pending_delivery": action.DeliveredVersion < action.Version})
		}
		common.ReplyOK(w, map[string]any{"actions": results})
		return
	}
	var action ToolConfigurationAction
	if db.Where("id = ? AND user_id = ? AND conversation_id = ?", req.ActionID, userID, conversationID).First(&action).Error != nil {
		common.ReplyErr(w, "not found", 404)
		return
	}
	if req.Operation == "ack" {
		if req.Version < 1 || req.RequestID == "" {
			common.ReplyErr(w, "invalid acknowledgement", 400)
			return
		}
		result := db.Model(&ToolConfigurationAction{}).Where("id = ? AND user_id = ? AND version = ? AND delivered_version < ?",
			action.ID, userID, req.Version, req.Version).Updates(map[string]any{"delivered_version": req.Version, "request_id": req.RequestID})
		if result.Error != nil {
			common.ReplyErr(w, "acknowledgement failed", 500)
			return
		}
		common.ReplyOK(w, map[string]any{"acknowledged": result.RowsAffected == 1})
		return
	}
	if req.Operation != "poll" {
		common.ReplyErr(w, "invalid operation", 400)
		return
	}
	snapshot, err := refreshToolConfiguration(r.Context(), db, &action)
	if err != nil {
		common.ReplyErr(w, "configuration unavailable", 503)
		return
	}
	common.ReplyOK(w, map[string]any{"action": action, "tool_config": snapshot.Config, "mcp_config": snapshot.MCP,
		"pending_delivery": action.DeliveredVersion < action.Version})
}

func ListToolConfigurations(w http.ResponseWriter, r *http.Request) {
	db, userID := store.DB(), store.UserID(r)
	conversationID := common.PathVar(r, "conversation_id")
	if db == nil || validateConfigurationOwner(r.Context(), db, userID, conversationID) != nil {
		common.ReplyErr(w, "not found", 404)
		return
	}
	var actions []ToolConfigurationAction
	query := db.WithContext(r.Context()).Where("user_id = ? AND conversation_id = ?", userID, conversationID)
	if historyID := r.URL.Query().Get("history_id"); historyID != "" {
		query = query.Where("history_id = ?", historyID)
	}
	if err := query.
		Order("created_at DESC").Limit(100).Find(&actions).Error; err != nil {
		common.ReplyErr(w, "configuration unavailable", 503)
		return
	}
	// Public reads return only action references and verified status, never runtime credentials.
	if _, err := refreshToolConfigurations(r.Context(), db, actions); err != nil {
		common.ReplyErr(w, "configuration unavailable", 503)
		return
	}
	common.ReplyOK(w, ToolConfigurationListResponse{Actions: actions})
}
