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

func scheduleImageGroupLazyEmbed(ctx context.Context) {
	SetImageEmbedRequired(false)
	go func() {
		embed := "embed"
		if err := setImageGroupLazyMode(ctx, &embed); err != nil {
			log.Logger.Warn().Err(err).Msg("failed to set image group lazy_mode=embed")
		}
	}()
}

func scheduleImageGroupLazyClear(ctx context.Context) {
	SetImageEmbedRequired(true)
	go func() {
		if err := setImageGroupLazyMode(ctx, nil); err != nil {
			log.Logger.Warn().Err(err).Msg("failed to clear image group lazy_mode")
		}
	}()
}

func isMultimodalEmbeddingModelType(modelType string) bool {
	switch modelType {
	case "multimodal_embedding", "cross_modal_embed":
		return true
	default:
		return false
	}
}
