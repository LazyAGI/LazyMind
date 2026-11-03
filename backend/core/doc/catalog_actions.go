package doc

import (
	"context"
	"lazymind/core/acl"
	"slices"
)

func (s *DatasetCatalogService) addAllowedActions(ctx context.Context, c DatasetCatalogCaller, datasets []Dataset) {
	ids := make([]string, len(datasets))
	for i, ds := range datasets {
		ids[i] = ds.DatasetID
		datasets[i].AllowedActions = []string{"read"}
	}
	if !slices.Contains(c.Permissions, "document.write") && !slices.Contains(c.Permissions, "*") {
		return
	}
	for _, action := range []struct{ name, permission string }{{"upload", acl.PermissionDatasetUpload}, {"write", acl.PermissionDatasetWrite}} {
		decisions, ok := scanSourceAccessByDatasetForCaller(ctx, c, ids, action.permission)
		if !ok {
			continue
		}
		for i, ds := range datasets {
			d, found := decisions[ds.DatasetID]
			if found && (!d.Exists || d.Allowed) && (ds.IsOwner || catalogAllowsAction(ds.Acl, action.permission)) {
				datasets[i].AllowedActions = append(datasets[i].AllowedActions, action.name)
				if action.name == "write" && ds.IsOwner {
					datasets[i].AllowedActions = append(datasets[i].AllowedActions, "delete")
				}
			}
		}
	}
}
