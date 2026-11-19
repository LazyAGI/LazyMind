package doc

import (
	"context"
	"lazymind/core/acl"
	"slices"
)

func (s *DatasetCatalogService) addAllowedActions(ctx context.Context, c DatasetCatalogCaller, datasets []Dataset) {
	for i := range datasets {
		datasets[i].AllowedActions = []string{"read"}
	}
	if !slices.Contains(c.Permissions, "document.write") && !slices.Contains(c.Permissions, "*") {
		return
	}
	for _, action := range []struct{ name, permission string }{{"upload", acl.PermissionDatasetUpload}, {"write", acl.PermissionDatasetWrite}} {
		for i, ds := range datasets {
			if ds.IsOwner || catalogAllowsAction(ds.Acl, action.permission) {
				datasets[i].AllowedActions = append(datasets[i].AllowedActions, action.name)
				if action.name == "write" && ds.IsOwner {
					datasets[i].AllowedActions = append(datasets[i].AllowedActions, "delete")
				}
			}
		}
	}
}
