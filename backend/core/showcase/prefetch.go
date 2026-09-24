package showcase

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
)

// Match FeaturedCases.tsx: the first eight home cases per entry type,
// ordered by placement.order. Chat is the default landing page.
func assetPrefetchOrder(catalog Catalog, downloads assetDownloads) []string {
	cases := append([]FeaturedDefinition(nil), catalog.Cases...)
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Placement.Order < cases[j].Placement.Order })
	var result []string
	seen := map[string]bool{}
	add := func(item FeaturedDefinition) {
		key := item.ID + "/" + item.Version
		if _, ok := downloads.Bundles[key]; ok && !seen[key] {
			result = append(result, key)
			seen[key] = true
		}
	}
	for _, chat := range []bool{true, false} {
		count := 0
		for _, item := range cases {
			if item.Status == StatusPublished && item.Placement.Home && (item.Type == TypeChat) == chat {
				if count < 8 {
					add(item)
				}
				count++
			}
		}
	}
	for _, item := range cases {
		if item.Status == StatusPublished && (item.Placement.Home || item.Placement.Gallery) {
			add(item)
		}
	}
	return result
}

// StartAssetPrefetch runs after history injection, without delaying readiness.
// Sequential downloads finish the home page before competing for bandwidth
// with the rest of the gallery. On-demand requests share the same cache locks.
func StartAssetPrefetch(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		root, catalogPath := os.Getenv("LAZYMIND_RUNTIME_ROOT"), CatalogPath()
		if root == "" || catalogPath == "" {
			return
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(catalogPath), "downloads.json"))
		if os.IsNotExist(err) {
			return
		}
		var downloads assetDownloads
		if err != nil || json.Unmarshal(data, &downloads) != nil || downloads.SchemaVersion != 1 {
			log.Warn().Msg("featured asset prefetch manifest unavailable")
			return
		}
		catalog, err := LoadCatalog(catalogPath)
		if err != nil {
			log.Warn().Err(err).Msg("featured asset prefetch catalog unavailable")
			return
		}
		cache := filepath.Join(root, "cache", "featured-assets")
		for _, key := range assetPrefetchOrder(catalog, downloads) {
			if ctx.Err() != nil {
				return
			}
			downloadCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			_, err := ensureAssetBundle(downloadCtx, cache, downloads.Bundles[key])
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Str("case", key).Msg("featured asset prefetch failed; on-demand access will retry")
			}
		}
	}()
	return done
}
