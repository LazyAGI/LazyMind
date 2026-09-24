package resourceupdate

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common"
)

type whenToUseConflictSkill struct {
	SkillKey         string `json:"skill_key"`
	Name             string `json:"name"`
	Category         string `json:"category"`
	CurrentWhenToUse string `json:"current_when_to_use"`
}

type whenToUseConflictGroup struct {
	ID     string                   `json:"id"`
	Reason string                   `json:"reason"`
	Skills []whenToUseConflictSkill `json:"skills"`
}

func parseWhenToUseConflicts(summary string) []whenToUseConflictGroup {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	var payload struct {
		Conflicts []whenToUseConflictGroup `json:"when_to_use_conflicts"`
	}
	if err := json.Unmarshal([]byte(summary), &payload); err != nil {
		return nil
	}
	return payload.Conflicts
}

// ResolveWhenToUseConflicts rejects the retired description-based invocation policy.
// Call modes are managed by the ordinary Skill settings endpoint.
func ResolveWhenToUseConflicts(w http.ResponseWriter, r *http.Request) {
	common.ReplyErr(w, "description-based invocation choices are no longer supported; update the skill call mode instead", http.StatusGone)
}
