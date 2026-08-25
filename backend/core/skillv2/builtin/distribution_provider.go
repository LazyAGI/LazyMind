package builtin

import skilldistribution "lazymind/core/skillv2/distribution"

// DistributionProvider adapts the runtime builtin catalog to the distribution
// upgrade use case without exposing catalog storage details to its callers.
type DistributionProvider struct{}

func (DistributionProvider) Latest(uid string) (skilldistribution.Package, bool, error) {
	pkg, found, err := PackageByUID(uid)
	if err != nil || !found {
		return skilldistribution.Package{}, found, err
	}
	return skilldistribution.Package{
		UID: pkg.UID, Version: pkg.Version, ArchiveSHA256: pkg.SHA256,
		TreeSHA256: pkg.TreeSHA256, Files: pkg.Files,
	}, true, nil
}
