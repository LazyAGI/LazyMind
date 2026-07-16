package doc

type TaskType string

type TaskState string

const (
	TaskTypeUnspecified   TaskType = "TASK_TYPE_UNSPECIFIED"
	TaskTypeParse         TaskType = "TASK_TYPE_PARSE"
	TaskTypeReparse       TaskType = "TASK_TYPE_REPARSE"
	TaskTypeCopy          TaskType = "TASK_TYPE_COPY"
	TaskTypeParseUploaded TaskType = "TASK_TYPE_PARSE_UPLOADED"
	TaskTypeMove          TaskType = "TASK_TYPE_MOVE"
)

const (
	TaskStateCreating  TaskState = "CREATING"
	TaskStateUploading TaskState = "UPLOADING"
	TaskStateUploaded  TaskState = "UPLOADED"
	TaskStateRunning   TaskState = "RUNNING"
	TaskStateSucceeded TaskState = "SUCCEEDED"
	TaskStateFailed    TaskState = "FAILED"
	TaskStateCancelled TaskState = "CANCELED"
	TaskStateSuspended TaskState = "SUSPENDED"
)

type TaskFile struct {
	DisplayName     string `json:"display_name,omitempty"`
	StoredName      string `json:"stored_name,omitempty"`
	StoredPath      string `json:"stored_path,omitempty"`
	ParseStoredPath string `json:"parse_stored_path,omitempty"`
	FileSize        int64  `json:"file_size,omitempty"`
	RelativePath    string `json:"relative_path,omitempty"`
	ContentType     string `json:"content_type,omitempty"`
}

type TaskDocumentInfo struct {
	DocumentID    string `json:"document_id,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	DocumentState string `json:"document_state,omitempty"`
	DocumentSize  int64  `json:"document_size,omitempty"`
}

type TaskInfo struct {
	TotalDocumentSize     int64 `json:"total_document_size,omitempty"`
	TotalDocumentCount    int64 `json:"total_document_count,omitempty"`
	SucceedDocumentSize   int64 `json:"succeed_document_size,omitempty"`
	SucceedDocumentCount  int64 `json:"succeed_document_count,omitempty"`
	SucceedTokenCount     int64 `json:"succeed_token_count,omitempty"`
	FailedDocumentSize    int64 `json:"failed_document_size,omitempty"`
	FailedDocumentCount   int64 `json:"failed_document_count,omitempty"`
	FilteredDocumentCount int64 `json:"filtered_document_count,omitempty"`
}

type TaskPayload struct {
	DataSourceType  string     `json:"data_source_type,omitempty"`
	TaskType        TaskType   `json:"task_type,omitempty"`
	DocumentPID     string     `json:"document_pid,omitempty"`
	RelativePath    string     `json:"relative_path,omitempty"`
	DisplayName     string     `json:"display_name,omitempty"`
	DocumentID      string     `json:"document_id,omitempty"`
	DocumentIDs     []string   `json:"document_ids,omitempty"`
	Files           []TaskFile `json:"files,omitempty"`
	ReparseGroups   []string   `json:"reparse_groups,omitempty"`
	ReparseMode     string     `json:"reparse_mode,omitempty"`
	DocumentTags    []string   `json:"document_tags,omitempty"`
	TargetDatasetID string     `json:"target_dataset_id,omitempty"`
	TargetPath      string     `json:"target_path,omitempty"`
	TargetPID       string     `json:"target_pid,omitempty"`
}

type CreateTaskItem struct {
	Task         TaskPayload `json:"task"`
	TaskID       string      `json:"task_id,omitempty"`
	CrossDataset bool        `json:"cross_dataset,omitempty"`
	UploadFileID string      `json:"upload_file_id,omitempty"`
	ContentHash  string      `json:"content_hash,omitempty"`
}

type CreateTaskRequest struct {
	Parent string           `json:"parent,omitempty"`
	Items  []CreateTaskItem `json:"items"`
}

type CreateTasksResponse struct {
	Tasks []TaskResponse `json:"tasks"`
}

type StartTaskRequest struct {
	TaskIDs   []string `json:"task_ids"`
	StartMode string   `json:"start_mode,omitempty"`
}

type StartTaskResult struct {
	TaskID       string `json:"task_id"`
	DocumentID   string `json:"document_id,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	Status       string `json:"status"`
	SubmitStatus string `json:"submit_status,omitempty"`
	Message      string `json:"message,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

type StartTasksResponse struct {
	Tasks          []StartTaskResult `json:"tasks"`
	RequestedCount int               `json:"requested_count"`
	StartedCount   int               `json:"started_count"`
	FailedCount    int               `json:"failed_count"`
}

type SearchTasksRequest struct {
	TaskIDs   []string `json:"task_ids"`
	TaskState string   `json:"task_state,omitempty"`
}

type SuspendJobRequest struct {
	TaskID string `json:"task_id,omitempty"`
}

type ResumeTaskRequest struct {
	TaskID string `json:"task_id,omitempty"`
}

type ExternalCancelTaskRequest struct {
	TaskID string `json:"task_id"`
}

type InitUploadRequest struct {
	DocumentPID    string `json:"document_pid,omitempty"`
	RelativePath   string `json:"relative_path,omitempty"`
	Filename       string `json:"filename"`
	FileSize       int64  `json:"file_size,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	PartSize       int64  `json:"part_size,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type InitUploadResponse struct {
	UploadID    string `json:"upload_id"`
	TaskID      string `json:"task_id,omitempty"`
	DocumentID  string `json:"document_id,omitempty"`
	DatasetID   string `json:"dataset_id,omitempty"`
	StoredName  string `json:"stored_name"`
	UploadMode  string `json:"upload_mode"`
	PartSize    int64  `json:"part_size,omitempty"`
	TotalParts  int    `json:"total_parts,omitempty"`
	UploadState string `json:"upload_state"`
	UploadScope string `json:"upload_scope,omitempty"`
}

type CompleteUploadRequest struct {
	AutoStart      bool   `json:"auto_start,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type CompleteUploadResponse struct {
	TaskID          string `json:"task_id,omitempty"`
	UploadID        string `json:"upload_id"`
	DocumentID      string `json:"document_id,omitempty"`
	UploadFileID    string `json:"upload_file_id,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	DatasetID       string `json:"dataset_id,omitempty"`
	StoredPath      string `json:"stored_path"`
	ParseStoredPath string `json:"parse_stored_path,omitempty"`
	ContentURL      string `json:"content_url,omitempty"`
	DownloadURL     string `json:"download_url,omitempty"`
	FileURL         string `json:"file_url,omitempty"`
	FileSize        int64  `json:"file_size,omitempty"`
	ConvertStatus   string `json:"convert_status,omitempty"`
	ConvertError    string `json:"convert_error,omitempty"`
	UploadScope     string `json:"upload_scope,omitempty"`
}

const (
	UploadedFileStateUploaded = "UPLOADED"
	UploadedFileStateBound    = "BOUND"
	UploadedFileStateExpired  = "EXPIRED"
)

type UploadFileRequest struct {
	DocumentPID  string   `json:"document_pid,omitempty"`
	RelativePath string   `json:"relative_path,omitempty"`
	DocumentTags []string `json:"document_tags,omitempty"`
}

type UploadFileResponse struct {
	UploadFileID string   `json:"upload_file_id"`
	ContentHash  string   `json:"content_hash,omitempty"`
	DatasetID    string   `json:"dataset_id,omitempty"`
	Filename     string   `json:"filename"`
	StoredName   string   `json:"stored_name"`
	StoredPath   string   `json:"stored_path"`
	RelativePath string   `json:"relative_path,omitempty"`
	DocumentPID  string   `json:"document_pid,omitempty"`
	DocumentTags []string `json:"document_tags,omitempty"`
	FileSize     int64    `json:"file_size,omitempty"`
	ContentType  string   `json:"content_type,omitempty"`
	ContentURL   string   `json:"content_url,omitempty"`
	DownloadURL  string   `json:"download_url,omitempty"`
	FileURL      string   `json:"file_url,omitempty"`
	Status       string   `json:"status"`
	UploadScope  string   `json:"upload_scope,omitempty"`
}

type UploadFilesResponse struct {
	Files []UploadFileResponse `json:"files"`
}

type CheckFileHashesRequest struct {
	Hashes []string `json:"hashes"`
}

type CheckFileHashesResponse struct {
	MissingHashes []string `json:"missing_hashes"`
}

type uploadedFileExt struct {
	StoredPath       string   `json:"stored_path,omitempty"`
	StoredName       string   `json:"stored_name,omitempty"`
	OriginalFilename string   `json:"original_filename,omitempty"`
	FileSize         int64    `json:"file_size,omitempty"`
	ContentType      string   `json:"content_type,omitempty"`
	RelativePath     string   `json:"relative_path,omitempty"`
	DocumentPID      string   `json:"document_pid,omitempty"`
	DocumentTags     []string `json:"document_tags,omitempty"`
}

type AbortUploadRequest struct {
	Reason string `json:"reason,omitempty"`
}

type BatchUploadTasksResponse struct {
	Tasks []TaskResponse `json:"tasks"`
}

type TaskResponse struct {
	Name             string             `json:"name,omitempty"`
	TaskID           string             `json:"task_id,omitempty"`
	DocumentID       string             `json:"document_id,omitempty"`
	DataSourceType   string             `json:"data_source_type,omitempty"`
	TaskState        string             `json:"task_state"`
	Creator          string             `json:"creator,omitempty"`
	ErrMsg           string             `json:"err_msg,omitempty"`
	TaskInfo         TaskInfo           `json:"task_info,omitempty"`
	DocumentInfo     []TaskDocumentInfo `json:"document_info,omitempty"`
	Files            []TaskFile         `json:"files,omitempty"`
	CreateTime       string             `json:"create_time,omitempty"`
	StartTime        string             `json:"start_time,omitempty"`
	FinishTime       string             `json:"finish_time,omitempty"`
	DisplayName      string             `json:"display_name,omitempty"`
	TaskType         string             `json:"task_type,omitempty"`
	TargetDatasetID  string             `json:"target_dataset_id,omitempty"`
	TargetPID        string             `json:"target_pid,omitempty"`
	ParseStoredPath  string             `json:"parse_stored_path,omitempty"`
	PDFConvertResult string             `json:"pdf_convert_result,omitempty"`
	ConvertRequired  bool               `json:"convert_required,omitempty"`
	ConvertStatus    string             `json:"convert_status,omitempty"`
	ConvertError     string             `json:"convert_error,omitempty"`
}

type ListTasksResponse struct {
	Tasks         []TaskResponse `json:"tasks"`
	TotalSize     int32          `json:"total_size,omitempty"`
	NextPageToken string         `json:"next_page_token,omitempty"`
}

type transferBinding struct {
	SourceDocumentID string `json:"source_document_id,omitempty"`
	TargetDocumentID string `json:"target_document_id,omitempty"`
	SourceLazyDocID  string `json:"source_lazy_doc_id,omitempty"`
	TargetLazyDocID  string `json:"target_lazy_doc_id,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	StoredPath       string `json:"stored_path,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Status           string `json:"status,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type taskExt struct {
	TaskType         string            `json:"task_type,omitempty"`
	TaskState        string            `json:"task_state,omitempty"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	DocumentPID      string            `json:"document_pid,omitempty"`
	DisplayName      string            `json:"display_name,omitempty"`
	TargetDatasetID  string            `json:"target_dataset_id,omitempty"`
	TargetPID        string            `json:"target_pid,omitempty"`
	TargetPath       string            `json:"target_path,omitempty"`
	DataSourceType   string            `json:"data_source_type,omitempty"`
	Files            []TaskFile        `json:"files,omitempty"`
	DocumentTags     []string          `json:"document_tags,omitempty"`
	ReparseGroups    []string          `json:"reparse_groups,omitempty"`
	ReparseMode      string            `json:"reparse_mode,omitempty"`
	TransferBindings []transferBinding `json:"transfer_bindings,omitempty"`
}

type documentExt struct {
	StoredPath       string   `json:"stored_path,omitempty"`
	StoredName       string   `json:"stored_name,omitempty"`
	OriginalFilename string   `json:"original_filename,omitempty"`
	FileSize         int64    `json:"file_size,omitempty"`
	ContentType      string   `json:"content_type,omitempty"`
	RelativePath     string   `json:"relative_path,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	// text（text，Defaulttext stored_path text）
	SourceStoredPath string `json:"source_stored_path,omitempty"`
	// text（Office Successtext PDF）
	ParseStoredPath  string `json:"parse_stored_path,omitempty"`
	ParseStoredName  string `json:"parse_stored_name,omitempty"`
	ParseContentType string `json:"parse_content_type,omitempty"`
	ParseFileSize    int64  `json:"parse_file_size,omitempty"`
	ConvertRequired  bool   `json:"convert_required,omitempty"`
	ConvertStatus    string `json:"convert_status,omitempty"`
	ConvertError     string `json:"convert_error,omitempty"`
	ConvertProvider  string `json:"convert_provider,omitempty"`
}

type uploadMeta struct {
	UploadID         string `json:"upload_id"`
	TaskID           string `json:"task_id,omitempty"`
	DocumentID       string `json:"document_id,omitempty"`
	DatasetID        string `json:"dataset_id,omitempty"`
	TenantID         string `json:"tenant_id,omitempty"`
	DocumentPID      string `json:"document_pid,omitempty"`
	RelativePath     string `json:"relative_path,omitempty"`
	OriginalFilename string `json:"original_filename"`
	StoredName       string `json:"stored_name"`
	FileSize         int64  `json:"file_size,omitempty"`
	ContentType      string `json:"content_type,omitempty"`
	PartSize         int64  `json:"part_size,omitempty"`
	TotalParts       int    `json:"total_parts,omitempty"`
	UploadedParts    []int  `json:"uploaded_parts,omitempty"`
	UploadState      string `json:"upload_state,omitempty"`
	UploadScope      string `json:"upload_scope,omitempty"`
	CreateUserID     string `json:"create_user_id,omitempty"`
	CreateUserName   string `json:"create_user_name,omitempty"`
}
