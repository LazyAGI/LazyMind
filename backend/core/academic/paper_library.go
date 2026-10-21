package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

const maxPaperLibraryBytes = 20 << 20
const emptyPaperLibrary = `{"schemaVersion":3,"folders":[],"collections":[],"papers":[],"attachments":[],"notes":[],"annotations":[],"relations":[],"mergeSnapshots":[]}`

type paperLibraryResponse struct {
	Data     json.RawMessage `json:"data"`
	Revision int64           `json:"revision"`
	CanWrite bool            `json:"can_write"`
}

// The complete graph is stored together so imports, folders, annotations and
// undo history cannot be partially committed. AcademicWork remains global
// scholarly metadata, never a container for dataset-specific reading state.
func readPaperLibrary(ctx context.Context, db *gorm.DB, datasetID string) (paperLibraryResponse, error) {
	var row orm.PaperLibrary
	err := db.WithContext(ctx).Where("dataset_id = ?", datasetID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return paperLibraryResponse{Data: json.RawMessage(emptyPaperLibrary)}, nil
	}
	if err != nil {
		return paperLibraryResponse{}, err
	}
	if row.Revision < 1 || row.Revision > 9007199254740991 {
		return paperLibraryResponse{}, errors.New("invalid stored paper library revision")
	}
	if _, err = validatePaperLibrary(row.DataJSON); err != nil {
		return paperLibraryResponse{}, fmt.Errorf("invalid stored paper library: %w", err)
	}
	return paperLibraryResponse{Data: row.DataJSON, Revision: row.Revision}, nil
}

func paperLibraryAccess(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	userID, ok := requireUser(w, r)
	if !ok {
		return "", false, false
	}
	datasetID := strings.TrimSpace(r.URL.Query().Get("target_dataset_id"))
	if datasetID == "" || len(datasetID) > 255 {
		replyError(w, 400, "INVALID_REQUEST", "target_dataset_id 不能为空且不得超过 255 字节")
		return "", false, false
	}
	catalog, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if err != nil {
		replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "论文库暂时不可用")
		return "", false, false
	}
	dataset, err := catalog.GetDataset(r.Context(), doc.DatasetGetRequest{UserID: userID, DatasetID: datasetID, Caller: caller(r)})
	if err != nil {
		var serviceErr *doc.DatasetServiceError
		if errors.As(err, &serviceErr) && (serviceErr.Code == doc.DatasetServiceNotFound || serviceErr.Code == doc.DatasetServiceForbidden) {
			replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的读取权限")
		} else {
			replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "无法读取目标论文库")
		}
		return "", false, false
	}
	if dataset.LibraryKind != "paper" {
		replyError(w, 400, "PAPER_LIBRARY_REQUIRED", "目标知识库不是论文库")
		return "", false, false
	}
	return datasetID, doc.DatasetAllowsWriteForCaller(r.Context(), caller(r), dataset), true
}

func GetPaperLibrary(w http.ResponseWriter, r *http.Request) {
	datasetID, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	result, err := readPaperLibrary(r.Context(), store.DB(), datasetID)
	if err != nil {
		replyError(w, 500, "PAPER_LIBRARY_LOAD_FAILED", "论文库读取失败，已保存的数据未被修改")
		return
	}
	result.CanWrite = canWrite
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func PutPaperLibrary(w http.ResponseWriter, r *http.Request) {
	datasetID, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	if !canWrite {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的编辑权限")
		return
	}
	var request struct {
		Revision *int64          `json:"revision"`
		Data     json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPaperLibraryBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	if err == nil {
		var extra any
		err = decoder.Decode(&extra)
		if errors.Is(err, io.EOF) {
			err = nil
		} else if err == nil {
			err = errors.New("extra JSON value")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			replyError(w, 413, "PAPER_LIBRARY_TOO_LARGE", "论文库数据超过 20 MiB 保存上限")
		} else {
			replyError(w, 400, "INVALID_REQUEST", "论文库请求 JSON 无效")
		}
		return
	}
	if request.Revision == nil || *request.Revision < 0 || *request.Revision >= 9007199254740991 {
		replyError(w, 400, "INVALID_REQUEST", "revision 必须是非负安全整数")
		return
	}
	documentIDs, err := validatePaperLibrary(request.Data)
	if err != nil {
		replyError(w, 400, "INVALID_PAPER_LIBRARY", err.Error())
		return
	}
	if len(documentIDs) > 0 {
		// Missing references can be historical (deleted PDFs and undo snapshots).
		// Existing files may only belong to this dataset; an ID in a saved graph
		// never grants access to a file in another knowledge base.
		ids := make([]string, 0, len(documentIDs))
		for id := range documentIDs {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += 400 {
			end := start + 400
			if end > len(ids) {
				end = len(ids)
			}
			var count int64
			if err = store.DB().WithContext(r.Context()).Unscoped().Model(&orm.Document{}).Where("id IN ? AND dataset_id <> ?", ids[start:end], datasetID).Count(&count).Error; err != nil {
				replyError(w, 500, "PAPER_LIBRARY_SAVE_FAILED", "无法验证论文附件，数据未保存")
				return
			}
			if count > 0 {
				replyError(w, 400, "INVALID_PAPER_LIBRARY", "论文附件不属于目标论文库")
				return
			}
		}
	}
	saved, err := savePaperLibrary(r.Context(), store.DB(), datasetID, *request.Revision, request.Data)
	if err != nil {
		replyError(w, 500, "PAPER_LIBRARY_SAVE_FAILED", "论文库保存失败，请重试；已保存的数据未被修改")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !saved {
		current, loadErr := readPaperLibrary(r.Context(), store.DB(), datasetID)
		if loadErr != nil {
			replyError(w, 500, "PAPER_LIBRARY_LOAD_FAILED", "论文库读取失败，已保存的数据未被修改")
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "PAPER_LIBRARY_CONFLICT", "message": "论文库已在其他窗口更新，请加载最新内容后重试", "data": current.Data, "revision": current.Revision, "can_write": canWrite})
		return
	}
	_ = json.NewEncoder(w).Encode(paperLibraryResponse{Data: request.Data, Revision: *request.Revision + 1, CanWrite: canWrite})
}

// Compare-and-swap is performed by the database, including the first save.
// No read-then-upsert or process-local lock can silently replace newer data.
func savePaperLibrary(ctx context.Context, db *gorm.DB, datasetID string, revision int64, data json.RawMessage) (bool, error) {
	now := time.Now().UTC()
	var result *gorm.DB
	if revision == 0 {
		row := orm.PaperLibrary{DatasetID: datasetID, DataJSON: data, Revision: 1, CreatedAt: now, UpdatedAt: now}
		result = db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dataset_id"}}, DoNothing: true}).Create(&row)
	} else {
		result = db.WithContext(ctx).Model(&orm.PaperLibrary{}).Where("dataset_id = ? AND revision = ?", datasetID, revision).Updates(map[string]any{"data_json": data, "revision": revision + 1, "updated_at": now})
	}
	return result.RowsAffected == 1, result.Error
}

// Validation intentionally preserves unknown fields. Both snapshots and
// future additive metadata must round-trip without lossy struct conversion.
func validatePaperLibrary(raw json.RawMessage) (map[string]bool, error) {
	if len(raw) == 0 || len(raw) > maxPaperLibraryBytes {
		return nil, errors.New("论文库数据为空或过大")
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, errors.New("论文库 JSON 无效")
	}
	nodes := 0
	var bounded func(any, int) error
	bounded = func(value any, depth int) error {
		nodes++
		if depth > 64 || nodes > 1000000 {
			return errors.New("论文库数据嵌套层级或项目数量过多")
		}
		switch value := value.(type) {
		case string:
			if len(value) > 2<<20 {
				return errors.New("论文库单项文本超过 2 MiB")
			}
		case []any:
			if len(value) > 50000 {
				return errors.New("论文库列表超过 50000 项")
			}
			for _, child := range value {
				if err := bounded(child, depth+1); err != nil {
					return err
				}
			}
		case map[string]any:
			if len(value) > 1000 {
				return errors.New("论文库单项字段过多")
			}
			for key, child := range value {
				if len(key) > 256 {
					return errors.New("论文库字段名称过长")
				}
				if err := bounded(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := bounded(root, 0); err != nil {
		return nil, err
	}
	documents := map[string]bool{}
	if err := validatePaperLibraryObject(root, documents); err != nil {
		return nil, err
	}
	return documents, nil
}

func validatePaperLibraryObject(value any, documents map[string]bool) error {
	root, ok := value.(map[string]any)
	if !ok || root["schemaVersion"] != float64(3) {
		return errors.New("论文库 schemaVersion 必须为 3")
	}
	for _, key := range []string{"folders", "collections", "papers", "attachments", "notes", "annotations", "relations", "mergeSnapshots", "hiddenArxivPapers", "migrationSnapshots"} {
		raw, present := root[key]
		if !present && (key == "hiddenArxivPapers" || key == "migrationSnapshots") {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("论文库 %s 必须为数组", key)
		}
		if (key == "papers" || key == "hiddenArxivPapers") && len(items) > 10000 {
			return errors.New("单个论文库最多支持 10000 篇论文")
		}
		seen := map[string]bool{}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("论文库 %s 项目必须为对象", key)
			}
			id, ok := object["id"].(string)
			if !ok || strings.TrimSpace(id) == "" || len(id) > 255 || seen[id] {
				return fmt.Errorf("论文库 %s 的 id 为空、重复或过长", key)
			}
			seen[id] = true
			required := []string{}
			switch key {
			case "folders", "collections":
				required = []string{"name"}
			case "papers", "hiddenArxivPapers":
				required = []string{"title", "authors", "venue", "status", "source", "parse"}
				if _, ok := object["important"].(bool); !ok {
					return errors.New("论文 important 必须为布尔值")
				}
				year, ok := object["year"].(float64)
				if !ok || math.Trunc(year) != year || math.Abs(year) > 10000 {
					return errors.New("论文 year 无效")
				}
				for _, array := range []string{"tags", "attachmentIds", "noteIds", "annotationIds", "collectionIds", "relatedPaperIds"} {
					if err := paperStringArray(object, array, true); err != nil {
						return err
					}
				}
				metadata, ok := object["metadata"].(map[string]any)
				if !ok {
					return errors.New("论文 metadata 必须为对象")
				}
				for _, field := range []string{"identifiers", "confidence"} {
					if _, ok := metadata[field].(map[string]any); !ok {
						return fmt.Errorf("论文 metadata.%s 必须为对象", field)
					}
				}
				if folder, exists := object["folderId"]; exists && folder != nil {
					if _, ok := folder.(string); !ok {
						return errors.New("论文 folderId 无效")
					}
				}
				for field, allowed := range map[string]string{"status": "|unread|reading|read|", "parse": "|ready|parsing|failed|", "source": "|local|zotero|google_scholar|semantic_scholar|doi|arxiv|"} {
					v, ok := object[field].(string)
					if !ok || !strings.Contains(allowed, "|"+v+"|") {
						return fmt.Errorf("论文 %s 无效", field)
					}
				}
			case "attachments":
				required = []string{"paperId", "name", "kind", "source", "addedAt"}
				if raw, present := object["contentRef"]; present {
					reference, ok := raw.(string)
					if !ok || len(reference) > 255 {
						return errors.New("论文附件 contentRef 无效")
					}
					if reference != "" {
						documents[reference] = true
					}
				}
			case "notes":
				required = []string{"paperId", "title", "content", "kind", "createdAt", "updatedAt"}
			case "annotations":
				required = []string{"paperId", "comment", "color", "createdAt"}
			case "relations":
				required = []string{"fromPaperId", "toPaperId", "kind"}
			case "mergeSnapshots":
				required = []string{"createdAt", "primaryId", "duplicateId"}
				if err := validatePaperLibraryObject(object["before"], documents); err != nil {
					return err
				}
			case "migrationSnapshots":
				required = []string{"createdAt", "source"}
				if err := validatePaperLibraryObject(object["data"], documents); err != nil {
					return err
				}
			}
			for _, field := range required {
				if _, ok := object[field].(string); !ok {
					return fmt.Errorf("论文库 %s.%s 必须为文本", key, field)
				}
			}
		}
	}
	for _, key := range []string{"folders", "collections"} {
		if err := validatePaperFolderTree(root[key].([]any)); err != nil {
			return err
		}
	}
	return paperStringArray(root, "hiddenArxivWorkIds", false)
}

func validatePaperFolderTree(items []any) error {
	parents := make(map[string]string, len(items))
	for _, raw := range items {
		item := raw.(map[string]any)
		id := item["id"].(string)
		parent := ""
		if value, present := item["parentId"]; present && value != nil {
			var ok bool
			parent, ok = value.(string)
			if !ok || len(parent) > 255 {
				return errors.New("论文文件夹 parentId 无效")
			}
		}
		parents[id] = parent
	}
	for id := range parents {
		seen := map[string]bool{}
		for cursor := id; cursor != ""; cursor = parents[cursor] {
			if _, exists := parents[cursor]; !exists {
				return errors.New("论文文件夹的父文件夹不存在")
			}
			if seen[cursor] {
				return errors.New("论文文件夹不能包含循环层级")
			}
			seen[cursor] = true
			if len(seen) > 32 {
				return errors.New("论文文件夹最多支持 32 层")
			}
		}
	}
	return nil
}

func paperStringArray(object map[string]any, key string, required bool) error {
	raw, present := object[key]
	if !present && !required {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("论文库 %s 必须为文本数组", key)
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return fmt.Errorf("论文库 %s 必须为文本数组", key)
		}
	}
	return nil
}
