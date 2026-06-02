package modelprovider

import (
	"context"
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

// imageEmbedEnabledCache caches image_embed_enabled permanently (derived from static yaml, never changes).
var imageEmbedEnabledCache struct {
	sync.Once
	value   bool
	fetched bool // true after the first successful or failed fetch
	err     error
}

// imageEmbedRequiredMu protects imageEmbedRequired and imageEmbedRequiredInit.
var imageEmbedRequiredMu sync.RWMutex

// imageEmbedRequiredInitMu serialises the one-time initialisation fetch so that
// concurrent callers do not all issue HTTP requests simultaneously (cache stampede).
var imageEmbedRequiredInitMu sync.Mutex

// imageEmbedRequired is the current value of image_embed_required.
// It is initialised from the algorithm service on first use and updated by
// SetImageEmbedRequired whenever lazy_mode changes.
var imageEmbedRequired bool

// imageEmbedRequiredInit is true once imageEmbedRequired has been populated
// (either from the algorithm service at startup or via SetImageEmbedRequired).
var imageEmbedRequiredInit bool

const modelFeaturesTimeout = 5 * time.Second

// algoRoleTypeResponse mirrors the algorithm GET /api/model/role_type JSON.
type algoRoleTypeResponse struct {
	Role      string `json:"role"`
	Type      string `json:"type"`
	Source    string `json:"source"`
	IsDynamic bool   `json:"is_dynamic"`
}

// roleTypeInfo holds the cached result of a single GET /api/model/role_type call.
type roleTypeInfo struct {
	Type      string
	IsDynamic bool
}

// roleTypeCache caches per-role info permanently (yaml is static per process).
// Key: role string, Value: roleTypeInfo
var roleTypeCache sync.Map

// fetchRoleTypeInfo calls /api/model/role_type and caches the result.
// On any error the zero value is returned so callers can decide their own fallback.
func fetchRoleTypeInfo(ctx context.Context, role string) (roleTypeInfo, error) {
	if v, ok := roleTypeCache.Load(role); ok {
		return v.(roleTypeInfo), nil
	}
	upstream := common.JoinURL(common.ChatServiceEndpoint(), "/api/model/role_type")
	var resp algoRoleTypeResponse
	if err := common.ApiGet(ctx, upstream+"?role="+role, nil, &resp, modelFeaturesTimeout); err != nil {
		return roleTypeInfo{}, err
	}
	info := roleTypeInfo{Type: resp.Type, IsDynamic: resp.IsDynamic}
	roleTypeCache.Store(role, info)
	return info, nil
}

// FetchRoleIsDynamic returns whether the given model_type role is source=dynamic in the runtime
// config yaml.  Results are permanently cached since the yaml does not change at runtime.
// Returns (true, nil) on any fetch error so that the caller falls back to the normal DB path.
func FetchRoleIsDynamic(ctx context.Context, role string) (bool, error) {
	info, err := fetchRoleTypeInfo(ctx, role)
	if err != nil {
		log.Logger.Warn().Err(err).Str("role", role).
			Msg("role_type fetch failed; assuming dynamic=true")
		return true, err
	}
	return info.IsDynamic, nil
}

// resolveModelType translates a runtime_models.yaml role key (e.g. "evo_llm") into the
// lazyllm technical type (e.g. "llm") by calling the algorithm service.
// If the algorithm service returns 404 (role not in yaml) or an error, the original
// roleKey is returned unchanged so the caller can still query the DB directly.
func resolveModelType(ctx context.Context, roleKey string) string {
	info, err := fetchRoleTypeInfo(ctx, roleKey)
	if err != nil {
		log.Logger.Warn().Err(err).Str("role", roleKey).
			Msg("resolveModelType: algo call failed, using role key as-is")
		return roleKey
	}
	if info.Type == "" {
		return roleKey
	}
	return info.Type
}

// fetchImageEmbedEnabled fetches and permanently caches image_embed_enabled from the algorithm service.
func fetchImageEmbedEnabled(ctx context.Context) (bool, error) {
	imageEmbedEnabledCache.Do(func() {
		upstream := common.JoinURL(common.ChatServiceEndpoint(), "/api/model/features")
		start := time.Now()
		var algo algoFeaturesResponse
		if err := common.ApiGet(ctx, upstream, nil, &algo, modelFeaturesTimeout); err != nil {
			log.Logger.Error().
				Err(err).
				Str("upstream", upstream).
				Dur("elapsed", time.Since(start)).
				Msg("model features fetch failed; defaulting image_embed_enabled=true")
			imageEmbedEnabledCache.value = true
			imageEmbedEnabledCache.err = err
			imageEmbedEnabledCache.fetched = true
			return
		}
		log.Logger.Info().
			Bool("image_embed_enabled", algo.ImageEmbedEnabled).
			Dur("elapsed", time.Since(start)).
			Msg("image_embed_enabled fetched and cached")
		imageEmbedEnabledCache.value = algo.ImageEmbedEnabled
		imageEmbedEnabledCache.fetched = true
	})
	return imageEmbedEnabledCache.value, imageEmbedEnabledCache.err
}

// ensureImageEmbedRequiredInit initialises imageEmbedRequired from the algorithm service
// if it has not been set yet. This runs at most once per process lifetime (the first
// GetModelFeatures call after startup). Subsequent changes come via SetImageEmbedRequired.
func ensureImageEmbedRequiredInit(ctx context.Context) {
	imageEmbedRequiredMu.RLock()
	already := imageEmbedRequiredInit
	imageEmbedRequiredMu.RUnlock()
	if already {
		return
	}

	// Serialise the one-time fetch so concurrent callers don't all issue HTTP requests.
	imageEmbedRequiredInitMu.Lock()
	defer imageEmbedRequiredInitMu.Unlock()

	// Double-check after acquiring the init lock — another goroutine may have finished.
	imageEmbedRequiredMu.RLock()
	already = imageEmbedRequiredInit
	imageEmbedRequiredMu.RUnlock()
	if already {
		return
	}

	upstream := common.JoinURL(common.ChatServiceEndpoint(), "/api/model/features")
	var algo algoFeaturesResponse
	if err := common.ApiGet(ctx, upstream, nil, &algo, modelFeaturesTimeout); err != nil {
		log.Logger.Warn().Err(err).
			Msg("image_embed_required init fetch failed; defaulting to false")
		// Leave imageEmbedRequiredInit=false so we retry on the next request.
		return
	}

	imageEmbedRequiredMu.Lock()
	imageEmbedRequired = algo.ImageEmbedRequired
	imageEmbedRequiredInit = true
	log.Logger.Info().
		Bool("image_embed_required", algo.ImageEmbedRequired).
		Msg("image_embed_required initialised from algorithm service")
	imageEmbedRequiredMu.Unlock()
}

// GetModelFeatures returns model feature flags.
// image_embed_enabled is permanently cached (static yaml value).
// image_embed_required is initialised from the algorithm service on first call and
// updated in-process by SetImageEmbedRequired whenever lazy_mode changes.
func GetModelFeatures(w http.ResponseWriter, r *http.Request) {
	enabled, _ := fetchImageEmbedEnabled(r.Context())
	ensureImageEmbedRequiredInit(r.Context())

	imageEmbedRequiredMu.RLock()
	required := imageEmbedRequired
	imageEmbedRequiredMu.RUnlock()

	common.ReplyOK(w, ModelFeaturesResponse{
		ImageEmbedEnabled:  enabled,
		ImageEmbedRequired: required,
	})
}

// GetCachedModelFeatures returns the current model features without making an HTTP request.
// image_embed_enabled uses the permanently cached value (defaults to true when not yet fetched).
// image_embed_required uses the in-process value (defaults to false when not yet initialised).
func GetCachedModelFeatures() ModelFeaturesResponse {
	enabled := true
	if imageEmbedEnabledCache.fetched {
		enabled = imageEmbedEnabledCache.value
	}

	imageEmbedRequiredMu.RLock()
	required := imageEmbedRequired
	imageEmbedRequiredMu.RUnlock()

	return ModelFeaturesResponse{
		ImageEmbedEnabled:  enabled,
		ImageEmbedRequired: required,
	}
}

// SetImageEmbedRequired updates the in-process image_embed_required value.
// Called after lazy_mode changes so feature checks see the updated state immediately.
func SetImageEmbedRequired(required bool) {
	imageEmbedRequiredMu.Lock()
	imageEmbedRequired = required
	imageEmbedRequiredInit = true
	imageEmbedRequiredMu.Unlock()
}

// ClearImageEmbedRequiredOverride resets imageEmbedRequiredInit so that the next
// GetModelFeatures call re-fetches the value from the algorithm service.
// Called when a lazy_mode update to the algorithm service fails.
func ClearImageEmbedRequiredOverride() {
	imageEmbedRequiredMu.Lock()
	imageEmbedRequiredInit = false
	imageEmbedRequiredMu.Unlock()
}
