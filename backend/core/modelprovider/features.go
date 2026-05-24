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
	ImageEmbedEnabled bool `json:"image_embed_enabled"`
}

// algoFeaturesResponse mirrors the algorithm GET /api/model/features JSON.
type algoFeaturesResponse struct {
	ImageEmbedEnabled bool `json:"image_embed_enabled"`
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
			// On error, default to true so the UI behaves as before (conservative fallback).
			featuresCache.value = ModelFeaturesResponse{ImageEmbedEnabled: true}
			featuresCache.err = err
			return
		}
		log.Logger.Info().
			Bool("image_embed_enabled", algo.ImageEmbedEnabled).
			Dur("elapsed", time.Since(start)).
			Msg("model features fetched and cached")
		featuresCache.value = ModelFeaturesResponse{ImageEmbedEnabled: algo.ImageEmbedEnabled}
	})
	common.ReplyOK(w, featuresCache.value)
}
