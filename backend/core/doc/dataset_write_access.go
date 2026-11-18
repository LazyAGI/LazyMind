package doc

import (
	"context"

	"lazymind/core/acl"
)

// DatasetAllowsWriteForCaller complements a successful GetDataset read check
// with the dataset edit ACL. Source-provider policies do not govern the library.
func DatasetAllowsWriteForCaller(ctx context.Context, caller DatasetCatalogCaller, dataset Dataset) bool {
	allowed := dataset.IsOwner
	for _, permission := range dataset.Acl {
		if permission == acl.PermissionDatasetWrite {
			allowed = true
		}
	}
	return allowed
}
