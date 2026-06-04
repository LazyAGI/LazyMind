package coreclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type HTTPCoreClient struct {
	baseURL      *url.URL
	httpClient   *http.Client
	contentStore ContentStore
}

type ContentStore interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

func NewHTTPCoreClient(baseURL string, client *http.Client) (*HTTPCoreClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("core base url must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPCoreClient{baseURL: parsed, httpClient: client}, nil
}

func (c *HTTPCoreClient) UseContentStore(store ContentStore) {
	c.contentStore = store
}

func (c *HTTPCoreClient) CreateDataset(ctx context.Context, req CreateDatasetRequest) (CreateDatasetResponse, error) {
	if req.IdempotencyKey == "" || req.Name == "" || req.CreatedBy == "" {
		return CreateDatasetResponse{}, fmt.Errorf("idempotency_key, name, and created_by are required")
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		req.DisplayName = req.Name
	}
	var out CreateDatasetResponse
	if err := c.doJSON(ctx, http.MethodPost, "/datasets", req, &out, req.CreatedBy, ""); err != nil {
		if resp, ok := recoverCreateDatasetConflict(err); ok {
			return resp, nil
		}
		return CreateDatasetResponse{}, err
	}
	return out, nil
}

func (c *HTTPCoreClient) DeleteDataset(ctx context.Context, req DeleteDatasetRequest) error {
	if strings.TrimSpace(req.DatasetID) == "" {
		return fmt.Errorf("dataset_id is required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/datasets/"+url.PathEscape(req.DatasetID), nil, nil, req.UserID, "")
}

func (c *HTTPCoreClient) CreateBindingRootDocument(ctx context.Context, req CreateBindingRootDocumentRequest) (CreateBindingRootDocumentResponse, error) {
	if req.IdempotencyKey == "" || req.DatasetID == "" || req.Name == "" {
		return CreateBindingRootDocumentResponse{}, fmt.Errorf("idempotency_key, dataset_id, and name are required")
	}
	var out struct {
		DocumentID string `json:"document_id"`
		Created    *bool  `json:"created"`
	}
	body := map[string]any{
		"display_name":    req.Name,
		"p_id":            req.ParentDocumentID,
		"idempotency_key": req.IdempotencyKey,
	}
	endpoint := "/datasets/" + url.PathEscape(req.DatasetID) + "/documents"
	if err := c.doJSON(ctx, http.MethodPost, endpoint, body, &out, req.UserID, ""); err != nil {
		if resp, ok := recoverBindingRootConflict(err); ok {
			return resp, nil
		}
		return CreateBindingRootDocumentResponse{}, err
	}
	if strings.TrimSpace(out.DocumentID) == "" {
		return CreateBindingRootDocumentResponse{}, fmt.Errorf("core create binding root returned empty document_id")
	}
	created := true
	if out.Created != nil {
		created = *out.Created
	}
	return CreateBindingRootDocumentResponse{DocumentID: strings.TrimSpace(out.DocumentID), Created: created}, nil
}

func (c *HTTPCoreClient) DeleteDocument(ctx context.Context, req DeleteDocumentRequest) error {
	if strings.TrimSpace(req.DocumentID) == "" {
		return fmt.Errorf("document_id is required")
	}
	if strings.TrimSpace(req.DatasetID) == "" {
		return fmt.Errorf("dataset_id is required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/datasets/"+url.PathEscape(req.DatasetID)+"/documents/"+url.PathEscape(req.DocumentID), nil, nil, req.UserID, "")
}

func (c *HTTPCoreClient) BatchDeleteDocuments(ctx context.Context, req BatchDeleteDocumentsRequest) error {
	if strings.TrimSpace(req.DatasetID) == "" {
		return fmt.Errorf("dataset_id is required")
	}
	ids := make([]string, 0, len(req.DocumentIDs))
	for _, id := range req.DocumentIDs {
		if v := strings.TrimSpace(id); v != "" {
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	body := map[string]any{
		"parent": "datasets/" + req.DatasetID,
		"names":  ids,
	}
	return c.doJSON(ctx, http.MethodPost, "/datasets/"+url.PathEscape(req.DatasetID)+":batchDelete", body, nil, req.UserID, "")
}

func (c *HTTPCoreClient) SubmitParseTask(ctx context.Context, req SubmitParseTaskRequest) (SubmitParseTaskResponse, error) {
	if req.IdempotencyKey == "" || req.DatasetID == "" || req.DisplayName == "" || req.Action == "" {
		return SubmitParseTaskResponse{}, fmt.Errorf("parse task idempotency_key, dataset_id, display_name, and action are required")
	}
	switch req.Action {
	case ActionCreate:
		if req.ParentDocumentID == "" {
			return SubmitParseTaskResponse{}, fmt.Errorf("parent_document_id is required for create")
		}
		if req.ContentURI == "" && req.Content == nil {
			return SubmitParseTaskResponse{}, fmt.Errorf("content_uri or content reader is required for create")
		}
		return c.submitCreate(ctx, req)
	case ActionReparse:
		if req.SourceDocumentID == "" {
			return SubmitParseTaskResponse{}, fmt.Errorf("source_document_id is required for reparse")
		}
		if req.ContentURI == "" && req.Content == nil {
			return SubmitParseTaskResponse{}, fmt.Errorf("content_uri or content reader is required for reparse")
		}
		return c.submitReparse(ctx, req)
	case ActionDelete:
		if req.SourceDocumentID == "" {
			return SubmitParseTaskResponse{}, fmt.Errorf("source_document_id is required for delete")
		}
		if err := c.DeleteDocument(ctx, DeleteDocumentRequest{DatasetID: req.DatasetID, DocumentID: req.SourceDocumentID, UserID: req.UserID}); err != nil {
			return SubmitParseTaskResponse{}, err
		}
		return SubmitParseTaskResponse{CoreDocumentID: req.SourceDocumentID, Status: StatusSucceeded, Created: false}, nil
	default:
		return SubmitParseTaskResponse{}, fmt.Errorf("unsupported parse action %s", req.Action)
	}
}

func (c *HTTPCoreClient) submitCreate(ctx context.Context, req SubmitParseTaskRequest) (SubmitParseTaskResponse, error) {
	uploadID, err := c.uploadContent(ctx, req)
	if err != nil {
		return SubmitParseTaskResponse{}, err
	}
	taskID, documentID, created, err := c.createCoreTask(ctx, req.DatasetID, req.UserID, map[string]any{
		"idempotency_key": req.IdempotencyKey,
		"items": []map[string]any{{
			"upload_file_id": uploadID,
			"task": map[string]any{
				"task_type":    "TASK_TYPE_PARSE_UPLOADED",
				"document_pid": req.ParentDocumentID,
				"display_name": uploadFileName(req),
			},
		}},
	})
	if err != nil {
		return SubmitParseTaskResponse{}, err
	}
	if created {
		if err := c.startCoreTask(ctx, req.DatasetID, taskID, req.UserID); err != nil {
			return SubmitParseTaskResponse{}, err
		}
	}
	status := StatusSubmitted
	if !created && taskID == "" {
		status = StatusSucceeded
	}
	if taskID != "" && created {
		status = StatusSubmitted
	}
	return SubmitParseTaskResponse{
		CoreTaskID:     taskID,
		CoreDocumentID: documentID,
		Status:         status,
		Created:        created,
	}, nil
}

func (c *HTTPCoreClient) submitReparse(ctx context.Context, req SubmitParseTaskRequest) (SubmitParseTaskResponse, error) {
	uploadID, err := c.uploadContent(ctx, req)
	if err != nil {
		return SubmitParseTaskResponse{}, err
	}
	taskID, documentID, created, err := c.createCoreTask(ctx, req.DatasetID, req.UserID, map[string]any{
		"idempotency_key": req.IdempotencyKey,
		"items": []map[string]any{{
			"upload_file_id": uploadID,
			"task": map[string]any{
				"task_type":    "TASK_TYPE_PARSE_UPLOADED",
				"document_pid": req.ParentDocumentID,
				"display_name": uploadFileName(req),
			},
		}},
	})
	if err != nil {
		return SubmitParseTaskResponse{}, err
	}
	if created {
		if err := c.startCoreTask(ctx, req.DatasetID, taskID, req.UserID); err != nil {
			return SubmitParseTaskResponse{}, err
		}
	}
	if created && documentID != "" && documentID != req.SourceDocumentID {
		if err := c.DeleteDocument(ctx, DeleteDocumentRequest{DatasetID: req.DatasetID, DocumentID: req.SourceDocumentID, UserID: req.UserID}); err != nil {
			return SubmitParseTaskResponse{}, err
		}
	}
	status := StatusSubmitted
	if !created && taskID == "" {
		status = StatusSucceeded
	}
	return SubmitParseTaskResponse{
		CoreTaskID:     taskID,
		CoreDocumentID: firstNonEmpty(documentID, req.SourceDocumentID),
		Status:         status,
		Created:        created,
	}, nil
}

func (c *HTTPCoreClient) GetCoreTaskResult(ctx context.Context, req GetCoreTaskResultRequest) (CoreTaskResult, error) {
	if strings.TrimSpace(req.DatasetID) == "" || strings.TrimSpace(req.CoreTaskID) == "" {
		return CoreTaskResult{}, fmt.Errorf("dataset_id and core_task_id are required")
	}
	var out struct {
		TaskID        string `json:"task_id"`
		DocumentID    string `json:"document_id"`
		TaskState     string `json:"task_state"`
		PDFConvert    string `json:"pdf_convert_result"`
		ConvertStatus string `json:"convert_status"`
		ConvertError  string `json:"convert_error"`
		ErrMsg        string `json:"err_msg"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/datasets/"+url.PathEscape(req.DatasetID)+"/tasks/"+url.PathEscape(req.CoreTaskID), nil, &out, req.UserID, ""); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return CoreTaskResult{Status: ResultStatusNotFound}, nil
		}
		return CoreTaskResult{}, err
	}
	result := CoreTaskResult{
		Status:         mapCoreTaskStatus(out.TaskState),
		CoreDocumentID: out.DocumentID,
		ErrorMessage:   firstNonEmpty(out.ErrMsg, out.ConvertError),
	}
	if result.Status == ResultStatusFailed {
		result.ErrorCode = "CORE_TASK_FAILED"
	}
	return result, nil
}

func (c *HTTPCoreClient) uploadContent(ctx context.Context, req SubmitParseTaskRequest) (string, error) {
	content, closeContent, err := c.contentReader(ctx, req)
	if err != nil {
		return "", err
	}
	defer closeContent()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", uploadFileName(req))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, content); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/datasets/"+url.PathEscape(req.DatasetID)+"/uploads"), &body)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	c.setAuthHeaders(httpReq, req.UserID)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", decodeCoreError(resp)
	}
	var out struct {
		Files []struct {
			UploadFileID string `json:"upload_file_id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Files) == 0 || strings.TrimSpace(out.Files[0].UploadFileID) == "" {
		return "", fmt.Errorf("core upload returned empty upload_file_id")
	}
	return strings.TrimSpace(out.Files[0].UploadFileID), nil
}

func uploadFileName(req SubmitParseTaskRequest) string {
	name := firstNonEmpty(req.DisplayName, "source-object")
	if strings.TrimSpace(path.Ext(name)) != "" {
		return name
	}
	ext := strings.TrimSpace(req.FileExtension)
	if ext == "" {
		return name
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if ext == "." {
		return name
	}
	return name + strings.ToLower(ext)
}

func (c *HTTPCoreClient) createCoreTask(ctx context.Context, datasetID, userID string, payload map[string]any) (string, string, bool, error) {
	var out struct {
		Tasks []struct {
			TaskID     string `json:"task_id"`
			DocumentID string `json:"document_id"`
		} `json:"tasks"`
		TaskID     string `json:"task_id"`
		DocumentID string `json:"document_id"`
		Created    *bool  `json:"created"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/datasets/"+url.PathEscape(datasetID)+"/tasks", payload, &out, userID, ""); err != nil {
		if resp, ok := recoverSubmitTaskConflict(err); ok {
			return resp.CoreTaskID, resp.CoreDocumentID, false, nil
		}
		return "", "", false, err
	}
	created := true
	if out.Created != nil {
		created = *out.Created
	}
	if strings.TrimSpace(out.TaskID) != "" || strings.TrimSpace(out.DocumentID) != "" {
		return strings.TrimSpace(out.TaskID), strings.TrimSpace(out.DocumentID), created, nil
	}
	if len(out.Tasks) == 0 || strings.TrimSpace(out.Tasks[0].TaskID) == "" {
		return "", "", false, fmt.Errorf("core create task returned empty task_id")
	}
	return strings.TrimSpace(out.Tasks[0].TaskID), strings.TrimSpace(out.Tasks[0].DocumentID), created, nil
}

func (c *HTTPCoreClient) startCoreTask(ctx context.Context, datasetID, taskID, userID string) error {
	body := map[string]any{
		"task_ids":   []string{taskID},
		"start_mode": "ASYNC",
	}
	return c.doJSON(ctx, http.MethodPost, "/datasets/"+url.PathEscape(datasetID)+"/tasks:start", body, nil, userID, "")
}

func (c *HTTPCoreClient) doJSON(ctx context.Context, method, endpoint string, in any, out any, userID, userName string) error {
	body, err := encodeBody(in)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.endpoint(endpoint), body)
	if err != nil {
		return err
	}
	if in != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")
	c.setAuthHeaders(httpReq, userID)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeCoreError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *HTTPCoreClient) setAuthHeaders(req *http.Request, userID string) {
	if strings.TrimSpace(userID) == "" {
		userID = "scan-control-plane"
	}
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-User-Name", userID)
}

func (c *HTTPCoreClient) endpoint(endpoint string) string {
	u := *c.baseURL
	rawQuery := ""
	if idx := strings.Index(endpoint, "?"); idx >= 0 {
		rawQuery = endpoint[idx+1:]
		endpoint = endpoint[:idx]
	}
	u.Path = path.Join(c.baseURL.Path, endpoint)
	u.RawQuery = rawQuery
	return u.String()
}

func encodeBody(in any) (io.Reader, error) {
	if in == nil {
		return nil, nil
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(in); err != nil {
		return nil, err
	}
	return &body, nil
}

func decodeCoreError(resp *http.Response) error {
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	code := stringFromAny(body["code"])
	message := stringFromAny(body["message"])
	if code == "" {
		code = ErrCodeCoreSubmitFailed
	}
	if resp.StatusCode == http.StatusConflict {
		code = ErrCodeIdempotencyKeyReused
	}
	return &Error{Code: code, Message: message, StatusCode: resp.StatusCode, Body: body}
}

func (c *HTTPCoreClient) contentReader(ctx context.Context, req SubmitParseTaskRequest) (io.Reader, func(), error) {
	if req.Content != nil {
		return req.Content, func() {}, nil
	}
	uri := strings.TrimSpace(req.ContentURI)
	if uri == "" {
		return nil, nil, fmt.Errorf("content_uri is required")
	}
	if c.contentStore != nil {
		reader, err := c.contentStore.Open(ctx, uri)
		if err != nil {
			return nil, nil, err
		}
		return reader, func() { _ = reader.Close() }, nil
	}
	return nil, nil, fmt.Errorf("content store is required to open content_uri")
}

func recoverCreateDatasetConflict(err error) (CreateDatasetResponse, bool) {
	if !isHTTPStatus(err, http.StatusConflict) {
		return CreateDatasetResponse{}, false
	}
	body := coreErrorBody(err)
	datasetID := firstNonEmpty(
		stringFromAny(body["dataset_id"]),
		stringFromAny(body["existing_dataset_id"]),
		nestedStringFromMap(body, "data", "dataset_id"),
		nestedStringFromMap(body, "data", "existing_dataset_id"),
		nestedStringFromMap(body, "resource", "dataset_id"),
	)
	if datasetID == "" {
		return CreateDatasetResponse{}, false
	}
	return CreateDatasetResponse{DatasetID: datasetID, Created: false}, true
}

func recoverBindingRootConflict(err error) (CreateBindingRootDocumentResponse, bool) {
	if !isHTTPStatus(err, http.StatusConflict) {
		return CreateBindingRootDocumentResponse{}, false
	}
	body := coreErrorBody(err)
	documentID := firstNonEmpty(
		stringFromAny(body["document_id"]),
		stringFromAny(body["existing_document_id"]),
		nestedStringFromMap(body, "data", "document_id"),
		nestedStringFromMap(body, "data", "existing_document_id"),
		nestedStringFromMap(body, "resource", "document_id"),
	)
	if documentID == "" {
		return CreateBindingRootDocumentResponse{}, false
	}
	return CreateBindingRootDocumentResponse{DocumentID: documentID, Created: false}, true
}

func recoverSubmitTaskConflict(err error) (SubmitParseTaskResponse, bool) {
	if !isHTTPStatus(err, http.StatusConflict) {
		return SubmitParseTaskResponse{}, false
	}
	body := coreErrorBody(err)
	taskID := firstNonEmpty(
		stringFromAny(body["task_id"]),
		stringFromAny(body["core_task_id"]),
		stringFromAny(body["existing_task_id"]),
		nestedStringFromMap(body, "data", "task_id"),
		nestedStringFromMap(body, "data", "core_task_id"),
		nestedStringFromMap(body, "data", "existing_task_id"),
		nestedStringFromMap(body, "resource", "task_id"),
	)
	documentID := firstNonEmpty(
		stringFromAny(body["document_id"]),
		stringFromAny(body["core_document_id"]),
		stringFromAny(body["existing_document_id"]),
		nestedStringFromMap(body, "data", "document_id"),
		nestedStringFromMap(body, "data", "core_document_id"),
		nestedStringFromMap(body, "data", "existing_document_id"),
		nestedStringFromMap(body, "resource", "document_id"),
	)
	if taskID == "" && documentID == "" {
		return SubmitParseTaskResponse{}, false
	}
	status := StatusSubmitted
	if taskID == "" {
		status = StatusSucceeded
	}
	return SubmitParseTaskResponse{CoreTaskID: taskID, CoreDocumentID: documentID, Status: status, Created: false}, true
}

func coreErrorBody(err error) map[string]any {
	var coreErr *Error
	if !errors.As(err, &coreErr) || coreErr.Body == nil {
		return nil
	}
	return coreErr.Body
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func nestedStringFromMap(root map[string]any, keys ...string) string {
	var current any = root
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	return stringFromAny(current)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mapCoreTaskStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED", "SUCCESS":
		return ResultStatusSucceeded
	case "FAILED", "CANCELED", "CANCELLED":
		return ResultStatusFailed
	case "RUNNING", "CREATING", "UPLOADING", "UPLOADED", "SUBMITTED", "":
		return ResultStatusRunning
	default:
		return ResultStatusRunning
	}
}

func isHTTPStatus(err error, status int) bool {
	var coreErr *Error
	return errors.As(err, &coreErr) && coreErr.StatusCode == status
}
