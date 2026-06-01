package modelprovider

import (
	"context"
	"time"

	"lazymind/core/common"
	"lazymind/core/log"
)

const imageNodeGroupName = "image"

func setImageGroupLazyMode(ctx context.Context, lazyMode *string) error {
	url := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/ng/"+imageNodeGroupName+"/lazy_mode")
	if lazyMode != nil {
		url += "?lazy_mode=" + *lazyMode
	}
	return common.ApiPost(ctx, url, nil, nil, nil, 15*time.Second)
}

// scheduleImageGroupLazyEmbed sets the in-process override to false immediately so that
// subsequent feature checks see image_embed_required=false without waiting for the algorithm
// service call. The algorithm service is updated asynchronously using a detached context so
// that HTTP request cancellation does not abort the background call.
// If the call fails the override is cleared so the next GetModelFeatures re-fetches live state.
func scheduleImageGroupLazyEmbed(_ context.Context) {
	SetImageEmbedRequired(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		embed := "embed"
		if err := setImageGroupLazyMode(ctx, &embed); err != nil {
			log.Logger.Warn().Err(err).Msg("failed to set image group lazy_mode=embed; clearing override")
			ClearImageEmbedRequiredOverride()
		}
	}()
}

// scheduleImageGroupLazyClear sets the in-process override to true immediately so that
// subsequent feature checks see image_embed_required=true without waiting for the algorithm
// service call. The algorithm service is updated asynchronously using a detached context so
// that HTTP request cancellation does not abort the background call.
// If the call fails the override is cleared so the next GetModelFeatures re-fetches live state.
func scheduleImageGroupLazyClear(_ context.Context) {
	SetImageEmbedRequired(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := setImageGroupLazyMode(ctx, nil); err != nil {
			log.Logger.Warn().Err(err).Msg("failed to clear image group lazy_mode; clearing override")
			ClearImageEmbedRequiredOverride()
		}
	}()
}

func isMultimodalEmbeddingModelType(modelType string) bool {
	return modelType == "embed_image"
}
