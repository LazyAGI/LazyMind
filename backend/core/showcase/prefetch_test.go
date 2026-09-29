package showcase

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestAssetPrefetchHomePagesBeforeGallery(t *testing.T) {
	var catalog Catalog
	downloads := assetDownloads{Bundles: map[string]assetBundle{}}
	for i := 10; i >= 0; i-- {
		for _, kind := range []string{TypeChat, TypeWorkflow} {
			id := fmt.Sprintf("%s-%02d", kind, i)
			catalog.Cases = append(catalog.Cases, FeaturedDefinition{ID: id, Version: "v1", Type: kind, Status: StatusPublished, Placement: FeaturedPlacement{Home: true, Gallery: true, Order: i}})
			downloads.Bundles[id+"/v1"] = assetBundle{}
		}
	}
	// A hidden case must never be downloaded just because its bundle exists.
	catalog.Cases = append(catalog.Cases, FeaturedDefinition{ID: "hidden", Version: "v1", Type: TypeChat, Status: StatusPublished})
	downloads.Bundles["hidden/v1"] = assetBundle{}
	got := assetPrefetchOrder(catalog, downloads)
	var first []string
	for _, kind := range []string{TypeChat, TypeWorkflow} {
		for i := 0; i < 8; i++ {
			first = append(first, fmt.Sprintf("%s-%02d/v1", kind, i))
		}
	}
	if len(got) != 22 || !reflect.DeepEqual(got[:16], first) {
		t.Fatalf("unexpected prefetch order: %v", got)
	}
	seen := map[string]bool{}
	for _, key := range got {
		if seen[key] {
			t.Fatalf("duplicate %s", key)
		}
		seen[key] = true
	}
}

func TestAssetPrefetchWithoutDesktopRuntimeExits(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_ROOT", "")
	select {
	case <-StartAssetPrefetch(context.Background()):
	case <-time.After(time.Second):
		t.Fatal("prefetch blocked without desktop runtime")
	}
}
