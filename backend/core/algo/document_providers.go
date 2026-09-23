package algo

import (
	"context"
	"time"

	"lazymind/core/common"
)

// DocumentProvider exposes registry declarations, not user configuration or
// authorization to perform a provider operation. IDs/capabilities are opaque.
type DocumentProvider struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities" required:"true"`
}
type DocumentProviderCatalog struct {
	Providers []*DocumentProvider `json:"providers" required:"true"`
}

// ListDocumentProviders fetches the current registry without user credentials,
// artifact context or a cache that could hide provider changes/failures.
func ListDocumentProviders(ctx context.Context) (*DocumentProviderCatalog, error) {
	var catalog DocumentProviderCatalog
	if err := common.ApiGet(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/document/providers"), nil, &catalog, 5*time.Second); err != nil {
		return nil, err
	}
	return &catalog, nil
}
