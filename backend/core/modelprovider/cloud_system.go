package modelprovider

import (
	"context"
	"sync"
)

const (
	CloudSystemProviderID   = "lazymind-cloud"
	CloudSystemProviderName = "LazyMind Cloud"
)

type CloudCatalogModel struct {
	ModelKey       string
	DisplayName    string
	ModelType      string
	Capabilities   []string
	Status         string
	Lifecycle      string
	DefaultForType bool
}

type CloudModelCatalog struct {
	Known           bool
	Available       bool
	HasTokenPlan    bool
	Reason          string
	PlanURL         string
	ProviderID      string
	ProviderName    string
	CatalogRevision string
	Models          []CloudCatalogModel
}

type CloudCatalogProvider interface {
	CloudModelCatalog(context.Context) (CloudModelCatalog, error)
}

var cloudCatalogState struct {
	sync.RWMutex
	provider CloudCatalogProvider
}

func SetCloudCatalogProvider(provider CloudCatalogProvider) {
	cloudCatalogState.Lock()
	cloudCatalogState.provider = provider
	cloudCatalogState.Unlock()
}

func ResolveCloudModelCatalog(ctx context.Context) (CloudModelCatalog, error) {
	cloudCatalogState.RLock()
	provider := cloudCatalogState.provider
	cloudCatalogState.RUnlock()
	if provider == nil {
		return CloudModelCatalog{}, nil
	}
	return provider.CloudModelCatalog(ctx)
}

func FindCloudCatalogModel(catalog CloudModelCatalog, modelType, modelKey string) *CloudCatalogModel {
	for index := range catalog.Models {
		model := &catalog.Models[index]
		if model.ModelType == modelType && model.ModelKey == modelKey {
			return model
		}
	}
	return nil
}

func DefaultCloudCatalogModel(catalog CloudModelCatalog, modelType string) *CloudCatalogModel {
	var fallback *CloudCatalogModel
	for index := range catalog.Models {
		model := &catalog.Models[index]
		if model.ModelType != modelType || !cloudCatalogModelSelectable(*model) {
			continue
		}
		if fallback == nil {
			fallback = model
		}
		if model.DefaultForType {
			return model
		}
	}
	return fallback
}

func cloudCatalogModelUsable(model CloudCatalogModel) bool {
	return (model.Status == "available" || model.Status == "degraded") && model.Lifecycle != "retired"
}

func cloudCatalogModelSelectable(model CloudCatalogModel) bool {
	return cloudCatalogModelUsable(model) && (model.Lifecycle == "" || model.Lifecycle == "active")
}
