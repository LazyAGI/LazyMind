package plugin

import (
	"encoding/json"
	"net/http"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/plugin/graphengine"
	"lazymind/core/store"
)

// ValidatePluginDraft returns the authoritative Go compiler diagnostics. Drafts
// may remain invalid while being edited; publish performs the same compilation
// with the strict publish profile.
func ValidatePluginDraft(w http.ResponseWriter, r *http.Request) {
	var draft orm.PluginDraft
	if err := store.DB().Where("id = ? AND created_by = ?", common.PathVar(r, "draft_id"), common.UserID(r)).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	profile := graphengine.ProfileEditor
	var body struct {
		Profile graphengine.Profile `json:"profile"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Profile == graphengine.ProfileGenerationPhase {
		profile = body.Profile
	}
	result := graphengine.Compile(draft.PluginYAMLContent, draft.StateYAMLContent, draft.ScenarioContent, profile)
	common.ReplyOK(w, result)
}
