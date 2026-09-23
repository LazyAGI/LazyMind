package service

import (
	"encoding/json"
	"strings"
)

func applySearchMetadata(updates map[string]any, req PatchSkillRequest) {
	if req.Field != nil {
		updates["field"] = strings.TrimSpace(*req.Field)
	}
	if req.Aliases != nil {
		raw, _ := json.Marshal(compactStrings(*req.Aliases))
		updates["aliases"] = raw
	}
	if req.Keywords != nil {
		raw, _ := json.Marshal(compactStrings(*req.Keywords))
		updates["keywords"] = raw
	}
}
