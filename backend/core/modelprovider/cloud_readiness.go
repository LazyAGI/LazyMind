package modelprovider

import (
	"context"
	"sync"
)

type CloudModelReadiness struct {
	Known   bool
	Ready   bool
	Reason  string
	PlanURL string
}

type CloudReadinessProvider interface {
	CloudModelReadiness(context.Context, string) (CloudModelReadiness, error)
}

var cloudReadinessState struct {
	sync.RWMutex
	provider CloudReadinessProvider
}

func SetCloudReadinessProvider(provider CloudReadinessProvider) {
	cloudReadinessState.Lock()
	cloudReadinessState.provider = provider
	cloudReadinessState.Unlock()
}

func resolveCloudModelReadiness(ctx context.Context, modelType string) (CloudModelReadiness, error) {
	cloudReadinessState.RLock()
	provider := cloudReadinessState.provider
	cloudReadinessState.RUnlock()
	if provider == nil {
		return CloudModelReadiness{}, nil
	}
	return provider.CloudModelReadiness(ctx, modelType)
}
