package doc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	corestore "lazymind/core/store"

	"gorm.io/gorm"
)

// DatasetService text，text。

// ----- API text：text ragservice_DatasettextDatasetServicetext.md -----

type Algo struct {
	AlgoID      string `json:"algo_id"`
	Description string `json:"description"`
	DisplayName string `json:"display_name"`
}

type ParserConfig struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
	Type   string         `json:"type"`
}

type Dataset struct {
	Name           string         `json:"name"`
	DatasetID      string         `json:"dataset_id"`
	DisplayName    string         `json:"display_name"`
	Desc           string         `json:"desc"`
	CoverImage     string         `json:"cover_image"`
	State          string         `json:"state"`
	IsEmpty        bool           `json:"is_empty"`
	DocumentCount  int64          `json:"document_count"`
	DocumentSize   int64          `json:"document_size"`
	SegmentCount   int64          `json:"segment_count"`
	TokenCount     int64          `json:"token_count"`
	Parsers        []ParserConfig `json:"parsers"`
	Algo           Algo           `json:"algo"`
	Creator        string         `json:"creator"`
	IsOwner        bool           `json:"is_owner"`
	CreateTime     time.Time      `json:"create_time"`
	UpdateTime     time.Time      `json:"update_time"`
	Acl            []string       `json:"acl"`
	ShareType      string         `json:"share_type"`
	Type           string         `json:"type"`
	Tags           []string       `json:"tags"`
	DefaultDataset bool           `json:"default_dataset"`
}

type ListAlgosResponse struct {
	Algos []Algo `json:"algos"`
}

type AllDatasetTagsResponse struct {
	Tags []string `json:"tags"`
}

type ListDatasetsResponse struct {
	Datasets      []Dataset `json:"datasets"`
	TotalSize     int32     `json:"total_size"`
	NextPageToken string    `json:"next_page_token"`
}

type SetDefaultDatasetRequest struct {
	Name string `json:"name"`
}

type UnsetDefaultDatasetRequest struct {
	Name string `json:"name"`
}

type algoListResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []struct {
			AlgoID      string `json:"algo_id"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
		} `json:"items"`
	} `json:"data"`
}

type extTags struct {
	Tags     []string       `json:"tags"`
	AlgoID   string         `json:"algo_id"`
	AlgoName string         `json:"algo_name"`
	Parsers  []ParserConfig `json:"parsers"`
}

type algoGroupInfoResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
		Active      *bool  `json:"active,omitempty"`
	} `json:"data"`
}

const (
	maxDatasetTags             = 10
	maxDatasetTagRunes         = 20
	maxDatasetDisplayNameRunes = 100
	datasetDisplayNameRule     = "dataset name supports Chinese/English, numbers, -, _, ., up to 100 characters"
)

func validateDatasetDisplayName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("dataset name is required")
	}
	if trimmed != name || utf8.RuneCountInString(trimmed) > maxDatasetDisplayNameRunes {
		return fmt.Errorf(datasetDisplayNameRule)
	}
	for _, r := range trimmed {
		if r >= '\u4e00' && r <= '\u9fa5' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '.' || r == '-' {
			continue
		}
		return fmt.Errorf(datasetDisplayNameRule)
	}
	return nil
}

func normalizeAndValidateDatasetTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return []string{}, nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := strings.TrimSpace(tag)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		if utf8.RuneCountInString(t) > maxDatasetTagRunes {
			return nil, fmt.Errorf("tag exceeds max length of %d", maxDatasetTagRunes)
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) > maxDatasetTags {
		return nil, fmt.Errorf("too many tags, max is %d", maxDatasetTags)
	}
	return out, nil
}

func parseDatasetTags(ext json.RawMessage) []string {
	if len(ext) == 0 {
		return nil
	}
	var v extTags
	if err := json.Unmarshal(ext, &v); err != nil {
		return nil
	}
	out := make([]string, 0, len(v.Tags))
	seen := map[string]struct{}{}
	for _, t := range v.Tags {
		tt := strings.TrimSpace(t)
		if tt == "" {
			continue
		}
		if _, ok := seen[tt]; ok {
			continue
		}
		seen[tt] = struct{}{}
		out = append(out, tt)
	}
	return out
}

func parseDatasetAlgo(ext json.RawMessage) Algo {
	if len(ext) == 0 {
		return Algo{}
	}
	var v extTags
	if err := json.Unmarshal(ext, &v); err != nil {
		return Algo{}
	}
	return Algo{AlgoID: strings.TrimSpace(v.AlgoID), DisplayName: strings.TrimSpace(v.AlgoName)}
}

func parseDatasetParsers(ext json.RawMessage) []ParserConfig {
	if len(ext) == 0 {
		return nil
	}
	var v extTags
	if err := json.Unmarshal(ext, &v); err != nil {
		return nil
	}
	if len(v.Parsers) == 0 {
		return nil
	}
	out := make([]ParserConfig, 0, len(v.Parsers))
	for _, p := range v.Parsers {
		out = append(out, ParserConfig{
			Name:   strings.TrimSpace(p.Name),
			Params: p.Params,
			Type:   strings.TrimSpace(p.Type),
		})
	}
	return out
}

func fetchParsersByAlgoID(ctx context.Context, algoID string) []ParserConfig {
	algoID = strings.TrimSpace(algoID)
	if algoID == "" {
		return nil
	}
	url := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/algo/"+algoID+"/groups")
	var resp algoGroupInfoResp
	if err := common.ApiGet(ctx, url, nil, &resp, 5*time.Second); err != nil {
		return nil
	}
	if resp.Code != 200 || len(resp.Data) == 0 {
		return nil
	}
	parserTypeMap := map[string]string{
		"Original Source": "PARSE_TYPE_CONVERT",
		"Chunk":           "PARSE_TYPE_SPLIT",
		"Summary":         "PARSE_TYPE_SUMMARY",
		"Image Info":      "PARSE_TYPE_IMAGE_CAPTION",
		"Question Answer": "PARSE_TYPE_QA",
	}
	out := make([]ParserConfig, 0, len(resp.Data))
	for _, item := range resp.Data {
		if item.Active != nil && !*item.Active {
			continue
		}
		parseType, ok := parserTypeMap[strings.TrimSpace(item.Type)]
		if !ok {
			continue
		}
		out = append(out, ParserConfig{
			Name:   strings.TrimSpace(item.Name),
			Params: map[string]any{},
			Type:   parseType,
		})
	}
	return out
}

func mergeParserConfigs(stored, live []ParserConfig) []ParserConfig {
	if len(stored) == 0 {
		return live
	}
	if len(live) == 0 {
		return stored
	}
	out := make([]ParserConfig, 0, len(stored)+len(live))
	seen := map[string]struct{}{}
	add := func(p ParserConfig) {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		p.Name = name
		p.Type = strings.TrimSpace(p.Type)
		out = append(out, p)
	}
	for _, p := range stored {
		add(p)
	}
	for _, p := range live {
		add(p)
	}
	return out
}

func datasetTypeToPB(t uint8) string {
	switch t {
	case 2:
		return "DATASET_TYPE_TABLE"
	case 3:
		return "DATASET_TYPE_GRAPH"
	default:
		return "DATASET_TYPE_TEXT"
	}
}

func datasetTypeFromPB(s string) uint8 {
	switch strings.TrimSpace(s) {
	case "DATASET_TYPE_TABLE":
		return 2
	case "DATASET_TYPE_GRAPH":
		return 3
	default:
		return 1
	}
}

func shareTypeToPB(_ uint8) string { return "SHARE_TYPE_UNSPECIFIED" }
func stateToPB(_ uint8) string     { return "STATE_UNSPECIFIED" }

func datasetIDFromPath(r *http.Request) string {
	raw := common.PathVar(r, "dataset")
	raw = strings.TrimPrefix(raw, "datasets/")
	raw = strings.TrimPrefix(raw, "/")
	return raw
}

func documentIDFromPath(r *http.Request) string {
	return common.PathVar(r, "document")
}

func taskIDFromPath(r *http.Request) string {
	return common.PathVar(r, "task")
}

func uploadIDFromPath(r *http.Request) string {
	return common.PathVar(r, "upload_id")
}

func uploadFileIDFromPath(r *http.Request) string {
	return common.PathVar(r, "upload_file_id")
}

func userIDFromPath(r *http.Request) string {
	return strings.TrimSpace(common.PathVar(r, "user_id"))
}

func groupIDFromPath(r *http.Request) string {
	return strings.TrimSpace(common.PathVar(r, "group_id"))
}

func datasetACLForUser(ds *orm.Dataset, userID string) []string {
	return datasetACLForUserWithGroups(ds, userID, nil)
}

func datasetACLForUserWithGroups(ds *orm.Dataset, userID string, groupIDs []string) []string {
	if ds == nil {
		return nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.TrimSpace(ds.ID) == "" {
		return nil
	}
	if strings.TrimSpace(ds.CreateUserID) == userID {
		return []string{acl.PermissionDatasetRead, acl.PermissionDatasetWrite, acl.PermissionDatasetUpload}
	}
	permissions, _ := acl.PermissionsForWithGroups(acl.ResourceTypeDB, ds.ID, userID, groupIDs)
	return permissions
}

func canAccessDataset(ds *orm.Dataset, userID string, action string) bool {
	if ds == nil {
		return false
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.TrimSpace(ds.ID) == "" {
		return false
	}
	if strings.TrimSpace(ds.CreateUserID) == userID {
		return true
	}
	return acl.Can(userID, acl.ResourceTypeDB, ds.ID, action)
}

func ListAlgos(w http.ResponseWriter, r *http.Request) {
	const listAlgosPath = "/v1/algo/list"
	algoURL := common.JoinURL(common.AlgoServiceEndpoint(), listAlgosPath)

	timeout := 5 * time.Second
	start := time.Now()
	var ar algoListResp
	if err := common.ApiGet(r.Context(), algoURL, nil, &ar, timeout); err != nil {
		log.Logger.Error().
			Err(err).
			Str("algo_url", algoURL).
			Dur("timeout", timeout).
			Dur("elapsed", time.Since(start)).
			Msg("algo service request failed")
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "algo service unavailable", err), http.StatusBadGateway)
		return
	}
	if ar.Code != 200 {
		log.Logger.Warn().
			Int("algo_service_code", ar.Code).
			Str("algo_service_msg", strings.TrimSpace(ar.Msg)).
			Str("algo_url", algoURL).
			Dur("elapsed", time.Since(start)).
			Msg("algo service returned error code")
		common.ReplyErr(w, "algo service error: "+strings.TrimSpace(ar.Msg), http.StatusBadGateway)
		return
	}

	algos := make([]Algo, 0, len(ar.Data.Items))
	for _, a := range ar.Data.Items {
		algos = append(algos, Algo{AlgoID: a.AlgoID, DisplayName: a.DisplayName, Description: a.Description})
	}
	common.ReplyJSON(w, ListAlgosResponse{Algos: algos})
}
func AllDatasetTags(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var datasets []orm.Dataset
	if err := corestore.DB().
		Select("id, create_user_id, ext").
		Where("deleted_at IS NULL").
		Find(&datasets).Error; err != nil {
		common.ReplyErr(w, "query datasets failed", http.StatusInternalServerError)
		return
	}

	groupIDs := acl.ResolveUserGroupIDs(userID)
	seen := map[string]struct{}{}
	// Keep JSON stable: return [] instead of null when empty.
	tags := make([]string, 0)
	for _, ds := range datasets {
		if len(datasetACLForUserWithGroups(&ds, userID, groupIDs)) == 0 {
			continue
		}
		for _, t := range parseDatasetTags(ds.Ext) {
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	common.ReplyJSON(w, AllDatasetTagsResponse{Tags: tags})
}
func ListDatasets(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	q := r.URL.Query()
	pageToken := strings.TrimSpace(q.Get("page_token"))
	pageSizeStr := strings.TrimSpace(q.Get("page_size"))
	orderBy := strings.TrimSpace(q.Get("order_by"))
	keyword := strings.TrimSpace(q.Get("keyword"))
	rawTags := q["tags"]

	pageSize := 20
	if pageSizeStr != "" {
		if v, err := strconv.Atoi(pageSizeStr); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if pageToken != "" {
		if v, err := parseDatasetPageToken(pageToken); err == nil && v >= 0 {
			offset = v
		}
	}

	// Parse tags (query supports tags=a,b and repeated tags params)
	tagSet := map[string]struct{}{}
	var wantTags []string
	for _, rt := range rawTags {
		for _, part := range strings.Split(rt, ",") {
			t := strings.TrimSpace(part)
			if t == "" {
				continue
			}
			if _, ok := tagSet[t]; ok {
				continue
			}
			tagSet[t] = struct{}{}
			wantTags = append(wantTags, t)
		}
	}

	db := corestore.DB()
	base := db.Model(&orm.Dataset{}).
		Where("deleted_at IS NULL")

	orderClause := "updated_at desc"
	if orderBy != "" {
		if ob, err := normalizeDatasetOrderBy(orderBy); err == nil {
			orderClause = ob
		}
	}

	groupIDs := acl.ResolveUserGroupIDs(userID)

	const fetchFactor = 5
	fetchSize := pageSize * fetchFactor
	if fetchSize < pageSize {
		fetchSize = pageSize
	}
	if fetchSize > 500 {
		fetchSize = 500
	}

	total := 0
	page := make([]orm.Dataset, 0, pageSize)
	scanOffset := 0
	hasMoreRows := true

	for hasMoreRows {
		var rows []orm.Dataset
		query := base.
			Select(`id, kb_id, create_user_id, create_user_name, display_name, "desc", cover_image, created_at, updated_at, ext, type, share_type, dataset_state`).
			Order(orderClause).
			Offset(scanOffset).
			Limit(fetchSize)
		if err := query.Find(&rows).Error; err != nil {
			common.ReplyErr(w, "query datasets failed", http.StatusInternalServerError)
			return
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
			if total >= offset && len(page) < pageSize {
				page = append(page, ds)
			}
			total++
		}
	}

	end := offset + len(page)

	out := make([]Dataset, 0, len(page))
	dsIDs := make([]string, 0, len(page))
	for _, ds := range page {
		dsIDs = append(dsIDs, ds.ID)
	}
	statsMap := calcDatasetStatsBatch(r.Context(), dsIDs)
	parserCache := map[string][]ParserConfig{}

	for _, ds := range page {
		datasetACL := datasetACLForUserWithGroups(&ds, userID, groupIDs)
		algo := parseDatasetAlgo(ds.Ext)
		liveParsers, ok := parserCache[algo.AlgoID]
		if !ok {
			liveParsers = fetchParsersByAlgoID(r.Context(), algo.AlgoID)
			parserCache[algo.AlgoID] = liveParsers
		}
		parsers := mergeParserConfigs(parseDatasetParsers(ds.Ext), liveParsers)
		stats := statsMap[ds.ID]
		out = append(out, Dataset{
			Name:           "datasets/" + ds.ID,
			DatasetID:      ds.ID,
			DisplayName:    ds.DisplayName,
			Desc:           ds.Desc,
			CoverImage:     ds.CoverImage,
			State:          stateToPB(ds.DatasetState),
			IsEmpty:        stats.DocumentCount == 0,
			DocumentCount:  stats.DocumentCount,
			DocumentSize:   stats.DocumentSize,
			SegmentCount:   0,
			TokenCount:     0,
			Parsers:        parsers,
			Algo:           algo,
			Creator:        ds.CreateUserName,
			IsOwner:        ds.CreateUserID == userID,
			CreateTime:     ds.CreatedAt,
			UpdateTime:     ds.UpdatedAt,
			Acl:            datasetACL,
			ShareType:      shareTypeToPB(ds.ShareType),
			Type:           datasetTypeToPB(ds.Type),
			Tags:           parseDatasetTags(ds.Ext),
			DefaultDataset: isDefaultDatasetForUser(r.Context(), userID, ds.ID),
		})
	}

	nextToken := ""
	if end < total {
		nextToken = encodeDatasetPageToken(end, pageSize, total)
	}
	common.ReplyJSON(w, ListDatasetsResponse{
		Datasets:      out,
		TotalSize:     int32(total),
		NextPageToken: nextToken,
	})
}

func parseDatasetPageToken(token string) (int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, nil
	}
	// Backward compatibility: old token format is plain offset integer.
	if v, err := strconv.Atoi(token); err == nil && v >= 0 {
		return v, nil
	}

	decoders := []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	}
	for _, decoder := range decoders {
		b, err := decoder.DecodeString(token)
		if err != nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(b, &payload); err != nil {
			continue
		}
		candidates := []string{"Start", "start", "offset", "Offset"}
		for _, key := range candidates {
			if raw, ok := payload[key]; ok {
				switch v := raw.(type) {
				case float64:
					if v >= 0 {
						return int(v), nil
					}
				case int:
					if v >= 0 {
						return v, nil
					}
				case string:
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
						return n, nil
					}
				}
			}
		}
	}
	return 0, errors.New("invalid page_token")
}

func encodeDatasetPageToken(start, limit, total int) string {
	payload := map[string]int{
		"Start":      start,
		"Limit":      limit,
		"TotalCount": total,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return strconv.Itoa(start)
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

func normalizeDatasetOrderBy(orderBy string) (string, error) {
	orderBy = strings.TrimSpace(orderBy)
	if orderBy == "" {
		return "", errors.New("empty")
	}
	parts := strings.Fields(orderBy)
	if len(parts) == 0 {
		return "", errors.New("empty")
	}
	field := parts[0]
	dir := "asc"
	if len(parts) > 1 {
		dir = strings.ToLower(parts[1])
	}
	if dir != "asc" && dir != "desc" {
		return "", errors.New("bad dir")
	}
	switch field {
	case "create_time", "created_at":
		return "created_at " + dir, nil
	case "update_time", "updated_at":
		return "updated_at " + dir, nil
	case "display_name":
		return "display_name " + dir, nil
	default:
		return "", errors.New("unsupported order_by")
	}
}

func containsAll(have []string, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := map[string]struct{}{}
	for _, h := range have {
		set[h] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func datasetMatchesKeyword(ds *orm.Dataset, keyword string) bool {
	if ds == nil {
		return false
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return true
	}

	if strings.Contains(strings.ToLower(ds.DisplayName), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(ds.Desc), kw) {
		return true
	}
	for _, t := range parseDatasetTags(ds.Ext) {
		if strings.Contains(strings.ToLower(strings.TrimSpace(t)), kw) {
			return true
		}
	}
	return false
}

func newDatasetID() string {
	// 16 bytes -> 32 hex chars
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("ds_%x", b[:])
}

func isDefaultDatasetForUser(ctx context.Context, userID, datasetID string) bool {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(datasetID) == "" {
		return false
	}
	var n int64
	_ = corestore.DB().WithContext(ctx).
		Model(&orm.DefaultDataset{}).
		Where("create_user_id = ? AND dataset_id = ? AND deleted_at IS NULL", userID, datasetID).
		Count(&n).Error
	return n > 0
}

func algoDatasetDisplayName(userID, displayName string) string {
	return fmt.Sprintf("user@%s@%s", strings.TrimSpace(userID), strings.TrimSpace(displayName))
}

func hasReservedDatasetDisplayNamePrefix(displayName string) bool {
	name := strings.ToLower(strings.TrimSpace(displayName))
	for _, prefix := range []string{"user@", "feishu@", "local@"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

type kbCreateRequest struct {
	KbID           string         `json:"kb_id"`
	DisplayName    string         `json:"display_name"`
	Description    *string        `json:"description,omitempty"`
	OwnerID        string         `json:"owner_id"`
	Meta           map[string]any `json:"meta,omitempty"`
	AlgoID         string         `json:"algo_id,omitempty"`
	IdempotencyKey *string        `json:"idempotency_key,omitempty"`
}

type kbUpdateRequest struct {
	DisplayName    *string        `json:"display_name,omitempty"`
	Description    *string        `json:"description,omitempty"`
	OwnerID        *string        `json:"owner_id,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
	AlgoID         *string        `json:"algo_id,omitempty"`
	IdempotencyKey *string        `json:"idempotency_key,omitempty"`
}

func CreateDataset(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	// query: dataset_id (optional)
	datasetID := strings.TrimSpace(r.URL.Query().Get("dataset_id"))
	if datasetID == "" {
		datasetID = newDatasetID()
	}

	var body Dataset
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if err := validateDatasetDisplayName(body.DisplayName); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	desc := strings.TrimSpace(body.Desc)
	cover := strings.TrimSpace(body.CoverImage)
	if hasReservedDatasetDisplayNamePrefix(displayName) {
		common.ReplyErr(w, "dataset name uses reserved prefix", http.StatusBadRequest)
		return
	}
	normalizedTags, err := normalizeAndValidateDatasetTags(body.Tags)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Tags = normalizedTags
	// Provide explicit feedback for duplicate dataset names under the same user.
	var existed int64
	if err := corestore.DB().
		Model(&orm.Dataset{}).
		Where("create_user_id = ? AND display_name = ? AND deleted_at IS NULL", userID, displayName).
		Count(&existed).Error; err != nil {
		common.ReplyErr(w, "query datasets failed", http.StatusInternalServerError)
		return
	}
	if existed > 0 {
		common.ReplyErr(w, "dataset name already exists", http.StatusConflict)
		return
	}

	algoID := strings.TrimSpace(body.Algo.AlgoID)

	// 1) text POST /v1/kbs Create KB
	const createKBPath = "/v1/kbs"
	kbURL := common.JoinURL(common.AlgoServiceEndpoint(), createKBPath)

	req := kbCreateRequest{
		KbID:        datasetID,
		DisplayName: algoDatasetDisplayName(userID, displayName),
		OwnerID:     userID,
		AlgoID:      algoID, // omitempty: not sent when empty; DocServer will not bind any algo
		Meta:        map[string]any{"tags": body.Tags},
	}
	if desc != "" {
		req.Description = &desc
	}

	kbTimeout := 10 * time.Second
	kbStart := time.Now()
	// Accept flexible response shapes; we only need kb_id if provided.
	var kbResp map[string]any
	if err := common.ApiPost(r.Context(), kbURL, req, nil, &kbResp, kbTimeout); err != nil {
		log.Logger.Error().
			Err(err).
			Str("kb_url", kbURL).
			Str("kb_id", datasetID).
			Str("dataset_id", datasetID).
			Str("user_id", userID).
			Str("algo_id", algoID).
			Dur("timeout", kbTimeout).
			Dur("elapsed", time.Since(kbStart)).
			Msg("kb service create failed")
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "kb service create failed", err), http.StatusBadGateway)
		return
	}

	// Prefer kb_id returned by external service; fall back to datasetID.
	kbID := datasetID
	if v, ok := kbResp["kb_id"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			kbID = strings.TrimSpace(s)
		}
	}
	// Some services wrap data as { data: { kb_id: ... } }.
	if v, ok := kbResp["data"]; ok && kbID == datasetID {
		if m, ok := v.(map[string]any); ok {
			if vv, ok := m["kb_id"]; ok {
				if s, ok := vv.(string); ok && strings.TrimSpace(s) != "" {
					kbID = strings.TrimSpace(s)
				}
			}
		}
	}
	log.Logger.Info().
		Str("kb_url", kbURL).
		Str("kb_id", kbID).
		Str("dataset_id", datasetID).
		Str("user_id", userID).
		Str("algo_id", algoID).
		Dur("elapsed", time.Since(kbStart)).
		Msg("kb service create ok")

	// 2) text datasets（text kb_id）
	now := time.Now().UTC()
	parsers := fetchParsersByAlgoID(r.Context(), algoID)
	extBytes, _ := json.Marshal(map[string]any{
		"tags":      body.Tags,
		"algo_id":   algoID,
		"algo_name": body.Algo.DisplayName,
		"parsers":   parsers,
	})

	ds := orm.Dataset{
		ID:          datasetID,
		KbID:        kbID,
		DisplayName: displayName,
		Desc:        desc,
		CoverImage:  cover,

		// text not null，textDefaulttext（text ragservice text）。
		ResourceUID:            datasetID,
		BucketName:             "",
		OssPath:                "",
		DatasetInfo:            json.RawMessage(`{}`),
		DatasetState:           0,
		EmbeddingModel:         "default",
		EmbeddingModelProvider: "default",
		ShareType:              0,
		TenantID:               "",
		IsDemonstrate:          false,
		Type:                   uint8(1),
		Ext:                    extBytes,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}

	if err := corestore.DB().WithContext(context.Background()).Create(&ds).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "create dataset failed", err), http.StatusInternalServerError)
		return
	}
	if st := acl.GetStore(); st != nil {
		st.EnsureKB(kbID, displayName, userID)
		ensureDatasetCreatorMember(st, datasetID, userID)
	}

	common.ReplyJSON(w, Dataset{
		Name:           "datasets/" + ds.ID,
		DatasetID:      ds.ID,
		DisplayName:    ds.DisplayName,
		Desc:           ds.Desc,
		CoverImage:     ds.CoverImage,
		State:          "STATE_UNSPECIFIED",
		IsEmpty:        true,
		DocumentCount:  0,
		DocumentSize:   0,
		SegmentCount:   0,
		TokenCount:     0,
		Parsers:        parsers,
		Algo:           Algo{AlgoID: algoID, DisplayName: body.Algo.DisplayName, Description: body.Algo.Description},
		Creator:        userName,
		IsOwner:        true,
		CreateTime:     ds.CreatedAt,
		UpdateTime:     ds.UpdatedAt,
		Acl:            []string{acl.PermissionDatasetRead, acl.PermissionDatasetWrite, acl.PermissionDatasetUpload},
		ShareType:      "SHARE_TYPE_UNSPECIFIED",
		Type:           datasetTypeToPB(ds.Type),
		Tags:           body.Tags,
		DefaultDataset: false,
	})
}
func GetDataset(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	datasetID := datasetIDFromPath(r)
	log.Logger.Info().Str("dataset_id", datasetID).Str("user_id", userID).Msg("get dataset requested")
	if datasetID == "" {
		common.ReplyErr(w, "invalid dataset id", http.StatusBadRequest)
		return
	}

	var ds orm.Dataset
	if err := corestore.DB().
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	datasetACL := datasetACLForUser(&ds, userID)
	if len(datasetACL) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	algo := parseDatasetAlgo(ds.Ext)
	parsers := mergeParserConfigs(parseDatasetParsers(ds.Ext), fetchParsersByAlgoID(r.Context(), algo.AlgoID))
	stats := calcDatasetStats(r.Context(), ds.ID)
	common.ReplyJSON(w, Dataset{
		Name:           "datasets/" + ds.ID,
		DatasetID:      ds.ID,
		DisplayName:    ds.DisplayName,
		Desc:           ds.Desc,
		CoverImage:     ds.CoverImage,
		State:          stateToPB(ds.DatasetState),
		IsEmpty:        stats.DocumentCount == 0,
		DocumentCount:  stats.DocumentCount,
		DocumentSize:   stats.DocumentSize,
		SegmentCount:   0,
		TokenCount:     0,
		Parsers:        parsers,
		Algo:           algo,
		Creator:        ds.CreateUserName,
		IsOwner:        ds.CreateUserID == userID,
		CreateTime:     ds.CreatedAt,
		UpdateTime:     ds.UpdatedAt,
		Acl:            datasetACL,
		ShareType:      shareTypeToPB(ds.ShareType),
		Type:           datasetTypeToPB(ds.Type),
		Tags:           parseDatasetTags(ds.Ext),
		DefaultDataset: isDefaultDatasetForUser(r.Context(), userID, ds.ID),
	})
}
func DeleteDataset(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "invalid dataset id", http.StatusBadRequest)
		return
	}

	var ds orm.Dataset
	if err := corestore.DB().
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	if !canAccessDataset(&ds, userID, acl.PermRead) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	if err := deleteScanSourceForDataset(r.Context(), datasetID); err != nil {
		log.Logger.Error().
			Err(err).
			Str("dataset_id", datasetID).
			Str("user_id", userID).
			Msg("scan source delete failed")
		common.ReplyErr(w, "delete scan source failed", http.StatusBadGateway)
		return
	}

	// 1) text DELETE /v1/kbs/{kb_id}
	kbID := ds.KbID
	if strings.TrimSpace(kbID) == "" {
		kbID = ds.ID
	}
	kbURL := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs/"+kbID)
	kbTimeout := 10 * time.Second
	kbStart := time.Now()
	if err := common.ApiDelete(r.Context(), kbURL, nil, nil, kbTimeout); err != nil {
		log.Logger.Error().
			Err(err).
			Str("kb_url", kbURL).
			Str("kb_id", kbID).
			Str("dataset_id", datasetID).
			Str("user_id", userID).
			Dur("timeout", kbTimeout).
			Dur("elapsed", time.Since(kbStart)).
			Msg("kb service delete failed")
		common.ReplyErr(w, "external delete failed", http.StatusBadGateway)
		return
	}
	log.Logger.Info().
		Str("kb_url", kbURL).
		Str("kb_id", kbID).
		Str("dataset_id", datasetID).
		Str("user_id", userID).
		Dur("elapsed", time.Since(kbStart)).
		Msg("kb service delete ok")

	// 2) text datasets
	now := time.Now().UTC()
	if err := corestore.DB().Transaction(func(tx *gorm.DB) error {
		ds.DeletedAt = &now
		ds.UpdatedAt = now
		if err := tx.Save(&ds).Error; err != nil {
			return err
		}
		if err := cleanupEvalSetDatasetReferences(r.Context(), tx, datasetID, now); err != nil {
			return err
		}
		return tx.
			Where("create_user_id = ? AND dataset_id = ?", userID, datasetID).
			Delete(&orm.DefaultDataset{}).Error
	}); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "delete dataset failed", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func deleteScanSourceForDataset(ctx context.Context, datasetID string) error {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil
	}
	scanURL := common.JoinURL(common.ScanControlPlaneEndpoint(), "/api/scan/internal/sources/by-dataset/"+url.PathEscape(datasetID))
	if err := common.ApiDelete(ctx, scanURL, nil, nil, 10*time.Second); err != nil {
		var httpErr *common.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

func cleanupEvalSetDatasetReferences(ctx context.Context, tx *gorm.DB, datasetID string, now time.Time) error {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil
	}

	var rows []orm.EvalSet
	if err := tx.WithContext(ctx).Select("id", "dataset_ids").Find(&rows).Error; err != nil {
		return err
	}

	for _, row := range rows {
		datasetIDs, changed := removeDatasetIDFromEvalSet(row.DatasetIDs, datasetID)
		if !changed {
			continue
		}
		raw, err := json.Marshal(datasetIDs)
		if err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&orm.EvalSet{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{
				"dataset_ids": json.RawMessage(raw),
				"updated_at":  now,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

func removeDatasetIDFromEvalSet(raw json.RawMessage, datasetID string) ([]string, bool) {
	var ids []string
	if len(raw) == 0 {
		return nil, false
	}
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, false
	}

	out := make([]string, 0, len(ids))
	changed := false
	seen := map[string]struct{}{}
	for _, item := range ids {
		id := strings.TrimSpace(item)
		if id == "" {
			changed = true
			continue
		}
		if id == datasetID {
			changed = true
			continue
		}
		if _, ok := seen[id]; ok {
			changed = true
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, changed
}

func UpdateDataset(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "invalid dataset id", http.StatusBadRequest)
		return
	}

	var body Dataset
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}

	var ds orm.Dataset
	if err := corestore.DB().
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	if !canAccessDataset(&ds, userID, acl.PermRead) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	newDisplay := strings.TrimSpace(body.DisplayName)
	newDesc := strings.TrimSpace(body.Desc)
	newCover := strings.TrimSpace(body.CoverImage)
	if newDisplay == "" {
		newDisplay = ds.DisplayName
	} else if err := validateDatasetDisplayName(body.DisplayName); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if hasReservedDatasetDisplayNamePrefix(newDisplay) {
		common.ReplyErr(w, "dataset name uses reserved prefix", http.StatusBadRequest)
		return
	}
	if newDesc == "" {
		newDesc = ds.Desc
	}
	if newCover == "" {
		newCover = ds.CoverImage
	}
	normalizedTags, err := normalizeAndValidateDatasetTags(body.Tags)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Tags = normalizedTags

	// Update ext: tags / algo
	algo := parseDatasetAlgo(ds.Ext)
	// newAlgoID is only non-empty when the caller explicitly requests an algo change.
	newAlgoID := strings.TrimSpace(body.Algo.AlgoID)
	algoID := firstNonEmpty(newAlgoID, algo.AlgoID)
	algoName := strings.TrimSpace(body.Algo.DisplayName)
	if algoName == "" {
		algoName = algo.DisplayName
	}
	parsers := body.Parsers
	if len(parsers) == 0 {
		parsers = fetchParsersByAlgoID(r.Context(), algoID)
	}
	extBytes, _ := json.Marshal(map[string]any{
		"tags":      body.Tags,
		"algo_id":   algoID,
		"algo_name": algoName,
		"parsers":   parsers,
	})

	// 1) text POST /v1/kbs/{kb_id}
	kbID := ds.KbID
	if strings.TrimSpace(kbID) == "" {
		kbID = ds.ID
	}
	kbURL := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/kbs/"+kbID)
	extMeta := map[string]any{"tags": body.Tags}
	algoDisplayName := algoDatasetDisplayName(userID, newDisplay)
	req := kbUpdateRequest{
		DisplayName: &algoDisplayName,
		Description: &newDesc,
		OwnerID:     &userID,
		Meta:        extMeta,
	}
	// Only forward algo_id to DocServer when the caller explicitly changes it;
	// this avoids re-binding an algo that was already bound.
	if newAlgoID != "" {
		req.AlgoID = &newAlgoID
	}
	kbTimeout := 10 * time.Second
	kbStart := time.Now()
	if err := common.ApiPost(r.Context(), kbURL, req, nil, nil, kbTimeout); err != nil {
		log.Logger.Error().
			Err(err).
			Str("kb_url", kbURL).
			Str("kb_id", kbID).
			Str("dataset_id", datasetID).
			Str("user_id", userID).
			Str("algo_id", algoID).
			Dur("timeout", kbTimeout).
			Dur("elapsed", time.Since(kbStart)).
			Msg("kb service update failed")
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "kb service update failed", err), http.StatusBadGateway)
		return
	}
	log.Logger.Info().
		Str("kb_url", kbURL).
		Str("kb_id", kbID).
		Str("dataset_id", datasetID).
		Str("user_id", userID).
		Str("algo_id", algoID).
		Dur("elapsed", time.Since(kbStart)).
		Msg("kb service update ok")

	now := time.Now().UTC()
	ds.DisplayName = newDisplay
	ds.Desc = newDesc
	ds.CoverImage = newCover
	ds.Ext = extBytes
	ds.UpdatedAt = now
	ds.CreateUserName = userName

	if err := corestore.DB().Save(&ds).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "update dataset failed", err), http.StatusInternalServerError)
		return
	}

	datasetACL := datasetACLForUser(&ds, userID)
	stats := calcDatasetStats(r.Context(), ds.ID)
	common.ReplyJSON(w, Dataset{
		Name:           "datasets/" + ds.ID,
		DatasetID:      ds.ID,
		DisplayName:    ds.DisplayName,
		Desc:           ds.Desc,
		CoverImage:     ds.CoverImage,
		State:          stateToPB(ds.DatasetState),
		IsEmpty:        stats.DocumentCount == 0,
		DocumentCount:  stats.DocumentCount,
		DocumentSize:   stats.DocumentSize,
		SegmentCount:   0,
		TokenCount:     0,
		Parsers:        parseDatasetParsers(ds.Ext),
		Algo:           parseDatasetAlgo(ds.Ext),
		Creator:        ds.CreateUserName,
		CreateTime:     ds.CreatedAt,
		UpdateTime:     ds.UpdatedAt,
		Acl:            datasetACL,
		ShareType:      shareTypeToPB(ds.ShareType),
		Type:           datasetTypeToPB(ds.Type),
		Tags:           parseDatasetTags(ds.Ext),
		DefaultDataset: isDefaultDatasetForUser(r.Context(), userID, ds.ID),
	})
}
func SetDefault(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "invalid dataset id", http.StatusBadRequest)
		return
	}
	var body SetDefaultDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		common.ReplyErr(w, "name required", http.StatusBadRequest)
		return
	}

	// text dataset textUsertextPermission
	var ds orm.Dataset
	if err := corestore.DB().
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	if !canAccessDataset(&ds, userID, acl.PermRead) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	now := time.Now().UTC()
	row := orm.DefaultDataset{
		DatasetID:   datasetID,
		DatasetName: body.Name,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	// upsert: delete old then insert (text，text DB Upsert text)
	_ = corestore.DB().
		Where("create_user_id = ? AND dataset_id = ?", userID, datasetID).
		Delete(&orm.DefaultDataset{}).Error
	if err := corestore.DB().Create(&row).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "set default failed", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
func UnsetDefault(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "invalid dataset id", http.StatusBadRequest)
		return
	}
	var body UnsetDefaultDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		common.ReplyErr(w, "name required", http.StatusBadRequest)
		return
	}

	var ds orm.Dataset
	if err := corestore.DB().
		Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&ds).Error; err != nil {
		common.ReplyErr(w, "dataset not found", http.StatusNotFound)
		return
	}
	if !canAccessDataset(&ds, userID, acl.PermRead) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(common.ForbiddenBody))
		return
	}

	if err := corestore.DB().
		Where("create_user_id = ? AND dataset_id = ?", userID, datasetID).
		Delete(&orm.DefaultDataset{}).Error; err != nil {
		common.ReplyErr(w, "unset default failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type datasetStats struct {
	DocumentCount int64
	DocumentSize  int64
}

// calcDatasetStats calculates the number of files and the total size under a dataset
// (excluding folder-like documents).
// file_size is stored in document.ext (JSON); we aggregate it in memory in Go.
func calcDatasetStats(ctx context.Context, datasetID string) datasetStats {
	var docs []orm.Document
	if err := corestore.DB().WithContext(ctx).
		Select("ext").
		Where("dataset_id = ? AND deleted_at IS NULL", datasetID).
		Find(&docs).Error; err != nil {
		return datasetStats{}
	}
	var count, size int64
	for _, d := range docs {
		if isFolderLikeDocument(d) {
			continue
		}
		var ext documentExt
		_ = json.Unmarshal(d.Ext, &ext)
		count++
		size += ext.FileSize
	}
	return datasetStats{DocumentCount: count, DocumentSize: size}
}

// calcDatasetStatsBatch calculates stats for multiple datasets in one query to avoid N+1.
func calcDatasetStatsBatch(ctx context.Context, datasetIDs []string) map[string]datasetStats {
	if len(datasetIDs) == 0 {
		return map[string]datasetStats{}
	}
	var docs []orm.Document
	if err := corestore.DB().WithContext(ctx).
		Select("dataset_id, ext").
		Where("dataset_id IN ? AND deleted_at IS NULL", datasetIDs).
		Find(&docs).Error; err != nil {
		return map[string]datasetStats{}
	}
	result := make(map[string]datasetStats, len(datasetIDs))
	for _, d := range docs {
		if isFolderLikeDocument(d) {
			continue
		}
		var ext documentExt
		_ = json.Unmarshal(d.Ext, &ext)
		s := result[d.DatasetID]
		s.DocumentCount++
		s.DocumentSize += ext.FileSize
		result[d.DatasetID] = s
	}
	return result
}
