package doc

import (
	"context"

	"lazymind/core/acl"
)

// DatasetAllowsWriteForCaller complements a successful GetDataset read check
// with both the dataset edit ACL and the backing scan-source write policy.
func DatasetAllowsWriteForCaller(ctx context.Context, caller DatasetCatalogCaller, dataset Dataset) bool {
	allowed := dataset.IsOwner
	for _, permission := range dataset.Acl {
		if permission == acl.PermissionDatasetWrite {
			allowed = true
		}
	}
	return allowed && datasetAllowedByScanSourceForCaller(ctx, caller, dataset.DatasetID, acl.PermissionDatasetWrite)
}
