package server

import (
	"log"
	"net/http"
	"strings"

	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
)

type bindingChatSettingEntry struct {
	BindingID        string   `json:"binding_id"`
	TargetRef        string   `json:"target_ref"`
	ConnectorType    string   `json:"connector_type"`
	TargetType       string   `json:"target_type"`
	Status           string   `json:"status"`
	ChatEnabled      bool     `json:"chat_enabled"`
	IncludeExtensions []string `json:"include_extensions,omitempty"`
}

type sourceChatSettingEntry struct {
	SourceID  string                    `json:"source_id"`
	Name      string                    `json:"name"`
	DatasetID string                    `json:"dataset_id"`
	TenantID  string                    `json:"tenant_id"`
	Bindings  []bindingChatSettingEntry `json:"bindings"`
}

type chatSettingsResponse struct {
	Sources []sourceChatSettingEntry `json:"sources"`
}

func (h *Handler) listBindingChatSettings(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	actor, err := actorFromRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}

	// List all readable sources
	sourceIDs, err := h.access.ListReadableSourceIDs(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}

	listResp, err := h.sources.ListSources(r.Context(), sourceengine.ListSourcesRequest{
		CallerID:  actor.UserID,
		TenantID:  actor.TenantID,
		SourceIDs: sourceIDs,
		Page:      1,
		PageSize:  1000,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	var result chatSettingsResponse
	for _, item := range listResp.Items {
		if item.Status != sourceengine.SourceStatusActive {
			continue
		}
		// Get bindings for this source
		getResp, err := h.sources.GetSource(r.Context(), sourceengine.GetSourceRequest{
			CallerID:        actor.UserID,
			TenantID:        actor.TenantID,
			SourceID:        item.SourceID,
			IncludeBindings: true,
			IncludeSummary:  false,
		})
		if err != nil {
			writeError(w, err)
			return
		}

		var chatBindings []bindingChatSettingEntry
		for _, b := range getResp.Bindings {
			if !isLocalFSBinding(b) || b.Status != sourceengine.BindingStatusActive {
				continue
			}
			chatBindings = append(chatBindings, bindingChatSettingEntry{
				BindingID:        b.BindingID,
				TargetRef:        b.TargetRef,
				ConnectorType:    b.ConnectorType,
				TargetType:       b.TargetType,
				Status:           b.Status,
				ChatEnabled:      b.ChatEnabled,
				IncludeExtensions: b.IncludeExtensions,
			})
		}

		// Only include sources that have at least one local_fs active binding
		if len(chatBindings) > 0 {
			result.Sources = append(result.Sources, sourceChatSettingEntry{
				SourceID:  item.SourceID,
				Name:      item.Name,
				DatasetID: item.DatasetID,
				TenantID:  firstNonEmptyStr(item.TenantID, "root"),
				Bindings:  chatBindings,
			})
		}
	}

	writeJSON(w, http.StatusOK, result)
}

type updateBindingChatSettingRequest struct {
	ChatEnabled *bool `json:"chat_enabled"`
}

func (h *Handler) updateBindingChatSetting(w http.ResponseWriter, r *http.Request) {
	if h.sources == nil {
		writeError(w, missingDependency("source engine"))
		return
	}
	bindingID := r.PathValue("binding_id")
	if strings.TrimSpace(bindingID) == "" {
		writeError(w, invalidJSON(nil))
		return
	}

	var req updateBindingChatSettingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, invalidJSON(err))
		return
	}
	if req.ChatEnabled == nil {
		writeError(w, &sourceengine.EngineError{
			Code:    sourceengine.ErrCodeInvalidRequest,
			Message: "chat_enabled is required",
		})
		return
	}

	log.Printf("[BINDING_CHAT] PUT binding_id=%s chat_enabled=%v", bindingID, *req.ChatEnabled)
	if err := h.sources.UpdateBindingChatEnabled(r.Context(), bindingID, *req.ChatEnabled); err != nil {
		log.Printf("[BINDING_CHAT] PUT binding_id=%s error: %v", bindingID, err)
		writeError(w, err)
		return
	}
	log.Printf("[BINDING_CHAT] PUT binding_id=%s success", bindingID)

	writeJSON(w, http.StatusOK, map[string]any{
		"binding_id":   bindingID,
		"chat_enabled": *req.ChatEnabled,
	})
}

func isLocalFSBinding(b sourceengine.SourceBindingResponse) bool {
	ct := strings.TrimSpace(b.ConnectorType)
	tt := strings.TrimSpace(b.TargetType)
	return strings.EqualFold(ct, "local_fs") || strings.EqualFold(tt, "local_path")
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
