package common

import "net/http"

func init() {
	registerAdditionalError(
		"description-based invocation choices are no longer supported; update the skill call mode instead",
		http.StatusGone,
		2002945,
	)
	registerAdditionalError("value must be a string or string array", http.StatusBadRequest, 2002946)
	registerAdditionalError("mode must be light or deep", http.StatusBadRequest, 2002947)
	registerAdditionalErrorPattern(
		"skill name %q is ambiguous; specify its full category/name",
		"Skill name is ambiguous; specify its full category/name",
		http.StatusBadRequest,
		2002948,
	)
	registerAdditionalError("decode skill usage", http.StatusInternalServerError, 2002949)
	registerAdditionalError("cannot delete original revision", http.StatusConflict, 2002950)
	registerAdditionalErrorPattern("unsupported discovery field %q", "Unsupported discovery field", http.StatusBadRequest, 2002951)
	registerAdditionalError("discovery value required", http.StatusBadRequest, 2002952)
	registerAdditionalError("skill organize task is not running", http.StatusConflict, 2003145)
	registerAdditionalError("skill organize cancel failed", http.StatusBadGateway, 2003146)
	registerAdditionalError("request_id and item_ids are required", http.StatusBadRequest, 2003163)
	registerAdditionalError("action must be accept, reject, or revoke", http.StatusBadRequest, 2003164)
	registerAdditionalErrorPattern("缺少审批项 %s 的 Skill 绑定", "Skill organize approval binding is missing", http.StatusInternalServerError, 2003165)
	registerAdditionalErrorPattern("unsupported organize approval action %q", "Unsupported organize approval action", http.StatusBadRequest, 2003166)
	registerAdditionalErrorAlias("整理项缺少目标 Skill", "Skill organize item has no target skill", http.StatusBadRequest, 2003167)
	registerAdditionalErrorAlias("整理项没有待审批正文", "Skill organize item has no content to approve", http.StatusBadRequest, 2003168)
	registerAdditionalErrorPattern("找不到整理范围内的 Skill %s", "Skill in organize scope was not found", http.StatusNotFound, 2003169)
	registerAdditionalErrorPattern("Skill %s 不在本次整理范围", "Skill is outside the organize scope", http.StatusBadRequest, 2003170)
	registerAdditionalErrorPattern("找不到整理目标 Skill %s", "Skill organize target was not found", http.StatusNotFound, 2003171)
	registerAdditionalErrorPattern("找不到要移除的 Skill %s", "Skill to remove was not found", http.StatusNotFound, 2003172)
}
