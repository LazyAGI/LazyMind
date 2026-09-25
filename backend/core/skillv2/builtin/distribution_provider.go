package builtin

import (
	"context"
	"strings"

	skilldistribution "lazymind/core/skillv2/distribution"
)

// DistributionProvider adapts the runtime builtin catalog to the distribution
// upgrade use case without exposing catalog storage details to its callers.
type DistributionProvider struct{}

func (DistributionProvider) Latest(uid string) (skilldistribution.Package, bool, error) {
	catalogPath := CatalogPath()
	if catalogPath == "" {
		return skilldistribution.Package{}, false, nil
	}
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return skilldistribution.Package{}, false, err
	}
	for _, entry := range catalog.Skills {
		if entry.UID == strings.TrimSpace(uid) {
			return skilldistribution.Package{
				UID: entry.UID, Version: entry.Version, ArchiveSHA256: entry.ArchiveSHA256,
				TreeSHA256: entry.TreeSHA256,
			}, true, nil
		}
	}
	return skilldistribution.Package{}, false, nil
}

// Acquire is reserved for an explicit user-initiated upgrade. Latest only
// reads the catalog, so background version checks never initiate downloads.
func (DistributionProvider) Acquire(ctx context.Context, uid string) (skilldistribution.Package, error) {
	pkg, err := AcquirePackageByUID(ctx, uid)
	if err != nil {
		return skilldistribution.Package{}, err
	}
	return skilldistribution.Package{
		UID: pkg.UID, Version: pkg.Version, ArchiveSHA256: pkg.SHA256,
		TreeSHA256: pkg.TreeSHA256, Files: pkg.Files,
	}, nil
}
