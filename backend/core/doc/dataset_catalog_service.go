package doc

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

type DatasetServiceErrorCode string

const (
	DatasetServiceInvalidArgument DatasetServiceErrorCode = "INVALID_ARGUMENT"
	DatasetServiceNotFound        DatasetServiceErrorCode = "NOT_FOUND"
	DatasetServiceForbidden       DatasetServiceErrorCode = "FORBIDDEN"
	DatasetServiceUnavailable     DatasetServiceErrorCode = "UNAVAILABLE"
	DatasetServiceInternal        DatasetServiceErrorCode = "INTERNAL"
)

type DatasetServiceError struct {
	Code    DatasetServiceErrorCode
	Message string
	Err     error
}

func (e *DatasetServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *DatasetServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type DatasetCatalogCaller struct {
	UserID        string
	Authorization string
	TenantID      string
	UserRole      string
}

type DatasetCatalogServiceDeps struct {
	DB *gorm.DB
}

type DatasetCatalogService struct {
	db *gorm.DB
}

func NewDatasetCatalogService(deps DatasetCatalogServiceDeps) (*DatasetCatalogService, error) {
	if deps.DB == nil {
		return nil, &DatasetServiceError{Code: DatasetServiceInternal, Message: "gorm db is required"}
	}
	return &DatasetCatalogService{db: deps.DB}, nil
}

type DatasetListRequest struct {
	UserID       string
	Keyword      string
	Tags         []string
	Offset       int
	Limit        int
	OrderBy      string
	SourceFilter string
	Caller       DatasetCatalogCaller
}

type DatasetListResult struct {
	Datasets   []Dataset
	TotalSize  int64 // Total user-visible datasets after ACL, scan source, keyword, tags, and source filters.
	NextOffset int
	HasMore    bool
}

type DatasetGetRequest struct {
	UserID    string
	DatasetID string
	Caller    DatasetCatalogCaller
}

func (s *DatasetCatalogService) ListDatasets(ctx context.Context, req DatasetListRequest) (DatasetListResult, error) {
	if s == nil || s.db == nil {
		return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceInternal, Message: "dataset service is not configured"}
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceInvalidArgument, Message: "user_id is required"}
	}
	pageSize := normalizeDatasetPageSize(req.Limit)
	offset := req.Offset
	if offset < 0 {
		return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceInvalidArgument, Message: "offset must be >= 0"}
	}
	keyword := strings.TrimSpace(req.Keyword)
	wantTags := uniqueNonEmptyStrings(req.Tags)
	sourceFilter := strings.ToLower(strings.TrimSpace(req.SourceFilter))
	if sourceFilter != "cloud" && sourceFilter != "manual" {
		sourceFilter = ""
	}
	caller := req.Caller
	caller.UserID = firstNonEmpty(strings.TrimSpace(caller.UserID), userID)

	base := s.db.WithContext(ctx).Model(&orm.Dataset{}).Where("deleted_at IS NULL")
	orderClause := "updated_at desc"
	if orderBy := strings.TrimSpace(req.OrderBy); orderBy != "" {
		if ob, err := normalizeDatasetOrderBy(orderBy); err == nil {
			orderClause = ob
		}
	}

	groupIDs := acl.ResolveUserGroupIDs(userID)
	fetchSize := datasetFetchSize(pageSize)
	total := 0
	page := make([]orm.Dataset, 0, pageSize)
	scanOffset := 0
	hasMoreRows := true
	candidates := make([]orm.Dataset, 0, pageSize)
	pageSourceMap := make(map[string]bool, pageSize)

	for hasMoreRows {
		var rows []orm.Dataset
		query := base.
			Select(`id, kb_id, create_user_id, create_user_name, display_name, "desc", cover_image, created_at, updated_at, ext, type, share_type, dataset_state`).
			Order(orderClause).
			Offset(scanOffset).
			Limit(fetchSize)
		if err := query.Find(&rows).Error; err != nil {
			return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceUnavailable, Message: "query datasets failed", Err: err}
		}
		if len(rows) < fetchSize {
			hasMoreRows = false
		}
		scanOffset += len(rows)
		if len(rows) == 0 {
			break
		}
		for _, ds := range rows {
			perms := datasetACLForUserWithGroups(&ds, userID, groupIDs)
			if len(perms) == 0 {
				continue
			}
			if !datasetMatchesKeyword(&ds, keyword) {
				continue
			}
			if len(wantTags) > 0 && !containsAll(parseDatasetTags(ds.Ext), wantTags) {
				continue
			}
			candidates = append(candidates, ds)
		}
		candidates = filterDatasetsByScanSourceAccessForCaller(ctx, caller, candidates, acl.PermissionDatasetRead)

		if len(candidates) > 0 {
			candidateIDs := make([]string, len(candidates))
			for i, c := range candidates {
				candidateIDs[i] = c.ID
			}
			sourceMap := batchCheckDatasetsHaveSource(ctx, candidateIDs)

			for _, c := range candidates {
				if sourceFilter == "cloud" && !sourceMap[c.ID] {
					continue
				}
				if sourceFilter == "manual" && sourceMap[c.ID] {
					continue
				}
				if total >= offset && len(page) < pageSize {
					page = append(page, c)
					pageSourceMap[c.ID] = sourceMap[c.ID]
				}
				total++
			}

			candidates = candidates[:0]
		}
	}

	out := s.datasetsFromRows(ctx, page, userID, groupIDs, pageSourceMap)
	end := offset + len(page)
	return DatasetListResult{
		Datasets:   out,
		TotalSize:  int64(total),
		NextOffset: end,
		HasMore:    end < total,
	}, nil
}

func (s *DatasetCatalogService) GetDataset(ctx context.Context, req DatasetGetRequest) (Dataset, error) {
	if s == nil || s.db == nil {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceInternal, Message: "dataset service is not configured"}
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceInvalidArgument, Message: "user_id is required"}
	}
	datasetID := strings.TrimSpace(req.DatasetID)
	if datasetID == "" {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceInvalidArgument, Message: "invalid dataset id"}
	}
	caller := req.Caller
	caller.UserID = firstNonEmpty(strings.TrimSpace(caller.UserID), userID)

	var ds orm.Dataset
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Dataset{}, &DatasetServiceError{Code: DatasetServiceNotFound, Message: "dataset not found", Err: err}
		}
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceUnavailable, Message: "query dataset failed", Err: err}
	}
	datasetACL := datasetACLForUser(&ds, userID)
	if len(datasetACL) == 0 {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "dataset forbidden"}
	}
	if !datasetAllowedByScanSourceForCaller(ctx, caller, ds.ID, acl.PermissionDatasetRead) {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "dataset forbidden"}
	}

	createdByDataSource := isDatasetCreatedByDataSource(ctx, ds.ID)
	return s.datasetFromRow(ctx, ds, userID, datasetACL, calcDatasetStatsWithDB(ctx, s.db, ds.ID), createdByDataSource, nil), nil
}

func (s *DatasetCatalogService) datasetsFromRows(ctx context.Context, rows []orm.Dataset, userID string, groupIDs []string, sourceMap map[string]bool) []Dataset {
	out := make([]Dataset, 0, len(rows))
	dsIDs := make([]string, 0, len(rows))
	for _, ds := range rows {
		dsIDs = append(dsIDs, ds.ID)
	}
	statsMap := calcDatasetStatsBatchWithDB(ctx, s.db, dsIDs)
	parserCache := map[string][]ParserConfig{}
	for _, ds := range rows {
		datasetACL := datasetACLForUserWithGroups(&ds, userID, groupIDs)
		algo := parseDatasetAlgo(ds.Ext)
		liveParsers, ok := parserCache[algo.AlgoID]
		if !ok {
			liveParsers = fetchParsersByAlgoID(ctx, algo.AlgoID)
			parserCache[algo.AlgoID] = liveParsers
		}
		out = append(out, s.datasetFromRow(ctx, ds, userID, datasetACL, statsMap[ds.ID], sourceMap[ds.ID], liveParsers))
	}
	return out
}

func (s *DatasetCatalogService) datasetFromRow(ctx context.Context, ds orm.Dataset, userID string, datasetACL []string, stats datasetStats, createdByDataSource bool, liveParsers []ParserConfig) Dataset {
	algo := parseDatasetAlgo(ds.Ext)
	if liveParsers == nil {
		liveParsers = fetchParsersByAlgoID(ctx, algo.AlgoID)
	}
	parsers := mergeParserConfigs(parseDatasetParsers(ds.Ext), liveParsers)
	return Dataset{
		Name:                "datasets/" + ds.ID,
		DatasetID:           ds.ID,
		DisplayName:         ds.DisplayName,
		Desc:                ds.Desc,
		CoverImage:          ds.CoverImage,
		State:               stateToPB(ds.DatasetState),
		IsEmpty:             stats.DocumentCount == 0,
		DocumentCount:       stats.DocumentCount,
		DocumentSize:        stats.DocumentSize,
		SegmentCount:        0,
		TokenCount:          0,
		Parsers:             parsers,
		Algo:                algo,
		Creator:             ds.CreateUserName,
		IsOwner:             ds.CreateUserID == userID,
		CreateTime:          ds.CreatedAt,
		UpdateTime:          ds.UpdatedAt,
		Acl:                 datasetACL,
		ShareType:           shareTypeToPB(ds.ShareType),
		Type:                datasetTypeToPB(ds.Type),
		Tags:                parseDatasetTags(ds.Ext),
		DefaultDataset:      isDefaultDatasetForUserWithDB(ctx, s.db, userID, ds.ID),
		CreatedByDataSource: &createdByDataSource,
	}
}

func normalizeDatasetPageSize(pageSize int) int {
	if pageSize <= 0 {
		return 20
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

func datasetFetchSize(pageSize int) int {
	const fetchFactor = 5
	fetchSize := pageSize * fetchFactor
	if fetchSize < pageSize {
		fetchSize = pageSize
	}
	if fetchSize > 500 {
		fetchSize = 500
	}
	return fetchSize
}
