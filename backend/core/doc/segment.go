package doc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

type SegmentItem struct {
	Name                      *string        `json:"name"`
	SegmentID                 string         `json:"segment_id"`
	DatasetID                 string         `json:"dataset_id"`
	DocumentID                string         `json:"document_id"`
	DisplayContent            string         `json:"display_content"`
	Content                   string         `json:"content"`
	Text                      string         `json:"text"`
	Number                    int32          `json:"number"`
	IsActive                  *bool          `json:"is_active"`
	ImageKeys                 []string       `json:"image_keys"`
	Meta                      *string        `json:"meta"`
	Answer                    string         `json:"answer"`
	TokenCount                *int32         `json:"token_count"`
	Metadata                  map[string]any `json:"metadata"`
	Words                     *int32         `json:"words"`
	SegmentType               any            `json:"segment_type"`
	TableContent              *string        `json:"table_content"`
	ImageKey                  *string        `json:"image_key"`
	ImageURI                  *string        `json:"image_uri"`
	DisplayType               any            `json:"display_type"`
	StructuredData            any            `json:"structured_data"`
	ExcludedEmbedMetadataKeys []string       `json:"excluded_embed_metadata_keys"`
	ExcludedLLMMetadataKeys   []string       `json:"excluded_llm_metadata_keys"`
	Parent                    *string        `json:"parent"`
	Children                  map[string]any `json:"children"`
	GlobalMeta                *string        `json:"global_meta"`
	Group                     *string        `json:"group"`
	Enabled                   *bool          `json:"enabled"`
	Status                    *string        `json:"status"`
	CreateTime                *string        `json:"create_time"`
	UpdateTime                *string        `json:"update_time"`
}

type ListSegmentsResponse struct {
	Segments      []SegmentItem `json:"segments"`
	TotalSize     int32         `json:"total_size"`
	NextPageToken string        `json:"next_page_token,omitempty"`
}

type segmentSearchInput struct {
	PageToken string `json:"page_token,omitempty"`
	PageSize  int    `json:"page_size,omitempty"`
	Group     string `json:"group,omitempty"`
}

func ListSegments(w http.ResponseWriter, r *http.Request) {
	datasetID, documentID, lazyDocID, algoID, group, ok := prepareSegmentRequest(w, r, "ListSegments", nil)
	if !ok {
		return
	}
	pageSize := parseSegmentPageSize(r, nil)
	page, err := parseSegmentPage(r, nil, pageSize)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
		return
	}
	raw, queryURL, err := fetchChunksPage(r, datasetID, documentID, lazyDocID, algoID, group, page, pageSize, "ListSegments")
	if err != nil {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	segments, totalSize, nextPageToken := parseChunkSearchResponse(datasetID, documentID, raw, page, pageSize)
	log.Logger.Info().
		Str("handler", "ListSegments").
		Str("dataset_id", datasetID).
		Str("document_id", documentID).
		Str("lazyllm_doc_id", lazyDocID).
		Str("algo_id", strings.TrimSpace(algoID)).
		Str("group", strings.TrimSpace(group)).
		Str("external_url", queryURL).
		Int("page", page).
		Int("page_size", pageSize).
		Int("segments_count", len(segments)).
		Int32("total_size", totalSize).
		Str("next_page_token", strings.TrimSpace(nextPageToken)).
		Msg("external chunks list request succeeded")
	common.ReplyJSON(w, ListSegmentsResponse{Segments: segments, TotalSize: totalSize, NextPageToken: nextPageToken})
}

// SearchSegments shares the same downstream query with ListSegments. Fields like
// keyword/order_by will be forwarded once supported by the downstream service.
func SearchSegments(w http.ResponseWriter, r *http.Request) {
	body := parseSegmentSearchInput(r)
	datasetID, documentID, lazyDocID, algoID, group, ok := prepareSegmentRequest(w, r, "SearchSegments", body)
	if !ok {
		return
	}
	pageSize := parseSegmentPageSize(r, body)
	page, err := parseSegmentPage(r, body, pageSize)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
		return
	}
	raw, _, err := fetchChunksPage(r, datasetID, documentID, lazyDocID, algoID, group, page, pageSize, "SearchSegments")
	if err != nil {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	segments, totalSize, nextPageToken := parseChunkSearchResponse(datasetID, documentID, raw, page, pageSize)
	common.ReplyJSON(w, ListSegmentsResponse{Segments: segments, TotalSize: totalSize, NextPageToken: nextPageToken})
}

func GetSegment(w http.ResponseWriter, r *http.Request) {
	datasetID, documentID, lazyDocID, algoID, group, ok := prepareSegmentRequest(w, r, "GetSegment", nil)
	if !ok {
		return
	}
	segmentID := strings.TrimSpace(common.PathVar(r, "segment"))
	if segmentID == "" {
		common.ReplyErr(w, "missing segment", http.StatusBadRequest)
		return
	}
	segment, found, err := fetchSegmentByID(r, datasetID, documentID, lazyDocID, algoID, group, segmentID)
	if err != nil {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	if !found {
		common.ReplyErr(w, "segment not found", http.StatusNotFound)
		return
	}
	common.ReplyJSON(w, segment)
}
func parseSegmentPageSize(r *http.Request, body *segmentSearchInput) int {
	if body != nil && body.PageSize > 0 {
		if body.PageSize > 1000 {
			return 1000
		}
		return body.PageSize
	}
	pageSize := firstPositiveQueryInt(r, 20, "page_size", "pageSize", "size", "limit")
	if pageSize > 1000 {
		return 1000
	}
	return pageSize
}

func parseSegmentPage(r *http.Request, body *segmentSearchInput, pageSize int) (int, error) {
	resolvePageFromToken := func(token string) (int, error) {
		token = strings.TrimSpace(token)
		if token == "" {
			return 0, nil
		}
		if v, err := strconv.Atoi(token); err == nil && v > 0 {
			return v, nil
		}
		offset, err := parseDatasetPageToken(token)
		if err != nil {
			return 0, err
		}
		if pageSize <= 0 {
			pageSize = 20
		}
		return offset/pageSize + 1, nil
	}

	if body != nil {
		if page, err := resolvePageFromToken(body.PageToken); err != nil {
			return 0, err
		} else if page > 0 {
			return page, nil
		}
	}
	if token := firstNonEmptyQuery(r, "page_token", "pageToken", "cursor", "offset_token"); token != "" {
		page, err := resolvePageFromToken(token)
		if err != nil {
			return 0, err
		}
		if page > 0 {
			return page, nil
		}
	}
	return firstPositiveQueryInt(r, 1, "page", "page_no", "pageNo"), nil
}

func parseSegmentGroup(r *http.Request, body *segmentSearchInput) string {
	if body != nil {
		if group := strings.TrimSpace(body.Group); group != "" {
			return group
		}
	}
	if group := firstNonEmptyQuery(r, "group", "group_name", "groupName"); group != "" {
		return group
	}
	return ""
}

func parseSegmentSearchInput(r *http.Request) *segmentSearchInput {
	if r == nil || r.Body == nil {
		return nil
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		return nil
	}
	var body segmentSearchInput
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil
	}
	return &body
}

func firstNonEmptyQuery(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(r.URL.Query().Get(key)); v != "" {
			return v
		}
	}
	return ""
}

func firstPositiveQueryInt(r *http.Request, fallback int, keys ...string) int {
	for _, key := range keys {
		v := strings.TrimSpace(r.URL.Query().Get(key))
		if v == "" {
			continue
		}
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func resolveSegmentGroup(r *http.Request, algoID string, body *segmentSearchInput) string {
	if group := parseSegmentGroup(r, body); group != "" {
		log.Logger.Info().
			Str("handler", "SearchSegments").
			Str("algo_id", strings.TrimSpace(algoID)).
			Str("group", strings.TrimSpace(group)).
			Msg("segment group resolved from request")
		return group
	}
	if group := fetchChunkGroupName(r, algoID); group != "" {
		log.Logger.Info().
			Str("handler", "SearchSegments").
			Str("algo_id", strings.TrimSpace(algoID)).
			Str("group", strings.TrimSpace(group)).
			Msg("segment group resolved from algo groups")
		return group
	}
	log.Logger.Warn().
		Str("handler", "SearchSegments").
		Str("algo_id", strings.TrimSpace(algoID)).
		Str("group", "Chunk").
		Msg("segment group fallback to default")
	return "Chunk"
}

func fetchChunkGroupName(r *http.Request, algoID string) string {
	algoID = strings.TrimSpace(algoID)
	if algoID == "" {
		log.Logger.Warn().
			Str("handler", "SearchSegments").
			Msg("fetch chunk group skipped because algo_id is empty")
		return ""
	}
	url := common.JoinURL(common.AlgoServiceEndpoint(), "/v1/algo/"+algoID+"/groups")
	log.Logger.Info().
		Str("handler", "SearchSegments").
		Str("algo_id", algoID).
		Str("external_url", url).
		Msg("calling external algo groups request")
	var resp algoGroupInfoResp
	if err := common.ApiGet(r.Context(), url, nil, &resp, 5_000_000_000); err != nil {
		log.Logger.Error().
			Err(err).
			Str("handler", "SearchSegments").
			Str("algo_id", algoID).
			Str("external_url", url).
			Msg("external algo groups request failed")
		return ""
	}
	if resp.Code != 200 || len(resp.Data) == 0 {
		log.Logger.Warn().
			Str("handler", "SearchSegments").
			Str("algo_id", algoID).
			Str("external_url", url).
			Int("algo_service_code", resp.Code).
			Str("algo_service_msg", strings.TrimSpace(resp.Msg)).
			Any("response_data", resp.Data).
			Msg("external algo groups response is empty or unsuccessful")
		return ""
	}
	for _, item := range resp.Data {
		if strings.EqualFold(strings.TrimSpace(item.Type), "Chunk") && strings.TrimSpace(item.Name) != "" {
			resolved := strings.TrimSpace(item.Name)
			log.Logger.Info().
				Str("handler", "SearchSegments").
				Str("algo_id", algoID).
				Str("external_url", url).
				Str("group", resolved).
				Str("display_name", strings.TrimSpace(item.DisplayName)).
				Msg("resolved chunk group from algo groups response")
			return resolved
		}
	}
	log.Logger.Warn().
		Str("handler", "SearchSegments").
		Str("algo_id", algoID).
		Str("external_url", url).
		Any("response_data", resp.Data).
		Msg("chunk group not found in algo groups response")
	return ""
}

// buildChunksURL constructs the /v1/chunks query URL.
// algoID is now optional: when non-empty it is forwarded as a hint so DocServer
// can use the algo-specific retriever; when empty DocServer resolves the algo
// automatically via _find_algo_for_group (node-group refactor).
func buildChunksURL(kbID, algoID, lazyDocID, group string, page, pageSize int) string {
	params := url.Values{}
	params.Set("kb_id", kbID)
	params.Set("doc_id", lazyDocID)
	params.Set("group", firstNonEmpty(group, "Chunk"))
	if strings.TrimSpace(algoID) != "" {
		params.Set("algo_id", algoID)
	}
	params.Set("page", strconv.Itoa(page))
	params.Set("page_size", strconv.Itoa(pageSize))
	return common.JoinURL(common.AlgoServiceEndpoint(), "/v1/chunks") + "?" + params.Encode()
}

func buildParserChunksURL(kbID, algoID, lazyDocID, group string, page, pageSize int) string {
	params := url.Values{}
	params.Set("kb_id", kbID)
	params.Set("doc_id", lazyDocID)
	params.Set("group", firstNonEmpty(group, "Chunk"))
	if strings.TrimSpace(algoID) != "" {
		params.Set("algo_id", algoID)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	params.Set("offset", strconv.Itoa((page-1)*pageSize))
	params.Set("page_size", strconv.Itoa(pageSize))
	return common.JoinURL(common.ParsingServiceEndpoint(), "/doc/chunks") + "?" + params.Encode()
}

func prepareSegmentRequest(w http.ResponseWriter, r *http.Request, handler string, body *segmentSearchInput) (datasetID, documentID, lazyDocID, algoID, group string, ok bool) {
	datasetID = datasetIDFromPath(r)
	documentID = documentIDFromPath(r)
	if datasetID == "" || documentID == "" {
		common.ReplyErr(w, "missing dataset or document", http.StatusBadRequest)
		return "", "", "", "", "", false
	}
	if _, userID, allowed := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !allowed {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return "", "", "", "", "", false
	}
	var docRow orm.Document
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Take(&docRow).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "document not found", err), http.StatusNotFound)
		return "", "", "", "", "", false
	}
	lazyDocID = strings.TrimSpace(docRow.LazyllmDocID)
	algoID = datasetAlgoIDByID(datasetID)
	group = resolveSegmentGroup(r, algoID, body)
	log.Logger.Info().
		Str("handler", handler).
		Str("dataset_id", datasetID).
		Str("document_id", documentID).
		Str("lazyllm_doc_id", lazyDocID).
		Str("algo_id", strings.TrimSpace(algoID)).
		Str("group", strings.TrimSpace(group)).
		Any("request_body", body).
		Msg("prepared segment request context")
	return datasetID, documentID, lazyDocID, algoID, group, true
}

func fetchChunksPage(r *http.Request, datasetID, documentID, lazyDocID, algoID, group string, page, pageSize int, handler string) (map[string]any, string, error) {
	if strings.TrimSpace(lazyDocID) == "" {
		return map[string]any{"items": []any{}, "total_size": 0}, "", nil
	}
	queryURL := buildChunksURL(datasetID, algoID, lazyDocID, group, page, pageSize)
	log.Logger.Info().
		Str("handler", handler).
		Str("dataset_id", datasetID).
		Str("document_id", documentID).
		Str("lazyllm_doc_id", lazyDocID).
		Str("algo_id", strings.TrimSpace(algoID)).
		Str("group", strings.TrimSpace(group)).
		Int("page", page).
		Int("page_size", pageSize).
		Str("external_url", queryURL).
		Msg("calling external chunks request")
	var raw map[string]any
	if err := common.ApiGet(r.Context(), queryURL, nil, &raw, 10_000_000_000); err != nil {
		log.Logger.Error().
			Err(err).
			Str("handler", handler).
			Str("dataset_id", datasetID).
			Str("document_id", documentID).
			Str("lazyllm_doc_id", lazyDocID).
			Str("algo_id", strings.TrimSpace(algoID)).
			Str("group", strings.TrimSpace(group)).
			Str("external_url", queryURL).
			Msg("external chunks request failed")
		fallbackURL := buildParserChunksURL(datasetID, algoID, lazyDocID, group, page, pageSize)
		log.Logger.Warn().
			Err(err).
			Str("handler", handler).
			Str("dataset_id", datasetID).
			Str("document_id", documentID).
			Str("lazyllm_doc_id", lazyDocID).
			Str("algo_id", strings.TrimSpace(algoID)).
			Str("group", strings.TrimSpace(group)).
			Str("external_url", fallbackURL).
			Str("failed_external_url", queryURL).
			Msg("falling back to parser chunks request")
		if fallbackErr := common.ApiGet(r.Context(), fallbackURL, nil, &raw, 10_000_000_000); fallbackErr != nil {
			log.Logger.Error().
				Err(fallbackErr).
				Str("handler", handler).
				Str("dataset_id", datasetID).
				Str("document_id", documentID).
				Str("lazyllm_doc_id", lazyDocID).
				Str("algo_id", strings.TrimSpace(algoID)).
				Str("group", strings.TrimSpace(group)).
				Str("external_url", fallbackURL).
				Str("failed_external_url", queryURL).
				Msg("fallback parser chunks request failed")
			return nil, fallbackURL, fmt.Errorf("%w; fallback parser chunks failed: %v", err, fallbackErr)
		}
		return raw, fallbackURL, nil
	}
	return raw, queryURL, nil
}

func fetchSegmentByID(r *http.Request, datasetID, documentID, lazyDocID, algoID, group, segmentID string) (SegmentItem, bool, error) {
	if strings.TrimSpace(lazyDocID) == "" {
		return SegmentItem{}, false, nil
	}
	page := 1
	pageSize := 100
	for {
		raw, _, err := fetchChunksPage(r, datasetID, documentID, lazyDocID, algoID, group, page, pageSize, "GetSegment")
		if err != nil {
			return SegmentItem{}, false, err
		}
		items := extractChunkItems(raw)
		if len(items) == 0 {
			return SegmentItem{}, false, nil
		}
		for _, item := range items {
			segment := mapChunkToSegment(datasetID, documentID, item)
			if strings.TrimSpace(segment.SegmentID) == segmentID {
				return segment, true, nil
			}
		}
		total := extractChunkTotal(raw, len(items))
		if int(total) <= page*pageSize || len(items) < pageSize {
			break
		}
		page++
	}
	return SegmentItem{}, false, nil
}

func parseChunkSearchResponse(datasetID, documentID string, raw map[string]any, page, pageSize int) ([]SegmentItem, int32, string) {
	items := extractChunkItems(raw)
	segments := make([]SegmentItem, 0, len(items))
	for _, item := range items {
		segments = append(segments, mapChunkToSegment(datasetID, documentID, item))
	}
	total := extractChunkTotal(raw, len(segments))
	nextPageToken := ""
	if int(total) > page*pageSize {
		nextPageToken = encodeDatasetPageToken(page*pageSize, pageSize, int(total))
	}
	return segments, total, nextPageToken
}

func extractChunkItems(raw map[string]any) []map[string]any {
	candidates := []any{raw["data"], raw["items"], raw["chunks"], raw["list"]}
	for _, candidate := range candidates {
		if items := toMapSlice(candidate); len(items) > 0 {
			return items
		}
		if m, ok := candidate.(map[string]any); ok {
			for _, key := range []string{"items", "chunks", "list", "data"} {
				if items := toMapSlice(m[key]); len(items) > 0 {
					return items
				}
			}
		}
	}
	return []map[string]any{}
}

func toMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func extractChunkTotal(raw map[string]any, fallback int) int32 {
	for _, candidate := range []any{raw["total_size"], raw["total"], raw["count"]} {
		if v, ok := toInt32(candidate); ok {
			return v
		}
	}
	for _, key := range []string{"data", "result"} {
		if m, ok := raw[key].(map[string]any); ok {
			for _, candidate := range []any{m["total_size"], m["total"], m["count"]} {
				if v, ok := toInt32(candidate); ok {
					return v
				}
			}
		}
	}
	return int32(fallback)
}

func toInt32(v any) (int32, bool) {
	switch vv := v.(type) {
	case int:
		return int32(vv), true
	case int32:
		return vv, true
	case int64:
		return int32(vv), true
	case float64:
		return int32(vv), true
	case json.Number:
		if i, err := vv.Int64(); err == nil {
			return int32(i), true
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(vv)); err == nil {
			return int32(i), true
		}
	}
	return 0, false
}

func signSegmentImageKeys(keys []string) []string {
	if len(keys) == 0 {
		return keys
	}
	signed := make([]string, len(keys))
	for i, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			signed[i] = key
			continue
		}
		if strings.HasPrefix(key, "/static-files/") {
			signed[i] = key
			continue
		}
		if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
			signed[i] = key
			continue
		}
		if url := staticFileURLFromFullPath(key); url != "" {
			signed[i] = url
			continue
		}
		signed[i] = key
	}
	return signed
}

func mapChunkToSegment(datasetID, documentID string, item map[string]any) SegmentItem {
	meta := nestedMap(item, "metadata")
	globalMetaMap := firstMap(item, nil, "global_metadata", "global_meta")
	segmentID := firstAnyStringWithMeta(item, meta, "", "segment_id", "chunk_id", "id", "uid")
	resolvedDatasetID := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, datasetID, "dataset_id", "kb_id")
	resolvedDocumentID := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, documentID, "document_id", "doc_id", "docid", "core_document_id")
	content := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "content", "text", "chunk_text", "content_with_weight")
	status := firstAnyStringWithMeta(item, meta, "", "status", "segment_state", "state")
	createTime := firstAnyStringWithMeta(item, meta, "", "create_time", "created_at")
	updateTime := firstAnyStringWithMeta(item, meta, "", "update_time", "updated_at")
	number, _ := firstInt32(item, meta, "number")
	tokenCount, hasTokenCount := firstInt32(item, meta, "token_count")
	words, hasWords := firstInt32(item, meta, "words")
	segmentType := normalizeSegmentType(firstAny(item, meta, "segment_type", "type"))
	displayType := normalizeDisplayType(firstAny(item, meta, "display_type"), segmentType)
	structuredData := firstAny(item, meta, "structured_data")
	children := firstMap(item, meta, "children")
	imageKeys := firstStringSlice(item, meta, "image_keys")
	excludedEmbedMetadataKeys := firstStringSlice(item, meta, "excluded_embed_metadata_keys")
	excludedLLMMetadataKeys := firstStringSlice(item, meta, "excluded_llm_metadata_keys")
	metaText := firstJSONString(item, meta, "meta")
	// Keep meta aligned with metadata payload: serialize full metadata as string.
	if meta != nil && len(meta) > 0 {
		if bs, err := json.Marshal(meta); err == nil {
			metaText = string(bs)
		}
	}
	if metaText == "" {
		metaText = firstJSONString(globalMetaMap, nil, "meta")
	}
	globalMeta := firstJSONString(item, meta, "global_meta", "global_metadata")
	group := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "group")
	parent := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "parent")
	answer := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "answer")
	if metaText == "" {
		metaText = firstJSONString(item, meta, "bbox", "position", "locator")
	}
	tableContent := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "table_content")
	imageKey := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "image_key")
	imageURI := firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "image_uri")
	sourcePath := firstAnyStringWithMeta(item, meta, "", "source_path")
	var displayContent string
	if sourcePath != "" {
		imageKeys = []string{sourcePath}
	} else {
		displayContent = firstAnyStringWithFallbackMaps(item, meta, globalMetaMap, "", "display_content")
		if displayContent == "" {
			displayContent = content
		}
	}
	if len(imageKeys) == 0 {
		imageKeys = []string{}
	}
	imageKeys = signSegmentImageKeys(imageKeys)
	if sourcePath != "" {
		fileName := firstAnyStringWithMeta(item, meta, "", "file_name")
		if strings.TrimSpace(fileName) == "" {
			fileName = filepath.Base(sourcePath)
		}
		imgURL := sourcePath
		if len(imageKeys) > 0 {
			if signed := strings.TrimSpace(imageKeys[0]); signed != "" {
				imgURL = signed
			}
		}
		displayContent = fmt.Sprintf("![%s](%s)", fileName, imgURL)
	}
	if len(excludedEmbedMetadataKeys) == 0 {
		excludedEmbedMetadataKeys = []string{}
	}
	if len(excludedLLMMetadataKeys) == 0 {
		excludedLLMMetadataKeys = []string{}
	}
	if meta == nil {
		meta = map[string]any{}
	}
	if children == nil {
		children = map[string]any{}
	}
	var enabled *bool
	if v, ok := firstBool(item, meta, "enabled", "enable", "is_enabled"); ok {
		enabled = &v
	}
	var isActive *bool
	if v, ok := firstBool(item, meta, "is_active", "active"); ok {
		isActive = &v
	} else if enabled != nil {
		isActive = enabled
	} else {
		defaultActive := true
		isActive = &defaultActive
	}
	var name *string
	if segmentID != "" {
		v := "datasets/" + resolvedDatasetID + "/documents/" + resolvedDocumentID + "/segments/" + segmentID
		name = &v
	}
	return SegmentItem{
		Name:                      name,
		SegmentID:                 segmentID,
		DatasetID:                 resolvedDatasetID,
		DocumentID:                resolvedDocumentID,
		DisplayContent:            displayContent,
		Content:                   content,
		Text:                      content,
		Number:                    number,
		IsActive:                  isActive,
		ImageKeys:                 imageKeys,
		Meta:                      nullableString(metaText),
		Answer:                    answer,
		TokenCount:                nullableInt32(tokenCount, hasTokenCount),
		Metadata:                  meta,
		Words:                     nullableInt32(words, hasWords),
		SegmentType:               nullIfNil(segmentType),
		TableContent:              nullableString(tableContent),
		ImageKey:                  nullableString(imageKey),
		ImageURI:                  nullableString(imageURI),
		DisplayType:               nullIfNil(displayType),
		StructuredData:            nullIfNil(structuredData),
		ExcludedEmbedMetadataKeys: excludedEmbedMetadataKeys,
		ExcludedLLMMetadataKeys:   excludedLLMMetadataKeys,
		Parent:                    nullableString(parent),
		Children:                  children,
		GlobalMeta:                nullableString(globalMeta),
		Group:                     nullableString(group),
		Enabled:                   enabled,
		Status:                    nullableString(status),
		CreateTime:                nullableString(createTime),
		UpdateTime:                nullableString(updateTime),
	}
}

func firstInt32(m map[string]any, meta map[string]any, keys ...string) (int32, bool) {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if src == nil {
				continue
			}
			if v, ok := toInt32(src[key]); ok {
				return v, true
			}
		}
	}
	return 0, false
}

func firstAnyStringWithFallbackMaps(primary map[string]any, secondary map[string]any, tertiary map[string]any, fallback string, keys ...string) string {
	for _, src := range []map[string]any{primary, secondary, tertiary} {
		if src == nil {
			continue
		}
		if v := firstAnyString(src, "", keys...); v != "" {
			return v
		}
	}
	return fallback
}

func firstAny(m map[string]any, meta map[string]any, keys ...string) any {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if src == nil {
				continue
			}
			if v, ok := src[key]; ok && v != nil {
				return v
			}
		}
	}
	return nil
}

func firstMap(m map[string]any, meta map[string]any, keys ...string) map[string]any {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if src == nil {
				continue
			}
			if vv, ok := src[key].(map[string]any); ok && len(vv) > 0 {
				return vv
			}
		}
	}
	return nil
}

func firstStringSlice(m map[string]any, meta map[string]any, keys ...string) []string {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if src == nil {
				continue
			}
			if out := toStringSlice(src[key]); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func firstJSONString(m map[string]any, meta map[string]any, keys ...string) string {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if src == nil {
				continue
			}
			if v, ok := src[key]; ok {
				switch vv := v.(type) {
				case string:
					if strings.TrimSpace(vv) != "" {
						return vv
					}
				case map[string]any, []any:
					if bs, err := json.Marshal(vv); err == nil {
						return string(bs)
					}
				}
			}
		}
	}
	return ""
}

func toStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		if len(vv) == 0 {
			return nil
		}
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func nullableString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	out := strings.TrimSpace(v)
	return &out
}

func nullableInt32(v int32, ok bool) *int32 {
	if !ok {
		return nil
	}
	out := v
	return &out
}

func nullIfNil(v any) any {
	if v == nil {
		return nil
	}
	return v
}

func normalizeSegmentType(v any) any {
	if v == nil {
		return nil
	}
	switch vv := v.(type) {
	case string:
		s := strings.TrimSpace(vv)
		if s == "" {
			return nil
		}
		if mapped, ok := segmentTypeNameByValue[s]; ok {
			return mapped
		}
		return s
	case int:
		if mapped, ok := segmentTypeNameByValue[strconv.Itoa(vv)]; ok {
			return mapped
		}
	case int32:
		if mapped, ok := segmentTypeNameByValue[strconv.FormatInt(int64(vv), 10)]; ok {
			return mapped
		}
	case int64:
		if mapped, ok := segmentTypeNameByValue[strconv.FormatInt(vv, 10)]; ok {
			return mapped
		}
	case float64:
		if mapped, ok := segmentTypeNameByValue[strconv.FormatInt(int64(vv), 10)]; ok {
			return mapped
		}
	}
	return v
}

func normalizeDisplayType(v any, segmentType any) any {
	if normalized := normalizeDisplayTypeValue(v); normalized != nil {
		return normalized
	}
	s, ok := segmentType.(string)
	if !ok {
		return nil
	}
	switch s {
	case "SEGMENT_TYPE_TEXT", "SEGMENT_TYPE_QA", "SEGMENT_TYPE_ONLINE_SEARCH":
		return "DISPLAY_TYPE_TEXT"
	case "SEGMENT_TYPE_TABLE", "SEGMENT_TYPE_STRUCTURED_DATA":
		return "DISPLAY_TYPE_TABLE"
	default:
		return nil
	}
}

func normalizeDisplayTypeValue(v any) any {
	if v == nil {
		return nil
	}
	switch vv := v.(type) {
	case string:
		s := strings.TrimSpace(vv)
		if s == "" {
			return nil
		}
		if mapped, ok := displayTypeNameByValue[s]; ok {
			return mapped
		}
		return s
	case int:
		if mapped, ok := displayTypeNameByValue[strconv.Itoa(vv)]; ok {
			return mapped
		}
	case int32:
		if mapped, ok := displayTypeNameByValue[strconv.FormatInt(int64(vv), 10)]; ok {
			return mapped
		}
	case int64:
		if mapped, ok := displayTypeNameByValue[strconv.FormatInt(vv, 10)]; ok {
			return mapped
		}
	case float64:
		if mapped, ok := displayTypeNameByValue[strconv.FormatInt(int64(vv), 10)]; ok {
			return mapped
		}
	}
	return v
}

var segmentTypeNameByValue = map[string]string{
	"0": "SEGMENT_TYPE_UNSPECIFIED",
	"1": "SEGMENT_TYPE_TEXT",
	"2": "SEGMENT_TYPE_IMAGE",
	"3": "SEGMENT_TYPE_TABLE",
	"4": "SEGMENT_TYPE_WEB_IMAGE",
	"5": "SEGMENT_TYPE_STRUCTURED_DATA",
	"6": "SEGMENT_TYPE_QA",
	"7": "SEGMENT_TYPE_ONLINE_SEARCH",
}

var displayTypeNameByValue = map[string]string{
	"0": "DISPLAY_TYPE_UNSPECIFIED",
	"1": "DISPLAY_TYPE_TEXT",
	"2": "DISPLAY_TYPE_MARKDOWN",
	"3": "DISPLAY_TYPE_TABLE",
}

func firstBool(m map[string]any, meta map[string]any, keys ...string) (bool, bool) {
	for _, src := range []map[string]any{m, meta} {
		for _, key := range keys {
			if v, ok := src[key]; ok {
				switch vv := v.(type) {
				case bool:
					return vv, true
				case string:
					s := strings.ToLower(strings.TrimSpace(vv))
					if s == "true" || s == "1" {
						return true, true
					}
					if s == "false" || s == "0" {
						return false, true
					}
				case float64:
					return vv != 0, true
				}
			}
		}
	}
	return false, false
}
