package doc

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/learning"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
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
	UserName      string
	Permissions   []string
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
	DatasetIDs     []string
	Match          string
	Scene          string
	RequiredAction string
	UserID         string
	Keyword        string
	Tags           []string
	Offset         int
	Limit          int
	OrderBy        string
	SourceFilter   string
	Caller         DatasetCatalogCaller
}

type DatasetListResult struct {
	Datasets   []Dataset
	TotalSize  int64 // Total user-visible datasets after ACL, scan source, keyword, tags, and source filters.
	NextOffset int
	HasMore    bool
}

type DatasetGetRequest struct {
	IncludeConfiguration bool
	UserID               string
	DatasetID            string
	Caller               DatasetCatalogCaller
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
	if sourceFilter != "cloud" && sourceFilter != "manual" && sourceFilter != "official_installed" {
		sourceFilter = ""
	}
	caller := req.Caller
	if caller.UserID != "" && strings.TrimSpace(caller.UserID) != userID {
		return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "caller identity mismatch"}
	}
	caller.UserID = userID
	action := req.RequiredAction
	if action == "" {
		action = "read"
	}
	permission := map[string]string{"read": acl.PermissionDatasetRead, "write": acl.PermissionDatasetWrite, "upload": acl.PermissionDatasetUpload}[action]
	if permission == "" || (req.Match != "" && req.Match != "contains" && req.Match != "exact") {
		return DatasetListResult{}, &DatasetServiceError{Code: DatasetServiceInvalidArgument, Message: "invalid catalog filter"}
	}

	order := resolveDatasetListOrder(req.OrderBy)
	base := s.db.WithContext(ctx).Model(&orm.Dataset{}).Where("datasets.deleted_at IS NULL")
	base = base.Where("COALESCE(NULLIF(TRIM(datasets.tenant_id), ''), 'default') = ?", resourceTenant(caller.TenantID))
	if len(req.DatasetIDs) > 0 {
		base = base.Where("datasets.id IN ?", req.DatasetIDs)
	}
	if req.Match == "exact" {
		base = base.Where("datasets.display_name = ?", keyword)
	}
	if req.Scene != "" {
		if req.Scene == "academic_papers" || req.Scene == "paper_collection" {
			base = base.Where("datasets.scene IN ?", []string{"academic_papers", "paper_collection"})
		} else {
			base = base.Where("datasets.scene = ?", req.Scene)
		}
	}
	if order.usageJoin {
		base = base.Joins("LEFT JOIN dataset_user_states dus ON dus.dataset_id = datasets.id AND dus.create_user_id = ?", userID)
	}

	groupIDs := acl.ResolveUserGroupIDs(userID)
	fetchSize := datasetFetchSize(pageSize)
	total := 0
	page := make([]orm.Dataset, 0, pageSize)
	scanOffset := 0
	hasMoreRows := true
	candidates := make([]orm.Dataset, 0, pageSize)
	pageSourceMap := make(map[string]bool, pageSize)
	pageSourceTypeMap := make(map[string]string, pageSize)

	for hasMoreRows {
		var rows []orm.Dataset
		query := base
		if order.usageJoin {
			query = query.Select("datasets.*")
		} else {
			query = query.Select("datasets.*")
		}
		query = query.
			Order(order.clause).
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
			if !catalogAllowsAction(perms, permission) {
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
		candidates = filterDatasetsByScanSourceAccessForCaller(ctx, caller, candidates, permission)

		if len(candidates) > 0 {
			candidateIDs := make([]string, len(candidates))
			for i, c := range candidates {
				candidateIDs[i] = c.ID
			}
			sourceMap := batchCheckDatasetsHaveSource(ctx, candidateIDs)
			installedMap := batchCheckInstalledMarketDatasets(ctx, userID, candidateIDs)
			sourceTypeMap := buildDatasetSourceTypeMap(candidateIDs, sourceMap, installedMap)

			for _, c := range candidates {
				if !datasetMatchesSourceFilter(sourceFilter, sourceTypeMap[c.ID]) {
					continue
				}
				if total >= offset && len(page) < pageSize {
					page = append(page, c)
					pageSourceMap[c.ID] = sourceMap[c.ID]
					pageSourceTypeMap[c.ID] = sourceTypeMap[c.ID]
				}
				total++
			}

			candidates = candidates[:0]
		}
	}

	out := s.datasetsFromRows(ctx, page, userID, groupIDs, pageSourceMap, pageSourceTypeMap)
	if err := s.addPaperStatistics(ctx, page, out); err != nil {
		return DatasetListResult{}, err
	}
	s.addAllowedActions(ctx, caller, out)
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
	if caller.UserID != "" && strings.TrimSpace(caller.UserID) != userID {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "caller identity mismatch"}
	}
	caller.UserID = userID

	var ds orm.Dataset
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Dataset{}, &DatasetServiceError{Code: DatasetServiceNotFound, Message: "dataset not found", Err: err}
		}
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceUnavailable, Message: "query dataset failed", Err: err}
	}
	if resourceTenant(ds.TenantID) != resourceTenant(caller.TenantID) {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceNotFound, Message: "dataset not found"}
	}
	datasetACL := datasetACLForUser(&ds, userID)
	if len(datasetACL) == 0 {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "dataset forbidden"}
	}
	if !datasetAllowedByScanSourceForCaller(ctx, caller, ds.ID, acl.PermissionDatasetRead) {
		return Dataset{}, &DatasetServiceError{Code: DatasetServiceForbidden, Message: "dataset forbidden"}
	}

	createdByDataSource := isDatasetCreatedByDataSource(ctx, ds.ID)
	installedMap := batchCheckInstalledMarketDatasets(ctx, userID, []string{ds.ID})
	cloudMap := map[string]bool{ds.ID: createdByDataSource}
	sourceTypeMap := buildDatasetSourceTypeMap([]string{ds.ID}, cloudMap, installedMap)
	out := []Dataset{s.datasetFromRow(ctx, ds, userID, datasetACL, calcDatasetStatsWithDB(ctx, s.db, ds.ID), createdByDataSource, sourceTypeMap[ds.ID], nil)}
	if err := s.addPaperStatistics(ctx, []orm.Dataset{ds}, out); err != nil {
		return Dataset{}, err
	}
	if req.IncludeConfiguration {
		var refs []learning.KnowledgeBaseCapability
		if err := s.db.WithContext(ctx).Where("dataset_id = ? AND enabled = ?", ds.ID, true).Order("display_order ASC").Find(&refs).Error; err != nil {
			return Dataset{}, err
		}
		out[0].ProfileID, _ = knowledgeExt(ds.Ext)["profile_id"].(string)
		for _, row := range refs {
			var config map[string]any
			_ = json.Unmarshal([]byte(row.SettingsJSON), &config)
			out[0].ReadingCapabilities = append(out[0].ReadingCapabilities, learning.CapabilityRef{Key: row.CapabilityKey, Version: row.CapabilityVersion, Enabled: row.Enabled, DisplayOrder: row.DisplayOrder, Settings: config})
		}
	}
	s.addAllowedActions(ctx, caller, out)
	return out[0], nil
}

func (s *DatasetCatalogService) datasetsFromRows(ctx context.Context, rows []orm.Dataset, userID string, groupIDs []string, sourceMap map[string]bool, sourceTypeMap map[string]string) []Dataset {
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
		out = append(out, s.datasetFromRow(ctx, ds, userID, datasetACL, statsMap[ds.ID], sourceMap[ds.ID], sourceTypeMap[ds.ID], liveParsers))
	}
	return out
}

func (s *DatasetCatalogService) addPaperStatistics(ctx context.Context, rows []orm.Dataset, out []Dataset) error {
	ids := []string{}
	for _, row := range rows {
		if canonicalKnowledgeScene(row.Scene) == "academic_papers" {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	views, err := paperlibrary.LoadMany(ctx, s.db, ids, "")
	if err != nil {
		return &DatasetServiceError{Code: DatasetServiceUnavailable, Message: "query paper library statistics failed", Err: err}
	}
	for i := range out {
		if view, ok := views[out[i].DatasetID]; ok {
			count, pdfCount := view.PaperCount, view.PDFAttachmentCount
			out[i].PaperCount, out[i].PDFAttachmentCount = &count, &pdfCount
			out[i].IsEmpty = count == 0
		}
	}
	return nil
}

func (s *DatasetCatalogService) datasetFromRow(ctx context.Context, ds orm.Dataset, userID string, datasetACL []string, stats datasetStats, createdByDataSource bool, sourceType string, liveParsers []ParserConfig) Dataset {
	algo := parseDatasetAlgo(ds.Ext)
	if liveParsers == nil {
		liveParsers = fetchParsersByAlgoID(ctx, algo.AlgoID)
	}
	parsers := mergeParserConfigs(parseDatasetParsers(ds.Ext), liveParsers)
	return Dataset{
		Name:  "datasets/" + ds.ID,
		Scene: canonicalKnowledgeScene(ds.Scene), ResourceVersion: ds.ResourceVersion,
		DatasetID:              ds.ID,
		DisplayName:            ds.DisplayName,
		Desc:                   ds.Desc,
		CoverImage:             ds.CoverImage,
		State:                  stateToPB(ds.DatasetState),
		IsEmpty:                stats.DocumentCount == 0,
		DocumentCount:          stats.DocumentCount,
		DocumentSize:           stats.DocumentSize,
		SegmentCount:           0,
		TokenCount:             0,
		Parsers:                parsers,
		Algo:                   algo,
		Creator:                ds.CreateUserName,
		IsOwner:                ds.CreateUserID == userID,
		CreateTime:             ds.CreatedAt,
		UpdateTime:             ds.UpdatedAt,
		Acl:                    datasetACL,
		ShareType:              shareTypeToPB(ds.ShareType),
		Type:                   datasetTypeToPB(ds.Type),
		Tags:                   parseDatasetTags(ds.Ext),
		DefaultDataset:         isDefaultDatasetForUserWithDB(ctx, s.db, userID, ds.ID),
		CreatedByDataSource:    &createdByDataSource,
		SourceType:             sourceType,
		ProcessingLevel:        effectiveProcessingLevel(ds.ProcessingLevel),
		ProcessingRevision:     ds.ProcessingRevision,
		TransitionStatus:       effectiveTransitionStatus(ds.TransitionStatus),
		ReaderFallbackAccepted: ds.ReaderFallbackAccepted,
		Capabilities:           capabilitiesForProcessingLevel(ds.ProcessingLevel),
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

func catalogAllowsAction(permissions []string, action string) bool {
	for _, permission := range permissions {
		if permission == action || permission == acl.PermissionDatasetWrite {
			return true
		}
		if action == acl.PermissionDatasetRead && permission == acl.PermissionDatasetUpload {
			return true
		}
	}
	return false
}

func canonicalKnowledgeScene(value string) string {
	scene, err := resourceScene(value)
	if err != nil {
		return value
	}
	return scene
}
