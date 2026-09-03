package modelprovider

import (
	"encoding/json"
	"strings"

	"lazymind/core/common/orm"
)

var autoModelSlots = []struct {
	ModelKey     string
	CatalogTypes []string
}{
	{ModelKey: "llm", CatalogTypes: []string{"llm"}},
	{ModelKey: "vlm", CatalogTypes: []string{"vlm"}},
	{ModelKey: "embed_main", CatalogTypes: []string{"embed"}},
	// The UI presents text generation and editable image models in one image-generator
	// slot. An image_editing selection is mirrored to image_generator by the runtime.
	{ModelKey: "image_generator", CatalogTypes: []string{"text2image", "image_editing"}},
}

func preferredAutoModel(
	baseURL string,
	catalogTypes []string,
	models []orm.UserModelProviderGroupModel,
) (orm.UserModelProviderGroupModel, bool) {
	typeSet := make(map[string]struct{}, len(catalogTypes))
	for _, modelType := range catalogTypes {
		typeSet[strings.ToLower(strings.TrimSpace(modelType))] = struct{}{}
	}
	candidates := make([]orm.UserModelProviderGroupModel, 0, len(models))
	for _, model := range models {
		if _, ok := typeSet[strings.ToLower(strings.TrimSpace(model.ModelType))]; ok {
			candidates = append(candidates, model)
		}
	}
	if len(candidates) == 0 {
		return orm.UserModelProviderGroupModel{}, false
	}

	preferredIndex := -1
	for index, candidate := range candidates {
		if candidate.FreeAutoSelectPriority <= 0 ||
			!freeAutoSelectAppliesToBaseURL(candidate.FreeAutoSelectBaseURLs, baseURL) {
			continue
		}
		if preferredIndex < 0 ||
			candidate.FreeAutoSelectPriority < candidates[preferredIndex].FreeAutoSelectPriority {
			preferredIndex = index
		}
	}
	if preferredIndex >= 0 {
		return candidates[preferredIndex], true
	}
	return candidates[0], true
}

func freeAutoSelectAppliesToBaseURL(encodedBaseURLs, baseURL string) bool {
	if strings.TrimSpace(encodedBaseURLs) == "" {
		return true
	}
	var allowed []string
	if err := json.Unmarshal([]byte(encodedBaseURLs), &allowed); err != nil {
		return false
	}
	baseURL = normalizeBaseURLForCompare(baseURL)
	for _, candidate := range allowed {
		if normalizeBaseURLForCompare(candidate) == baseURL {
			return true
		}
	}
	return false
}

// FreeAutoSelectAppliesToBaseURL exposes the catalog's endpoint scoping rule to
// request-time model routers without duplicating URL normalization semantics.
func FreeAutoSelectAppliesToBaseURL(encodedBaseURLs, baseURL string) bool {
	return freeAutoSelectAppliesToBaseURL(encodedBaseURLs, baseURL)
}
