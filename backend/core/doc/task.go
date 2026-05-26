package doc

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
	"lazymind/core/store"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func newTaskID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "task_" + fmtHex(b[:])
}

func newUploadID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "upload_" + fmtHex(b[:])
}

const (
	uploadScopeTask    = "TASK"
	uploadScopeDataset = "DATASET"
	uploadScopeTemp    = "TEMP"
)

func streamUploadedFile(w http.ResponseWriter, r *http.Request, inline bool) {
	datasetID := datasetIDFromPath(r)
	uploadFileID := uploadFileIDFromPath(r)
	if datasetID == "" || uploadFileID == "" {
		common.ReplyErr(w, "missing dataset or upload_file_id", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var row orm.UploadedFile
	if err := store.DB().WithContext(r.Context()).Where("upload_file_id = ? AND dataset_id = ? AND deleted_at IS NULL", uploadFileID, datasetID).Take(&row).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "uploaded file not found", err), http.StatusNotFound)
		return
	}
	var ext uploadedFileExt
	_ = json.Unmarshal(row.Ext, &ext)
	storedPath := strings.TrimSpace(ext.StoredPath)
	if storedPath == "" {
		common.ReplyErr(w, "uploaded file path is empty", http.StatusNotFound)
		return
	}
	filename := firstNonEmpty(strings.TrimSpace(ext.OriginalFilename), filepath.Base(strings.TrimSpace(storedPath)))
	streamLocalFile(w, storedPath, filename, ext.ContentType, inline)
}

func StartTask(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}

	var req StartTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(req.TaskIDs) == 0 {
		common.ReplyErr(w, "task_ids is required", http.StatusBadRequest)
		return
	}
	resp := StartTasksResponse{Tasks: make([]StartTaskResult, 0, len(req.TaskIDs)), RequestedCount: len(req.TaskIDs)}
	results, err := startTasksInternal(r, datasetID, req.TaskIDs)
	if err != nil && len(results) == 0 {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	resp.Tasks = results
	for _, item := range results {
		if item.Status == "STARTED" {
			resp.StartedCount++
		} else {
			resp.FailedCount++
		}
	}
	common.ReplyJSON(w, resp)
}

func SearchTasks(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var req SearchTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(req.TaskIDs) == 0 {
		common.ReplyErr(w, "task_ids is required", http.StatusBadRequest)
		return
	}
	ids := make([]string, 0, len(req.TaskIDs))
	seen := map[string]struct{}{}
	for _, id := range req.TaskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		common.ReplyErr(w, "task_ids is required", http.StatusBadRequest)
		return
	}
	var rows []orm.Task
	query := store.DB().WithContext(r.Context()).Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Where("id IN ?", ids)
	if err := query.Order("created_at DESC").Find(&rows).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query tasks failed", err), http.StatusInternalServerError)
		return
	}
	resp := make([]TaskResponse, 0, len(rows))
	filterState := strings.TrimSpace(req.TaskState)
	for _, row := range rows {
		item := buildTaskResponse(r, row)
		if filterState != "" && item.TaskState != filterState {
			continue
		}
		resp = append(resp, item)
	}
	common.ReplyJSON(w, ListTasksResponse{Tasks: resp, TotalSize: int32(len(resp))})
}

func parseDocumentPIDFromParentName(parent string) string {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return ""
	}
	idx := strings.LastIndex(parent, "/documents/")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(parent[idx+len("/documents/"):])
}

func formValueFirst(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(r.FormValue(key)); v != "" {
			return v
		}
	}
	return ""
}

func resolveMultipartDocumentPID(r *http.Request) string {
	return firstNonEmpty(
		formValueFirst(r, "document_pid", "p_id", "pid"),
		parseDocumentPIDFromParentName(formValueFirst(r, "parent")),
	)
}

func BatchUploadTasks(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	ds, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload)
	if !ok {
		replyDatasetForbidden(w)
		return
	}
	if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "embedding"); err != nil || !ready {
		common.ReplyErr(w, "embedding model is not ready", http.StatusUnprocessableEntity)
		return
	}
	if features := modelprovider.GetCachedModelFeatures(); features.ImageEmbedRequired {
		if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "multimodal_embedding"); err != nil || !ready {
			common.ReplyErr(w, "multimodal embedding model is not ready", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid multipart form", err), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = flattenMultipartFiles(r.MultipartForm.File)
	}
	if len(files) == 0 {
		common.ReplyErr(w, "no files uploaded", http.StatusBadRequest)
		return
	}
	relativePath := strings.TrimSpace(r.FormValue("relative_path"))
	documentPID := resolveMultipartDocumentPID(r)
	tags := splitCSV(r.FormValue("document_tags"))
	now := time.Now().UTC()
	baseTasks := make([]orm.Task, 0, len(files))

	for _, fh := range files {
		createdTask, _, _, err := createUploadedTaskAndDocument(r, ds, datasetID, userID, userName, now, fh, relativePath, documentPID, tags)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request", err), http.StatusBadRequest)
			return
		}
		baseTasks = append(baseTasks, createdTask)
	}

	resp := make([]TaskResponse, 0, len(baseTasks))
	for _, row := range baseTasks {
		resp = append(resp, buildTaskResponse(r, row))
	}
	common.ReplyJSON(w, BatchUploadTasksResponse{Tasks: resp})
}

func UploadFile(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	ds, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload)
	if !ok {
		replyDatasetForbidden(w)
		return
	}
	if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "embedding"); err != nil || !ready {
		common.ReplyErr(w, "embedding model is not ready", http.StatusUnprocessableEntity)
		return
	}
	if features := modelprovider.GetCachedModelFeatures(); features.ImageEmbedRequired {
		if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "multimodal_embedding"); err != nil || !ready {
			common.ReplyErr(w, "multimodal embedding model is not ready", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid multipart form", err), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		files = flattenMultipartFiles(r.MultipartForm.File)
	}
	if len(files) == 0 {
		common.ReplyErr(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	relativePath := strings.TrimSpace(r.FormValue("relative_path"))
	documentPID := resolveMultipartDocumentPID(r)
	tags := splitCSV(r.FormValue("document_tags"))
	resp := make([]UploadFileResponse, 0, len(files))
	now := time.Now().UTC()
	for _, fh := range files {
		row, ext, err := createUploadedFileRecord(r, ds, datasetID, userID, userName, now, fh, relativePath, documentPID, tags)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request", err), http.StatusBadRequest)
			return
		}
		resp = append(resp, UploadFileResponse{
			UploadFileID: row.UploadFileID,
			DatasetID:    row.DatasetID,
			Filename:     ext.OriginalFilename,
			StoredName:   ext.StoredName,
			StoredPath:   ext.StoredPath,
			RelativePath: ext.RelativePath,
			DocumentPID:  ext.DocumentPID,
			DocumentTags: ext.DocumentTags,
			FileSize:     ext.FileSize,
			ContentType:  ext.ContentType,
			ContentURL:   uploadedFileContentPath(row.DatasetID, row.UploadFileID),
			DownloadURL:  uploadedFileDownloadPath(row.DatasetID, row.UploadFileID),
			FileURL:      staticFileURLFromFullPath(ext.StoredPath),
			Status:       row.Status,
			UploadScope:  uploadScopeDataset,
		})
	}
	common.ReplyJSON(w, UploadFilesResponse{Files: resp})
}

func UploadTempFile(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid multipart form", err), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		files = flattenMultipartFiles(r.MultipartForm.File)
	}
	if len(files) == 0 {
		common.ReplyErr(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	resp := make([]UploadFileResponse, 0, len(files))
	now := time.Now().UTC()
	for _, fh := range files {
		uploadFileID := newUploadID()
		storedName := storedFileName(fh.Filename, uploadFileID)
		finalDir := buildTempUploadFileDir(userID, uploadFileID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "create temp dir failed", err), http.StatusInternalServerError)
			return
		}
		finalPath := filepath.Join(finalDir, storedName)
		file, err := fh.Open()
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "open upload file failed", err), http.StatusBadRequest)
			return
		}
		out, err := os.Create(finalPath)
		if err != nil {
			_ = file.Close()
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "create upload target failed", err), http.StatusInternalServerError)
			return
		}
		size, copyErr := io.Copy(out, file)
		_ = out.Close()
		_ = file.Close()
		if copyErr != nil {
			common.ReplyErr(w, "save upload file failed", http.StatusInternalServerError)
			return
		}
		contentType := fh.Header.Get("Content-Type")
		row := orm.UploadedFile{UploadFileID: uploadFileID, DatasetID: "", TenantID: "", TaskID: "", DocumentID: "", Status: UploadedFileStateUploaded, Ext: mustJSON(uploadedFileExt{StoredPath: finalPath, StoredName: storedName, OriginalFilename: fh.Filename, FileSize: size, ContentType: contentType}), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
		if err := store.DB().WithContext(r.Context()).Create(&row).Error; err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "create uploaded file failed", err), http.StatusInternalServerError)
			return
		}
		resp = append(resp, UploadFileResponse{UploadFileID: uploadFileID, Filename: fh.Filename, StoredName: storedName, StoredPath: finalPath, FileSize: size, ContentType: contentType, FileURL: staticFileURLFromFullPath(finalPath), Status: UploadedFileStateUploaded, UploadScope: uploadScopeTemp})
	}
	common.ReplyJSON(w, UploadFilesResponse{Files: resp})
}

func flattenMultipartFiles(form map[string][]*multipart.FileHeader) []*multipart.FileHeader {
	out := make([]*multipart.FileHeader, 0)
	for _, list := range form {
		out = append(out, list...)
	}
	return out
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ListTasks(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}

	q := r.URL.Query()
	pageToken := strings.TrimSpace(q.Get("page_token"))
	pageSize := 20
	if v, err := strconv.Atoi(strings.TrimSpace(q.Get("page_size"))); err == nil && v > 0 {
		pageSize = v
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	offset := 0
	if pageToken != "" {
		v, err := parseDatasetPageToken(pageToken)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid page_token", err), http.StatusBadRequest)
			return
		}
		offset = v
	}

	var rows []orm.Task
	db := store.DB().WithContext(r.Context())
	query := db.Where("dataset_id = ? AND deleted_at IS NULL", datasetID)
	filterTaskState := strings.TrimSpace(q.Get("task_state"))
	if taskType := strings.TrimSpace(q.Get("task_type")); taskType != "" {
		query = query.Where("task_type = ?", taskType)
	}
	if documentID := strings.TrimSpace(q.Get("document_id")); documentID != "" {
		query = query.Where("doc_id = ?", documentID)
	}
	if documentPID := strings.TrimSpace(q.Get("document_pid")); documentPID != "" {
		query = query.Where("document_pid = ?", documentPID)
	}
	var total int64
	_ = query.Model(&orm.Task{}).Count(&total).Error
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "query tasks failed", err), http.StatusInternalServerError)
		return
	}

	out := make([]TaskResponse, 0, len(rows))
	for _, row := range rows {
		item := buildTaskResponse(r, row)
		if filterTaskState != "" && item.TaskState != filterTaskState {
			continue
		}
		out = append(out, item)
	}
	next := ""
	if offset+len(rows) < int(total) {
		next = encodeDatasetPageToken(offset+len(rows), pageSize, int(total))
	}
	totalResp := total
	if filterTaskState != "" {
		totalResp = int64(len(out))
		if next != "" {
			next = ""
		}
	}
	common.ReplyJSON(w, ListTasksResponse{Tasks: out, TotalSize: int32(totalResp), NextPageToken: next})
}

func CreateTask(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}
	if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "embedding"); err != nil || !ready {
		common.ReplyErr(w, "embedding model is not ready", http.StatusUnprocessableEntity)
		return
	}
	if features := modelprovider.GetCachedModelFeatures(); features.ImageEmbedRequired {
		if ready, err := modelprovider.IsModelReady(r.Context(), store.DB(), userID, "multimodal_embedding"); err != nil || !ready {
			common.ReplyErr(w, "multimodal embedding model is not ready", http.StatusUnprocessableEntity)
			return
		}
	}

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 {
		common.ReplyErr(w, "items is required", http.StatusBadRequest)
		return
	}
	resp := make([]TaskResponse, 0, len(req.Items))

	// Pre-allocate one new folder per unique relative_path prefix across all items in
	// this request. This ensures that:
	//   - multiple files sharing the same prefix in one request land in the same folder
	//   - a second request with the same prefix creates a separate, independent folder
	folderByPrefix := make(map[string]string) // prefix -> folderID
	for i := range req.Items {
		item := &req.Items[i]
		if item.UploadFileID == "" {
			continue
		}
		relPath := strings.TrimSpace(item.Task.RelativePath)
		if relPath == "" || strings.TrimSpace(item.Task.DocumentPID) != "" {
			continue
		}
		normalized := strings.ReplaceAll(relPath, "\\", "/")
		parts := strings.SplitN(normalized, "/", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		prefix := strings.TrimSpace(parts[0])
		if _, exists := folderByPrefix[prefix]; !exists {
			folderByPrefix[prefix] = createTopLevelFolder(r.Context(), datasetID, userID, userName, relPath)
		}
		item.Task.DocumentPID = folderByPrefix[prefix]
		item.Task.RelativePath = "" // consumed — no need to re-derive in createTaskFromUploadedFile
	}

	for _, item := range req.Items {
		tType := string(item.Task.TaskType)
		if tType == "" {
			tType = string(TaskTypeParseUploaded)
		}
		if err := validateCreateTaskItem(item, datasetID, tType); err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request", err), http.StatusBadRequest)
			return
		}

		expandedItems := expandCreateTaskItems(item, tType)
		for _, expandedItem := range expandedItems {
			if strings.TrimSpace(expandedItem.UploadFileID) != "" {
				taskRows, err := createTaskFromUploadedFile(r, datasetID, userID, userName, expandedItem, tType)
				if err != nil {
					common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request", err), http.StatusBadRequest)
					return
				}
				for _, t := range taskRows {
					resp = append(resp, buildTaskResponse(r, t))
				}
			} else {
				taskRow, err := createTaskFromExistingDocument(r, datasetID, userID, userName, expandedItem, tType)
				if err != nil {
					common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid request", err), http.StatusBadRequest)
					return
				}
				resp = append(resp, buildTaskResponse(r, taskRow))
			}
		}
	}
	common.ReplyJSON(w, CreateTasksResponse{Tasks: resp})
}

func GetTask(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	taskID := taskIDFromPath(r)
	if datasetID == "" || taskID == "" {
		common.ReplyErr(w, "missing dataset or task", http.StatusBadRequest)
		return
	}
	if _, userID, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetRead); !ok {
		if userID == "" {
			common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		} else {
			replyDatasetForbidden(w)
		}
		return
	}
	var row orm.Task
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&row).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "task not found", err), http.StatusNotFound)
		return
	}
	common.ReplyJSON(w, buildTaskResponse(r, row))
}

func DeleteTask(w http.ResponseWriter, r *http.Request) {
	updateTaskDeletion(w, r, true)
}

func SuspendTask(w http.ResponseWriter, r *http.Request) {
	updateTaskStateEndpoint(w, r, string(TaskStateCancelled))
}

func ResumeTask(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	taskID := taskIDFromPath(r)
	if datasetID == "" || taskID == "" {
		common.ReplyErr(w, "missing dataset or task", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}

	var raw map[string]any
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && err != io.EOF {
			common.ReplyErr(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	bodyTaskIDValue, _ := raw["task_id"].(string)
	bodyTaskID := strings.TrimSpace(bodyTaskIDValue)
	if bodyTaskID != "" && bodyTaskID != taskID {
		common.ReplyErr(w, "task_id in body does not match path", http.StatusBadRequest)
		return
	}

	var taskRow orm.Task
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).First(&taskRow).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "task not found", err), http.StatusNotFound)
			return
		}
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
		return
	}

	state := strings.ToUpper(strings.TrimSpace(buildTaskResponse(r, taskRow).TaskState))
	if state != string(TaskStateFailed) && state != string(TaskStateCancelled) {
		common.ReplyErr(w, "task can only be resumed from FAILED or CANCELED state", http.StatusBadRequest)
		return
	}

	results, err := startTasksInternal(r, datasetID, []string{taskID})
	if err != nil && len(results) == 0 {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	resp := StartTasksResponse{Tasks: results, RequestedCount: 1}
	for _, item := range results {
		if item.Status == "STARTED" {
			resp.StartedCount++
		} else {
			resp.FailedCount++
		}
	}
	common.ReplyJSON(w, resp)
}

func InitUpload(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	if datasetID == "" {
		common.ReplyErr(w, "missing dataset", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	ds, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload)
	if !ok {
		replyDatasetForbidden(w)
		return
	}
	var req InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Filename) == "" {
		common.ReplyErr(w, "filename is required", http.StatusBadRequest)
		return
	}
	if req.FileSize < 0 {
		common.ReplyErr(w, "file_size must be >= 0", http.StatusBadRequest)
		return
	}
	if req.PartSize < 0 {
		common.ReplyErr(w, "part_size must be >= 0", http.StatusBadRequest)
		return
	}
	resp, row, err := initUploadSession(r.Context(), initUploadSessionArgs{
		Scope:          uploadScopeDataset,
		DatasetID:      datasetID,
		TenantID:       ds.TenantID,
		DocumentPID:    strings.TrimSpace(req.DocumentPID),
		RelativePath:   strings.TrimSpace(req.RelativePath),
		Filename:       req.Filename,
		FileSize:       req.FileSize,
		ContentType:    req.ContentType,
		PartSize:       req.PartSize,
		CreateUserID:   userID,
		CreateUserName: userName,
	})
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
		return
	}
	_ = row
	common.ReplyJSON(w, resp)
}

func UploadPart(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	uploadID := uploadIDFromPath(r)
	partNumber, err := strconv.Atoi(strings.TrimSpace(mux.Vars(r)["part_number"]))
	if datasetID == "" || uploadID == "" || err != nil || partNumber <= 0 {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND dataset_id = ? AND deleted_at IS NULL", uploadID, datasetID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	meta, _, metaErr := loadUploadMeta(session)
	if metaErr != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeDataset {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	uploadPartInternal(w, r, session, partNumber)
}

func CompleteUpload(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	uploadID := uploadIDFromPath(r)
	if datasetID == "" || uploadID == "" {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	ds, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload)
	if !ok {
		replyDatasetForbidden(w)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND dataset_id = ? AND deleted_at IS NULL", uploadID, datasetID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	meta, _, metaErr := loadUploadMeta(session)
	if metaErr != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeDataset {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	resp, statusCode, err := completeUploadInternal(r.Context(), session, completeUploadArgs{Dataset: ds})
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), statusCode)
		return
	}
	common.ReplyJSON(w, resp)
}

func InitTempUpload(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	userName := store.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Filename) == "" {
		common.ReplyErr(w, "filename is required", http.StatusBadRequest)
		return
	}
	if req.FileSize < 0 {
		common.ReplyErr(w, "file_size must be >= 0", http.StatusBadRequest)
		return
	}
	if req.PartSize < 0 {
		common.ReplyErr(w, "part_size must be >= 0", http.StatusBadRequest)
		return
	}
	resp, _, err := initUploadSession(r.Context(), initUploadSessionArgs{Scope: uploadScopeTemp, Filename: req.Filename, FileSize: req.FileSize, ContentType: req.ContentType, PartSize: req.PartSize, CreateUserID: userID, CreateUserName: userName})
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, resp)
}

func UploadTempPart(w http.ResponseWriter, r *http.Request) {
	uploadID := uploadIDFromPath(r)
	partNumber, err := strconv.Atoi(strings.TrimSpace(mux.Vars(r)["part_number"]))
	if uploadID == "" || err != nil || partNumber <= 0 {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND deleted_at IS NULL", uploadID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(session.CreateUserID) != userID {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	meta, _, err := loadUploadMeta(session)
	if err != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeTemp {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	uploadPartInternal(w, r, session, partNumber)
}

func CompleteTempUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := uploadIDFromPath(r)
	if uploadID == "" {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND deleted_at IS NULL", uploadID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(session.CreateUserID) != userID {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	meta, _, err := loadUploadMeta(session)
	if err != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeTemp {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	resp, statusCode, err := completeUploadInternal(r.Context(), session, completeUploadArgs{})
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), statusCode)
		return
	}
	common.ReplyJSON(w, resp)
}

func AbortUpload(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPath(r)
	uploadID := uploadIDFromPath(r)
	if datasetID == "" || uploadID == "" {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND dataset_id = ? AND deleted_at IS NULL", uploadID, datasetID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	meta, _, metaErr := loadUploadMeta(session)
	if metaErr != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeDataset {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	abortUploadSession(r.Context(), session)
	common.ReplyJSON(w, map[string]any{"upload_id": uploadID, "upload_state": string(TaskStateCancelled)})
}

func AbortTempUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := uploadIDFromPath(r)
	if uploadID == "" {
		common.ReplyErr(w, "invalid path", http.StatusBadRequest)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var session orm.UploadSession
	if err := store.DB().WithContext(r.Context()).Where("upload_id = ? AND deleted_at IS NULL", uploadID).Take(&session).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "upload session not found", err), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(session.CreateUserID) != userID {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	meta, _, err := loadUploadMeta(session)
	if err != nil || strings.TrimSpace(meta.UploadScope) != uploadScopeTemp {
		common.ReplyErr(w, "upload session not found", http.StatusNotFound)
		return
	}
	abortUploadSession(r.Context(), session)
	common.ReplyJSON(w, map[string]any{"upload_id": uploadID, "upload_state": string(TaskStateCancelled)})
}

type initUploadSessionArgs struct {
	Scope          string
	DatasetID      string
	TaskID         string
	DocumentID     string
	TenantID       string
	DocumentPID    string
	RelativePath   string
	Filename       string
	FileSize       int64
	ContentType    string
	PartSize       int64
	CreateUserID   string
	CreateUserName string
}

type completeUploadArgs struct {
	Dataset *orm.Dataset
}

func initUploadSession(ctx context.Context, args initUploadSessionArgs) (InitUploadResponse, orm.UploadSession, error) {
	uploadID := newUploadID()
	partSize := args.PartSize
	if partSize <= 0 {
		partSize = 8 * 1024 * 1024
	}
	totalParts := 1
	if args.FileSize > 0 {
		totalParts = int(math.Ceil(float64(args.FileSize) / float64(partSize)))
		if totalParts < 1 {
			totalParts = 1
		}
	}
	storedRef := firstNonEmpty(args.DocumentID, uploadID)
	meta := uploadMeta{
		UploadID:         uploadID,
		TaskID:           args.TaskID,
		DocumentID:       args.DocumentID,
		DatasetID:        args.DatasetID,
		TenantID:         args.TenantID,
		DocumentPID:      args.DocumentPID,
		RelativePath:     args.RelativePath,
		OriginalFilename: args.Filename,
		StoredName:       storedFileName(args.Filename, storedRef),
		FileSize:         args.FileSize,
		ContentType:      args.ContentType,
		PartSize:         partSize,
		TotalParts:       totalParts,
		UploadedParts:    []int{},
		UploadState:      string(TaskStateUploading),
		UploadScope:      strings.ToUpper(strings.TrimSpace(args.Scope)),
		CreateUserID:     args.CreateUserID,
		CreateUserName:   args.CreateUserName,
	}
	if meta.UploadScope == "" {
		meta.UploadScope = uploadScopeTask
	}
	dir := uploadDirForMeta(meta, orm.UploadSession{UploadID: uploadID, TaskID: args.TaskID, DatasetID: args.DatasetID, TenantID: args.TenantID, BaseModel: orm.BaseModel{CreateUserID: args.CreateUserID}})
	if err := os.MkdirAll(filepath.Join(dir, "parts"), 0o755); err != nil {
		return InitUploadResponse{}, orm.UploadSession{}, fmt.Errorf("create upload dir failed")
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), mustJSON(meta), 0o644); err != nil {
		return InitUploadResponse{}, orm.UploadSession{}, fmt.Errorf("write upload meta failed")
	}
	now := time.Now().UTC()
	row := orm.UploadSession{UploadID: uploadID, TaskID: args.TaskID, DatasetID: args.DatasetID, TenantID: args.TenantID, DocumentID: args.DocumentID, UploadState: meta.UploadState, Ext: mustJSON(meta), BaseModel: orm.BaseModel{CreateUserID: args.CreateUserID, CreateUserName: args.CreateUserName, CreatedAt: now, UpdatedAt: now}}
	if err := store.DB().WithContext(ctx).Create(&row).Error; err != nil {
		return InitUploadResponse{}, orm.UploadSession{}, fmt.Errorf("create upload session failed")
	}
	return InitUploadResponse{UploadID: uploadID, TaskID: args.TaskID, DocumentID: args.DocumentID, DatasetID: args.DatasetID, StoredName: meta.StoredName, UploadMode: multipartOrSingle(totalParts), PartSize: partSize, TotalParts: totalParts, UploadState: meta.UploadState, UploadScope: meta.UploadScope}, row, nil
}

func uploadPartInternal(w http.ResponseWriter, r *http.Request, session orm.UploadSession, partNumber int) {
	meta, dir, err := loadUploadMeta(session)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "load upload meta failed", err), http.StatusInternalServerError)
		return
	}
	partPath := filepath.Join(dir, "parts", fmt.Sprintf("%06d.part", partNumber))
	f, err := os.Create(partPath)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "create part failed", err), http.StatusInternalServerError)
		return
	}
	n, copyErr := io.Copy(f, r.Body)
	_ = f.Close()
	if copyErr != nil {
		common.ReplyErr(w, "write part failed", http.StatusInternalServerError)
		return
	}
	meta.UploadedParts = appendUniquePart(meta.UploadedParts, partNumber)
	sort.Ints(meta.UploadedParts)
	meta.UploadState = string(TaskStateUploading)
	session.Ext = mustJSON(meta)
	session.UploadState = meta.UploadState
	session.UpdatedAt = time.Now().UTC()
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), mustJSON(meta), 0o644)
	_ = store.DB().WithContext(r.Context()).Save(&session).Error
	common.ReplyJSON(w, map[string]any{"upload_id": session.UploadID, "part_number": partNumber, "part_size": n, "uploaded_parts": len(meta.UploadedParts), "total_parts": meta.TotalParts, "upload_state": meta.UploadState})
}

func abortUploadSession(ctx context.Context, session orm.UploadSession) {
	meta, dir, err := loadUploadMeta(session)
	if err == nil {
		_ = os.RemoveAll(dir)
		meta.UploadState = string(TaskStateCancelled)
		session.Ext = mustJSON(meta)
	}
	session.UploadState = string(TaskStateCancelled)
	session.UpdatedAt = time.Now().UTC()
	_ = store.DB().WithContext(ctx).Save(&session).Error
}

func completeUploadInternal(ctx context.Context, session orm.UploadSession, args completeUploadArgs) (CompleteUploadResponse, int, error) {
	meta, dir, err := loadUploadMeta(session)
	if err != nil {
		return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("load upload meta failed")
	}
	if len(meta.UploadedParts) == 0 {
		return CompleteUploadResponse{}, http.StatusBadRequest, fmt.Errorf("no uploaded parts")
	}
	if meta.TotalParts > 0 && len(meta.UploadedParts) != meta.TotalParts {
		return CompleteUploadResponse{}, http.StatusBadRequest, fmt.Errorf("uploaded parts are incomplete")
	}
	mergedPath := filepath.Join(dir, meta.StoredName)
	merged, err := os.Create(mergedPath)
	if err != nil {
		return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("create merged file failed")
	}
	var totalSize int64
	for _, part := range meta.UploadedParts {
		p := filepath.Join(dir, "parts", fmt.Sprintf("%06d.part", part))
		in, openErr := os.Open(p)
		if openErr != nil {
			_ = merged.Close()
			return CompleteUploadResponse{}, http.StatusBadRequest, fmt.Errorf("part not found")
		}
		n, copyErr := io.Copy(merged, in)
		_ = in.Close()
		if copyErr != nil {
			_ = merged.Close()
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("merge part failed")
		}
		totalSize += n
	}
	_ = merged.Close()

	switch meta.UploadScope {
	case uploadScopeTemp:
		finalDir := buildTempUploadFileDir(firstNonEmpty(meta.CreateUserID, session.CreateUserID), session.UploadID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("create final dir failed")
		}
		finalPath := filepath.Join(finalDir, meta.StoredName)
		if err := os.Rename(mergedPath, finalPath); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("move file failed")
		}
		meta.UploadState = string(TaskStateUploaded)
		meta.FileSize = totalSize
		session.Ext = mustJSON(meta)
		session.UploadState = meta.UploadState
		session.UpdatedAt = time.Now().UTC()
		_ = store.DB().WithContext(ctx).Save(&session).Error
		return CompleteUploadResponse{UploadID: session.UploadID, StoredPath: finalPath, FileURL: staticFileURLFromFullPath(finalPath), FileSize: totalSize, UploadScope: meta.UploadScope}, http.StatusOK, nil
	case uploadScopeDataset:
		if args.Dataset == nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("dataset context is required")
		}
		uploadFileID := newUploadID()
		finalDir := buildDatasetDocFileDir(args.Dataset.TenantID, session.DatasetID, meta.RelativePath, uploadFileID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("create final dir failed")
		}
		finalPath := filepath.Join(finalDir, meta.StoredName)
		if err := os.Rename(mergedPath, finalPath); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("move file failed")
		}
		meta.UploadState = string(TaskStateUploaded)
		meta.FileSize = totalSize
		session.Ext = mustJSON(meta)
		session.UploadState = meta.UploadState
		session.UpdatedAt = time.Now().UTC()
		_ = store.DB().WithContext(ctx).Save(&session).Error

		now := time.Now().UTC()
		uploadedExt := uploadedFileExt{
			StoredPath:       finalPath,
			StoredName:       meta.StoredName,
			OriginalFilename: meta.OriginalFilename,
			FileSize:         totalSize,
			ContentType:      meta.ContentType,
			RelativePath:     meta.RelativePath,
			DocumentPID:      meta.DocumentPID,
		}
		uploaded := orm.UploadedFile{
			UploadFileID: uploadFileID,
			DatasetID:    session.DatasetID,
			TenantID:     args.Dataset.TenantID,
			TaskID:       "",
			DocumentID:   "",
			Status:       UploadedFileStateUploaded,
			Ext:          mustJSON(uploadedExt),
			BaseModel: orm.BaseModel{
				CreateUserID:   session.CreateUserID,
				CreateUserName: session.CreateUserName,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}
		if err := store.DB().WithContext(ctx).Create(&uploaded).Error; err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("create uploaded file failed")
		}
		return CompleteUploadResponse{
			UploadID:     session.UploadID,
			UploadFileID: uploadFileID,
			DatasetID:    session.DatasetID,
			StoredPath:   finalPath,
			ContentURL:   uploadedFileContentPath(session.DatasetID, uploadFileID),
			DownloadURL:  uploadedFileDownloadPath(session.DatasetID, uploadFileID),
			FileURL:      staticFileURLFromFullPath(finalPath),
			FileSize:     totalSize,
			UploadScope:  meta.UploadScope,
		}, http.StatusOK, nil
	default:
		if args.Dataset == nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("dataset context is required")
		}
		finalDir := buildDatasetDocFileDir(args.Dataset.TenantID, session.DatasetID, meta.RelativePath, session.DocumentID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("create final dir failed")
		}
		finalPath := filepath.Join(finalDir, meta.StoredName)
		if err := os.Rename(mergedPath, finalPath); err != nil {
			return CompleteUploadResponse{}, http.StatusInternalServerError, fmt.Errorf("move file failed")
		}
		meta.UploadState = string(TaskStateUploaded)
		meta.FileSize = totalSize
		session.Ext = mustJSON(meta)
		session.UploadState = meta.UploadState
		session.UpdatedAt = time.Now().UTC()
		_ = store.DB().WithContext(ctx).Save(&session).Error
		var docRow orm.Document
		var completeExt documentExt
		if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", session.DocumentID, session.DatasetID).Take(&docRow).Error; err == nil {
			completeExt = newDocumentExt(finalPath, meta.StoredName, meta.OriginalFilename, totalSize, meta.ContentType, meta.RelativePath, nil)
			docRow.Ext = mustJSON(completeExt)
			docRow.PDFConvertResult = completeExt.ConvertStatus
			docRow.DisplayName = meta.OriginalFilename
			docRow.PID = meta.DocumentPID
			docRow.UpdatedAt = time.Now().UTC()
			_ = store.DB().WithContext(ctx).Save(&docRow).Error
		}
		previewURL := staticFileURLFromFullPath(firstNonEmpty(strings.TrimSpace(completeExt.ParseStoredPath), finalPath))
		return CompleteUploadResponse{TaskID: session.TaskID, UploadID: session.UploadID, DocumentID: session.DocumentID, DatasetID: session.DatasetID, StoredPath: finalPath, ParseStoredPath: strings.TrimSpace(completeExt.ParseStoredPath), ContentURL: documentContentPath(session.DatasetID, session.DocumentID), DownloadURL: documentDownloadPath(session.DatasetID, session.DocumentID), FileURL: previewURL, FileSize: totalSize, ConvertStatus: completeExt.ConvertStatus, ConvertError: completeExt.ConvertError, UploadScope: meta.UploadScope}, http.StatusOK, nil
	}
}

func startTaskInternal(r *http.Request, datasetID, taskID string) error {
	_, err := startTasksInternal(r, datasetID, []string{taskID})
	return err
}

func startTasksInternal(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	resultsByTaskID := make(map[string]StartTaskResult, len(taskIDs))
	seen := map[string]struct{}{}
	orderedUniqueIDs := make([]string, 0, len(taskIDs))
	parseTaskIDs := make([]string, 0, len(taskIDs))
	reparseTaskIDs := make([]string, 0, len(taskIDs))
	copyTaskIDs := make([]string, 0, len(taskIDs))
	moveTaskIDs := make([]string, 0, len(taskIDs))

	for _, rawTaskID := range taskIDs {
		taskID := strings.TrimSpace(rawTaskID)
		if taskID == "" {
			continue
		}
		if _, ok := seen[taskID]; ok {
			continue
		}
		seen[taskID] = struct{}{}
		orderedUniqueIDs = append(orderedUniqueIDs, taskID)

		var taskRow orm.Task
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&taskRow).Error; err != nil {
			resultsByTaskID[taskID] = StartTaskResult{TaskID: taskID, Status: "FAILED", SubmitStatus: "REJECTED", Message: "task not found"}
			continue
		}

		switch TaskType(strings.TrimSpace(taskRow.TaskType)) {
		case TaskTypeParse, TaskTypeParseUploaded:
			parseTaskIDs = append(parseTaskIDs, taskID)
		case TaskTypeReparse:
			reparseTaskIDs = append(reparseTaskIDs, taskID)
		case TaskTypeCopy:
			copyTaskIDs = append(copyTaskIDs, taskID)
		case TaskTypeMove:
			moveTaskIDs = append(moveTaskIDs, taskID)
		default:
			resultsByTaskID[taskID] = StartTaskResult{TaskID: taskID, DocumentID: taskRow.DocID, DisplayName: taskRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "unsupported task type"}
		}
	}

	mergeTaskResults := func(items []StartTaskResult) {
		for _, item := range items {
			resultsByTaskID[item.TaskID] = item
		}
	}

	if len(parseTaskIDs) > 0 {
		items, _ := startParseTasksInternal(r, datasetID, parseTaskIDs)
		mergeTaskResults(items)
	}
	if len(reparseTaskIDs) > 0 {
		items, _ := startReparseTasksInternal(r, datasetID, reparseTaskIDs)
		mergeTaskResults(items)
	}
	if len(copyTaskIDs) > 0 {
		items, _ := startCopyTasksInternal(r, datasetID, copyTaskIDs)
		mergeTaskResults(items)
	}
	if len(moveTaskIDs) > 0 {
		items, _ := startMoveTasksInternal(r, datasetID, moveTaskIDs)
		mergeTaskResults(items)
	}

	resultsResp := make([]StartTaskResult, 0, len(orderedUniqueIDs))
	startedCount := 0
	for _, taskID := range orderedUniqueIDs {
		if result, ok := resultsByTaskID[taskID]; ok {
			if result.Status == "STARTED" {
				startedCount++
			}
			resultsResp = append(resultsResp, result)
		}
	}
	if startedCount == 0 {
		return resultsResp, fmt.Errorf("no tasks submitted successfully")
	}
	return resultsResp, nil
}

func startParseTasksInternal(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	kbID := datasetKbIDByID(datasetID)
	if kbID == "" {
		return nil, fmt.Errorf("dataset kb mapping not found")
	}
	userID := common.UserID(r)
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), store.DB(), userID)
	if err != nil {
		log.Printf("[startParseTasksInternal] failed to load llm_config for user=%s: %v", userID, err)
		llmConfig = nil
	}
	resultsByTaskID := make(map[string]StartTaskResult, len(taskIDs))
	orderedUniqueIDs := make([]string, 0, len(taskIDs))

	type startCandidate struct {
		task   orm.Task
		doc    orm.Document
		docExt documentExt
	}
	candidates := make([]startCandidate, 0, len(taskIDs))
	pdfCandidates := make([]startCandidate, 0, len(taskIDs))
	officeCandidates := make([]startCandidate, 0, len(taskIDs))

	for _, taskID := range taskIDs {
		orderedUniqueIDs = append(orderedUniqueIDs, taskID)
		var taskRow orm.Task
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&taskRow).Error; err != nil {
			resultsByTaskID[taskID] = StartTaskResult{TaskID: taskID, Status: "FAILED", SubmitStatus: "REJECTED", Message: "task not found"}
			continue
		}
		var docRow orm.Document
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.DocID, datasetID).Take(&docRow).Error; err != nil {
			log.Printf("[startTask] document not found task=%s doc=%s dataset=%s err=%v", taskID, taskRow.DocID, datasetID, err)
			resultsByTaskID[taskID] = StartTaskResult{TaskID: taskID, DocumentID: taskRow.DocID, DisplayName: taskRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "document not found"}
			continue
		}
		var dExt documentExt
		_ = json.Unmarshal(docRow.Ext, &dExt)
		if strings.TrimSpace(dExt.StoredPath) == "" {
			resultsByTaskID[taskID] = StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "uploaded file path is empty"}
			continue
		}
		candidate := startCandidate{task: taskRow, doc: docRow, docExt: dExt}
		candidates = append(candidates, candidate)
		if dExt.ConvertRequired {
			officeCandidates = append(officeCandidates, candidate)
		} else {
			pdfCandidates = append(pdfCandidates, candidate)
		}
	}

	if len(candidates) == 0 {
		resultsResp := make([]StartTaskResult, 0, len(orderedUniqueIDs))
		for _, taskID := range orderedUniqueIDs {
			if result, ok := resultsByTaskID[taskID]; ok {
				resultsResp = append(resultsResp, result)
			}
		}
		return resultsResp, fmt.Errorf("no valid tasks to start")
	}

	if len(pdfCandidates) > 0 {
		baseTasks := make([]orm.Task, 0, len(pdfCandidates))
		baseDocs := make([]orm.Document, 0, len(pdfCandidates))
		baseDocExts := make([]documentExt, 0, len(pdfCandidates))
		items := make([]addFileItem, 0, len(pdfCandidates))
		for _, candidate := range pdfCandidates {
			parsePath := parsePathForAdd(candidate.docExt)
			if strings.TrimSpace(parsePath) == "" {
				resultsByTaskID[candidate.task.ID] = StartTaskResult{TaskID: candidate.task.ID, DocumentID: candidate.doc.ID, DisplayName: candidate.doc.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "parse file path is empty"}
				continue
			}
			baseTasks = append(baseTasks, candidate.task)
			baseDocs = append(baseDocs, candidate.doc)
			baseDocExts = append(baseDocExts, candidate.docExt)
			items = append(items, buildAddFileItem(datasetID, candidate.task, candidate.doc, candidate.docExt, parsePath))
		}
		if len(baseTasks) > 0 {
			extResults, err := callExternalAddDocs(r, addRequest{Items: items, KbID: kbID, SourceType: "EXTERNAL", IdempotencyKey: newTaskID(), ModelConfig: llmConfig})
			if err != nil {
				for i, taskRow := range baseTasks {
					resolved := common.ResolveAppError(err.Error(), http.StatusBadGateway)
					resultsByTaskID[taskRow.ID] = StartTaskResult{TaskID: taskRow.ID, DocumentID: baseDocs[i].ID, DisplayName: baseDocs[i].DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)}
				}
			} else {
				created, bindErr := bindExternalBatchAddResults(datasetID, baseTasks, baseDocs, baseDocExts, extResults)
				if bindErr != nil {
					for i, taskRow := range baseTasks {
						resolved := common.ResolveAppError(bindErr.Error(), http.StatusBadGateway)
						resultsByTaskID[taskRow.ID] = StartTaskResult{TaskID: taskRow.ID, DocumentID: baseDocs[i].ID, DisplayName: baseDocs[i].DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)}
					}
				} else {
					for _, row := range created {
						resultsByTaskID[row.ID] = StartTaskResult{TaskID: row.ID, DocumentID: row.DocID, DisplayName: row.DisplayName, Status: "STARTED", SubmitStatus: "SUBMITTED"}
					}
				}
			}
		}
	}

	if len(officeCandidates) > 0 {
		type officeOutcome struct {
			task   orm.Task
			doc    orm.Document
			docExt documentExt
			result StartTaskResult
		}
		outcomes := make([]officeOutcome, len(officeCandidates))
		workerLimit := officeConvertWorkers()
		guard := make(chan struct{}, workerLimit)
		var wg sync.WaitGroup
		for i, candidate := range officeCandidates {
			wg.Add(1)
			guard <- struct{}{}
			go func(idx int, candidate startCandidate) {
				defer wg.Done()
				defer func() { <-guard }()
				dExt := cloneDocumentExt(candidate.docExt)
				callOfficeConvertWithRetry(r.Context(), &dExt)
				persistDocumentConvertState(r.Context(), datasetID, candidate.doc.ID, dExt)
				parsePath := parsePathForAdd(dExt)
				if strings.TrimSpace(parsePath) == "" {
					outcomes[idx] = officeOutcome{task: candidate.task, doc: candidate.doc, docExt: dExt, result: StartTaskResult{TaskID: candidate.task.ID, DocumentID: candidate.doc.ID, DisplayName: candidate.doc.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "parse file path is empty"}}
					return
				}
				item := buildAddFileItem(datasetID, candidate.task, candidate.doc, dExt, parsePath)
				extResults, err := callExternalAddDocs(r, addRequest{Items: []addFileItem{item}, KbID: kbID, SourceType: "EXTERNAL", IdempotencyKey: newTaskID(), ModelConfig: llmConfig})
				if err != nil {
					resolved := common.ResolveAppError(err.Error(), http.StatusBadGateway)
					outcomes[idx] = officeOutcome{task: candidate.task, doc: candidate.doc, docExt: dExt, result: StartTaskResult{TaskID: candidate.task.ID, DocumentID: candidate.doc.ID, DisplayName: candidate.doc.DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)}}
					return
				}
				created, bindErr := bindExternalBatchAddResults(datasetID, []orm.Task{candidate.task}, []orm.Document{candidate.doc}, []documentExt{dExt}, extResults)
				if bindErr != nil || len(created) == 0 {
					msg := "bind external add result failed"
					if bindErr != nil {
						msg = bindErr.Error()
					}
					outcomes[idx] = officeOutcome{task: candidate.task, doc: candidate.doc, docExt: dExt, result: StartTaskResult{TaskID: candidate.task.ID, DocumentID: candidate.doc.ID, DisplayName: candidate.doc.DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: msg}}
					return
				}
				outcomes[idx] = officeOutcome{task: candidate.task, doc: candidate.doc, docExt: dExt, result: StartTaskResult{TaskID: created[0].ID, DocumentID: created[0].DocID, DisplayName: created[0].DisplayName, Status: "STARTED", SubmitStatus: "SUBMITTED"}}
			}(i, candidate)
		}
		wg.Wait()
		for _, outcome := range outcomes {
			if strings.TrimSpace(outcome.task.ID) == "" {
				continue
			}
			resultsByTaskID[outcome.task.ID] = outcome.result
		}
	}

	resultsResp := make([]StartTaskResult, 0, len(orderedUniqueIDs))
	startedCount := 0
	for _, taskID := range orderedUniqueIDs {
		if result, ok := resultsByTaskID[taskID]; ok {
			if result.Status == "STARTED" {
				startedCount++
			}
			resultsResp = append(resultsResp, result)
		}
	}
	if startedCount == 0 {
		return resultsResp, fmt.Errorf("no tasks submitted successfully")
	}
	return resultsResp, nil
}

func buildTaskResponse(r *http.Request, row orm.Task) TaskResponse {
	var ext taskExt
	_ = json.Unmarshal(row.Ext, &ext)
	resp := TaskResponse{
		Name:            "datasets/" + row.DatasetID + "/tasks/" + row.ID,
		TaskID:          row.ID,
		DocumentID:      row.DocID,
		DataSourceType:  firstNonEmpty(ext.DataSourceType, "LOCAL_FILE"),
		Creator:         row.CreateUserName,
		TaskInfo:        TaskInfo{TotalDocumentCount: 1},
		CreateTime:      row.CreatedAt.UTC().Format(time.RFC3339),
		DisplayName:     firstNonEmpty(row.DisplayName, ext.DisplayName),
		TaskType:        firstNonEmpty(row.TaskType, ext.TaskType),
		TargetDatasetID: firstNonEmpty(row.TargetDatasetID, ext.TargetDatasetID),
		TargetPID:       firstNonEmpty(row.TargetPID, ext.TargetPID),
	}
	lazyDoc := ""
	var docRow orm.Document
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", row.DocID, row.DatasetID).Take(&docRow).Error; err == nil {
		lazyDoc = strings.TrimSpace(docRow.LazyllmDocID)
		var dExt documentExt
		_ = json.Unmarshal(docRow.Ext, &dExt)
		resp.PDFConvertResult = strings.TrimSpace(docRow.PDFConvertResult)
		resp.ParseStoredPath = strings.TrimSpace(dExt.ParseStoredPath)
		resp.ConvertRequired = dExt.ConvertRequired
		resp.ConvertStatus = dExt.ConvertStatus
		resp.ConvertError = dExt.ConvertError
		resp.DocumentInfo = []TaskDocumentInfo{{DocumentID: docRow.ID, DisplayName: docRow.DisplayName, DocumentSize: dExt.FileSize}}
		if dExt.StoredName != "" || dExt.StoredPath != "" || dExt.ParseStoredPath != "" {
			resp.Files = []TaskFile{{DisplayName: docRow.DisplayName, StoredName: dExt.StoredName, StoredPath: dExt.StoredPath, ParseStoredPath: dExt.ParseStoredPath, FileSize: dExt.FileSize, RelativePath: dExt.RelativePath, ContentType: dExt.ContentType}}
		}
		resp.TaskInfo.TotalDocumentSize = dExt.FileSize
		resp.TaskInfo.TotalDocumentCount = 1
	}
	var extTask readonlyorm.LazyLLMDocServiceTaskRow
	extTaskFound := false
	lazyTask := strings.TrimSpace(row.LazyllmTaskID)
	if lazyTask != "" && lazyDoc != "" {
		if err := store.LazyLLMDB().WithContext(r.Context()).Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).Where("task_id = ? AND doc_id = ?", lazyTask, lazyDoc).Take(&extTask).Error; err == nil {
			extTaskFound = true
		}
	}
	if !extTaskFound && lazyDoc != "" && TaskType(strings.TrimSpace(row.TaskType)) != TaskTypeReparse {
		if err := store.LazyLLMDB().WithContext(r.Context()).Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).Where("doc_id = ?", lazyDoc).Order("updated_at DESC").Take(&extTask).Error; err == nil {
			extTaskFound = true
		}
	}
	if extTaskFound {
		resp.TaskState = strings.TrimSpace(extTask.Status)
		if extTask.ErrorMsg != nil && strings.TrimSpace(*extTask.ErrorMsg) != "" {
			resp.ErrMsg = strings.TrimSpace(*extTask.ErrorMsg)
		}
		if extTask.StartedAt != nil {
			resp.StartTime = extTask.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if extTask.FinishedAt != nil {
			resp.FinishTime = extTask.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
	} else {
		resp.TaskState = firstNonEmpty(strings.TrimSpace(ext.TaskState), string(TaskStateCreating))
		resp.ErrMsg = strings.TrimSpace(ext.ErrorMessage)
	}
	var extDoc readonlyorm.LazyLLMDocRow
	if lazyDoc != "" {
		if err := store.LazyLLMDB().WithContext(r.Context()).Table((readonlyorm.LazyLLMDocRow{}).TableName()).Where("doc_id = ?", lazyDoc).Take(&extDoc).Error; err == nil {
			sz := int64(0)
			if extDoc.SizeBytes != nil {
				sz = int64(*extDoc.SizeBytes)
			}
			if len(resp.DocumentInfo) == 0 {
				resp.DocumentInfo = []TaskDocumentInfo{{DocumentID: row.DocID, DisplayName: extDoc.Filename, DocumentState: strings.TrimSpace(extDoc.UploadStatus), DocumentSize: sz}}
			} else {
				resp.DocumentInfo[0].DisplayName = firstNonEmpty(resp.DocumentInfo[0].DisplayName, extDoc.Filename)
				resp.DocumentInfo[0].DocumentState = strings.TrimSpace(extDoc.UploadStatus)
				resp.DocumentInfo[0].DocumentSize = maxInt64(resp.DocumentInfo[0].DocumentSize, sz)
			}
			if len(resp.Files) == 0 {
				resp.Files = []TaskFile{{DisplayName: extDoc.Filename, StoredName: filepath.Base(extDoc.Path), StoredPath: extDoc.Path, FileSize: sz}}
			} else if resp.Files[0].FileSize == 0 {
				resp.Files[0].FileSize = sz
			}
			resp.TaskInfo.TotalDocumentSize = maxInt64(resp.TaskInfo.TotalDocumentSize, sz)
		}
	}
	if isSuccessState(resp.TaskState) {
		resp.TaskInfo.SucceedDocumentSize = resp.TaskInfo.TotalDocumentSize
		resp.TaskInfo.SucceedDocumentCount = resp.TaskInfo.TotalDocumentCount
	} else if resp.TaskState == string(TaskStateFailed) {
		resp.TaskInfo.FailedDocumentSize = resp.TaskInfo.TotalDocumentSize
		resp.TaskInfo.FailedDocumentCount = resp.TaskInfo.TotalDocumentCount
	}
	return resp
}

func updateTaskDeletion(w http.ResponseWriter, r *http.Request, deleted bool) {
	datasetID := datasetIDFromPath(r)
	taskID := taskIDFromPath(r)
	if datasetID == "" || taskID == "" {
		common.ReplyErr(w, "missing dataset or task", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}
	now := time.Now().UTC()
	if deleted {
		_ = store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Update("deleted_at", now).Error
	}
	w.WriteHeader(http.StatusOK)
}

func updateTaskStateEndpoint(w http.ResponseWriter, r *http.Request, action string) {
	datasetID := datasetIDFromPath(r)
	taskID := taskIDFromPath(r)
	if datasetID == "" || taskID == "" {
		common.ReplyErr(w, "missing dataset or task", http.StatusBadRequest)
		return
	}
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return
	}

	var raw map[string]any
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && err != io.EOF {
			common.ReplyErr(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	var taskRow orm.Task
	if err := store.DB().WithContext(r.Context()).
		Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).
		First(&taskRow).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "request failed", err), http.StatusInternalServerError)
		return
	}

	bodyTaskIDValue, _ := raw["task_id"].(string)
	bodyTaskID := strings.TrimSpace(bodyTaskIDValue)
	if bodyTaskID != "" && bodyTaskID != taskID {
		common.ReplyErr(w, "task_id in body does not match path", http.StatusBadRequest)
		return
	}

	externalTaskID := strings.TrimSpace(taskRow.LazyllmTaskID)
	if externalTaskID == "" {
		common.ReplyErr(w, "external task id is empty", http.StatusBadRequest)
		return
	}

	err := callExternalSuspendJob(r, ExternalCancelTaskRequest{TaskID: externalTaskID})
	if err != nil {
		common.ReplyAppErr(w, common.ResolveAppError(err.Error(), http.StatusBadGateway))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func loadUploadMeta(session orm.UploadSession) (uploadMeta, string, error) {
	var meta uploadMeta
	_ = json.Unmarshal(session.Ext, &meta)
	dir := uploadDirForMeta(meta, session)
	if meta.UploadID == "" {
		b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			return meta, dir, err
		}
		if err := json.Unmarshal(b, &meta); err != nil {
			return meta, dir, err
		}
	}
	if strings.TrimSpace(meta.UploadScope) == "" {
		meta.UploadScope = uploadScopeTask
	}
	if strings.TrimSpace(meta.CreateUserID) == "" {
		meta.CreateUserID = strings.TrimSpace(session.CreateUserID)
	}
	if strings.TrimSpace(meta.CreateUserName) == "" {
		meta.CreateUserName = strings.TrimSpace(session.CreateUserName)
	}
	return meta, dir, nil
}

func uploadDirForMeta(meta uploadMeta, session orm.UploadSession) string {
	scope := strings.ToUpper(strings.TrimSpace(meta.UploadScope))
	switch scope {
	case uploadScopeDataset:
		return buildDatasetUploadDir(session.TenantID, session.DatasetID, session.UploadID)
	case uploadScopeTemp:
		return buildTempUploadDir(firstNonEmpty(meta.CreateUserID, session.CreateUserID), session.UploadID)
	default:
		return buildTaskUploadDir(session.TenantID, session.DatasetID, session.TaskID, session.UploadID)
	}
}

func multipartOrSingle(totalParts int) string {
	if totalParts > 1 {
		return "MULTIPART"
	}
	return "SINGLE"
}

func appendUniquePart(parts []int, part int) []int {
	for _, p := range parts {
		if p == part {
			return parts
		}
	}
	return append(parts, part)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isSuccessState(state string) bool {
	return state == string(TaskStateSucceeded) || state == "SUCCEEDED"
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func validateCreateTaskItem(item CreateTaskItem, datasetID, taskType string) error {
	if strings.TrimSpace(datasetID) == "" {
		return fmt.Errorf("dataset is required")
	}
	switch TaskType(taskType) {
	case TaskTypeParse, TaskTypeParseUploaded, TaskTypeReparse, TaskTypeCopy, TaskTypeMove:
	default:
		return fmt.Errorf("unsupported task type")
	}
	if strings.TrimSpace(item.Task.DocumentID) != "" && len(item.Task.DocumentIDs) > 0 {
		return fmt.Errorf("document_id and document_ids cannot be set together")
	}
	if (TaskType(taskType) == TaskTypeCopy || TaskType(taskType) == TaskTypeMove) && strings.TrimSpace(item.Task.TargetDatasetID) == "" {
		return fmt.Errorf("target_dataset_id is required for copy/move task")
	}
	// target_pid can be empty for copy/move, which means moving/copying into dataset root.
	if strings.TrimSpace(item.UploadFileID) != "" {
		if strings.TrimSpace(item.Task.DocumentID) != "" || len(item.Task.DocumentIDs) > 0 {
			return fmt.Errorf("document_id/document_ids cannot be set when upload_file_id is used")
		}
		for _, f := range item.Task.Files {
			if strings.TrimSpace(f.StoredPath) != "" {
				return fmt.Errorf("stored_path must not be provided when upload_file_id is used")
			}
		}
		return nil
	}
	if TaskType(taskType) == TaskTypeParse || TaskType(taskType) == TaskTypeParseUploaded {
		return fmt.Errorf("upload_file_id is required for parse task")
	}
	if strings.TrimSpace(item.Task.DocumentID) == "" && len(item.Task.DocumentIDs) == 0 {
		return fmt.Errorf("document_id is required")
	}
	return nil
}

func expandCreateTaskItems(item CreateTaskItem, taskType string) []CreateTaskItem {
	if strings.TrimSpace(item.UploadFileID) != "" {
		return []CreateTaskItem{item}
	}
	if strings.TrimSpace(item.Task.DocumentID) != "" {
		return []CreateTaskItem{item}
	}
	if len(item.Task.DocumentIDs) <= 1 {
		return []CreateTaskItem{item}
	}
	switch TaskType(taskType) {
	case TaskTypeReparse, TaskTypeCopy, TaskTypeMove:
		expanded := make([]CreateTaskItem, 0, len(item.Task.DocumentIDs))
		for _, documentID := range item.Task.DocumentIDs {
			docID := strings.TrimSpace(documentID)
			if docID == "" {
				continue
			}
			next := item
			next.Task.DocumentID = docID
			next.Task.DocumentIDs = nil
			expanded = append(expanded, next)
		}
		if len(expanded) > 0 {
			return expanded
		}
	}
	return []CreateTaskItem{item}
}

func bindExternalAddResults(datasetID string, baseTask orm.Task, baseDoc orm.Document, baseDocExt documentExt, results []addResultItem) error {
	_, err := bindExternalBatchAddResults(datasetID, []orm.Task{baseTask}, []orm.Document{baseDoc}, []documentExt{baseDocExt}, results)
	return err
}

func bindExternalBatchAddResults(datasetID string, baseTasks []orm.Task, baseDocs []orm.Document, baseDocExts []documentExt, results []addResultItem) ([]orm.Task, error) {
	if len(baseTasks) == 0 || len(baseDocs) == 0 || len(baseDocExts) == 0 {
		return nil, fmt.Errorf("empty base task/document set")
	}
	if len(baseTasks) != len(baseDocs) || len(baseDocs) != len(baseDocExts) {
		return nil, fmt.Errorf("base task/document set size mismatch")
	}
	matchedResults, err := matchAddResultsPrecisely(baseTasks, baseDocs, baseDocExts, results)
	if err != nil {
		return nil, err
	}
	created := make([]orm.Task, 0, len(matchedResults))
	now := time.Now().UTC()
	err = store.DB().Transaction(func(tx *gorm.DB) error {
		for i, result := range matchedResults {
			baseTask := baseTasks[i]
			baseDoc := baseDocs[i]
			baseDocExt := baseDocExts[i]
			newLazyllmDoc := strings.TrimSpace(result.DocID)
			newLazyllmTask := strings.TrimSpace(result.TaskID)
			displayName := firstNonEmpty(result.DisplayName, baseDoc.DisplayName)
			docExt := baseDocExt
			updatesDoc := map[string]any{"lazyllm_doc_id": newLazyllmDoc, "file_id": baseDoc.ID, "display_name": displayName, "ext": mustJSON(docExt), "updated_at": now}
			if err := tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", baseDoc.ID, datasetID).Updates(updatesDoc).Error; err != nil {
				return err
			}
			var ext taskExt
			_ = json.Unmarshal(baseTask.Ext, &ext)
			ext.DisplayName = displayName
			ext.DataSourceType = "LOCAL_FILE"
			updatesTask := map[string]any{"lazyllm_task_id": newLazyllmTask, "display_name": displayName, "ext": mustJSON(ext), "updated_at": now}
			if err := tx.Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", baseTask.ID, datasetID).Updates(updatesTask).Error; err != nil {
				return err
			}
			created = append(created, orm.Task{ID: baseTask.ID, LazyllmTaskID: newLazyllmTask, DocID: baseTask.DocID, KbID: baseTask.KbID, AlgoID: baseTask.AlgoID, DatasetID: datasetID, TaskType: baseTask.TaskType, DocumentPID: baseTask.DocumentPID, TargetPID: baseTask.TargetPID, TargetDatasetID: baseTask.TargetDatasetID, DisplayName: displayName, Ext: mustJSON(ext), BaseModel: orm.BaseModel{CreateUserID: baseTask.CreateUserID, CreateUserName: baseTask.CreateUserName, CreatedAt: baseTask.CreatedAt, UpdatedAt: now}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func matchAddResultsPrecisely(baseTasks []orm.Task, baseDocs []orm.Document, baseDocExts []documentExt, results []addResultItem) ([]addResultItem, error) {
	if len(results) == 0 {
		results = make([]addResultItem, 0, len(baseTasks))
		for i := range baseTasks {
			results = append(results, addResultItem{
				TaskID:         baseTasks[i].LazyllmTaskID,
				CoreTaskID:     baseTasks[i].ID,
				DocID:          baseDocs[i].LazyllmDocID,
				CoreDocumentID: baseDocs[i].ID,
				FilePath:       parsePathForAdd(baseDocExts[i]),
				DisplayName:    baseDocs[i].DisplayName,
			})
		}
	}
	byCoreTaskID := make(map[string]addResultItem, len(results))
	byCoreDocumentID := make(map[string]addResultItem, len(results))
	byDocID := make(map[string]addResultItem, len(results))
	byFilePath := make(map[string]addResultItem, len(results))
	used := make(map[int]bool, len(results))
	for i, result := range results {
		if v := strings.TrimSpace(result.CoreTaskID); v != "" {
			byCoreTaskID[v] = result
		}
		if v := strings.TrimSpace(result.CoreDocumentID); v != "" {
			byCoreDocumentID[v] = result
		}
		if v := strings.TrimSpace(result.DocID); v != "" {
			byDocID[v] = result
		}
		if v := strings.TrimSpace(result.FilePath); v != "" {
			byFilePath[v] = result
		}
		_ = i
	}
	matched := make([]addResultItem, 0, len(baseTasks))
	for i := range baseTasks {
		if result, ok := byCoreTaskID[strings.TrimSpace(baseTasks[i].ID)]; ok {
			matched = append(matched, result)
			markMatchedIndex(results, used, result)
			continue
		}
		if result, ok := byCoreDocumentID[strings.TrimSpace(baseDocs[i].ID)]; ok {
			matched = append(matched, result)
			markMatchedIndex(results, used, result)
			continue
		}
		if ld := strings.TrimSpace(baseDocs[i].LazyllmDocID); ld != "" {
			if result, ok := byDocID[ld]; ok {
				matched = append(matched, result)
				markMatchedIndex(results, used, result)
				continue
			}
		}
		parseP := strings.TrimSpace(parsePathForAdd(baseDocExts[i]))
		srcP := strings.TrimSpace(baseDocExts[i].StoredPath)
		if result, ok := byFilePath[parseP]; ok {
			matched = append(matched, result)
			markMatchedIndex(results, used, result)
			continue
		}
		if result, ok := byFilePath[srcP]; ok {
			matched = append(matched, result)
			markMatchedIndex(results, used, result)
			continue
		}
		fallbackIdx := firstUnusedResultIndex(results, used)
		if fallbackIdx < 0 {
			return nil, fmt.Errorf("external add result cannot be matched precisely")
		}
		used[fallbackIdx] = true
		matched = append(matched, results[fallbackIdx])
	}
	return matched, nil
}

func markMatchedIndex(results []addResultItem, used map[int]bool, target addResultItem) {
	for i, result := range results {
		if used[i] {
			continue
		}
		if strings.TrimSpace(result.CoreTaskID) != "" && strings.TrimSpace(result.CoreTaskID) == strings.TrimSpace(target.CoreTaskID) {
			used[i] = true
			return
		}
		if strings.TrimSpace(result.DocID) != "" && strings.TrimSpace(result.DocID) == strings.TrimSpace(target.DocID) {
			used[i] = true
			return
		}
		if strings.TrimSpace(result.CoreDocumentID) != "" && strings.TrimSpace(result.CoreDocumentID) == strings.TrimSpace(target.CoreDocumentID) {
			used[i] = true
			return
		}
		if strings.TrimSpace(result.FilePath) != "" && strings.TrimSpace(result.FilePath) == strings.TrimSpace(target.FilePath) {
			used[i] = true
			return
		}
	}
}

func firstUnusedResultIndex(results []addResultItem, used map[int]bool) int {
	for i := range results {
		if !used[i] {
			return i
		}
	}
	return -1
}

func createUploadedTaskAndDocument(r *http.Request, ds *orm.Dataset, datasetID, userID, userName string, now time.Time, fh *multipart.FileHeader, relativePath, documentPID string, tags []string) (orm.Task, orm.Document, documentExt, error) {
	file, err := fh.Open()
	if err != nil {
		return orm.Task{}, orm.Document{}, documentExt{}, fmt.Errorf("open upload file failed")
	}
	defer file.Close()
	documentID := newDocID()
	taskID := newTaskID()
	storedName := storedFileName(fh.Filename, documentID)
	finalDir := buildDatasetDocFileDir(ds.TenantID, datasetID, relativePath, documentID)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return orm.Task{}, orm.Document{}, documentExt{}, fmt.Errorf("create final dir failed")
	}
	finalPath := filepath.Join(finalDir, storedName)
	out, err := os.Create(finalPath)
	if err != nil {
		return orm.Task{}, orm.Document{}, documentExt{}, fmt.Errorf("create upload target failed")
	}
	size, err := io.Copy(out, file)
	_ = out.Close()
	if err != nil {
		return orm.Task{}, orm.Document{}, documentExt{}, fmt.Errorf("save upload file failed")
	}
	docExt := newDocumentExt(finalPath, storedName, fh.Filename, size, fh.Header.Get("Content-Type"), relativePath, tags)
	docRow := orm.Document{ID: documentID, LazyllmDocID: "", DatasetID: datasetID, DisplayName: fh.Filename, PID: documentPID, Tags: mustJSON(tags), FileID: documentID, PDFConvertResult: docExt.ConvertStatus, Ext: mustJSON(docExt), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
	tExt := taskExt{TaskType: string(TaskTypeParseUploaded), DocumentPID: documentPID, DisplayName: fh.Filename, DataSourceType: "LOCAL_FILE", Files: []TaskFile{{DisplayName: fh.Filename, StoredName: storedName, StoredPath: finalPath, FileSize: size, RelativePath: relativePath, ContentType: fh.Header.Get("Content-Type")}}, DocumentTags: tags}
	taskRow := orm.Task{ID: taskID, LazyllmTaskID: "", DocID: documentID, KbID: datasetID, AlgoID: datasetAlgoIDByID(datasetID), DatasetID: datasetID, TaskType: string(TaskTypeParseUploaded), DocumentPID: documentPID, DisplayName: fh.Filename, Ext: mustJSON(tExt), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
	if err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&docRow).Error; err != nil {
			return err
		}
		if err := tx.Create(&taskRow).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return orm.Task{}, orm.Document{}, documentExt{}, fmt.Errorf("create uploaded task failed")
	}
	recalcAffectedFolderStats(r.Context(), datasetID, documentPID)
	return taskRow, docRow, docExt, nil
}

func createUploadedFileRecord(r *http.Request, ds *orm.Dataset, datasetID, userID, userName string, now time.Time, fh *multipart.FileHeader, relativePath, documentPID string, tags []string) (orm.UploadedFile, uploadedFileExt, error) {
	file, err := fh.Open()
	if err != nil {
		return orm.UploadedFile{}, uploadedFileExt{}, fmt.Errorf("open upload file failed")
	}
	defer file.Close()
	uploadFileID := newUploadID()
	storedName := storedFileName(fh.Filename, uploadFileID)
	finalDir := buildDatasetDocFileDir(ds.TenantID, datasetID, relativePath, uploadFileID)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return orm.UploadedFile{}, uploadedFileExt{}, fmt.Errorf("create final dir failed")
	}
	finalPath := filepath.Join(finalDir, storedName)
	out, err := os.Create(finalPath)
	if err != nil {
		return orm.UploadedFile{}, uploadedFileExt{}, fmt.Errorf("create upload target failed")
	}
	size, err := io.Copy(out, file)
	_ = out.Close()
	if err != nil {
		return orm.UploadedFile{}, uploadedFileExt{}, fmt.Errorf("save upload file failed")
	}
	ext := uploadedFileExt{StoredPath: finalPath, StoredName: storedName, OriginalFilename: fh.Filename, FileSize: size, ContentType: fh.Header.Get("Content-Type"), RelativePath: relativePath, DocumentPID: documentPID, DocumentTags: tags}
	row := orm.UploadedFile{UploadFileID: uploadFileID, DatasetID: datasetID, TenantID: ds.TenantID, TaskID: "", DocumentID: "", Status: UploadedFileStateUploaded, Ext: mustJSON(ext), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
	if err := store.DB().WithContext(r.Context()).Create(&row).Error; err != nil {
		return orm.UploadedFile{}, uploadedFileExt{}, fmt.Errorf("create uploaded file failed")
	}
	return row, ext, nil
}

func createTaskFromUploadedFile(r *http.Request, datasetID, userID, userName string, item CreateTaskItem, tType string) ([]orm.Task, error) {
	uploadFileID := strings.TrimSpace(item.UploadFileID)
	if uploadFileID == "" {
		return nil, fmt.Errorf("upload_file_id is required")
	}
	taskID := strings.TrimSpace(item.TaskID)
	if taskID == "" {
		taskID = newTaskID()
	}
	now := time.Now().UTC()
	var uploaded0 orm.UploadedFile
	if err := store.DB().WithContext(r.Context()).Where("upload_file_id = ? AND deleted_at IS NULL", uploadFileID).Take(&uploaded0).Error; err != nil {
		return nil, fmt.Errorf("upload file not found")
	}
	if strings.TrimSpace(uploaded0.DatasetID) != "" && strings.TrimSpace(uploaded0.DatasetID) != datasetID {
		return nil, fmt.Errorf("upload file does not belong to current dataset")
	}
	if strings.TrimSpace(uploaded0.CreateUserID) != userID {
		return nil, fmt.Errorf("upload file does not belong to current user")
	}
	if strings.TrimSpace(uploaded0.Status) != UploadedFileStateUploaded {
		return nil, fmt.Errorf("upload file is not available for binding")
	}
	var upExt uploadedFileExt
	_ = json.Unmarshal(uploaded0.Ext, &upExt)

	documentPID := firstNonEmpty(strings.TrimSpace(item.Task.DocumentPID), strings.TrimSpace(upExt.DocumentPID))
	relativePath := firstNonEmpty(strings.TrimSpace(item.Task.RelativePath), strings.TrimSpace(upExt.RelativePath))
	if relativePath != "" && documentPID == "" {
		documentPID = createTopLevelFolder(r.Context(), datasetID, userID, userName, relativePath)
	}

	if strings.ToLower(filepath.Ext(upExt.OriginalFilename)) == ".zip" {
		if documentPID == "" {
			zipFolderName := strings.TrimSuffix(upExt.OriginalFilename, filepath.Ext(upExt.OriginalFilename))
			zipFolderName = strings.TrimSpace(zipFolderName)
			if zipFolderName == "" {
				zipFolderName = "zip-upload"
			}
			virtualRelPath := zipFolderName + "/__placeholder__"
			documentPID = createTopLevelFolder(r.Context(), datasetID, userID, userName, virtualRelPath)
		}
		tasks, err := createTasksFromZipUpload(r, datasetID, userID, userName, uploadFileID, upExt, documentPID, item.Task.DocumentTags)
		if err != nil {
			return nil, err
		}
		return tasks, nil
	}

	documentID := strings.TrimSpace(item.Task.DocumentID)
	if documentID == "" {
		if len(item.Task.DocumentIDs) == 1 {
			documentID = strings.TrimSpace(item.Task.DocumentIDs[0])
		} else {
			documentID = newDocID()
		}
	}
	displayName := strings.TrimSpace(item.Task.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(upExt.OriginalFilename)
	}
	if displayName == "" {
		displayName = documentID
	}
	tags := item.Task.DocumentTags
	if len(tags) == 0 {
		tags = append([]string(nil), upExt.DocumentTags...)
	}
	docExt := newDocumentExt(upExt.StoredPath, upExt.StoredName, upExt.OriginalFilename, upExt.FileSize, upExt.ContentType, upExt.RelativePath, tags)

	var created orm.Task
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var uploaded orm.UploadedFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("upload_file_id = ? AND deleted_at IS NULL", uploadFileID).Take(&uploaded).Error; err != nil {
			return fmt.Errorf("upload file not found")
		}
		if strings.TrimSpace(uploaded.DatasetID) != "" && strings.TrimSpace(uploaded.DatasetID) != datasetID {
			return fmt.Errorf("upload file does not belong to current dataset")
		}
		if strings.TrimSpace(uploaded.CreateUserID) != userID {
			return fmt.Errorf("upload file does not belong to current user")
		}
		if strings.TrimSpace(uploaded.Status) != UploadedFileStateUploaded {
			return fmt.Errorf("upload file is not available for binding")
		}
		tFiles := item.Task.Files
		if len(tFiles) == 0 {
			tFiles = []TaskFile{{DisplayName: displayName, StoredName: upExt.StoredName, StoredPath: upExt.StoredPath, FileSize: upExt.FileSize, RelativePath: upExt.RelativePath, ContentType: upExt.ContentType}}
		}
		tExt := taskExt{TaskType: tType, DocumentPID: documentPID, DisplayName: displayName, TargetDatasetID: strings.TrimSpace(item.Task.TargetDatasetID), TargetPID: strings.TrimSpace(item.Task.TargetPID), TargetPath: strings.TrimSpace(item.Task.TargetPath), DataSourceType: firstNonEmpty(strings.TrimSpace(item.Task.DataSourceType), "LOCAL_FILE"), Files: tFiles, DocumentTags: tags}
		docRow := orm.Document{ID: documentID, LazyllmDocID: "", DatasetID: datasetID, DisplayName: displayName, PID: documentPID, Tags: mustJSON(tags), FileID: documentID, PDFConvertResult: docExt.ConvertStatus, Ext: mustJSON(docExt), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
		taskRow := orm.Task{ID: taskID, LazyllmTaskID: "", DocID: documentID, KbID: datasetID, AlgoID: datasetAlgoIDByID(datasetID), DatasetID: datasetID, TaskType: tType, DocumentPID: documentPID, TargetPID: strings.TrimSpace(item.Task.TargetPID), TargetDatasetID: strings.TrimSpace(item.Task.TargetDatasetID), DisplayName: displayName, Ext: mustJSON(tExt), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
		if err := tx.Create(&docRow).Error; err != nil {
			return fmt.Errorf("create document failed")
		}
		if err := tx.Create(&taskRow).Error; err != nil {
			return fmt.Errorf("create task failed")
		}
		updates := map[string]any{"status": UploadedFileStateBound, "task_id": taskID, "document_id": documentID, "updated_at": now}
		if err := tx.Model(&orm.UploadedFile{}).Where("upload_file_id = ? AND deleted_at IS NULL", uploadFileID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update uploaded file state failed")
		}
		created = taskRow
		return nil
	})
	if err != nil {
		return nil, err
	}
	recalcAffectedFolderStats(r.Context(), datasetID, documentPID)
	return []orm.Task{created}, nil
}

func createTasksFromZipUpload(r *http.Request, datasetID, userID, userName, uploadFileID string, upExt uploadedFileExt, documentPID string, tags []string) ([]orm.Task, error) {
	log.Printf("[zip] createTasksFromZipUpload start uploadFileID=%s storedPath=%s documentPID=%s", uploadFileID, upExt.StoredPath, documentPID)
	zipBytes, err := os.ReadFile(upExt.StoredPath)
	if err != nil {
		return nil, fmt.Errorf("read zip file failed: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("parse zip file failed: %w", err)
	}

	type zipEntry struct {
		f    *zip.File
		name string
	}
	var entries []zipEntry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.Trim(filepath.ToSlash(f.Name), "/")
		if name == "" {
			continue
		}
		// skip macOS metadata: __MACOSX/ prefix or ._* resource fork files
		if strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		base := filepath.Base(name)
		if strings.HasPrefix(base, "._") {
			continue
		}
		entries = append(entries, zipEntry{f: f, name: name})
	}
	log.Printf("[zip] collected %d file entries from zip (after filtering macOS metadata)", len(entries))

	// test/a.pdf, test/b.pdf  → topDir="test"
	// a.pdf, b.pdf            → topDir=""
	// test/a.pdf, other/b.pdf → topDir=""
	topDir := ""
	for i, e := range entries {
		parts := strings.SplitN(e.name, "/", 2)
		if len(parts) < 2 {
			topDir = ""
			break
		}
		if i == 0 {
			topDir = parts[0]
		} else if parts[0] != topDir {
			topDir = ""
			break
		}
	}
	log.Printf("[zip] topDir detected: %q", topDir)

	now := time.Now().UTC()
	tasks := make([]orm.Task, 0)

	for _, e := range entries {
		name := e.name
		// strip common top-level directory
		if topDir != "" {
			name = strings.TrimPrefix(name, topDir+"/")
		}
		// only keep first-level files: no "/" in path (direct children, ignore subdirectory files)
		if strings.Contains(name, "/") || name == "" {
			log.Printf("[zip] skip entry %q (after strip: %q)", e.name, name)
			continue
		}
		filename := name
		log.Printf("[zip] processing entry %q -> filename=%q", e.name, filename)

		rc, err := e.f.Open()
		if err != nil {
			continue
		}

		documentID := newDocID()
		taskID := newTaskID()
		storedName := storedFileName(filename, documentID)
		finalDir := filepath.Join(filepath.Dir(upExt.StoredPath), "zip_extracted", documentID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("create dir failed")
		}
		finalPath := filepath.Join(finalDir, storedName)
		out, err := os.Create(finalPath)
		if err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("create file failed")
		}
		size, err := io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("save file failed")
		}

		if len(tags) == 0 {
			tags = append([]string(nil), upExt.DocumentTags...)
		}
		docExt := newDocumentExt(finalPath, storedName, filename, size, "", "", tags)
		docRow := orm.Document{
			ID:               documentID,
			DatasetID:        datasetID,
			DisplayName:      filename,
			PID:              documentPID,
			Tags:             mustJSON(tags),
			FileID:           documentID,
			PDFConvertResult: docExt.ConvertStatus,
			Ext:              mustJSON(docExt),
			BaseModel:        orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
		}
		tExt := taskExt{
			TaskType:       string(TaskTypeParseUploaded),
			DocumentPID:    documentPID,
			DisplayName:    filename,
			DataSourceType: "LOCAL_FILE",
			Files:          []TaskFile{{DisplayName: filename, StoredName: storedName, StoredPath: finalPath, FileSize: size}},
			DocumentTags:   tags,
		}
		taskRow := orm.Task{
			ID:          taskID,
			DocID:       documentID,
			KbID:        datasetID,
			AlgoID:      datasetAlgoIDByID(datasetID),
			DatasetID:   datasetID,
			TaskType:    string(TaskTypeParseUploaded),
			DocumentPID: documentPID,
			DisplayName: filename,
			Ext:         mustJSON(tExt),
			BaseModel:   orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
		}
		log.Printf("[zip] attempting to create doc=%s task=%s dataset=%s pid=%s file=%s", documentID, taskID, datasetID, documentPID, filename)
		if err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&docRow).Error; err != nil {
				log.Printf("[zip] create document FAILED doc=%s err=%v", documentID, err)
				return fmt.Errorf("create document failed: %w", err)
			}
			log.Printf("[zip] create document OK doc=%s", documentID)
			if err := tx.Create(&taskRow).Error; err != nil {
				log.Printf("[zip] create task FAILED task=%s err=%v", taskID, err)
				return fmt.Errorf("create task failed: %w", err)
			}
			log.Printf("[zip] create task OK task=%s", taskID)
			return nil
		}); err != nil {
			log.Printf("[zip] transaction FAILED doc=%s task=%s err=%v", documentID, taskID, err)
			return nil, err
		}
		log.Printf("[zip] transaction OK doc=%s task=%s", documentID, taskID)
		recalcAffectedFolderStats(r.Context(), datasetID, documentPID)
		tasks = append(tasks, taskRow)
	}

	// mark original zip uploaded_file as BOUND
	store.DB().WithContext(r.Context()).Model(&orm.UploadedFile{}).
		Where("upload_file_id = ? AND deleted_at IS NULL", uploadFileID).
		Updates(map[string]any{"status": UploadedFileStateBound, "updated_at": now})

	log.Printf("[zip] createTasksFromZipUpload done, created %d tasks", len(tasks))
	return tasks, nil
}

func createTaskFromExistingDocument(r *http.Request, datasetID, userID, userName string, item CreateTaskItem, tType string) (orm.Task, error) {
	taskID := strings.TrimSpace(item.TaskID)
	if taskID == "" {
		taskID = newTaskID()
	}
	documentID := strings.TrimSpace(item.Task.DocumentID)
	if documentID == "" && len(item.Task.DocumentIDs) == 1 {
		documentID = strings.TrimSpace(item.Task.DocumentIDs[0])
	}
	if documentID == "" {
		return orm.Task{}, fmt.Errorf("document_id is required")
	}

	var baseDoc orm.Document
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Take(&baseDoc).Error; err != nil {
		return orm.Task{}, fmt.Errorf("document not found")
	}
	if TaskType(tType) == TaskTypeReparse && isFolderLikeDocument(baseDoc) {
		return orm.Task{}, fmt.Errorf("folder document cannot be reparsed")
	}
	if TaskType(tType) == TaskTypeCopy || TaskType(tType) == TaskTypeMove {
		if err := validateTransferTarget(r.Context(), strings.TrimSpace(item.Task.TargetDatasetID), strings.TrimSpace(item.Task.TargetPID)); err != nil {
			return orm.Task{}, err
		}
	}

	displayName := strings.TrimSpace(item.Task.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(baseDoc.DisplayName)
	}
	if displayName == "" {
		displayName = documentID
	}

	documentPID := firstNonEmpty(strings.TrimSpace(item.Task.DocumentPID), strings.TrimSpace(baseDoc.PID))
	var tags []string
	_ = json.Unmarshal(baseDoc.Tags, &tags)
	if len(item.Task.DocumentTags) > 0 {
		tags = append([]string(nil), item.Task.DocumentTags...)
	}

	var dExt documentExt
	_ = json.Unmarshal(baseDoc.Ext, &dExt)
	tFiles := item.Task.Files
	if len(tFiles) == 0 && (strings.TrimSpace(dExt.StoredPath) != "" || strings.TrimSpace(dExt.ParseStoredPath) != "") {
		tFiles = []TaskFile{{DisplayName: displayName, StoredName: dExt.StoredName, StoredPath: dExt.StoredPath, ParseStoredPath: dExt.ParseStoredPath, FileSize: dExt.FileSize, RelativePath: dExt.RelativePath, ContentType: dExt.ContentType}}
	}
	tExt := taskExt{TaskType: tType, DocumentPID: documentPID, DisplayName: displayName, TargetDatasetID: strings.TrimSpace(item.Task.TargetDatasetID), TargetPID: strings.TrimSpace(item.Task.TargetPID), TargetPath: strings.TrimSpace(item.Task.TargetPath), DataSourceType: firstNonEmpty(strings.TrimSpace(item.Task.DataSourceType), "LOCAL_FILE"), Files: tFiles, DocumentTags: tags, ReparseGroups: item.Task.ReparseGroups}
	now := time.Now().UTC()
	taskRow := orm.Task{ID: taskID, LazyllmTaskID: "", DocID: baseDoc.ID, KbID: datasetKbIDByID(datasetID), AlgoID: datasetAlgoIDByID(datasetID), DatasetID: datasetID, TaskType: tType, DocumentPID: documentPID, TargetPID: strings.TrimSpace(item.Task.TargetPID), TargetDatasetID: strings.TrimSpace(item.Task.TargetDatasetID), DisplayName: displayName, Ext: mustJSON(tExt), BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
	if err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&taskRow).Error; err != nil {
			return err
		}
		if TaskType(tType) == TaskTypeReparse {
			if err := tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", baseDoc.ID, datasetID).Update("updated_at", now).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return orm.Task{}, fmt.Errorf("create task failed")
	}
	return taskRow, nil
}

func startReparseTasksInternal(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	kbID := datasetKbIDByID(datasetID)
	results := make([]StartTaskResult, 0, len(taskIDs))
	userID := common.UserID(r)
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), store.DB(), userID)
	if err != nil {
		log.Printf("[startReparseTasksInternal] failed to load llm_config for user=%s: %v", userID, err)
		llmConfig = nil
	}
	docIDs := make([]string, 0, len(taskIDs))
	taskRows := make([]orm.Task, 0, len(taskIDs))
	docRows := make([]orm.Document, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		var taskRow orm.Task
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&taskRow).Error; err != nil {
			results = append(results, StartTaskResult{TaskID: taskID, Status: "FAILED", SubmitStatus: "REJECTED", Message: "task not found"})
			continue
		}
		var docRow orm.Document
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.DocID, datasetID).Take(&docRow).Error; err != nil {
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: taskRow.DocID, DisplayName: taskRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "document not found"})
			continue
		}
		if isFolderLikeDocument(docRow) {
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "folder document cannot be reparsed"})
			continue
		}
		if strings.TrimSpace(docRow.LazyllmDocID) == "" {
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "lazyllm doc id is empty"})
			continue
		}
		docIDs = append(docIDs, strings.TrimSpace(docRow.LazyllmDocID))
		taskRows = append(taskRows, taskRow)
		docRows = append(docRows, docRow)
	}
	if len(taskRows) == 0 {
		return results, fmt.Errorf("no valid tasks to start")
	}
	// Collect ng_names from the first task that has ReparseGroups set.
	// All tasks in a single reparse batch share the same reparse_groups selection.
	var ngNames []string
	for _, taskRow := range taskRows {
		var ext taskExt
		_ = json.Unmarshal(taskRow.Ext, &ext)
		if len(ext.ReparseGroups) > 0 {
			ngNames = ext.ReparseGroups
			break
		}
	}
	lazyllmTaskIDs, err := callExternalReparseDocs(r, reparseRequest{DocIDs: docIDs, KbID: kbID, NgNames: ngNames, IdempotencyKey: newTaskID(), ModelConfig: llmConfig})
	if err != nil {
		for i, taskRow := range taskRows {
			results = append(results, StartTaskResult{TaskID: taskRow.ID, DocumentID: docRows[i].ID, DisplayName: docRows[i].DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: common.ResolveAppError(err.Error(), http.StatusBadGateway).Message, Detail: fmt.Sprint(common.ResolveAppError(err.Error(), http.StatusBadGateway).Detail)})
		}
		return results, err
	}
	now := time.Now().UTC()
	for i, taskRow := range taskRows {
		var ext taskExt
		_ = json.Unmarshal(taskRow.Ext, &ext)
		ext.TaskState = string(TaskStateRunning)
		lazyllmTaskID := ""
		if i < len(lazyllmTaskIDs) {
			lazyllmTaskID = strings.TrimSpace(lazyllmTaskIDs[i])
		}
		updates := map[string]any{"ext": mustJSON(ext), "updated_at": now}
		if lazyllmTaskID != "" {
			updates["lazyllm_task_id"] = lazyllmTaskID
		}
		_ = store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.ID, datasetID).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docRows[i].ID, datasetID).Update("updated_at", now).Error; err != nil {
				return err
			}
			return nil
		})
		results = append(results, StartTaskResult{TaskID: taskRow.ID, DocumentID: docRows[i].ID, DisplayName: docRows[i].DisplayName, Status: "STARTED", SubmitStatus: "SUBMITTED"})
	}
	return results, nil
}

func validateTransferTarget(ctx context.Context, targetDatasetID, targetPID string) error {
	targetDatasetID = strings.TrimSpace(targetDatasetID)
	targetPID = strings.TrimSpace(targetPID)
	if targetDatasetID == "" {
		return fmt.Errorf("target_dataset_id is required")
	}
	if targetPID == "" {
		return nil
	}
	var targetDoc orm.Document
	if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", targetPID, targetDatasetID).Take(&targetDoc).Error; err != nil {
		return fmt.Errorf("target folder not found")
	}
	if !isFolderLikeDocument(targetDoc) {
		return fmt.Errorf("target_pid must be a folder")
	}
	return nil
}

func startCopyTasksInternal(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	return startTransferTasksInternal(r, datasetID, taskIDs, "copy")
}

func startMoveTasksInternal(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	return startTransferTasksInternal(r, datasetID, taskIDs, "move")
}

func startTransferTasksInternal(r *http.Request, datasetID string, taskIDs []string, mode string) ([]StartTaskResult, error) {
	results := make([]StartTaskResult, 0, len(taskIDs))
	validTaskRows := make([]orm.Task, 0, len(taskIDs))
	validDocRows := make([]orm.Document, 0, len(taskIDs))
	preparedExts := make([]taskExt, 0, len(taskIDs))
	transferItemsByTask := make([][]transferItem, 0, len(taskIDs))
	allItems := make([]transferItem, 0, len(taskIDs))

	for _, taskID := range taskIDs {
		var taskRow orm.Task
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&taskRow).Error; err != nil {
			results = append(results, StartTaskResult{TaskID: taskID, Status: "FAILED", SubmitStatus: "REJECTED", Message: "task not found"})
			continue
		}
		var docRow orm.Document
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.DocID, datasetID).Take(&docRow).Error; err != nil {
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: taskRow.DocID, DisplayName: taskRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "document not found"})
			continue
		}
		targetDatasetID := strings.TrimSpace(taskRow.TargetDatasetID)
		if targetDatasetID == "" {
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: "target_dataset_id is required"})
			continue
		}

		if mode == "move" && targetDatasetID == datasetID {
			if err := validateLocalMoveTarget(r.Context(), datasetID, docRow, strings.TrimSpace(taskRow.TargetPID)); err != nil {
				resolved := common.ResolveAppError(err.Error(), http.StatusBadRequest)
				results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)})
				continue
			}
			now := time.Now().UTC()
			oldPID := strings.TrimSpace(docRow.PID)
			newPID := strings.TrimSpace(taskRow.TargetPID)
			if err := store.DB().WithContext(r.Context()).Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docRow.ID, datasetID).Updates(map[string]any{"p_id": newPID, "updated_at": now}).Error; err != nil {
				resolved := common.ResolveAppError(err.Error(), http.StatusInternalServerError)
				results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)})
				continue
			}
			var ext taskExt
			_ = json.Unmarshal(taskRow.Ext, &ext)
			ext.TaskState = string(TaskStateSucceeded)
			_ = store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.ID, datasetID).Updates(map[string]any{"ext": mustJSON(ext), "updated_at": now}).Error
			recalcAffectedFolderStats(r.Context(), datasetID, oldPID, newPID)
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "STARTED", SubmitStatus: "SUBMITTED", Message: "moved locally"})
			continue
		}

		var ext taskExt
		_ = json.Unmarshal(taskRow.Ext, &ext)
		log.Printf("[transfer] start mode=%s task=%s dataset=%s source_doc=%s source_display=%q source_lazy_doc=%q target_dataset=%s target_pid=%s", mode, taskRow.ID, datasetID, docRow.ID, docRow.DisplayName, strings.TrimSpace(docRow.LazyllmDocID), strings.TrimSpace(taskRow.TargetDatasetID), strings.TrimSpace(taskRow.TargetPID))
		bindings, taskItems, err := prepareTransferTargets(r.Context(), taskRow, docRow, mode)
		if err != nil {
			resolved := common.ResolveAppError(err.Error(), http.StatusInternalServerError)
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: resolved.Message, Detail: fmt.Sprint(resolved.Detail)})
			continue
		}
		if len(taskItems) == 0 {
			log.Printf("[transfer] no transferable items mode=%s task=%s source_doc=%s bindings=%d", mode, taskRow.ID, docRow.ID, len(bindings))
			for idx, binding := range bindings {
				log.Printf("[transfer] binding[%d] source_doc=%s target_doc=%s source_lazy_doc=%q display=%q stored_path=%q status=%s error=%q", idx, strings.TrimSpace(binding.SourceDocumentID), strings.TrimSpace(binding.TargetDocumentID), strings.TrimSpace(binding.SourceLazyDocID), strings.TrimSpace(binding.DisplayName), strings.TrimSpace(binding.StoredPath), strings.TrimSpace(binding.Status), strings.TrimSpace(binding.ErrorMessage))
			}
			ext.TransferBindings = bindings
			ext.TaskState = string(TaskStateFailed)
			errMsg := "no transferable file nodes found"
			for _, binding := range bindings {
				if strings.TrimSpace(binding.SourceDocumentID) == strings.TrimSpace(docRow.ID) && strings.TrimSpace(binding.SourceLazyDocID) == "" {
					errMsg = "source lazyllm doc id is empty"
					break
				}
			}
			ext.ErrorMessage = errMsg
			now := time.Now().UTC()
			_ = store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.ID, datasetID).Updates(map[string]any{"ext": mustJSON(ext), "updated_at": now}).Error
			results = append(results, StartTaskResult{TaskID: taskID, DocumentID: docRow.ID, DisplayName: docRow.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED", Message: errMsg})
			continue
		}
		ext.TransferBindings = bindings
		validTaskRows = append(validTaskRows, taskRow)
		validDocRows = append(validDocRows, docRow)
		preparedExts = append(preparedExts, ext)
		transferItemsByTask = append(transferItemsByTask, taskItems)
		allItems = append(allItems, taskItems...)
	}
	if len(validTaskRows) == 0 {
		if len(results) == 0 {
			return nil, fmt.Errorf("no valid tasks to start")
		}
		return results, nil
	}
	log.Printf("[transfer] submit external mode=%s dataset=%s task_count=%d items_count=%d", mode, datasetID, len(validTaskRows), len(allItems))
	for i, taskRow := range validTaskRows {
		log.Printf("[transfer] submit task[%d]=%s source_doc=%s target_dataset=%s bindings=%d items=%d", i, taskRow.ID, taskRow.DocID, strings.TrimSpace(taskRow.TargetDatasetID), len(preparedExts[i].TransferBindings), len(transferItemsByTask[i]))
	}
	for i, item := range allItems {
		log.Printf("[transfer] submit item[%d] source_lazy_doc=%s target_doc=%s source_kb=%s target_kb=%s mode=%s", i, strings.TrimSpace(item.DocID), strings.TrimSpace(item.TargetDocID), strings.TrimSpace(item.SourceKbID), strings.TrimSpace(item.TargetKbID), strings.TrimSpace(item.Mode))
	}
	if err := callExternalTransferDocs(r, transferRequest{Items: allItems, IdempotencyKey: newTaskID()}); err != nil {
		for i, taskRow := range validTaskRows {
			results = append(results, StartTaskResult{TaskID: taskRow.ID, DocumentID: validDocRows[i].ID, DisplayName: validDocRows[i].DisplayName, Status: "FAILED", SubmitStatus: "FAILED", Message: common.ResolveAppError(err.Error(), http.StatusBadGateway).Message, Detail: fmt.Sprint(common.ResolveAppError(err.Error(), http.StatusBadGateway).Detail)})
		}
		return results, err
	}
	now := time.Now().UTC()
	for i, taskRow := range validTaskRows {
		ext := preparedExts[i]
		bindings, bindErr := bindTransferTargetsFromReadonly(r.Context(), taskRow, ext.TransferBindings)
		if bindErr == nil && len(bindings) > 0 {
			ext.TransferBindings = bindings
		}
		if mode == "move" && strings.TrimSpace(taskRow.TargetDatasetID) != datasetID {
			_ = cleanupMovedSourceTree(r.Context(), datasetID, ext.TransferBindings)
		}
		recalcTransferFolderStats(r.Context(), datasetID, taskRow, validDocRows[i], ext.TransferBindings, mode)
		ext.TaskState = string(TaskStateRunning)
		ext.ErrorMessage = ""
		_ = transferItemsByTask
		_ = store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskRow.ID, datasetID).Updates(map[string]any{"ext": mustJSON(ext), "updated_at": now}).Error
		results = append(results, StartTaskResult{TaskID: taskRow.ID, DocumentID: validDocRows[i].ID, DisplayName: validDocRows[i].DisplayName, Status: "STARTED", SubmitStatus: "SUBMITTED"})
	}
	return results, nil
}

func validateLocalMoveTarget(ctx context.Context, datasetID string, sourceDoc orm.Document, targetPID string) error {
	targetPID = strings.TrimSpace(targetPID)
	if targetPID == "" {
		return nil
	}
	if sourceDoc.ID == targetPID {
		return fmt.Errorf("target_pid cannot be the same as source document")
	}
	if !isFolderLikeDocument(sourceDoc) {
		return nil
	}
	current := targetPID
	for current != "" {
		if current == sourceDoc.ID {
			return fmt.Errorf("cannot move folder into its descendant")
		}
		var parent orm.Document
		if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", current, datasetID).Take(&parent).Error; err != nil {
			break
		}
		current = strings.TrimSpace(parent.PID)
	}
	return nil
}

func prepareTransferTargets(ctx context.Context, taskRow orm.Task, rootDoc orm.Document, mode string) ([]transferBinding, []transferItem, error) {
	targetDatasetID := strings.TrimSpace(taskRow.TargetDatasetID)
	log.Printf("[transfer] prepare start mode=%s task=%s source_doc=%s source_display=%q source_lazy_doc=%q target_dataset=%s target_pid=%s", mode, taskRow.ID, rootDoc.ID, rootDoc.DisplayName, strings.TrimSpace(rootDoc.LazyllmDocID), targetDatasetID, strings.TrimSpace(taskRow.TargetPID))
	targetPID := strings.TrimSpace(taskRow.TargetPID)
	if targetDatasetID == "" {
		return nil, nil, fmt.Errorf("target_dataset_id is required")
	}
	nodes, err := loadDocumentSubtree(ctx, taskRow.DatasetID, rootDoc.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(nodes) == 0 {
		nodes = []orm.Document{rootDoc}
	}
	log.Printf("[transfer] prepare loaded nodes task=%s count=%d", taskRow.ID, len(nodes))
	now := time.Now().UTC()
	bindings := make([]transferBinding, 0, len(nodes))
	items := make([]transferItem, 0, len(nodes))
	idMap := map[string]string{}
	for _, node := range nodes {
		newID := newDocID()
		idMap[node.ID] = newID
	}
	for _, node := range nodes {
		newID := idMap[node.ID]
		newPID := targetPID
		if node.ID != rootDoc.ID {
			newPID = idMap[node.PID]
		}
		clone := node
		clone.ID = newID
		clone.LazyllmDocID = ""
		clone.DatasetID = targetDatasetID
		clone.PID = newPID
		clone.FileID = newID
		clone.CreatedAt = now
		clone.UpdatedAt = now
		clone.DeletedAt = nil
		if err := store.DB().WithContext(ctx).Create(&clone).Error; err != nil {
			return nil, nil, fmt.Errorf("precreate target document failed")
		}
		var ext documentExt
		_ = json.Unmarshal(node.Ext, &ext)
		sourceLazyDocID := strings.TrimSpace(node.LazyllmDocID)
		storedPath := strings.TrimSpace(firstNonEmpty(ext.ParseStoredPath, ext.StoredPath))
		isFolder := isFolderLikeDocument(node)
		log.Printf("[transfer] node task=%s source_doc=%s target_doc=%s display=%q is_folder=%t source_lazy_doc=%q stored_path=%q pid=%s", taskRow.ID, node.ID, newID, strings.TrimSpace(node.DisplayName), isFolder, sourceLazyDocID, storedPath, strings.TrimSpace(node.PID))
		bindings = append(bindings, transferBinding{SourceDocumentID: node.ID, TargetDocumentID: newID, SourceLazyDocID: sourceLazyDocID, DisplayName: strings.TrimSpace(node.DisplayName), StoredPath: storedPath, Mode: mode, Status: string(TaskStateCreating)})
		if isFolder || sourceLazyDocID == "" {
			log.Printf("[transfer] skip item task=%s source_doc=%s reason=%s", taskRow.ID, node.ID, map[bool]string{true: "folder or empty source lazy doc id", false: ""}[isFolder || sourceLazyDocID == ""])
			continue
		}
		items = append(items, transferItem{DocID: sourceLazyDocID, TargetDocID: newID, SourceKbID: datasetKbIDByID(taskRow.DatasetID), TargetKbID: datasetKbIDByID(targetDatasetID), Mode: mode})
		log.Printf("[transfer] add item task=%s source_doc=%s source_lazy_doc=%s target_doc=%s", taskRow.ID, node.ID, sourceLazyDocID, newID)
	}
	log.Printf("[transfer] prepare done task=%s bindings=%d items=%d", taskRow.ID, len(bindings), len(items))
	return bindings, items, nil
}

func loadDocumentSubtree(ctx context.Context, datasetID, rootID string) ([]orm.Document, error) {
	var all []orm.Document
	if err := store.DB().WithContext(ctx).Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Order("created_at ASC").Find(&all).Error; err != nil {
		return nil, err
	}
	byPID := make(map[string][]orm.Document)
	byID := make(map[string]orm.Document)
	for _, row := range all {
		byID[row.ID] = row
		byPID[strings.TrimSpace(row.PID)] = append(byPID[strings.TrimSpace(row.PID)], row)
	}
	root, ok := byID[rootID]
	if !ok {
		return nil, fmt.Errorf("document not found")
	}
	out := make([]orm.Document, 0)
	queue := []orm.Document{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		out = append(out, current)
		if !isFolderLikeDocument(current) {
			continue
		}
		children := byPID[current.ID]
		queue = append(queue, children...)
	}
	return out, nil
}

func isFolderLikeDocument(doc orm.Document) bool {
	var ext documentExt
	_ = json.Unmarshal(doc.Ext, &ext)
	name := strings.TrimSpace(firstNonEmpty(doc.DisplayName, ext.OriginalFilename))
	return documentTypeFromName(name) == "FOLDER"
}

func bindTransferTargetsFromReadonly(ctx context.Context, taskRow orm.Task, bindings []transferBinding) ([]transferBinding, error) {
	if len(bindings) == 0 {
		log.Printf("[transfer-bind] skip empty bindings task=%s target_dataset=%s", taskRow.ID, strings.TrimSpace(taskRow.TargetDatasetID))
		return bindings, nil
	}
	targetKbID := datasetKbIDByID(strings.TrimSpace(taskRow.TargetDatasetID))
	log.Printf("[transfer-bind] start task=%s target_dataset=%s target_kb=%s bindings=%d", taskRow.ID, strings.TrimSpace(taskRow.TargetDatasetID), targetKbID, len(bindings))
	if targetKbID == "" {
		log.Printf("[transfer-bind] target kb empty task=%s target_dataset=%s", taskRow.ID, strings.TrimSpace(taskRow.TargetDatasetID))
		return bindings, fmt.Errorf("target kb id is empty")
	}
	var kbDocs []readonlyorm.LazyLLMKBDocRow
	if err := store.LazyLLMDB().WithContext(ctx).Table((readonlyorm.LazyLLMKBDocRow{}).TableName()).Where("kb_id = ?", targetKbID).Order("created_at DESC").Find(&kbDocs).Error; err != nil {
		log.Printf("[transfer-bind] query kb docs failed task=%s target_kb=%s err=%v", taskRow.ID, targetKbID, err)
		return bindings, err
	}
	log.Printf("[transfer-bind] loaded kb docs task=%s target_kb=%s count=%d", taskRow.ID, targetKbID, len(kbDocs))
	if len(kbDocs) == 0 {
		log.Printf("[transfer-bind] no kb docs task=%s target_kb=%s", taskRow.ID, targetKbID)
		return bindings, nil
	}
	candidateIDs := make([]string, 0, len(kbDocs))
	for _, row := range kbDocs {
		candidateIDs = append(candidateIDs, row.DocID)
	}
	var docs []readonlyorm.LazyLLMDocRow
	if err := store.LazyLLMDB().WithContext(ctx).Table((readonlyorm.LazyLLMDocRow{}).TableName()).Where("doc_id IN ?", candidateIDs).Order("created_at DESC").Find(&docs).Error; err != nil {
		log.Printf("[transfer-bind] query readonly docs failed task=%s candidate_count=%d err=%v", taskRow.ID, len(candidateIDs), err)
		return bindings, err
	}
	log.Printf("[transfer-bind] loaded readonly docs task=%s candidate_count=%d docs_count=%d", taskRow.ID, len(candidateIDs), len(docs))
	for i, binding := range bindings {
		log.Printf("[transfer-bind] binding[%d] source_doc=%s target_doc=%s source_lazy_doc=%q display=%q stored_path=%q", i, strings.TrimSpace(binding.SourceDocumentID), strings.TrimSpace(binding.TargetDocumentID), strings.TrimSpace(binding.SourceLazyDocID), strings.TrimSpace(binding.DisplayName), strings.TrimSpace(binding.StoredPath))
	}
	for i, row := range docs {
		meta := ""
		if row.Meta != nil {
			meta = strings.TrimSpace(*row.Meta)
		}
		log.Printf("[transfer-bind] readonly-doc[%d] doc_id=%s filename=%q path=%q upload_status=%q meta=%q", i, strings.TrimSpace(row.DocID), strings.TrimSpace(row.Filename), strings.TrimSpace(row.Path), strings.TrimSpace(row.UploadStatus), meta)
	}
	updated := append([]transferBinding(nil), bindings...)
	used := map[string]struct{}{}
	for _, binding := range updated {
		if strings.TrimSpace(binding.TargetLazyDocID) != "" {
			used[strings.TrimSpace(binding.TargetLazyDocID)] = struct{}{}
		}
	}
	matched := make([]bool, len(updated))
	for i := range updated {
		for _, row := range docs {
			if _, ok := used[strings.TrimSpace(row.DocID)]; ok {
				continue
			}
			if !matchTransferBindingCandidate(updated[i], row) {
				continue
			}
			updated[i].TargetLazyDocID = strings.TrimSpace(row.DocID)
			updated[i].Status = string(TaskStateRunning)
			used[strings.TrimSpace(row.DocID)] = struct{}{}
			matched[i] = true
			log.Printf("[transfer-bind] precise match task=%s binding_index=%d target_doc=%s matched_lazy_doc=%s", taskRow.ID, i, strings.TrimSpace(updated[i].TargetDocumentID), strings.TrimSpace(row.DocID))
			break
		}
	}
	for i := range updated {
		if matched[i] {
			continue
		}
		for _, row := range docs {
			if _, ok := used[strings.TrimSpace(row.DocID)]; ok {
				continue
			}
			updated[i].TargetLazyDocID = strings.TrimSpace(row.DocID)
			updated[i].Status = string(TaskStateRunning)
			used[strings.TrimSpace(row.DocID)] = struct{}{}
			matched[i] = true
			log.Printf("[transfer-bind] fallback match task=%s binding_index=%d target_doc=%s matched_lazy_doc=%s", taskRow.ID, i, strings.TrimSpace(updated[i].TargetDocumentID), strings.TrimSpace(row.DocID))
			break
		}
	}
	now := time.Now().UTC()
	for i := range updated {
		if strings.TrimSpace(updated[i].TargetLazyDocID) == "" {
			updated[i].Status = string(TaskStateFailed)
			updated[i].ErrorMessage = "target lazy doc id not found"
			log.Printf("[transfer-bind] final unresolved task=%s binding_index=%d target_doc=%s", taskRow.ID, i, strings.TrimSpace(updated[i].TargetDocumentID))
			continue
		}
		log.Printf("[transfer-bind] persist target lazy doc task=%s binding_index=%d target_doc=%s target_lazy_doc=%s", taskRow.ID, i, strings.TrimSpace(updated[i].TargetDocumentID), strings.TrimSpace(updated[i].TargetLazyDocID))
		_ = store.DB().WithContext(ctx).Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", updated[i].TargetDocumentID, strings.TrimSpace(taskRow.TargetDatasetID)).Updates(map[string]any{"lazyllm_doc_id": updated[i].TargetLazyDocID, "updated_at": now}).Error
	}
	log.Printf("[transfer-bind] done task=%s", taskRow.ID)
	return updated, nil
}

func matchTransferBindingCandidate(binding transferBinding, row readonlyorm.LazyLLMDocRow) bool {
	if strings.TrimSpace(binding.TargetDocumentID) != "" && strings.TrimSpace(binding.TargetDocumentID) == strings.TrimSpace(row.DocID) {
		return true
	}
	if strings.TrimSpace(binding.DisplayName) != "" && strings.EqualFold(strings.TrimSpace(binding.DisplayName), strings.TrimSpace(row.Filename)) {
		return true
	}
	if strings.TrimSpace(binding.StoredPath) != "" && strings.TrimSpace(binding.StoredPath) == strings.TrimSpace(row.Path) {
		return true
	}
	if row.Meta != nil && strings.TrimSpace(*row.Meta) != "" {
		meta := strings.TrimSpace(*row.Meta)
		if strings.TrimSpace(binding.TargetDocumentID) != "" && strings.Contains(meta, strings.TrimSpace(binding.TargetDocumentID)) {
			return true
		}
		if strings.TrimSpace(binding.SourceDocumentID) != "" && strings.Contains(meta, strings.TrimSpace(binding.SourceDocumentID)) {
			return true
		}
	}
	return false
}

func cleanupMovedSourceTree(ctx context.Context, datasetID string, bindings []transferBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Status == string(TaskStateFailed) {
			continue
		}
		if strings.TrimSpace(binding.SourceDocumentID) != "" {
			ids = append(ids, strings.TrimSpace(binding.SourceDocumentID))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return store.DB().WithContext(ctx).Model(&orm.Document{}).Where("dataset_id = ? AND id IN ? AND deleted_at IS NULL", datasetID, ids).Update("deleted_at", now).Error
}

func recalcTransferFolderStats(ctx context.Context, sourceDatasetID string, taskRow orm.Task, sourceRoot orm.Document, bindings []transferBinding, mode string) {
	_ = mode
	touched := map[string]struct{}{}
	appendFolderSelf := func(datasetID, documentID string) {
		docID := strings.TrimSpace(documentID)
		if docID == "" {
			return
		}
		var row orm.Document
		if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", docID, datasetID).Take(&row).Error; err != nil {
			return
		}
		if !isFolderLikeDocument(row) {
			return
		}
		key := datasetID + ":" + docID
		touched[key] = struct{}{}
	}
	appendAncestors := func(datasetID, pid string) {
		current := strings.TrimSpace(pid)
		for current != "" {
			key := datasetID + ":" + current
			if _, ok := touched[key]; ok {
				break
			}
			touched[key] = struct{}{}
			var row orm.Document
			if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", current, datasetID).Take(&row).Error; err != nil {
				break
			}
			current = strings.TrimSpace(row.PID)
		}
	}
	appendAncestors(sourceDatasetID, strings.TrimSpace(sourceRoot.PID))
	appendAncestors(strings.TrimSpace(taskRow.TargetDatasetID), strings.TrimSpace(taskRow.TargetPID))
	appendFolderSelf(sourceDatasetID, sourceRoot.ID)
	for _, binding := range bindings {
		appendAncestors(sourceDatasetID, parentPIDForDocument(ctx, sourceDatasetID, binding.SourceDocumentID))
		appendAncestors(strings.TrimSpace(taskRow.TargetDatasetID), parentPIDForDocument(ctx, strings.TrimSpace(taskRow.TargetDatasetID), binding.TargetDocumentID))
		appendFolderSelf(strings.TrimSpace(taskRow.TargetDatasetID), binding.TargetDocumentID)
	}
	for key := range touched {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		recalcSingleFolderStats(ctx, parts[0], parts[1])
	}
}

func recalcAffectedFolderStats(ctx context.Context, datasetID string, pids ...string) {
	touched := map[string]struct{}{}
	for _, pid := range pids {
		current := strings.TrimSpace(pid)
		for current != "" {
			if _, ok := touched[current]; ok {
				break
			}
			touched[current] = struct{}{}
			var row orm.Document
			if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", current, datasetID).Take(&row).Error; err != nil {
				break
			}
			current = strings.TrimSpace(row.PID)
		}
	}
	for folderID := range touched {
		recalcSingleFolderStats(ctx, datasetID, folderID)
	}
}

func parentPIDForDocument(ctx context.Context, datasetID, documentID string) string {
	if strings.TrimSpace(documentID) == "" {
		return ""
	}
	var row orm.Document
	if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Take(&row).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(row.PID)
}

func recalcSingleFolderStats(ctx context.Context, datasetID, folderID string) {
	var folder orm.Document
	if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", folderID, datasetID).Take(&folder).Error; err != nil {
		return
	}
	var all []orm.Document
	if err := store.DB().WithContext(ctx).Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Find(&all).Error; err != nil {
		return
	}
	childrenByPID := make(map[string][]orm.Document)
	for _, row := range all {
		childrenByPID[strings.TrimSpace(row.PID)] = append(childrenByPID[strings.TrimSpace(row.PID)], row)
	}
	directChildren := childrenByPID[folderID]
	directFileCount := 0
	directFolderCount := 0
	recursiveFileCount := 0
	recursiveFolderCount := 0
	recursiveTotalSize := int64(0)
	queue := append([]orm.Document(nil), directChildren...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		var ext documentExt
		_ = json.Unmarshal(current.Ext, &ext)
		if isFolderLikeDocument(current) {
			recursiveFolderCount++
			queue = append(queue, childrenByPID[current.ID]...)
		} else {
			recursiveFileCount++
			recursiveTotalSize += ext.FileSize
		}
	}
	for _, child := range directChildren {
		if isFolderLikeDocument(child) {
			directFolderCount++
		} else {
			directFileCount++
		}
	}
	var extMap map[string]any
	_ = json.Unmarshal(folder.Ext, &extMap)
	if extMap == nil {
		extMap = map[string]any{}
	}
	extMap["file_size"] = recursiveTotalSize
	extMap["child_document_count"] = directFileCount
	extMap["child_folder_count"] = directFolderCount
	extMap["recursive_document_count"] = recursiveFileCount
	extMap["recursive_folder_count"] = recursiveFolderCount
	extMap["recursive_file_size"] = recursiveTotalSize
	_ = store.DB().WithContext(ctx).Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", folderID, datasetID).Updates(map[string]any{"ext": mustJSON(extMap), "updated_at": time.Now().UTC()}).Error
}

func datasetKbIDByID(datasetID string) string {
	var ds orm.Dataset
	if err := store.DB().Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		return datasetID
	}
	if strings.TrimSpace(ds.KbID) != "" {
		return strings.TrimSpace(ds.KbID)
	}
	return datasetID
}

func datasetAlgoIDByID(datasetID string) string {
	var ds orm.Dataset
	if err := store.DB().Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		return ""
	}
	return parseDatasetAlgo(ds.Ext).AlgoID
}

// ensureTopLevelFolder extracts the first-level directory name from relative_path
// (e.g. "test/subdir/a.doc" -> "test"), then finds or creates the corresponding
// FOLDER document in the database and returns its ID.
// If relative_path is empty or has no directory component, it returns "".
// createTopLevelFolder always creates a new FOLDER document at the dataset root,
// named after the first path component of relativePath.
// Unlike the old "ensure" approach, it never reuses an existing folder —
// each call produces a distinct folder so that separate uploads stay independent.
func createTopLevelFolder(ctx context.Context, datasetID, userID, userName string, relativePath string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(relativePath), "\\", "/")
	parts := strings.SplitN(normalized, "/", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	folderName := strings.TrimSpace(parts[0])

	folderID := newDocID()
	now := time.Now().UTC()
	folder := orm.Document{
		ID:          folderID,
		DatasetID:   datasetID,
		DisplayName: folderName,
		PID:         "",
		FileID:      "",
		Ext:         json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := store.DB().WithContext(ctx).Create(&folder).Error; err != nil {
		return ""
	}
	return folderID
}

// ensureTopLevelFolder finds an existing root-level folder with the same name in the
// same dataset, or creates one if none exists. Use this only when you need idempotent
// folder lookup within a single upload batch (e.g. multiple files sharing the same
// relative_path prefix submitted together in one CreateTask call).
func ensureTopLevelFolder(ctx context.Context, datasetID, userID, userName string, relativePath string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(relativePath), "\\", "/")
	parts := strings.SplitN(normalized, "/", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	folderName := strings.TrimSpace(parts[0])

	// look for an existing root-level FOLDER document with the same name
	var existing orm.Document
	err := store.DB().WithContext(ctx).
		Where("dataset_id = ? AND display_name = ? AND p_id = '' AND deleted_at IS NULL", datasetID, folderName).
		Take(&existing).Error
	if err == nil {
		return existing.ID
	}

	// not found — create a new one
	folderID := newDocID()
	now := time.Now().UTC()
	folder := orm.Document{
		ID:          folderID,
		DatasetID:   datasetID,
		DisplayName: folderName,
		PID:         "",
		FileID:      "",
		Ext:         json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := store.DB().WithContext(ctx).Create(&folder).Error; err != nil {
		return ""
	}
	return folderID
}
