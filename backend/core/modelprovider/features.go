package modelprovider

import (
	"net/http"
	"sync"
	"time"

	"lazymind/core/common"
	"lazymind/core/log"
)

// ModelFeaturesResponse is the response shape for GET /model_providers/features.
type ModelFeaturesResponse struct {
	ImageEmbedEnabled  bool `json:"image_embed_enabled"`
	ImageEmbedRequired bool `json:"image_embed_required"`
}

// algoFeaturesResponse mirrors the algorithm GET /api/model/features JSON.
type algoFeaturesResponse struct {
	ImageEmbedEnabled  bool `json:"image_embed_enabled"`
	ImageEmbedRequired bool `json:"image_embed_required"`
}

// featuresCache holds the permanently cached result fetched once from the algorithm service.
var featuresCache struct {
	sync.Once
	value ModelFeaturesResponse
	err   error
}

const modelFeaturesTimeout = 5 * time.Second

// GetModelFeatures proxies to the algorithm service GET /api/model/features and caches the
// result permanently (sync.Once). The algorithm service derives the value from the static
// runtime_models.yaml at startup, so it never changes while the process is running.
func GetModelFeatures(w http.ResponseWriter, r *http.Request) {
	featuresCache.Do(func() {
		upstream := common.JoinURL(common.ChatServiceEndpoint(), "/api/model/features")
		start := time.Now()
		var algo algoFeaturesResponse
		if err := common.ApiGet(r.Context(), upstream, nil, &algo, modelFeaturesTimeout); err != nil {
			log.Logger.Error().
				Err(err).
				Str("upstream", upstream).
				Dur("elapsed", time.Since(start)).
				Msg("model features fetch failed; defaulting image_embed_enabled=true")
			featuresCache.value = ModelFeaturesResponse{ImageEmbedEnabled: true}
			featuresCache.err = err
			return
		}
		log.Logger.Info().
			Bool("image_embed_enabled", algo.ImageEmbedEnabled).
			Bool("image_embed_required", algo.ImageEmbedRequired).
			Dur("elapsed", time.Since(start)).
			Msg("model features fetched and cached")
		featuresCache.value = ModelFeaturesResponse{
			ImageEmbedEnabled:  algo.ImageEmbedEnabled,
			ImageEmbedRequired: algo.ImageEmbedRequired,
		}
	})
	common.ReplyOK(w, featuresCache.value)
}

// GetCachedModelFeatures returns the cached model features without making an HTTP request.
func GetCachedModelFeatures() ModelFeaturesResponse {
	return featuresCache.value
}

// SetImageEmbedRequired marks image embed as required in the in-memory cache.
// Called after lazy_mode is cleared so subsequent upload checks see the updated state
// without waiting for a process restart.
func SetImageEmbedRequired() {
	featuresCache.Do(func() {}) // ensure Once has run before we mutate value
	featuresCache.value.ImageEmbedRequired = true
}
