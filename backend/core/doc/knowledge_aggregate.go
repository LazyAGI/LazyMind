package doc

import (
	"context"
	"gorm.io/gorm"
	"sort"
)

// Budgets bound resource materialization and result cardinality independently.
type AggregateBudget struct{ MaxDocuments, MaxGroups, MaxDimensions int }

func AggregateMetadata(ctx context.Context, db, lazyDB *gorm.DB, caller DatasetCatalogCaller, req AggregateDocumentsRequest, budget AggregateBudget) (AggregateDocumentsResponse, error) {
	if budget.MaxDocuments <= 0 {
		budget.MaxDocuments = 10000
	}
	if budget.MaxGroups <= 0 {
		budget.MaxGroups = 1000
	}
	if budget.MaxDimensions <= 0 {
		budget.MaxDimensions = 3
	}
	fail := func(dimension string, actual, limit int) (AggregateDocumentsResponse, error) {
		return AggregateDocumentsResponse{}, &ResourceError{Code: "QUERY_TOO_LARGE", Message: "metadata query exceeds budget", Details: map[string]any{"dimension": dimension, "actual": actual, "limit": limit, "suggested_actions": []string{"select fewer knowledge bases or grouping dimensions"}}}
	}
	groups := normalizeGroupBy(req.GroupBy)
	if len(groups) != len(req.GroupBy) {
		return AggregateDocumentsResponse{}, resourceErr("INVALID_ARGUMENT", "unsupported or duplicate grouping dimension")
	}
	if len(groups) > budget.MaxDimensions {
		return fail("group_dimensions", len(groups), budget.MaxDimensions)
	}
	if len(req.DatasetIDs) == 0 || len(req.DatasetIDs) > 20 {
		return AggregateDocumentsResponse{}, resourceErr("INVALID_ARGUMENT", "select one to twenty libraries")
	}
	catalog, err := NewDatasetCatalogService(DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return AggregateDocumentsResponse{}, err
	}
	for _, id := range req.DatasetIDs {
		if _, err = catalog.GetDataset(ctx, DatasetGetRequest{UserID: caller.UserID, DatasetID: id, Caller: caller}); err != nil {
			return AggregateDocumentsResponse{}, err
		}
	}
	var ids []string
	if req.ParentID != nil && len(req.DatasetIDs) != 1 {
		return AggregateDocumentsResponse{}, resourceErr("INVALID_ARGUMENT", "parent_id requires a single library")
	}
	recursive := req.Recursive == nil || *req.Recursive
	query, err := NewDocumentCatalog(db, catalog).filteredQuery(ctx, DocumentCatalogQuery{DatasetIDs: req.DatasetIDs, ResourceIDs: req.ResourceIDs, ResourceType: "document", ParentID: req.ParentID, Recursive: recursive, Match: req.Match, Name: req.Name, Path: req.Path})
	if err != nil {
		return AggregateDocumentsResponse{}, err
	}
	if err = query.Order("id").Limit(budget.MaxDocuments+1).Pluck("id", &ids).Error; err != nil {
		return AggregateDocumentsResponse{}, err
	}
	if len(ids) > budget.MaxDocuments {
		return fail("documents", len(ids), budget.MaxDocuments)
	}
	rows, _, err := loadMergedDocumentsWithDB(ctx, db, lazyDB, ids, "", "", "", false, budget.MaxDocuments, 0)
	if err != nil {
		return AggregateDocumentsResponse{}, err
	}
	// A tag grouping expands each document. Bound that expansion before allocating
	// a cross product, including malformed historical metadata.
	estimated := 0
	for _, row := range rows {
		n := 1
		for _, g := range groups {
			if g == "tag" {
				n = len(documentRowTags(row))
				if n == 0 {
					n = 1
				}
			}
		}
		estimated += n
		if estimated > budget.MaxDocuments+budget.MaxGroups {
			return fail("group_expansion", estimated, budget.MaxDocuments+budget.MaxGroups)
		}
	}
	result := aggregateDocumentRows(rows, req)
	if len(result.Groups) > budget.MaxGroups {
		return fail("groups", len(result.Groups), budget.MaxGroups)
	}
	sort.Slice(result.Groups, func(i, j int) bool {
		return encodeAggregateKey(result.Groups[i].Key, groups) < encodeAggregateKey(result.Groups[j].Key, groups)
	})
	return result, nil
}
