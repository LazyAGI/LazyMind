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
}
